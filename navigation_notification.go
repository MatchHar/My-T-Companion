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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const navigationUpdateMinimumInterval = 15 * time.Second

type navigationLiveActivityEvent struct {
	EventID               string   `json:"event_id"`
	InstallationID        string   `json:"installation_id"`
	CarID                 int      `json:"car_id"`
	VehicleName           string   `json:"vehicle_name,omitempty"`
	Type                  string   `json:"type"`
	SessionID             string   `json:"session_id"`
	Destination           string   `json:"destination"`
	RemainingDistanceKM   *float64 `json:"remaining_distance_km,omitempty"`
	RemainingMinutes      *int     `json:"remaining_minutes,omitempty"`
	EstimatedArrivalAt    *int64   `json:"estimated_arrival_at,omitempty"`
	ArrivalBatteryLevel   *int     `json:"arrival_battery_level,omitempty"`
	DrivenDistanceKM      *float64 `json:"driven_distance_km,omitempty"`
	TotalDistanceKM       *float64 `json:"total_distance_km,omitempty"`
	HasVerifiedTrajectory bool     `json:"has_verified_trajectory"`
	ObservedAt            string   `json:"observed_at"`
}

type activeRouteMQTT struct {
	Destination      string   `json:"destination"`
	EnergyAtArrival  *int     `json:"energy_at_arrival"`
	MilesToArrival   *float64 `json:"miles_to_arrival"`
	MinutesToArrival *float64 `json:"minutes_to_arrival"`
	Error            any      `json:"error"`
}

type carNavigationState struct {
	DisplayName         string   `json:"display_name,omitempty"`
	VehicleState        string   `json:"vehicle_state,omitempty"`
	Destination         string   `json:"destination,omitempty"`
	RemainingDistanceKM *float64 `json:"remaining_distance_km,omitempty"`
	RemainingMinutes    *int     `json:"remaining_minutes,omitempty"`
	ArrivalBatteryLevel *int     `json:"arrival_battery_level,omitempty"`
	Active              bool     `json:"active"`
	SessionID           string   `json:"session_id,omitempty"`
	DriveID             int64    `json:"drive_id,omitempty"`
	Sequence            int      `json:"sequence"`
	StartDelivered      bool     `json:"start_delivered"`
	LastQueuedAt        string   `json:"last_queued_at,omitempty"`
}

type navigationNotificationStore struct {
	Cars      map[int]carNavigationState `json:"cars"`
	Delivered map[string]string          `json:"delivered"`
}

type navigationNotificationMonitor struct {
	mu             sync.Mutex
	store          navigationNotificationStore
	statePath      string
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
	pending        map[int]*time.Timer
}

func newNavigationNotificationMonitorFromEnvironment() *navigationNotificationMonitor {
	monitor := &navigationNotificationMonitor{
		statePath:      getenv("NAVIGATION_PUSH_STATE_PATH", "/data/navigation-live-activities.json"),
		installationID: strings.TrimSpace(os.Getenv("PUSH_INSTALLATION_ID")),
		relayURL:       strings.TrimSpace(os.Getenv("PUSH_RELAY_URL")),
		relaySecret:    strings.TrimSpace(os.Getenv("PUSH_RELAY_SECRET")),
		mqttBroker:     getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883"),
		mqttUsername:   strings.TrimSpace(os.Getenv("MQTT_USERNAME")),
		mqttPassword:   strings.TrimSpace(os.Getenv("MQTT_PASSWORD")),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		queue:          make(chan navigationLiveActivityEvent, 128),
		pending:        map[int]*time.Timer{},
		store: navigationNotificationStore{
			Cars:      map[int]carNavigationState{},
			Delivered: map[string]string{},
		},
	}
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
		go m.deliveryWorker()
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
	if pairing.RelayURL != officialSoftwarePushRelayURL {
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
	wasActive := state.Active
	routeInvalid := false
	switch field {
	case "display_name":
		state.DisplayName = normalizedMQTTValue(value)
	case "state":
		state.VehicleState = strings.ToLower(normalizedMQTTValue(value))
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

	shouldBeActive := !routeInvalid && state.VehicleState == "driving" && state.Destination != "" &&
		state.RemainingDistanceKM != nil && state.RemainingMinutes != nil
	if shouldBeActive && (!wasActive || state.SessionID == "") {
		driveID, _, _ := currentDriveDistances(carID)
		state.Active = true
		state.DriveID = driveID
		state.SessionID = navigationSessionID(m.installationID, carID, driveID, observedAt)
		state.Sequence = 0
		state.StartDelivered = false
		state.LastQueuedAt = ""
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
		m.enqueue(event)
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
	m.mu.Unlock()
	m.enqueue(event)
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
	idInput := fmt.Sprintf("%s:%s:%s:%d", m.installationID, state.SessionID, eventType, state.Sequence)
	idHash := sha256.Sum256([]byte(idInput))
	return navigationLiveActivityEvent{
		EventID:               hex.EncodeToString(idHash[:16]),
		InstallationID:        m.installationID,
		CarID:                 carID,
		VehicleName:           state.DisplayName,
		Type:                  eventType,
		SessionID:             state.SessionID,
		Destination:           state.Destination,
		RemainingDistanceKM:   cloneFloat(state.RemainingDistanceKM),
		RemainingMinutes:      cloneInt(state.RemainingMinutes),
		EstimatedArrivalAt:    eta,
		ArrivalBatteryLevel:   cloneInt(state.ArrivalBatteryLevel),
		DrivenDistanceKM:      driven,
		TotalDistanceKM:       total,
		HasVerifiedTrajectory: verified,
		ObservedAt:            observedAt.Format(time.RFC3339),
	}
}

func currentDriveDistances(carID int) (int64, *float64, bool) {
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
	select {
	case m.queue <- event:
	default:
		log.Printf("[error] navigation event queue is full; event=%s", event.EventID)
	}
}

func (m *navigationNotificationMonitor) deliveryWorker() {
	for event := range m.queue {
		delays := []time.Duration{0, 5 * time.Second, 30 * time.Second}
		for attempt, delay := range delays {
			if delay > 0 {
				time.Sleep(delay)
			}
			if err := m.deliver(event); err != nil {
				m.mu.Lock()
				m.lastError = err.Error()
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
			_ = m.saveLocked()
			m.mu.Unlock()
			break
		}
	}
}

func (m *navigationNotificationMonitor) deliver(event navigationLiveActivityEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	signature := hmac.New(sha256.New, relaySecretBytes(m.relaySecret))
	_, _ = signature.Write(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.relayURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-My-T-Installation", m.installationID)
	request.Header.Set("X-My-T-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
	response, err := m.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("relay returned HTTP %d", response.StatusCode)
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
		for carID, state := range m.store.Cars {
			if state.Active && timestampIsOlderThan(state.LastQueuedAt, now, navigationTransientMaximumAge) {
				state.Active = false
				state.SessionID = ""
				state.DriveID = 0
				state.StartDelivered = false
				state.LastQueuedAt = ""
				m.store.Cars[carID] = state
			}
		}
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

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
