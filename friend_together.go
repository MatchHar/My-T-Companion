package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	ft "mycarmate-states-api/internal/friendtogether"
)

var friendTogether *ft.HTTPService

func registerFriendTogether(mux *http.ServeMux) func() {
	friendTogether = nil
	enabled := strings.EqualFold(getenv("FRIEND_TOGETHER_ENABLED", "false"), "true")
	source := &friendTeslaMateSource{navigation: map[int64]friendNavigationSample{}}
	if enabled {
		// A separate bounded read-only pool prevents guest demand consuming the
		// main Companion pool. No connection is made while the feature is off.
		var err error
		source.database, err = openFriendDatabase()
		if err != nil {
			log.Printf("[warn] friend Together source configuration unavailable")
		}
		source.ownsDatabase = source.database != nil
	}
	var reader ft.SourceReader = source
	if enabled && source.database == nil {
		reader = nil
	}
	service, err := ft.NewHTTPService(ft.HTTPConfig{Enabled: enabled, GuestOrigin: getenv("FRIEND_TOGETHER_GUEST_ORIGIN", ""), StatePath: getenv("FRIEND_TOGETHER_STATE_PATH", "/data/friend-together/state.json"), AuthenticateOwner: authorized, Source: reader})
	if err != nil {
		source.stop()
		// Do not log configured origins, state contents or credentials.
		log.Printf("[warn] friend Together disabled: configuration or durable state unavailable")
		registerUnavailableFriendHandlers(mux, authorized)
		return func() {}
	}
	friendTogether = service
	mux.Handle("/api/v1/friend-together/", service.OwnerHandler())
	mux.Handle("/friend/v1/", service.GuestHandler())
	if enabled {
		source.start()
	}
	return source.stop
}

func registerUnavailableFriendHandlers(mux *http.ServeMux, owner func(*http.Request) bool) {
	deny := func(w http.ResponseWriter, code int, message string) {
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		writeJSON(w, code, map[string]string{"error": message})
	}
	mux.HandleFunc("/api/v1/friend-together/", func(w http.ResponseWriter, r *http.Request) {
		if owner == nil || !owner(r) {
			deny(w, 401, "not_authorized")
			return
		}
		deny(w, 503, "unavailable")
	})
	// Explicitly deny guest paths on every initialization failure. Never let
	// them reach the ordinary TeslaMate catch-all or owner authentication probe.
	mux.HandleFunc("/friend/v1/", func(w http.ResponseWriter, _ *http.Request) { deny(w, 503, "unavailable") })
}

// GPS/speed/battery always retain the position row's original observation time.
// The source never turns a successful query or an unrelated MQTT packet into a
// new location observation. Companion remains read-only and never calls Tesla.
type friendTeslaMateSource struct {
	database     *sql.DB
	ownsDatabase bool
	mu           sync.Mutex
	navigation   map[int64]friendNavigationSample
	client       mqtt.Client
}
type friendNavigationSample struct {
	revision       int64
	observation    *ft.NavigationObservation
	active         bool
	lastObservedMS int64
}

func (s *friendTeslaMateSource) OwnsCar(ctx context.Context, id int64) (bool, error) {
	if s.database == nil {
		return false, sql.ErrConnDone
	}
	var exists bool
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err := s.database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cars WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}
func (s *friendTeslaMateSource) ReadSource(ctx context.Context, key ft.SourceKey, consent int64, p ft.PermissionSet) (ft.SourceSnapshot, error) {
	out := ft.SourceSnapshot{Source: key, State: ft.StateUnknown}
	if s.database == nil {
		return out, sql.ErrConnDone
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var date time.Time
	var lat, lon, speed, soc, rangeKM sql.NullFloat64
	var driveID sql.NullInt64
	var state sql.NullString
	err := s.database.QueryRowContext(ctx, friendLatestPositionQuery, key.CarID, time.UnixMilli(consent).UTC()).Scan(&date, &lat, &lon, &speed, &soc, &rangeKM, &driveID, &state)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	at := date.UnixMilli()
	motion := ft.Motion{ObservedAtMS: &at}
	if lat.Valid && lon.Valid {
		motion.Latitude = &lat.Float64
		motion.Longitude = &lon.Float64
	}
	if speed.Valid {
		motion.SpeedKPH = &speed.Float64
	}
	out.Motion = &motion
	switch state.String {
	case "asleep":
		out.State = ft.StateAsleep
	case "offline":
		out.State = ft.StateOffline
	case "charging":
		out.State = ft.StateCharging
	case "driving":
		out.State = ft.StateDriving
	case "parked":
		out.State = ft.StateParked
	}
	if p.Battery && (soc.Valid || rangeKM.Valid) {
		battery := ft.Battery{ObservedAtMS: at}
		if soc.Valid {
			battery.Percentage = &soc.Float64
		}
		if rangeKM.Valid {
			battery.RatedRangeKM = &rangeKM.Float64
		}
		out.Battery = &battery
	}
	if p.Navigation {
		s.mu.Lock()
		if n, ok := s.navigation[key.CarID]; ok && n.active && n.observation != nil {
			copy := *n.observation
			out.Navigation = &copy
		}
		s.mu.Unlock()
	}
	if p.Trajectory && driveID.Valid {
		rows, err := s.database.QueryContext(ctx, friendTrajectoryQuery, key.CarID, driveID.Int64, time.UnixMilli(consent).UTC(), date)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		points := make([]ft.TrajectoryPoint, 0, 500)
		for rows.Next() {
			var d time.Time
			var point ft.TrajectoryPoint
			if err := rows.Scan(&d, &point.Latitude, &point.Longitude); err != nil {
				return out, err
			}
			point.ObservedAtMS = d.UnixMilli()
			// Millisecond wire precision may merge submillisecond database rows;
			// newest ID wins, without manufacturing another instant or a segment.
			if len(points) == 0 || points[len(points)-1].ObservedAtMS != point.ObservedAtMS {
				points = append(points, point)
			}
		}
		if err := rows.Err(); err != nil {
			return out, err
		}
		for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
			points[i], points[j] = points[j], points[i]
		}
		out.Trajectory = points
	}
	return out, nil
}

// TeslaMate states has only online/offline/asleep. Derive charging/driving
// from same-car session evidence at the position instant, never from NOW or
// merely a non-null historical drive_id. The consent bound allows upstream's
// date BRIN to prune old pages without installing new indexes in user databases.
const friendLatestPositionQuery = `WITH latest_position AS (
  SELECT id,car_id,date,latitude,longitude,speed,battery_level,rated_battery_range_km,drive_id
  FROM positions WHERE car_id=$1 AND date>=$2 ORDER BY date DESC,id DESC LIMIT 1)
  SELECT p.date,p.latitude,p.longitude,p.speed,p.battery_level,p.rated_battery_range_km,
  CASE WHEN d.id IS NOT NULL AND (d.end_date IS NULL OR d.end_date>p.date) THEN p.drive_id END,
  CASE WHEN s.state IN ('asleep','offline') THEN s.state::text
       WHEN d.id IS NOT NULL AND (d.end_date IS NULL OR d.end_date>p.date) THEN 'driving'
       WHEN EXISTS (SELECT 1 FROM charging_processes cp WHERE cp.car_id=p.car_id
         AND (cp.position_id=p.id OR cp.start_date<=p.date) AND (cp.end_date IS NULL OR cp.end_date>p.date)) THEN 'charging'
       WHEN s.state='online' THEN 'parked' ELSE 'unknown' END
  FROM latest_position p LEFT JOIN drives d ON d.id=p.drive_id AND d.car_id=p.car_id
  LEFT JOIN LATERAL (SELECT state FROM states WHERE car_id=p.car_id AND start_date<=p.date
    AND (end_date IS NULL OR end_date>p.date) ORDER BY start_date DESC,id DESC LIMIT 1) s ON true`
const friendTrajectoryQuery = `SELECT date,latitude,longitude FROM positions
  WHERE car_id=$1 AND drive_id=$2 AND date>=$3 AND date<=$4 AND latitude IS NOT NULL AND longitude IS NOT NULL
  ORDER BY date DESC,id DESC LIMIT 500`

func (s *friendTeslaMateSource) start() {
	options := mqtt.NewClientOptions().AddBroker(getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883")).SetClientID(getenv("MQTT_CLIENT_ID", "my-t-companion") + "-friend-together").SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(10 * time.Second).SetKeepAlive(30 * time.Second).SetOrderMatters(true)
	if username := strings.TrimSpace(getenv("MQTT_USERNAME", "")); username != "" {
		options.SetUsername(username)
		options.SetPassword(getenv("MQTT_PASSWORD", ""))
	}
	options.SetConnectionLostHandler(func(_ mqtt.Client, _ error) {
		s.mu.Lock()
		for id, sample := range s.navigation {
			sample.active = false
			sample.observation = nil
			s.navigation[id] = sample
		}
		s.mu.Unlock()
	})
	options.SetOnConnectHandler(func(client mqtt.Client) {
		token := client.Subscribe("teslamate/cars/+/active_route", 1, func(_ mqtt.Client, message mqtt.Message) {
			s.handleNavigationMessage(message, time.Now().UnixMilli())
		})
		if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
			log.Printf("[warn] friend Together navigation observation unavailable")
		}
	})
	s.client = mqtt.NewClient(options)
	go func() {
		token := s.client.Connect()
		if !token.WaitTimeout(15*time.Second) || token.Error() != nil {
			log.Printf("[warn] friend Together navigation source disconnected")
		}
	}()
}
func (s *friendTeslaMateSource) handleNavigationMessage(message mqtt.Message, at int64) {
	// Retained bootstrap has no original observation timestamp. Publishing it
	// as new after restart would make old destinations appear live.
	if message.Retained() || message.Duplicate() {
		return
	}
	parts := strings.Split(message.Topic(), "/")
	if len(parts) != 4 || parts[0] != "teslamate" || parts[1] != "cars" || parts[3] != "active_route" {
		return
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return
	}
	s.observeNavigation(id, message.Payload(), at)
}
func (s *friendTeslaMateSource) stop() {
	if s.client != nil && s.client.IsConnected() {
		s.client.Disconnect(250)
	}
	if s.ownsDatabase && s.database != nil {
		_ = s.database.Close()
	}
}

func pqKeyword(value string) string {
	return "'" + strings.ReplaceAll(value, `'`, `''`) + "'"
}

func friendDatabaseDSN() string {
	// Same keyword DSN as the working parking/notification pool. The previous
	// postgres:?host=... URL left lib/pq on a unix socket, so OwnsCar failed
	// with 503 source_unavailable while GET /status still looked ready.
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s connect_timeout=%s application_name=my-t-companion-friends options='-c statement_timeout=2000 -c default_transaction_read_only=on'",
		pqKeyword(getenv("DATABASE_HOST", "database")),
		pqKeyword(getenv("DATABASE_PORT", "5432")),
		pqKeyword(getenv("DATABASE_USER", "teslamate")),
		pqKeyword(getenv("DATABASE_PASS", "secret")),
		pqKeyword(getenv("DATABASE_NAME", "teslamate")),
		pqKeyword(getenv("DATABASE_SSL", "disable")),
		pqKeyword(getenv("DATABASE_TIMEOUT", "10")),
	)
}

func openFriendDatabase() (*sql.DB, error) {
	pool, err := sql.Open("postgres", friendDatabaseDSN())
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(2)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return pool, nil
}
func (s *friendTeslaMateSource) observeNavigation(id int64, payload []byte, at int64) {
	if len(payload) > 16<<10 {
		return
	}
	var route activeRouteMQTT
	valid := json.Unmarshal(payload, &route) == nil && route.Error == nil && route.Location != nil && route.Location.Latitude != nil && route.Location.Longitude != nil && route.MilesToArrival != nil && route.MinutesToArrival != nil
	if valid {
		valid = finiteRange(*route.Location.Latitude, -90, 90) && finiteRange(*route.Location.Longitude, -180, 180) && finiteRange(*route.MilesToArrival, 0, 100000) && finiteRange(*route.MinutesToArrival, 0, 100000)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.navigation[id]
	if at < 0 || at < old.lastObservedMS {
		return
	}
	if _, exists := s.navigation[id]; !exists && len(s.navigation) >= 256 {
		return
	}
	old.lastObservedMS = at
	if !valid {
		old.active = false
		old.observation = nil
		s.navigation[id] = old
		return
	}
	lat, lon := *route.Location.Latitude, *route.Location.Longitude
	km, minutes := *route.MilesToArrival*1.609344, *route.MinutesToArrival
	if !old.active || old.observation == nil || old.observation.Latitude == nil || old.observation.Longitude == nil || *old.observation.Latitude != lat || *old.observation.Longitude != lon {
		old.revision++
	}
	if old.revision < 1 {
		old.revision = 1
	}
	old.active = true
	old.observation = &ft.NavigationObservation{Revision: old.revision, Latitude: &lat, Longitude: &lon, RemainingDistanceKM: &km, RemainingMinutes: &minutes, ObservedAtMS: at}
	s.navigation[id] = old
}
func finiteRange(v, low, high float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= low && v <= high
}
