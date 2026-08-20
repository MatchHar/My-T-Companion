package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Keep remote Live Activity content reasonably fresh without hammering APNs.
const navigationUpdateMinimumInterval = 10 * time.Second
const navigationPushHistoryLimit = 200

type navigationLiveActivityEvent struct {
	EventID               string   `json:"event_id"`
	InstallationID        string   `json:"installation_id"`
	CarID                 int      `json:"car_id"`
	VehicleName           string   `json:"vehicle_name,omitempty"`
	Type                  string   `json:"type"`
	SessionID             string   `json:"session_id"`
	Destination           string   `json:"destination"`
	StartName             string   `json:"start_name,omitempty"`
	RemainingDistanceKM   *float64 `json:"remaining_distance_km,omitempty"`
	RemainingMinutes      *int     `json:"remaining_minutes,omitempty"`
	EstimatedArrivalAt    *int64   `json:"estimated_arrival_at,omitempty"`
	ArrivalBatteryLevel   *int     `json:"arrival_battery_level,omitempty"`
	DrivenDistanceKM      *float64 `json:"driven_distance_km,omitempty"`
	TotalDistanceKM       *float64 `json:"total_distance_km,omitempty"`
	HasVerifiedTrajectory bool     `json:"has_verified_trajectory"`
	// Trip timing for 100% real end-frame (unix seconds + RFC3339).
	TripStartedAt   *int64 `json:"trip_started_at,omitempty"`
	TripEndedAt     *int64 `json:"trip_ended_at,omitempty"`
	DurationMinutes *int   `json:"duration_minutes,omitempty"`
	EndReason       string `json:"end_reason,omitempty"`
	ObservedAt      string `json:"observed_at"`
	// Optional history end_reason override (not sent to push relay).
	endReasonOverride string
}

type activeRouteMQTT struct {
	Destination      string   `json:"destination"`
	EnergyAtArrival  *int     `json:"energy_at_arrival"`
	MilesToArrival   *float64 `json:"miles_to_arrival"`
	MinutesToArrival *float64 `json:"minutes_to_arrival"`
	Error            any      `json:"error"`
}

type carNavigationState struct {
	DisplayName  string `json:"display_name,omitempty"`
	VehicleState string `json:"vehicle_state,omitempty"`
	// Last known TeslaMate geofence name (MQTT), used as trip start label.
	Geofence string `json:"geofence,omitempty"`
	// Sticky non-empty geofence: MQTT clears the name after leaving a fence, but
	// navigation often starts after exit — keep the last real place for start_name.
	LastKnownGeofence string `json:"last_known_geofence,omitempty"`
	Destination       string `json:"destination,omitempty"`
	// Frozen label for where the navigation session began (geofence or drive start).
	StartName           string   `json:"start_name,omitempty"`
	RemainingDistanceKM *float64 `json:"remaining_distance_km,omitempty"`
	RemainingMinutes    *int     `json:"remaining_minutes,omitempty"`
	ArrivalBatteryLevel *int     `json:"arrival_battery_level,omitempty"`
	Active              bool     `json:"active"`
	SessionID           string   `json:"session_id,omitempty"`
	// RFC3339 when this navigation session became active (real trip start for end-frame).
	SessionStartedAt string `json:"session_started_at,omitempty"`
	DriveID          int64  `json:"drive_id,omitempty"`
	Sequence         int    `json:"sequence"`
	StartDelivered   bool   `json:"start_delivered"`
	LastQueuedAt     string `json:"last_queued_at,omitempty"`
	LastObservedAt   string `json:"last_observed_at,omitempty"`
}

type navigationNotificationStore struct {
	Cars      map[int]carNavigationState `json:"cars"`
	Delivered map[string]string          `json:"delivered"`
}

// navigationPushHistorySession is the App-readable source of truth for
// destination/name/distance/time that Companion already trusts for push.
type navigationPushHistorySession struct {
	SessionID                string   `json:"session_id"`
	CarID                    int      `json:"car_id"`
	VehicleName              string   `json:"vehicle_name,omitempty"`
	StartName                string   `json:"start_name,omitempty"`
	Destination              string   `json:"destination"`
	StartedAt                string   `json:"started_at"`
	EndedAt                  string   `json:"ended_at,omitempty"`
	EndReason                string   `json:"end_reason,omitempty"`
	StartRemainingDistanceKM *float64 `json:"start_remaining_distance_km,omitempty"`
	StartRemainingMinutes    *int     `json:"start_remaining_minutes,omitempty"`
	StartEstimatedArrivalAt  *int64   `json:"start_estimated_arrival_at,omitempty"`
	EndRemainingDistanceKM   *float64 `json:"end_remaining_distance_km,omitempty"`
	EndRemainingMinutes      *int     `json:"end_remaining_minutes,omitempty"`
	LastRemainingDistanceKM  *float64 `json:"last_remaining_distance_km,omitempty"`
	LastRemainingMinutes     *int     `json:"last_remaining_minutes,omitempty"`
	LastEstimatedArrivalAt   *int64   `json:"last_estimated_arrival_at,omitempty"`
	DrivenDistanceKM         *float64 `json:"driven_distance_km,omitempty"`
	TotalDistanceKM          *float64 `json:"total_distance_km,omitempty"`
	LastEventType            string   `json:"last_event_type"`
	LastDeliveryStatus       string   `json:"last_delivery_status,omitempty"`
	UpdatedAt                string   `json:"updated_at"`
}

type navigationPushHistoryStore struct {
	Sessions []navigationPushHistorySession `json:"sessions"`
}

type navigationNotificationMonitor struct {
	mu             sync.Mutex
	store          navigationNotificationStore
	history        navigationPushHistoryStore
	statePath      string
	historyPath    string
	installationID string
	relayURL       string
	relaySecret    string
	mqttBroker     string
	mqttUsername   string
	mqttPassword   string
	client         mqtt.Client
	httpClient     *http.Client
	enabled        bool
	connected      bool
	started        bool
	workerStarted  bool
	lastEventAt    *time.Time
	lastError      string
	queue          chan navigationLiveActivityEvent
	priorityQueue  chan navigationLiveActivityEvent
	pending        map[int]*time.Timer
}

func newNavigationNotificationMonitorFromEnvironment() *navigationNotificationMonitor {
	statePath := getenv("NAVIGATION_PUSH_STATE_PATH", "/data/navigation-live-activities.json")
	monitor := &navigationNotificationMonitor{
		statePath:      statePath,
		historyPath:    filepath.Join(filepath.Dir(statePath), "navigation-push-history.json"),
		installationID: strings.TrimSpace(os.Getenv("PUSH_INSTALLATION_ID")),
		relayURL:       strings.TrimSpace(os.Getenv("PUSH_RELAY_URL")),
		relaySecret:    strings.TrimSpace(os.Getenv("PUSH_RELAY_SECRET")),
		mqttBroker:     getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883"),
		mqttUsername:   strings.TrimSpace(os.Getenv("MQTT_USERNAME")),
		mqttPassword:   strings.TrimSpace(os.Getenv("MQTT_PASSWORD")),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		queue:          make(chan navigationLiveActivityEvent, 128),
		priorityQueue:  make(chan navigationLiveActivityEvent, 16),
		pending:        map[int]*time.Timer{},
		store: navigationNotificationStore{
			Cars:      map[int]carNavigationState{},
			Delivered: map[string]string{},
		},
		history: navigationPushHistoryStore{Sessions: []navigationPushHistorySession{}},
	}
	monitor.loadHistory()
	monitor.loadPairing()
	monitor.enabled = monitor.installationID != "" && monitor.relayURL != "" && monitor.relaySecret != ""
	monitor.load()
	return monitor
}

func (m *navigationNotificationMonitor) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	enabled := m.enabled
	startWorker := enabled && !m.workerStarted
	if startWorker {
		m.workerStarted = true
	}
	m.mu.Unlock()
	if !enabled {
		log.Printf("[info] navigation Live Activity push disabled; relay pairing is not configured")
		return
	}
	if startWorker {
		// Terminal events use a dedicated worker so a slow/retrying ordinary
		// update can never keep a finished trip alive on the Lock Screen.
		go m.deliveryWorker(m.queue)
		go m.deliveryWorker(m.priorityQueue)
		go m.reconcileRestoredSessions(time.Now().UTC())
	}
	options := mqtt.NewClientOptions().
		AddBroker(m.mqttBroker).
		SetClientID("my-t-parking-navigation").
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetOrderMatters(false)
	if m.mqttUsername != "" {
		options.SetUsername(m.mqttUsername)
		options.SetPassword(m.mqttPassword)
	}
	options.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		m.mu.Lock()
		m.connected = false
		m.lastError = err.Error()
		m.mu.Unlock()
	})
	options.SetOnConnectHandler(func(client mqtt.Client) {
		token := client.SubscribeMultiple(map[string]byte{
			"teslamate/cars/+/display_name": 1,
			"teslamate/cars/+/state":        1,
			"teslamate/cars/+/geofence":     1,
			"teslamate/cars/+/active_route": 1,
		}, m.handleMQTTMessage)
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			m.mu.Lock()
			m.connected = true
			m.lastError = ""
			m.mu.Unlock()
			log.Printf("[info] subscribed to TeslaMate navigation MQTT topics")
			return
		}
		err := token.Error()
		if err == nil {
			err = fmt.Errorf("MQTT navigation subscription timed out")
		}
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
	})
	client := mqtt.NewClient(options)
	m.mu.Lock()
	m.client = client
	m.mu.Unlock()
	token := client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		m.mu.Lock()
		m.lastError = "MQTT navigation connection timed out"
		m.mu.Unlock()
		return
	}
	if err := token.Error(); err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
	}
}

func (m *navigationNotificationMonitor) stop() {
	m.mu.Lock()
	for carID, timer := range m.pending {
		timer.Stop()
		delete(m.pending, carID)
	}
	client := m.client
	m.client = nil
	m.connected = false
	m.started = false
	m.mu.Unlock()
	if client != nil && client.IsConnected() {
		client.Disconnect(250)
	}
}

func (m *navigationNotificationMonitor) configure(pairing softwarePushPairing) error {
	if len(pairing.InstallationID) != 48 {
		return fmt.Errorf("missing pairing values")
	}
	if _, err := hex.DecodeString(pairing.InstallationID); err != nil {
		return fmt.Errorf("invalid installation ID")
	}
	secret, err := hex.DecodeString(pairing.RelaySecret)
	if err != nil || len(secret) != 32 {
		return fmt.Errorf("invalid relay secret")
	}
	if !isTrustedSoftwarePushRelayURL(pairing.RelayURL) {
		return fmt.Errorf("untrusted relay URL")
	}
	m.mu.Lock()
	samePairing := m.installationID == pairing.InstallationID &&
		m.relayURL == pairing.RelayURL &&
		m.relaySecret == pairing.RelaySecret
	m.installationID = pairing.InstallationID
	m.relayURL = pairing.RelayURL
	m.relaySecret = pairing.RelaySecret
	m.enabled = true
	if samePairing && m.started {
		m.mu.Unlock()
		return nil
	}
	oldClient := m.client
	m.client = nil
	m.connected = false
	m.started = false
	m.mu.Unlock()
	if oldClient != nil && oldClient.IsConnected() {
		oldClient.Disconnect(250)
	}
	m.start()
	return nil
}

func (m *navigationNotificationMonitor) disable() {
	m.stop()
	m.mu.Lock()
	m.enabled = false
	m.installationID = ""
	m.relayURL = ""
	m.relaySecret = ""
	m.lastError = ""
	m.mu.Unlock()
}

func (m *navigationNotificationMonitor) loadPairing() {
	path := filepath.Join(filepath.Dir(getenv("PUSH_STATE_PATH", "/data/software-notifications.json")), "software-push-pairing.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pairing softwarePushPairing
	if json.Unmarshal(data, &pairing) == nil {
		m.installationID = pairing.InstallationID
		m.relayURL = pairing.RelayURL
		m.relaySecret = pairing.RelaySecret
	}
}

func (m *navigationNotificationMonitor) handleMQTTMessage(_ mqtt.Client, message mqtt.Message) {
	parts := strings.Split(message.Topic(), "/")
	if len(parts) != 4 || parts[0] != "teslamate" || parts[1] != "cars" {
		return
	}
	carID, err := strconv.Atoi(parts[2])
	if err != nil || carID <= 0 {
		return
	}
	m.observe(carID, parts[3], strings.TrimSpace(string(message.Payload())), time.Now().UTC())
}

func (m *navigationNotificationMonitor) observe(carID int, field, value string, observedAt time.Time) {
	m.mu.Lock()
	state := m.store.Cars[carID]
	state.LastObservedAt = observedAt.Format(time.RFC3339)
	wasActive := state.Active
	routeInvalid := false
	previousDestination := state.Destination
	previousRemainingDistanceKM := cloneFloat(state.RemainingDistanceKM)
	switch field {
	case "display_name":
		state.DisplayName = collapsedDisplayName(value)
	case "state":
		state.VehicleState = strings.ToLower(normalizedMQTTValue(value))
	case "geofence":
		state.Geofence = normalizedMQTTValue(value)
		if state.Geofence != "" {
			state.LastKnownGeofence = state.Geofence
		}
	case "active_route":
		var route activeRouteMQTT
		if json.Unmarshal([]byte(value), &route) != nil || route.Error != nil ||
			strings.TrimSpace(route.Destination) == "" {
			routeInvalid = true
			if !wasActive {
				state.Destination = ""
				state.RemainingDistanceKM = nil
				state.RemainingMinutes = nil
				state.ArrivalBatteryLevel = nil
			}
		} else {
			state.Destination = strings.TrimSpace(route.Destination)
			if route.MilesToArrival != nil {
				km := *route.MilesToArrival * 1.609344
				state.RemainingDistanceKM = &km
			}
			if route.MinutesToArrival != nil {
				minutes := int(*route.MinutesToArrival + 0.5)
				state.RemainingMinutes = &minutes
			}
			if route.EnergyAtArrival != nil && *route.EnergyAtArrival >= 0 && *route.EnergyAtArrival <= 100 {
				state.ArrivalBatteryLevel = route.EnergyAtArrival
			}
		}
	default:
		m.mu.Unlock()
		return
	}

	// Destination and confirmed driving are sufficient to start. TeslaMate can
	// legitimately publish the route label before distance/minutes; those
	// optional values will populate in later updates.
	shouldBeActive := navigationShouldBeActive(routeInvalid, state)

	// Mid-drive destination change: close previous session as redirected, open a new one.
	if wasActive && shouldBeActive && state.SessionID != "" &&
		previousDestination != "" && state.Destination != "" &&
		navigationDestinationChangeStartsNewSession(
			previousDestination,
			state.Destination,
			previousRemainingDistanceKM,
			state.RemainingDistanceKM,
		) {
		if timer := m.pending[carID]; timer != nil {
			timer.Stop()
			delete(m.pending, carID)
		}
		endState := state
		endState.Destination = previousDestination
		endState.Active = false
		endEvent := m.makeEventLocked(carID, &endState, "navigation_ended", observedAt)
		endEvent.Destination = previousDestination
		endEvent.endReasonOverride = "redirected"
		endEvent.EndReason = "redirected"

		driveID, _, _ := currentDriveDistances(carID)
		state.Active = true
		state.DriveID = driveID
		state.SessionID = navigationSessionID(m.installationID, carID, driveID, observedAt)
		state.SessionStartedAt = observedAt.Format(time.RFC3339)
		state.Sequence = 0
		state.StartDelivered = false
		state.LastQueuedAt = ""
		// New session for the new destination — re-resolve start place for this leg.
		state.StartName = resolveNavigationStartName(carID, state.Geofence, state.LastKnownGeofence)
		startEvent := m.makeEventLocked(carID, &state, "navigation_started", observedAt)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		m.mu.Unlock()
		m.enqueue(endEvent)
		m.enqueue(startEvent)
		return
	}

	if shouldBeActive && (!wasActive || state.SessionID == "") {
		driveID, _, _ := currentDriveDistances(carID)
		state.Active = true
		state.DriveID = driveID
		state.SessionID = navigationSessionID(m.installationID, carID, driveID, observedAt)
		state.SessionStartedAt = observedAt.Format(time.RFC3339)
		state.Sequence = 0
		state.StartDelivered = false
		state.LastQueuedAt = ""
		state.StartName = resolveNavigationStartName(carID, state.Geofence, state.LastKnownGeofence)
		event := m.makeEventLocked(carID, &state, "navigation_started", observedAt)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		m.mu.Unlock()
		m.enqueue(event)
		return
	}
	if !shouldBeActive && wasActive {
		state.Active = false
		if timer := m.pending[carID]; timer != nil {
			timer.Stop()
			delete(m.pending, carID)
		}
		event := m.makeEventLocked(carID, &state, "navigation_ended", observedAt)
		if routeInvalid {
			state.Destination = ""
			state.RemainingDistanceKM = nil
			state.RemainingMinutes = nil
			state.ArrivalBatteryLevel = nil
		}
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		m.mu.Unlock()
		m.enqueue(event)
		return
	}
	// TeslaMate may reverse-geocode the drive start only after the first points land.
	// Keep trying until we freeze a non-empty start_name for push history / App titles.
	if state.Active && strings.TrimSpace(state.StartName) == "" {
		if filled := resolveNavigationStartName(carID, state.Geofence, state.LastKnownGeofence); filled != "" {
			state.StartName = filled
		}
	}
	m.store.Cars[carID] = state
	_ = m.saveLocked()
	if state.Active {
		if !state.StartDelivered {
			lastQueued, _ := time.Parse(time.RFC3339, state.LastQueuedAt)
			if lastQueued.IsZero() || observedAt.Sub(lastQueued) >= navigationUpdateMinimumInterval {
				event := m.makeEventLocked(carID, &state, "navigation_started", observedAt)
				m.store.Cars[carID] = state
				_ = m.saveLocked()
				m.mu.Unlock()
				m.enqueue(event)
				return
			}
		} else {
			m.scheduleUpdateLocked(carID, observedAt)
		}
	}
	m.mu.Unlock()
}

func navigationShouldBeActive(routeInvalid bool, state carNavigationState) bool {
	return !routeInvalid && state.VehicleState == "driving" && state.Destination != ""
}

func (m *navigationNotificationMonitor) scheduleUpdateLocked(carID int, observedAt time.Time) {
	state := m.store.Cars[carID]
	if !state.Active || !state.StartDelivered || state.SessionID == "" {
		return
	}
	lastQueued, _ := time.Parse(time.RFC3339, state.LastQueuedAt)
	delay := navigationUpdateMinimumInterval - observedAt.Sub(lastQueued)
	if lastQueued.IsZero() || delay <= 0 {
		event := m.makeEventLocked(carID, &state, "navigation_updated", observedAt)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		// Caller already holds m.mu — must not re-enter enqueue/recordHistoryEvent.
		m.enqueueAlreadyLocked(event)
		return
	}
	if _, exists := m.pending[carID]; exists {
		return
	}
	m.pending[carID] = time.AfterFunc(delay, func() {
		m.flushUpdate(carID)
	})
}

func (m *navigationNotificationMonitor) flushUpdate(carID int) {
	m.mu.Lock()
	delete(m.pending, carID)
	state := m.store.Cars[carID]
	if !state.Active || !state.StartDelivered {
		m.mu.Unlock()
		return
	}
	event := m.makeEventLocked(carID, &state, "navigation_updated", time.Now().UTC())
	m.store.Cars[carID] = state
	_ = m.saveLocked()
	m.enqueueAlreadyLocked(event)
	m.mu.Unlock()
}

func (m *navigationNotificationMonitor) makeEventLocked(
	carID int,
	state *carNavigationState,
	eventType string,
	observedAt time.Time,
) navigationLiveActivityEvent {
	state.Sequence++
	state.LastQueuedAt = observedAt.Format(time.RFC3339)
	driveID, driven, verified := currentDriveDistances(carID)
	if driveID != 0 {
		state.DriveID = driveID
	}
	var total *float64
	if verified && driven != nil && state.RemainingDistanceKM != nil {
		value := *driven + *state.RemainingDistanceKM
		total = &value
	}
	var eta *int64
	if state.RemainingMinutes != nil {
		value := observedAt.Add(time.Duration(*state.RemainingMinutes) * time.Minute).Unix()
		eta = &value
	}
	var tripStartedUnix *int64
	var tripEndedUnix *int64
	var durationMin *int
	if state.SessionStartedAt != "" {
		if startedAt, err := time.Parse(time.RFC3339, state.SessionStartedAt); err == nil {
			v := startedAt.Unix()
			tripStartedUnix = &v
			if eventType == "navigation_ended" {
				endV := observedAt.Unix()
				tripEndedUnix = &endV
				mins := int(observedAt.Sub(startedAt).Minutes() + 0.5)
				if mins < 0 {
					mins = 0
				}
				durationMin = &mins
			}
		}
	}
	idInput := fmt.Sprintf("%s:%s:%s:%d", m.installationID, state.SessionID, eventType, state.Sequence)
	idHash := sha256.Sum256([]byte(idInput))
	event := navigationLiveActivityEvent{
		EventID:               hex.EncodeToString(idHash[:16]),
		InstallationID:        m.installationID,
		CarID:                 carID,
		VehicleName:           state.DisplayName,
		Type:                  eventType,
		SessionID:             state.SessionID,
		Destination:           state.Destination,
		StartName:             state.StartName,
		RemainingDistanceKM:   cloneFloat(state.RemainingDistanceKM),
		RemainingMinutes:      cloneInt(state.RemainingMinutes),
		EstimatedArrivalAt:    eta,
		ArrivalBatteryLevel:   cloneInt(state.ArrivalBatteryLevel),
		DrivenDistanceKM:      driven,
		TotalDistanceKM:       total,
		HasVerifiedTrajectory: verified,
		TripStartedAt:         tripStartedUnix,
		TripEndedAt:           tripEndedUnix,
		DurationMinutes:       durationMin,
		ObservedAt:            observedAt.Format(time.RFC3339),
	}
	if eventType == "navigation_ended" {
		event.EndReason = navigationEndReason(event)
	}
	return event
}

// resolveNavigationStartName prefers (1) live MQTT geofence, (2) sticky last
// non-empty geofence, (3) open-drive start geofence/address, (4) first position
// address on the open drive, (5) previous completed drive end place.
// Empty string means unknown — App may still fall back to matching a TeslaMate drive.
func resolveNavigationStartName(carID int, liveGeofence, lastKnownGeofence string) string {
	for _, candidate := range []string{liveGeofence, lastKnownGeofence} {
		if name := strings.TrimSpace(candidate); name != "" {
			return name
		}
	}
	if db == nil {
		return ""
	}
	if label := queryOpenDriveStartLabel(carID); label != "" {
		return label
	}
	if label := queryOpenDriveFirstPositionAddress(carID); label != "" {
		return label
	}
	return queryLastCompletedDriveEndLabel(carID)
}

func queryOpenDriveStartLabel(carID int) string {
	var label string
	err := db.QueryRow(`
		SELECT COALESCE(
			NULLIF(TRIM(g.name), ''),
			NULLIF(TRIM(a.name), ''),
			NULLIF(TRIM(a.display_name), ''),
			''
		)
		FROM drives d
		LEFT JOIN geofences g ON g.id = d.start_geofence_id
		LEFT JOIN addresses a ON a.id = d.start_address_id
		WHERE d.car_id = $1 AND d.end_date IS NULL
		ORDER BY d.start_date DESC, d.id DESC
		LIMIT 1`, carID).Scan(&label)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(label)
}

func queryOpenDriveFirstPositionAddress(carID int) string {
	var label string
	// Prefer the earliest position that already has a reverse-geocoded address.
	err := db.QueryRow(`
		SELECT COALESCE(
			NULLIF(TRIM(a.name), ''),
			NULLIF(TRIM(a.display_name), ''),
			''
		)
		FROM drives d
		JOIN positions p ON p.drive_id = d.id
		JOIN addresses a ON a.id = p.address_id
		WHERE d.car_id = $1 AND d.end_date IS NULL
		ORDER BY p.date ASC NULLS LAST, p.id ASC
		LIMIT 1`, carID).Scan(&label)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(label)
}

func queryLastCompletedDriveEndLabel(carID int) string {
	var label string
	err := db.QueryRow(`
		SELECT COALESCE(
			NULLIF(TRIM(g.name), ''),
			NULLIF(TRIM(a.name), ''),
			NULLIF(TRIM(a.display_name), ''),
			''
		)
		FROM drives d
		LEFT JOIN geofences g ON g.id = d.end_geofence_id
		LEFT JOIN addresses a ON a.id = d.end_address_id
		WHERE d.car_id = $1 AND d.end_date IS NOT NULL
		ORDER BY d.end_date DESC, d.id DESC
		LIMIT 1`, carID).Scan(&label)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(label)
}

func currentDriveDistances(carID int) (int64, *float64, bool) {
	if db == nil {
		return 0, nil, false
	}
	var driveID int64
	var firstOdometer, latestOdometer float64
	var count int
	err := db.QueryRow(`
		SELECT d.id,
		       first_position.odometer,
		       latest_position.odometer,
		       position_count.count
		FROM drives d
		JOIN LATERAL (
			SELECT odometer FROM positions
			WHERE drive_id = d.id AND odometer IS NOT NULL
			ORDER BY id ASC LIMIT 1
		) first_position ON true
		JOIN LATERAL (
			SELECT odometer FROM positions
			WHERE drive_id = d.id AND odometer IS NOT NULL
			ORDER BY id DESC LIMIT 1
		) latest_position ON true
		JOIN LATERAL (
			SELECT COUNT(*)::int AS count FROM positions WHERE drive_id = d.id
		) position_count ON true
		WHERE d.car_id = $1 AND d.end_date IS NULL
		ORDER BY d.start_date DESC, d.id DESC
		LIMIT 1`, carID).Scan(&driveID, &firstOdometer, &latestOdometer, &count)
	if err != nil || count < 2 || latestOdometer < firstOdometer {
		return driveID, nil, false
	}
	driven := latestOdometer - firstOdometer
	return driveID, &driven, true
}

func (m *navigationNotificationMonitor) enqueue(event navigationLiveActivityEvent) {
	m.mu.Lock()
	m.enqueueAlreadyLocked(event)
	m.mu.Unlock()
}

// enqueueAlreadyLocked records history and enqueues delivery. Caller must hold m.mu.
func (m *navigationNotificationMonitor) enqueueAlreadyLocked(event navigationLiveActivityEvent) {
	m.recordHistoryEventLocked(event, "queued")
	if event.Type == "navigation_ended" && m.priorityQueue != nil {
		select {
		case m.priorityQueue <- event:
		default:
			log.Printf("[error] navigation priority queue is full; event=%s", event.EventID)
		}
		return
	}
	select {
	case m.queue <- event:
	default:
		log.Printf("[error] navigation event queue is full; event=%s", event.EventID)
	}
}

func (m *navigationNotificationMonitor) deliveryWorker(events <-chan navigationLiveActivityEvent) {
	for event := range events {
		delays := []time.Duration{0, 5 * time.Second, 30 * time.Second}
		for attempt, delay := range delays {
			if delay > 0 {
				time.Sleep(delay)
			}
			if err := m.deliver(event); err != nil {
				m.mu.Lock()
				m.lastError = err.Error()
				m.recordHistoryEventLocked(event, "failed")
				m.mu.Unlock()
				log.Printf("[warn] navigation relay event=%s attempt=%d: %v", event.EventID, attempt+1, err)
				continue
			}
			m.mu.Lock()
			m.store.Delivered[event.EventID] = event.ObservedAt
			state := m.store.Cars[event.CarID]
			if event.Type == "navigation_started" && state.SessionID == event.SessionID {
				state.StartDelivered = true
				state.LastQueuedAt = ""
				m.store.Cars[event.CarID] = state
				if state.Active {
					m.scheduleUpdateLocked(event.CarID, time.Now().UTC())
				}
			}
			now := time.Now().UTC()
			m.lastEventAt = &now
			m.lastError = ""
			m.recordHistoryEventLocked(event, "delivered")
			_ = m.saveLocked()
			m.mu.Unlock()
			break
		}
	}
}

func (m *navigationNotificationMonitor) reconcileRestoredSessions(startedAt time.Time) {
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	<-timer.C

	var endings []navigationLiveActivityEvent
	m.mu.Lock()
	for carID, state := range m.store.Cars {
		if !state.Active || state.SessionID == "" {
			continue
		}
		observedAt, _ := time.Parse(time.RFC3339, state.LastObservedAt)
		if observedAt.After(startedAt) {
			continue
		}
		// No fresh retained MQTT state arrived after restart. End the orphan
		// rather than silently deleting it and leaving the Lock Screen stuck.
		state.Active = false
		event := m.makeEventLocked(carID, &state, "navigation_ended", time.Now().UTC())
		m.store.Cars[carID] = state
		endings = append(endings, event)
	}
	_ = m.saveLocked()
	m.mu.Unlock()
	for _, event := range endings {
		m.enqueue(event)
	}
}

func (m *navigationNotificationMonitor) deliver(event navigationLiveActivityEvent) error {
	laErr := m.deliverTo(event, func(s pushSubscriber) bool { return s.NavigationLiveActivity })
	alertType := ""
	switch event.Type {
	case "navigation_started":
		alertType = "destination_trip_started"
	case "navigation_ended":
		if navigationEndReason(event) == "arrived" {
			alertType = "destination_trip_arrived"
		}
	}
	var alertErr error
	if alertType != "" {
		alertEvent := event
		alertEvent.Type = alertType
		alertEvent.EventID = event.EventID + ":" + alertType
		alertErr = m.deliverTo(alertEvent, func(s pushSubscriber) bool { return s.NavigationTripAlerts })
	}
	if laErr != nil {
		return laErr
	}
	return alertErr
}

func (m *navigationNotificationMonitor) deliverTo(
	event navigationLiveActivityEvent,
	pred func(pushSubscriber) bool,
) error {
	subs := []pushSubscriber{}
	if pushRegistry != nil {
		subs = pushRegistry.matching(event.CarID, pred)
	} else if m.installationID != "" && pred(pushSubscriber{
		InstallationID:         m.installationID,
		NavigationLiveActivity: true,
		Status:                 pushStatusActive,
	}) {
		subs = []pushSubscriber{{
			InstallationID:         m.installationID,
			RelayURL:               m.relayURL,
			RelaySecret:            m.relaySecret,
			NavigationLiveActivity: true,
			Status:                 pushStatusActive,
		}}
	}
	if len(subs) == 0 {
		return nil
	}
	var last error
	ok := 0
	for _, sub := range subs {
		copy := event
		copy.InstallationID = sub.InstallationID
		payload, err := json.Marshal(copy)
		if err != nil {
			last = err
			continue
		}
		if pushRegistry != nil {
			err = pushRegistry.deliverJSON(sub, payload)
		} else {
			signature := hmac.New(sha256.New, relaySecretBytes(sub.RelaySecret))
			_, _ = signature.Write(payload)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			request, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, sub.RelayURL, bytes.NewReader(payload))
			if reqErr != nil {
				cancel()
				last = reqErr
				continue
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-My-T-Installation", sub.InstallationID)
			request.Header.Set("X-My-T-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
			response, doErr := m.httpClient.Do(request)
			cancel()
			if doErr != nil {
				last = doErr
				continue
			}
			response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = fmt.Errorf("relay returned HTTP %d", response.StatusCode)
			}
		}
		if err != nil {
			last = err
			log.Printf("[warn] navigation fan-out installation=%s: %v", sub.InstallationID[:8], err)
			continue
		}
		ok++
	}
	if ok == 0 {
		if last == nil {
			return fmt.Errorf("no navigation subscribers")
		}
		return last
	}
	return nil
}

func (m *navigationNotificationMonitor) status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := 0
	for _, state := range m.store.Cars {
		if state.Active {
			active++
		}
	}
	result := map[string]any{
		"enabled":                m.enabled,
		"mqtt_connected":         m.connected,
		"delivery_mode":          "activitykit_push_to_start",
		"privacy_mode":           "navigation_summary_only_no_location_or_trajectory",
		"active_sessions":        active,
		"delivered_events":       len(m.store.Delivered),
		"minimum_update_seconds": int(navigationUpdateMinimumInterval.Seconds()),
	}
	if m.lastEventAt != nil {
		result["last_delivered_at"] = m.lastEventAt.Format(time.RFC3339)
	}
	if m.lastError != "" {
		result["last_error"] = m.lastError
	}
	return result
}

func (m *navigationNotificationMonitor) load() {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var stored navigationNotificationStore
	if json.Unmarshal(data, &stored) == nil {
		if stored.Cars != nil {
			m.store.Cars = stored.Cars
		}
		if stored.Delivered != nil {
			m.store.Delivered = stored.Delivered
		}
		now := time.Now().UTC()
		pruneTimestampMap(m.store.Delivered, now, navigationDeliveredRetention, navigationDeliveredMaximum)
		// Active sessions are reconciled after MQTT reconnect. Never silently
		// delete one here because that would strand its Live Activity.
	}
}

func (m *navigationNotificationMonitor) saveLocked() error {
	pruneTimestampMap(m.store.Delivered, time.Now().UTC(), navigationDeliveredRetention, navigationDeliveredMaximum)
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(m.store)
	if err != nil {
		return err
	}
	temp := m.statePath + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, m.statePath)
}

func navigationSessionID(installationID string, carID int, driveID int64, startedAt time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d", installationID, carID, driveID, startedAt.Unix())))
	return "navigation-" + hex.EncodeToString(sum[:16])
}

func (m *navigationNotificationMonitor) recordHistoryEvent(event navigationLiveActivityEvent, delivery string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordHistoryEventLocked(event, delivery)
}

func (m *navigationNotificationMonitor) recordHistoryEventLocked(event navigationLiveActivityEvent, delivery string) {
	if strings.TrimSpace(event.SessionID) == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	idx := -1
	for i := range m.history.Sessions {
		if m.history.Sessions[i].SessionID == event.SessionID {
			idx = i
			break
		}
	}
	if idx < 0 {
		session := navigationPushHistorySession{
			SessionID:          event.SessionID,
			CarID:              event.CarID,
			VehicleName:        event.VehicleName,
			StartName:          strings.TrimSpace(event.StartName),
			Destination:        event.Destination,
			StartedAt:          event.ObservedAt,
			LastEventType:      event.Type,
			LastDeliveryStatus: delivery,
			UpdatedAt:          now,
		}
		if event.Type == "navigation_started" || event.Type == "navigation_updated" {
			session.StartRemainingDistanceKM = cloneFloat(event.RemainingDistanceKM)
			session.StartRemainingMinutes = cloneInt(event.RemainingMinutes)
			session.StartEstimatedArrivalAt = cloneInt64(event.EstimatedArrivalAt)
		}
		session.LastRemainingDistanceKM = cloneFloat(event.RemainingDistanceKM)
		session.LastRemainingMinutes = cloneInt(event.RemainingMinutes)
		session.LastEstimatedArrivalAt = cloneInt64(event.EstimatedArrivalAt)
		session.DrivenDistanceKM = cloneFloat(event.DrivenDistanceKM)
		session.TotalDistanceKM = cloneFloat(event.TotalDistanceKM)
		if event.Type == "navigation_ended" {
			session.EndedAt = event.ObservedAt
			session.EndReason = navigationEndReason(event)
			session.EndRemainingDistanceKM = cloneFloat(event.RemainingDistanceKM)
			session.EndRemainingMinutes = cloneInt(event.RemainingMinutes)
		}
		m.history.Sessions = append([]navigationPushHistorySession{session}, m.history.Sessions...)
	} else {
		session := m.history.Sessions[idx]
		if event.VehicleName != "" {
			session.VehicleName = event.VehicleName
		}
		// Freeze start name on first value; never overwrite with later empties.
		if session.StartName == "" && strings.TrimSpace(event.StartName) != "" {
			session.StartName = strings.TrimSpace(event.StartName)
		}
		if event.Destination != "" {
			session.Destination = event.Destination
		}
		if session.StartedAt == "" {
			session.StartedAt = event.ObservedAt
		}
		if session.StartRemainingDistanceKM == nil && event.RemainingDistanceKM != nil {
			session.StartRemainingDistanceKM = cloneFloat(event.RemainingDistanceKM)
		}
		if session.StartRemainingMinutes == nil && event.RemainingMinutes != nil {
			session.StartRemainingMinutes = cloneInt(event.RemainingMinutes)
		}
		if session.StartEstimatedArrivalAt == nil && event.EstimatedArrivalAt != nil {
			session.StartEstimatedArrivalAt = cloneInt64(event.EstimatedArrivalAt)
		}
		session.LastRemainingDistanceKM = cloneFloat(event.RemainingDistanceKM)
		session.LastRemainingMinutes = cloneInt(event.RemainingMinutes)
		session.LastEstimatedArrivalAt = cloneInt64(event.EstimatedArrivalAt)
		if event.DrivenDistanceKM != nil {
			session.DrivenDistanceKM = cloneFloat(event.DrivenDistanceKM)
		}
		if event.TotalDistanceKM != nil {
			session.TotalDistanceKM = cloneFloat(event.TotalDistanceKM)
		}
		session.LastEventType = event.Type
		session.LastDeliveryStatus = delivery
		session.UpdatedAt = now
		if event.Type == "navigation_ended" {
			session.EndedAt = event.ObservedAt
			session.EndReason = navigationEndReason(event)
			session.EndRemainingDistanceKM = cloneFloat(event.RemainingDistanceKM)
			session.EndRemainingMinutes = cloneInt(event.RemainingMinutes)
		}
		m.history.Sessions[idx] = session
	}
	if len(m.history.Sessions) > navigationPushHistoryLimit {
		m.history.Sessions = m.history.Sessions[:navigationPushHistoryLimit]
	}
	_ = m.saveHistoryLocked()
}

func navigationEndReason(event navigationLiveActivityEvent) string {
	if strings.TrimSpace(event.EndReason) != "" {
		return strings.TrimSpace(event.EndReason)
	}
	if strings.TrimSpace(event.endReasonOverride) != "" {
		return strings.TrimSpace(event.endReasonOverride)
	}
	if event.RemainingDistanceKM != nil && *event.RemainingDistanceKM <= 0.35 {
		return "arrived"
	}
	if event.RemainingMinutes != nil && *event.RemainingMinutes <= 2 {
		return "arrived"
	}
	return "navigation_ended"
}

func navigationDestinationEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// Tesla may replace a street address with its geofence label (for example,
// "20 Hi Mount Dr" -> "Home") while the physical route remains unchanged.
// Treat a label-only change with nearly identical remaining distance as the
// same session; a material route-distance change still starts a new session.
func navigationDestinationChangeStartsNewSession(
	previousDestination string,
	currentDestination string,
	previousRemainingDistanceKM *float64,
	currentRemainingDistanceKM *float64,
) bool {
	if navigationDestinationEqual(previousDestination, currentDestination) {
		return false
	}
	// Tesla often publishes the new label before miles/minutes. Missing
	// remaining distance is a label swap, not a new route.
	guardedPrevious := previousRemainingDistanceKM
	guardedCurrent := currentRemainingDistanceKM
	if guardedPrevious == nil || guardedCurrent == nil {
		return false
	}
	previous := math.Max(0, *guardedPrevious)
	current := math.Max(0, *guardedCurrent)
	tolerance := math.Min(3, math.Max(0.8, math.Max(previous, current)*0.12))
	return math.Abs(previous-current) > tolerance
}

func (m *navigationNotificationMonitor) historyForCar(carID int, limit int) []navigationPushHistorySession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > navigationPushHistoryLimit {
		limit = 50
	}
	out := make([]navigationPushHistorySession, 0, limit)
	for _, session := range m.history.Sessions {
		if carID > 0 && session.CarID != carID {
			continue
		}
		out = append(out, session)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (m *navigationNotificationMonitor) loadHistory() {
	data, err := os.ReadFile(m.historyPath)
	if err != nil {
		return
	}
	var stored navigationPushHistoryStore
	if json.Unmarshal(data, &stored) == nil && stored.Sessions != nil {
		m.history.Sessions = stored.Sessions
	}
}

func (m *navigationNotificationMonitor) saveHistoryLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.historyPath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(m.history)
	if err != nil {
		return err
	}
	temp := m.historyPath + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, m.historyPath)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
