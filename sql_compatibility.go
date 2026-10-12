package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
)

// The main read-only pool is separate from Friend Together's independent pool.
// Positive limits prevent sql.DB's zero (unlimited) default from being selected.
type sqlPoolConfig struct {
	MaxOpen, MaxIdle         int
	MaxIdleTime, MaxLifetime time.Duration
}

func sqlPoolConfigFromEnvironment() (sqlPoolConfig, error) {
	cfg := sqlPoolConfig{MaxOpen: 8, MaxIdle: 2, MaxIdleTime: 5 * time.Minute, MaxLifetime: 30 * time.Minute}
	for _, item := range []struct {
		key     string
		dest    *int
		minimum int
	}{
		{"DATABASE_MAX_OPEN_CONNS", &cfg.MaxOpen, 1},
		{"DATABASE_MAX_IDLE_CONNS", &cfg.MaxIdle, 0},
	} {
		if raw, exists := os.LookupEnv(item.key); exists {
			value, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || value < item.minimum || value > 64 {
				return cfg, fmt.Errorf("%s must be an integer between %d and 64", item.key, item.minimum)
			}
			*item.dest = value
		}
	}
	if cfg.MaxIdle > cfg.MaxOpen {
		return cfg, errors.New("DATABASE_MAX_IDLE_CONNS must not exceed DATABASE_MAX_OPEN_CONNS")
	}
	for _, item := range []struct {
		key  string
		dest *time.Duration
	}{
		{"DATABASE_CONN_MAX_IDLE_TIME", &cfg.MaxIdleTime},
		{"DATABASE_CONN_MAX_LIFETIME", &cfg.MaxLifetime},
	} {
		if raw, exists := os.LookupEnv(item.key); exists {
			value, err := time.ParseDuration(strings.TrimSpace(raw))
			if err != nil || value < time.Second || value > 24*time.Hour {
				return cfg, fmt.Errorf("%s must be a duration between 1s and 24h", item.key)
			}
			*item.dest = value
		}
	}
	return cfg, nil
}

func applySQLPoolConfig(database *sql.DB, cfg sqlPoolConfig) {
	database.SetMaxOpenConns(cfg.MaxOpen)
	database.SetMaxIdleConns(cfg.MaxIdle)
	database.SetConnMaxIdleTime(cfg.MaxIdleTime)
	database.SetConnMaxLifetime(cfg.MaxLifetime)
}

const (
	sqlStatesFeature          = "states"
	sqlCurrentDriveFeature    = "current_drive"
	sqlFriendSourceFeature    = "friend_source"
	sqlMQTTFleetFeature       = "mqtt_fleet"
	sqlFeatureProbeTimeout    = 2 * time.Second
	statesHistoryMaxPageLimit = 2000
)

var errSQLCarNotFound = errors.New("car_not_found")

// Friend Together uses a separate read-only pool. Capability probes must check
// that source's actual connection, not infer its health from the main pool.
var friendSQLProbeDatabase atomic.Pointer[sql.DB]

func setFriendSQLProbeDatabase(database *sql.DB) { friendSQLProbeDatabase.Store(database) }

type sqlFeatureError struct {
	Feature     string
	Unsupported bool
	Cause       error
}

func (e *sqlFeatureError) Error() string { return e.Feature + " SQL feature: " + e.Cause.Error() }
func (e *sqlFeatureError) Unwrap() error { return e.Cause }

// Execute actual column references and operators with LIMIT 0. A missing
// relation/column or incompatible operator is an unsupported schema; transport,
// permissions and timeout failures leave support unknown, never empty history.
const sqlStatesSchemaQuery = `SELECT s.id, s.state, s.start_date, s.end_date, c.name,
	p.id, p.date, p.battery_level, p.rated_battery_range_km
	FROM states s LEFT JOIN cars c ON c.id = s.car_id
	LEFT JOIN positions p ON p.car_id = s.car_id
	WHERE s.start_date < NOW() AND COALESCE(s.end_date, NOW()) > NOW()
	AND p.date <= s.start_date ORDER BY s.start_date, s.id, p.date, p.id LIMIT 0`

const sqlCurrentDriveSchemaQuery = `SELECT c.id, c.name, d.id, d.start_date, d.end_date,
	p.id, p.date, p.latitude, p.longitude, p.speed, p.odometer
	FROM cars c LEFT JOIN drives d ON d.car_id = c.id
	LEFT JOIN positions p ON p.drive_id = d.id
	WHERE d.end_date IS NULL AND p.latitude BETWEEN -90 AND 90
	AND p.longitude BETWEEN -180 AND 180 ORDER BY d.start_date, d.id, p.id LIMIT 0`

const sqlFriendOwnershipSchemaQuery = `SELECT id FROM cars WHERE id = $1 LIMIT 0`
const sqlFriendLatestSchemaQuery = `SELECT * FROM (` + friendLatestPositionQuery + `) companion_friend_source LIMIT 0`
const sqlFriendTrajectorySchemaQuery = `SELECT * FROM (` + friendTrajectoryQuery + `) companion_friend_trajectory LIMIT 0`
const sqlMQTTFleetSchemaQuery = `SELECT source.id, source.enabled FROM (` + mqttFleetQuery + `) source WHERE source.id > 0 AND source.enabled IS TRUE LIMIT 0`

func sqlFeatureFailure(feature string, err error) error {
	if err == nil || errors.Is(err, errSQLCarNotFound) {
		return err
	}
	var existing *sqlFeatureError
	if errors.As(err, &existing) {
		return err
	}
	var pgErr *pq.Error
	unsupported := errors.As(err, &pgErr) && (pgErr.Code == "42703" || pgErr.Code == "42P01" || pgErr.Code == "42883" || pgErr.Code == "42804")
	return &sqlFeatureError{Feature: feature, Unsupported: unsupported, Cause: err}
}

func checkSQLFeature(ctx context.Context, database *sql.DB, feature string) error {
	if database == nil {
		return sqlFeatureFailure(feature, errors.New("database unavailable"))
	}
	type probe struct {
		query string
		args  []any
	}
	var probes []probe
	switch feature {
	case sqlStatesFeature:
		probes = []probe{{query: sqlStatesSchemaQuery}}
	case sqlCurrentDriveFeature:
		probes = []probe{{query: sqlCurrentDriveSchemaQuery}}
	case sqlFriendSourceFeature:
		at := time.Unix(0, 0).UTC()
		probes = []probe{
			{sqlFriendOwnershipSchemaQuery, []any{int64(1)}},
			{sqlFriendLatestSchemaQuery, []any{int64(1), at}},
			{sqlFriendTrajectorySchemaQuery, []any{int64(1), int64(1), at, at}},
		}
	case sqlMQTTFleetFeature:
		probes = []probe{{query: sqlMQTTFleetSchemaQuery}}
	default:
		return sqlFeatureFailure(feature, errors.New("unknown SQL feature"))
	}
	ctx, cancel := context.WithTimeout(ctx, sqlFeatureProbeTimeout)
	defer cancel()
	for _, item := range probes {
		rows, err := database.QueryContext(ctx, item.query, item.args...)
		if err != nil {
			return sqlFeatureFailure(feature, err)
		}
		for rows.Next() {
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return sqlFeatureFailure(feature, err)
		}
		if closeErr != nil {
			return sqlFeatureFailure(feature, closeErr)
		}
	}
	return nil
}

func addSQLFeatureCapabilities(ctx context.Context, payload map[string]any) {
	if payload == nil {
		return
	}
	features := map[string]any{}
	groups := map[string][]string{
		sqlStatesFeature:       {"parking_state_history", "state_boundary_battery", "state_boundary_rated_range", "timestamped_boundary_telemetry", "bounded_boundary_telemetry", "live_open_intervals", "data_coverage", "state_history_keyset_pagination_v1"},
		sqlCurrentDriveFeature: {"current_drive_trajectory", "immutable_current_drive_start", "incremental_drive_points"},
		sqlFriendSourceFeature: {"friend_together_v1"},
		sqlMQTTFleetFeature:    {"vehicle_software_update_events", "charging_live_activity_events", "charging_live_activity_push_to_start", "charging_live_activity_remote_updates", "navigation_live_activity_events", "navigation_live_activity_push_to_start", "navigation_live_activity_remote_updates", "navigation_trip_alerts", "parking_observed_events", "parking_charge_connection_events", "parking_security_events", "parking_climate_events", "lock_secure_push", "low_battery_push", "vehicle_detail_status", "door_detail_status", "window_detail_status", "service_mode_status", "friend_together_v1"},
	}
	featureOrder := []string{sqlStatesFeature, sqlCurrentDriveFeature, sqlFriendSourceFeature, sqlMQTTFleetFeature}
	results := make([]error, len(featureOrder))
	var pending sync.WaitGroup
	for index, feature := range featureOrder {
		database := db
		if feature == sqlFriendSourceFeature {
			database = friendSQLProbeDatabase.Load()
		}
		if feature == sqlMQTTFleetFeature {
			if runtime := mqttSourceRuntime.Load(); runtime != nil {
				database = runtime.database
			}
		}
		pending.Add(1)
		go func(index int, feature string, database *sql.DB) {
			defer pending.Done()
			results[index] = checkSQLFeature(ctx, database, feature)
		}(index, feature, database)
	}
	pending.Wait()
	for index, feature := range featureOrder {
		err := results[index]
		status, code := "available", ""
		var supported any = true
		if err != nil {
			status, code, supported = "unknown", feature+"_unavailable", nil
			var failure *sqlFeatureError
			if errors.As(err, &failure) && failure.Unsupported {
				status, code, supported = "unsupported_schema", feature+"_unsupported", false
			}
			removeSQLCapabilities(payload, groups[feature])
		} else if feature == sqlStatesFeature {
			caps, _ := payload["capabilities"].([]string)
			found := false
			for _, capability := range caps {
				if capability == "state_history_keyset_pagination_v1" {
					found = true
					break
				}
			}
			if !found {
				payload["capabilities"] = append(caps, "state_history_keyset_pagination_v1")
			}
		}
		features[feature] = map[string]any{"status": status, "supported": supported, "error": code}
	}
	payload["sql_features"] = features
	if db != nil {
		stats := db.Stats()
		payload["database_pool"] = map[string]any{
			"max_open_connections": stats.MaxOpenConnections, "open_connections": stats.OpenConnections,
			"in_use": stats.InUse, "idle": stats.Idle, "wait_count": stats.WaitCount,
			"wait_duration_ms": stats.WaitDuration.Milliseconds(),
		}
	}
}

func removeSQLCapabilities(payload map[string]any, removed []string) {
	caps, ok := payload["capabilities"].([]string)
	if !ok {
		return
	}
	out := make([]string, 0, len(caps))
	for _, capability := range caps {
		keep := true
		for _, name := range removed {
			if name == capability {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, capability)
		}
	}
	payload["capabilities"] = out
}

func writeSQLFeatureError(w http.ResponseWriter, feature string, err error) {
	status, code := http.StatusServiceUnavailable, feature+"_unavailable"
	var failure *sqlFeatureError
	if errors.As(err, &failure) && failure.Unsupported {
		status, code = http.StatusNotImplemented, feature+"_unsupported"
	}
	if errors.Is(err, errSQLCarNotFound) {
		status, code = http.StatusNotFound, "car_not_found"
	}
	writeJSON(w, status, map[string]string{"error": code, "feature": feature})
}

type statesHistoryCursor struct {
	Version  int `json:"v"`
	CarID    int `json:"car"`
	From, To time.Time
	Date     time.Time
	ID       int64
}

type statesHistoryPageRequest struct {
	Limit  int
	Cursor *statesHistoryCursor
}
type statesHistoryPagination struct {
	Limit         int     `json:"limit"`
	ReturnedCount int     `json:"returned_count"`
	HasMore       bool    `json:"has_more"`
	NextCursor    *string `json:"next_cursor"`
}

// Process-local signing prevents a cursor crossing servers with identical IDs.
// Restarting requires restarting the requested window, as with tire history.
var statesHistoryCursorKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("unable to initialize state history cursor signing")
	}
	return key
}()

func parseStatesHistoryPage(values url.Values, carID int, from, to time.Time) (*statesHistoryPageRequest, error) {
	if !values.Has("limit") && !values.Has("cursor") {
		return nil, nil
	}
	page := &statesHistoryPageRequest{Limit: 1000}
	for _, name := range []string{"limit", "cursor"} {
		if entries, ok := values[name]; ok && (len(entries) != 1 || strings.TrimSpace(entries[0]) == "") {
			return nil, fmt.Errorf("Invalid %s", name)
		}
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > statesHistoryMaxPageLimit {
			return nil, errors.New("limit must be between 1 and 2000")
		}
		page.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeStatesHistoryCursor(raw)
		// An overlapping long interval may start before the requested window.
		if err != nil || cursor.Version != 1 || cursor.CarID != carID || !cursor.From.Equal(from) || !cursor.To.Equal(to) || cursor.ID <= 0 || cursor.Date.IsZero() || !cursor.Date.Before(to) {
			return nil, errors.New("invalid_cursor")
		}
		page.Cursor = &cursor
	}
	return page, nil
}

func encodeStatesHistoryCursor(cursor statesHistoryCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, statesHistoryCursorKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeStatesHistoryCursor(raw string) (statesHistoryCursor, error) {
	var cursor statesHistoryCursor
	parts := strings.Split(raw, ".")
	if len(raw) > 1024 || len(parts) != 2 {
		return cursor, errors.New("invalid_cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor, err
	}
	mac := hmac.New(sha256.New, statesHistoryCursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, errors.New("invalid_cursor")
	}
	err = json.Unmarshal(payload, &cursor)
	return cursor, err
}
