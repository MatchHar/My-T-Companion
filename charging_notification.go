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

const (
	chargingUpdateMinimumInterval     = 45 * time.Second
	fastChargingUpdateMinimumInterval = 15 * time.Second
	fastChargingPowerThresholdKW      = 50.0
)

type chargingLiveActivityEvent struct {
	EventID             string   `json:"event_id"`
	InstallationID      string   `json:"installation_id"`
	CarID               int      `json:"car_id"`
	VehicleName         string   `json:"vehicle_name,omitempty"`
	Type                string   `json:"type"`
	SessionID           string   `json:"session_id"`
	StartBatteryLevel   int      `json:"start_battery_level"`
	BatteryLevel        int      `json:"battery_level"`
	AddedBatteryPercent int      `json:"added_battery_percent"`
	TargetLevel         int      `json:"target_level"`
	StartRatedRangeKM   *float64 `json:"start_rated_range_km,omitempty"`
	RatedRangeKM        *float64 `json:"rated_range_km,omitempty"`
	AddedRangeKM        *float64 `json:"added_range_km,omitempty"`
	PowerKW             *float64 `json:"power_kw,omitempty"`
	RemainingSeconds    *int     `json:"remaining_seconds,omitempty"`
	EstimatedCompleteAt *int64   `json:"estimated_complete_at,omitempty"`
	ObservedAt          string   `json:"observed_at"`
}

type carChargingState struct {
	DisplayName       string   `json:"display_name,omitempty"`
	VehicleState      string   `json:"vehicle_state,omitempty"`
	ChargingState     string   `json:"charging_state,omitempty"`
	BatteryLevel      *int     `json:"battery_level,omitempty"`
	ChargeLimitSOC    *int     `json:"charge_limit_soc,omitempty"`
	RatedRangeKM      *float64 `json:"rated_range_km,omitempty"`
	ChargerPowerKW    *float64 `json:"charger_power_kw,omitempty"`
	TimeToFullHours   *float64 `json:"time_to_full_hours,omitempty"`
	Active            bool     `json:"active"`
	SessionID         string   `json:"session_id,omitempty"`
	SessionStartedAt  string   `json:"session_started_at,omitempty"`
	StartBatteryLevel int      `json:"start_battery_level,omitempty"`
	StartRatedRangeKM *float64 `json:"start_rated_range_km,omitempty"`
	StartDelivered    bool     `json:"start_delivered"`
	Sequence          int      `json:"sequence"`
	LastQueuedAt      string   `json:"last_queued_at,omitempty"`
}

type chargingNotificationStore struct {
	Cars      map[int]carChargingState `json:"cars"`
	Delivered map[string]string        `json:"delivered"`
}

type chargingNotificationMonitor struct {
	mu             sync.Mutex
	store          chargingNotificationStore
	statePath      string
	installationID string
	relayURL       string
	relaySecret    string
	mqttBroker     string
	mqttClientID   string
	mqttUsername   string
	mqttPassword   string
	client         mqtt.Client
	httpClient     *http.Client
	enabled        bool
	connected      bool
	lastEventAt    *time.Time
	lastError      string
	started        bool
	workerStarted  bool
	pending        map[int]*time.Timer
	queue          chan chargingLiveActivityEvent
}

func newChargingNotificationMonitorFromEnvironment() *chargingNotificationMonitor {
	monitor := &chargingNotificationMonitor{
		statePath:      getenv("CHARGING_PUSH_STATE_PATH", "/data/charging-live-activities.json"),
		installationID: strings.TrimSpace(os.Getenv("PUSH_INSTALLATION_ID")),
		relayURL:       strings.TrimSpace(os.Getenv("PUSH_RELAY_URL")),
		relaySecret:    strings.TrimSpace(os.Getenv("PUSH_RELAY_SECRET")),
		mqttBroker:     getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883"),
		mqttClientID:   getenv("MQTT_CLIENT_ID", "my-t-companion") + "-charging",
		mqttUsername:   strings.TrimSpace(os.Getenv("MQTT_USERNAME")),
		mqttPassword:   strings.TrimSpace(os.Getenv("MQTT_PASSWORD")),
		httpClient:     newPushRelayHTTPClient(12 * time.Second),
		pending:        map[int]*time.Timer{},
		queue:          make(chan chargingLiveActivityEvent, 64),
		store: chargingNotificationStore{
			Cars:      map[int]carChargingState{},
			Delivered: map[string]string{},
		},
	}
	monitor.load()
	monitor.loadPairing()
	monitor.enabled = monitor.installationID != "" && monitor.relayURL != "" && monitor.relaySecret != ""
	return monitor
}

func (m *chargingNotificationMonitor) start() {
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
		log.Printf("[info] charging Live Activity push disabled; relay pairing is not configured")
		return
	}

	if startWorker {
		go m.deliveryWorker()
	}
	options := mqtt.NewClientOptions().
		AddBroker(m.mqttBroker).
		SetClientID(m.mqttClientID).
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
		log.Printf("[warn] charging MQTT disconnected: %v", err)
	})
	options.SetOnConnectHandler(func(client mqtt.Client) {
		token := client.SubscribeMultiple(map[string]byte{
			"teslamate/cars/+/display_name":           1,
			"teslamate/cars/+/state":                  1,
			"teslamate/cars/+/charging_state":         1,
			"teslamate/cars/+/battery_level":          1,
			"teslamate/cars/+/charge_limit_soc":       1,
			"teslamate/cars/+/rated_battery_range_km": 1,
			"teslamate/cars/+/charger_power":          1,
			"teslamate/cars/+/time_to_full_charge":    1,
		}, m.handleMQTTMessage)
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			m.mu.Lock()
			m.connected = true
			m.lastError = ""
			m.mu.Unlock()
			log.Printf("[info] subscribed to TeslaMate charging MQTT topics")
			return
		}
		err := token.Error()
		if err == nil {
			err = fmt.Errorf("MQTT charging subscription timed out")
		}
		m.mu.Lock()
		m.connected = false
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
		m.lastError = "MQTT charging connection timed out"
		m.mu.Unlock()
		return
	}
	if err := token.Error(); err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("[warn] TeslaMate charging MQTT initial connection: %v", err)
	}
}

func (m *chargingNotificationMonitor) stop() {
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

func (m *chargingNotificationMonitor) configure(pairing softwarePushPairing) error {
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

func (m *chargingNotificationMonitor) disable() {
	m.stop()
	m.mu.Lock()
	m.enabled = false
	m.installationID = ""
	m.relayURL = ""
	m.relaySecret = ""
	m.lastError = ""
	m.mu.Unlock()
}

func (m *chargingNotificationMonitor) loadPairing() {
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

func (m *chargingNotificationMonitor) handleMQTTMessage(_ mqtt.Client, message mqtt.Message) {
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

func (m *chargingNotificationMonitor) observe(carID int, field, value string, observedAt time.Time) {
	m.mu.Lock()
	state := m.store.Cars[carID]
	wasActive := state.Active
	switch field {
	case "display_name":
		state.DisplayName = normalizedMQTTValue(value)
	case "state":
		state.VehicleState = strings.ToLower(normalizedMQTTValue(value))
	case "charging_state":
		state.ChargingState = strings.ToLower(normalizedMQTTValue(value))
	case "battery_level":
		state.BatteryLevel = parseBoundedInt(value, 0, 100)
	case "charge_limit_soc":
		state.ChargeLimitSOC = parseBoundedInt(value, 0, 100)
	case "rated_battery_range_km":
		state.RatedRangeKM = parseBoundedFloat(value, 0, 1000)
	case "charger_power":
		state.ChargerPowerKW = parseBoundedFloat(value, 0, 350)
	case "time_to_full_charge":
		state.TimeToFullHours = parseBoundedFloat(value, 0, 168)
	default:
		m.mu.Unlock()
		return
	}

	isCharging := state.VehicleState == "charging" || state.ChargingState == "charging"
	// Retained MQTT topics can arrive in any order after subscribe. Do not
	// push-start before both primary card values are present.
	if isCharging && !wasActive && state.BatteryLevel != nil && state.RatedRangeKM != nil {
		state.Active = true
		state.StartBatteryLevel = *state.BatteryLevel
		state.StartRatedRangeKM = cloneFloat(state.RatedRangeKM)
		state.SessionStartedAt = observedAt.Format(time.RFC3339)
		state.SessionID = chargingSessionID(m.installationID, carID, observedAt)
		state.Sequence = 0
		state.StartDelivered = false
		state.LastQueuedAt = ""
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		event := m.makeEventLocked(carID, &state, "charging_started", observedAt)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		m.mu.Unlock()
		m.enqueue(event)
		return
	}

	if !isCharging && wasActive {
		state.Active = false
		if timer := m.pending[carID]; timer != nil {
			timer.Stop()
			delete(m.pending, carID)
		}
		event := m.makeEventLocked(carID, &state, "charging_ended", observedAt)
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
			if lastQueued.IsZero() || observedAt.Sub(lastQueued) >= chargingUpdateMinimumInterval {
				event := m.makeEventLocked(carID, &state, "charging_started", observedAt)
				m.store.Cars[carID] = state
				_ = m.saveLocked()
				m.enqueue(event)
			}
		} else {
			m.scheduleUpdateLocked(carID, observedAt)
		}
	}
	m.mu.Unlock()
}

func (m *chargingNotificationMonitor) scheduleUpdateLocked(carID int, observedAt time.Time) {
	state := m.store.Cars[carID]
	if !state.StartDelivered || state.SessionID == "" || state.BatteryLevel == nil {
		return
	}
	lastQueued, _ := time.Parse(time.RFC3339, state.LastQueuedAt)
	minimumInterval := chargingUpdateMinimumInterval
	if state.ChargerPowerKW != nil && *state.ChargerPowerKW >= fastChargingPowerThresholdKW {
		minimumInterval = fastChargingUpdateMinimumInterval
	}
	delay := minimumInterval - observedAt.Sub(lastQueued)
	if lastQueued.IsZero() || delay <= 0 {
		event := m.makeEventLocked(carID, &state, "charging_updated", observedAt)
		m.store.Cars[carID] = state
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

func (m *chargingNotificationMonitor) flushUpdate(carID int) {
	m.mu.Lock()
	delete(m.pending, carID)
	state := m.store.Cars[carID]
	if !state.Active || !state.StartDelivered || state.BatteryLevel == nil {
		m.mu.Unlock()
		return
	}
	event := m.makeEventLocked(carID, &state, "charging_updated", time.Now().UTC())
	m.store.Cars[carID] = state
	_ = m.saveLocked()
	m.mu.Unlock()
	m.enqueue(event)
}

func (m *chargingNotificationMonitor) makeEventLocked(carID int, state *carChargingState, eventType string, observedAt time.Time) chargingLiveActivityEvent {
	state.Sequence++
	state.LastQueuedAt = observedAt.Format(time.RFC3339)
	target := 100
	if state.ChargeLimitSOC != nil {
		target = *state.ChargeLimitSOC
	}
	battery := state.StartBatteryLevel
	if state.BatteryLevel != nil {
		battery = *state.BatteryLevel
	}
	addedPercent := battery - state.StartBatteryLevel
	if addedPercent < 0 {
		addedPercent = 0
	}
	var addedRange *float64
	if state.StartRatedRangeKM != nil && state.RatedRangeKM != nil {
		value := *state.RatedRangeKM - *state.StartRatedRangeKM
		if value < 0 {
			value = 0
		}
		addedRange = &value
	}
	var remaining *int
	var estimated *int64
	if state.TimeToFullHours != nil {
		seconds := int(*state.TimeToFullHours * 3600)
		remaining = &seconds
		value := observedAt.Add(time.Duration(seconds) * time.Second).Unix()
		estimated = &value
	}
	idInput := fmt.Sprintf("%s:%s:%s:%d", m.installationID, state.SessionID, eventType, state.Sequence)
	idHash := sha256.Sum256([]byte(idInput))
	return chargingLiveActivityEvent{
		EventID:             hex.EncodeToString(idHash[:16]),
		InstallationID:      m.installationID,
		CarID:               carID,
		VehicleName:         state.DisplayName,
		Type:                eventType,
		SessionID:           state.SessionID,
		StartBatteryLevel:   state.StartBatteryLevel,
		BatteryLevel:        battery,
		AddedBatteryPercent: addedPercent,
		TargetLevel:         target,
		StartRatedRangeKM:   cloneFloat(state.StartRatedRangeKM),
		RatedRangeKM:        cloneFloat(state.RatedRangeKM),
		AddedRangeKM:        addedRange,
		PowerKW:             cloneFloat(state.ChargerPowerKW),
		RemainingSeconds:    remaining,
		EstimatedCompleteAt: estimated,
		ObservedAt:          observedAt.Format(time.RFC3339),
	}
}

func (m *chargingNotificationMonitor) enqueue(event chargingLiveActivityEvent) {
	select {
	case m.queue <- event:
	default:
		log.Printf("[error] charging event queue is full; event=%s", event.EventID)
	}
}

func (m *chargingNotificationMonitor) deliveryWorker() {
	for event := range m.queue {
		m.deliverWithRetry(event)
	}
}

func (m *chargingNotificationMonitor) deliverWithRetry(event chargingLiveActivityEvent) {
	delays := []time.Duration{0, 5 * time.Second, 30 * time.Second}
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		if err := m.deliver(event); err != nil {
			m.mu.Lock()
			m.lastError = err.Error()
			m.mu.Unlock()
			log.Printf("[warn] charging relay event=%s attempt=%d: %v", event.EventID, attempt+1, err)
			continue
		}
		m.mu.Lock()
		m.store.Delivered[event.EventID] = event.ObservedAt
		state := m.store.Cars[event.CarID]
		if event.Type == "charging_started" && state.SessionID == event.SessionID {
			state.StartDelivered = true
			state.LastQueuedAt = ""
			m.store.Cars[event.CarID] = state
			if state.Active {
				// Push-to-start may have retried while newer MQTT readings
				// arrived. Immediately send one complete latest snapshot.
				m.scheduleUpdateLocked(event.CarID, time.Now().UTC())
			}
		}
		now := time.Now().UTC()
		m.lastEventAt = &now
		m.lastError = ""
		_ = m.saveLocked()
		m.mu.Unlock()
		return
	}
}

func (m *chargingNotificationMonitor) deliver(event chargingLiveActivityEvent) error {
	subs := []pushSubscriber{}
	if pushRegistry != nil {
		subs = pushRegistry.matching(event.CarID, func(s pushSubscriber) bool { return s.ChargingLiveActivity })
	} else if m.installationID != "" {
		subs = []pushSubscriber{{
			InstallationID:       m.installationID,
			RelayURL:             m.relayURL,
			RelaySecret:          m.relaySecret,
			ChargingLiveActivity: true,
			Status:               pushStatusActive,
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
			err = m.deliverLegacy(sub, payload)
		}
		if err != nil {
			last = err
			log.Printf("[warn] charging fan-out installation=%s: %v", sub.InstallationID[:8], err)
			continue
		}
		ok++
	}
	if last != nil {
		return last
	}
	if ok == 0 {
		return fmt.Errorf("no charging live-activity subscribers")
	}
	return nil
}

func (m *chargingNotificationMonitor) deliverLegacy(sub pushSubscriber, payload []byte) error {
	signature := hmac.New(sha256.New, relaySecretBytes(sub.RelaySecret))
	_, _ = signature.Write(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, officialSoftwarePushRelayURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-My-T-Installation", sub.InstallationID)
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

func (m *chargingNotificationMonitor) status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := 0
	for _, state := range m.store.Cars {
		if state.Active {
			active++
		}
	}
	result := map[string]any{
		"enabled":                    m.enabled,
		"mqtt_connected":             m.connected,
		"delivery_mode":              "activitykit_push_to_start",
		"privacy_mode":               "charging_telemetry_only_no_vin_location_or_credentials",
		"tracked_cars":               len(m.store.Cars),
		"active_sessions":            active,
		"delivered_events":           len(m.store.Delivered),
		"minimum_update_seconds":     int(chargingUpdateMinimumInterval.Seconds()),
		"fast_charge_update_seconds": int(fastChargingUpdateMinimumInterval.Seconds()),
		"fast_charge_threshold_kw":   fastChargingPowerThresholdKW,
	}
	if m.lastEventAt != nil {
		result["last_delivered_at"] = m.lastEventAt.Format(time.RFC3339)
	}
	if m.lastError != "" {
		result["last_error"] = m.lastError
	}
	return result
}

func (m *chargingNotificationMonitor) load() {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var stored chargingNotificationStore
	if json.Unmarshal(data, &stored) == nil {
		if stored.Cars != nil {
			m.store.Cars = stored.Cars
		}
		if stored.Delivered != nil {
			m.store.Delivered = stored.Delivered
		}
		now := time.Now().UTC()
		pruneTimestampMap(m.store.Delivered, now, chargingDeliveredRetention, chargingDeliveredMaximum)
		for carID, state := range m.store.Cars {
			if state.Active && timestampIsOlderThan(state.LastQueuedAt, now, chargingTransientMaximumAge) {
				state.Active = false
				state.SessionID = ""
				state.SessionStartedAt = ""
				state.StartDelivered = false
				state.LastQueuedAt = ""
				m.store.Cars[carID] = state
			}
		}
	}
}

func (m *chargingNotificationMonitor) saveLocked() error {
	pruneTimestampMap(m.store.Delivered, time.Now().UTC(), chargingDeliveredRetention, chargingDeliveredMaximum)
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

func chargingSessionID(installationID string, carID int, startedAt time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", installationID, carID, startedAt.Unix())))
	return "charge-" + hex.EncodeToString(sum[:16])
}

func parseBoundedInt(value string, minimum, maximum int) *int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < minimum || parsed > maximum {
		return nil
	}
	return &parsed
}

func parseBoundedFloat(value string, minimum, maximum float64) *float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return nil
	}
	return &parsed
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
