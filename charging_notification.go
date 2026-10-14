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

const (
	chargingUpdateMinimumInterval     = 45 * time.Second
	fastChargingUpdateMinimumInterval = 15 * time.Second
	fastChargingPowerThresholdKW      = 50.0
)

var chargingMQTTFields = []string{"display_name", "state", "charging_state", "battery_level", "charge_limit_soc", "rated_battery_range_km", "charger_power", "time_to_full_charge", "healthy"}

type chargingLiveActivityEvent struct {
	EventID              string   `json:"event_id"`
	InstallationID       string   `json:"installation_id"`
	SourceID             string   `json:"source_id,omitempty"`
	CarID                int      `json:"car_id"`
	VehicleName          string   `json:"vehicle_name,omitempty"`
	Type                 string   `json:"type"`
	SessionID            string   `json:"session_id"`
	StartBatteryLevel    int      `json:"start_battery_level"`
	BatteryLevel         int      `json:"battery_level"`
	AddedBatteryPercent  int      `json:"added_battery_percent"`
	TargetLevel          int      `json:"target_level"`
	StartRatedRangeKM    *float64 `json:"start_rated_range_km,omitempty"`
	RatedRangeKM         *float64 `json:"rated_range_km,omitempty"`
	AddedRangeKM         *float64 `json:"added_range_km,omitempty"`
	PowerKW              *float64 `json:"power_kw,omitempty"`
	RemainingSeconds     *int     `json:"remaining_seconds,omitempty"`
	EstimatedCompleteAt  *int64   `json:"estimated_complete_at,omitempty"`
	ObservedAt           string   `json:"observed_at"`
	ObservationMode      string   `json:"observation_mode,omitempty"`
	targetInstallationID string
}

type carChargingState struct {
	DisplayName                 string   `json:"display_name,omitempty"`
	VehicleState                string   `json:"vehicle_state,omitempty"`
	ChargingState               string   `json:"charging_state,omitempty"`
	BatteryLevel                *int     `json:"battery_level,omitempty"`
	ChargeLimitSOC              *int     `json:"charge_limit_soc,omitempty"`
	RatedRangeKM                *float64 `json:"rated_range_km,omitempty"`
	ChargerPowerKW              *float64 `json:"charger_power_kw,omitempty"`
	TimeToFullHours             *float64 `json:"time_to_full_hours,omitempty"`
	Active                      bool     `json:"active"`
	SessionID                   string   `json:"session_id,omitempty"`
	SessionStartedAt            string   `json:"session_started_at,omitempty"`
	StartBatteryLevel           int      `json:"start_battery_level,omitempty"`
	StartRatedRangeKM           *float64 `json:"start_rated_range_km,omitempty"`
	StartDelivered              bool     `json:"start_delivered"`
	Sequence                    int      `json:"sequence"`
	LastQueuedAt                string   `json:"last_queued_at,omitempty"`
	ObservationMode             string   `json:"observation_mode,omitempty"`
	DatabaseProcessID           int      `json:"database_process_id,omitempty"`
	DatabaseTerminalProcessID   int      `json:"database_terminal_process_id,omitempty"`
	MQTTTerminalProcessID       int      `json:"mqtt_terminal_process_id,omitempty"`
	MQTTTerminalObservedAt      string   `json:"mqtt_terminal_observed_at,omitempty"`
	DatabaseStartedAt           string   `json:"database_started_at,omitempty"`
	DatabaseTerminalConfirmedAt string   `json:"database_terminal_confirmed_at,omitempty"`
	LastEventSampleAt           string   `json:"last_event_sample_at,omitempty"`
	LastSampleBatteryLevel      *int     `json:"last_sample_battery_level,omitempty"`
}

type chargingNotificationStore struct {
	Cars      map[int]carChargingState `json:"cars"`
	Delivered map[string]string        `json:"delivered"`
}

type chargingNotificationMonitor struct {
	mu                   sync.Mutex
	deliveryGate         chan struct{}
	store                chargingNotificationStore
	statePath            string
	installationID       string
	relayURL             string
	relaySecret          string
	mqttBroker           string
	mqttClientID         string
	mqttUsername         string
	mqttPassword         string
	client               mqtt.Client
	httpClient           *http.Client
	enabled              bool
	connected            bool
	lastEventAt          *time.Time
	lastError            string
	started              bool
	workerStarted        bool
	pending              map[int]*time.Timer
	queue                chan chargingLiveActivityEvent
	mqttBaselines        map[int]chargingMQTTBaseline
	mqttManaged          bool
	databaseManaged      bool
	databaseObservations map[int]chargingDatabaseSnapshot
	databaseError        string
}

type chargingMQTTBaseline struct {
	stateReceived       bool
	stateLive           bool
	chargeLive          bool
	stateLiveAt         time.Time
	chargeLiveAt        time.Time
	chargeReceived      bool
	batteryReceived     bool
	rangeReceived       bool
	liveState           bool
	healthy             bool
	unhealthy           bool
	seenNotCharging     bool
	timeToFullLive      bool
	timeToFullAt        time.Time
	timeToFullProcessID int
	timeToFullStartedAt time.Time
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
		if pushRegistry != nil {
			pushRegistry.addDeliveryResolver(m.reconcileChargingDeliveryResolution)
		}
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
	options.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		m.mu.Lock()
		if m.client != client {
			m.mu.Unlock()
			return
		}
		m.resetMQTTBaselineLocked()
		recordMQTTConnection("charging", false)
		m.connected = false
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("[warn] charging MQTT disconnected: %v", err)
	})
	options.SetOnConnectHandler(func(client mqtt.Client) {
		m.mu.Lock()
		if m.client != client || !client.IsConnected() {
			m.mu.Unlock()
			return
		}
		m.resetMQTTBaselineLocked()
		epoch := beginMQTTConnection("charging")
		m.mu.Unlock()
		token := client.SubscribeMultiple(teslaMateMQTTTopics(chargingMQTTFields), m.handleMQTTMessage)
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			m.mu.Lock()
			if m.client != client || !client.IsConnected() || !recordMQTTConnection("charging", true, epoch) {
				m.mu.Unlock()
				return
			}
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
		if m.client != client || mqttConnectionEpoch("charging") != epoch {
			m.mu.Unlock()
			return
		}
		m.connected = false
		m.lastError = err.Error()
		m.mu.Unlock()
		requestMQTTBaselineRetry()
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
	recordMQTTConnection("charging", false)
	m.resetMQTTBaselineLocked()
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

func (m *chargingNotificationMonitor) refreshFleetSubscription() {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return
	}
	epoch := mqttConnectionEpoch("charging")
	token := client.SubscribeMultiple(teslaMateMQTTTopics(chargingMQTTFields), m.handleMQTTMessage)
	if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		m.mu.Lock()
		if m.client != client || mqttConnectionEpoch("charging") != epoch {
			m.mu.Unlock()
			return
		}
		m.lastError = "MQTT charging fleet subscription failed"
		m.mu.Unlock()
		requestMQTTBaselineRetry()
		return
	}
	m.mu.Lock()
	if m.client != client || !client.IsConnected() || !recordMQTTConnection("charging", true, epoch) {
		m.mu.Unlock()
		return
	}
	m.connected = true
	if m.lastError == "MQTT charging fleet subscription failed" || m.lastError == "MQTT charging subscription timed out" {
		m.lastError = ""
	}
	m.mu.Unlock()
}

func (m *chargingNotificationMonitor) reconcileFleet(cars map[int]bool) {
	m.mu.Lock()
	var cleanup []chargingLiveActivityEvent
	for id := range m.databaseObservations {
		if !cars[id] {
			delete(m.databaseObservations, id)
		}
	}
	for id, state := range m.store.Cars {
		if cars[id] {
			continue
		}
		if timer := m.pending[id]; timer != nil {
			timer.Stop()
			delete(m.pending, id)
		}
		delete(m.mqttBaselines, id)
		delete(m.databaseObservations, id)
		if state.Active && state.SessionID != "" {
			if pushRegistry != nil {
				pushRegistry.rememberKnownLiveActivityCleanup(id, "charging_live_activity", state.SessionID)
			}
			event := m.makeEventLocked(id, &state, "charging_ended", time.Now().UTC())
			event.ObservationMode = "source_membership_cleanup"
			cleanup = append(cleanup, event)
		}
		delete(m.store.Cars, id)
	}
	_ = m.saveLocked()
	m.mu.Unlock()
	for _, event := range cleanup {
		m.enqueue(event)
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
	carID, field, ok := acceptTeslaMateMQTTMessage(message, "charging")
	if !ok || message.Duplicate() {
		return
	}
	m.observeMessage(carID, field, string(message.Payload()), time.Now().UTC(), message.Retained(), true)
}

func (m *chargingNotificationMonitor) observe(carID int, field, value string, observedAt time.Time) {
	m.observeMessage(carID, field, value, observedAt, false, false)
}

func (m *chargingNotificationMonitor) resetMQTTBaseline() {
	m.mu.Lock()
	m.resetMQTTBaselineLocked()
	m.mu.Unlock()
}

func (m *chargingNotificationMonitor) resetMQTTBaselineLocked() {
	m.mqttManaged = true
	m.mqttBaselines = map[int]chargingMQTTBaseline{}
	for id, timer := range m.pending {
		timer.Stop()
		delete(m.pending, id)
	}
}

func (m *chargingNotificationMonitor) observeMessage(carID int, field, value string, observedAt time.Time, retained, managed bool) {
	m.mu.Lock()
	if managed {
		m.mqttManaged = true
	}
	if m.mqttBaselines == nil {
		m.mqttBaselines = map[int]chargingMQTTBaseline{}
	}
	baseline := m.mqttBaselines[carID]
	state := m.store.Cars[carID]
	wasActive := state.Active
	switch field {
	case "display_name":
		state.DisplayName = normalizedMQTTValue(value)
	case "state":
		state.VehicleState = strings.ToLower(normalizedMQTTValue(value))
		baseline.stateReceived = chargingVehicleStateKnown(state.VehicleState)
		baseline.liveState = !retained && baseline.stateReceived
		baseline.stateLive = !retained && baseline.stateReceived
		if baseline.stateLive {
			baseline.stateLiveAt = observedAt
		}
	case "charging_state":
		state.ChargingState = strings.ToLower(normalizedMQTTValue(value))
		baseline.chargeReceived = parkingEventValueKnown("charging_state", state.ChargingState)
		baseline.chargeLive = !retained && baseline.chargeReceived
		if baseline.chargeLive {
			baseline.chargeLiveAt = observedAt
		}
		if !retained {
			baseline.liveState = baseline.chargeReceived
		}
	case "battery_level":
		state.BatteryLevel = parseBoundedInt(value, 0, 100)
		baseline.batteryReceived = state.BatteryLevel != nil
	case "charge_limit_soc":
		state.ChargeLimitSOC = parseBoundedInt(value, 0, 100)
	case "rated_battery_range_km":
		state.RatedRangeKM = parseBoundedFloat(value, 0, 1000)
		baseline.rangeReceived = state.RatedRangeKM != nil
	case "charger_power":
		state.ChargerPowerKW = parseBoundedFloat(value, 0, 350)
	case "time_to_full_charge":
		state.TimeToFullHours = parseBoundedFloat(value, 0, 168)
		baseline.timeToFullLive = !retained && state.TimeToFullHours != nil
		baseline.timeToFullProcessID, baseline.timeToFullStartedAt = 0, time.Time{}
		if baseline.timeToFullLive {
			baseline.timeToFullAt = observedAt
			if sample := m.databaseObservations[carID]; m.databaseManaged && m.databaseError == "" && chargingSnapshotFresh(sample, observedAt) {
				baseline.timeToFullProcessID, baseline.timeToFullStartedAt = sample.ProcessID, sample.StartedAt
			}
		}
	case "healthy":
		baseline.healthy = !retained && strings.TrimSpace(value) == "true"
		baseline.unhealthy = strings.TrimSpace(value) != "true"
	default:
		m.mu.Unlock()
		return
	}
	if baseline.stateReceived && baseline.chargeReceived && state.VehicleState != "charging" && state.ChargingState != "charging" {
		baseline.seenNotCharging = true
	}
	m.mqttBaselines[carID] = baseline
	m.store.Cars[carID] = state
	if m.mqttManaged && (retained || !m.chargingMQTTReadyLocked(carID)) {
		// Clearing an existing card needs a real, coherent terminal state, not
		// fresh numerical card values. Null SOC/range must not strand it.
		terminal := !retained && wasActive && (field == "state" || field == "charging_state") &&
			baseline.stateReceived && baseline.chargeReceived &&
			state.VehicleState != "charging" && state.ChargingState != "charging"
		if !terminal {
			_ = m.saveLocked()
			m.mu.Unlock()
			return
		}
	}

	event := m.reconcileChargingActivityLocked(carID, observedAt, !retained && (field == "state" || field == "charging_state"))
	m.mu.Unlock()
	if event != nil {
		m.enqueue(*event)
	}
}

// A timestamped database result and a real MQTT receipt use the same start and
// update rules. Database reconciliation can never invent an MQTT terminal.
// The caller owns m.mu; returned events are guarded again before delivery.
func (m *chargingNotificationMonitor) reconcileChargingActivityLocked(carID int, observedAt time.Time, allowMQTTTerminal bool) *chargingLiveActivityEvent {
	if m.databaseManaged && !mqttCarEligible(carID) {
		return nil
	}
	state := m.store.Cars[carID]
	baseline := m.mqttBaselines[carID]
	wasActive := state.Active
	isCharging := state.VehicleState == "charging" || state.ChargingState == "charging"
	// Retained MQTT topics can arrive in any order after subscribe. Do not
	// push-start before both primary card values are present.
	if isCharging && !wasActive && state.BatteryLevel != nil && state.RatedRangeKM != nil {
		if !m.chargingDataPublishableLocked(carID, carChargingState{DatabaseTerminalProcessID: state.DatabaseTerminalProcessID, MQTTTerminalProcessID: state.MQTTTerminalProcessID, MQTTTerminalObservedAt: state.MQTTTerminalObservedAt}, observedAt, false) {
			return nil
		}
		if sample := m.databaseObservations[carID]; m.databaseManaged {
			terminalAt, terminalErr := time.Parse(time.RFC3339Nano, state.MQTTTerminalObservedAt)
			etaSameProcess := baseline.timeToFullProcessID == sample.ProcessID && baseline.timeToFullStartedAt.Equal(sample.StartedAt)
			if !etaSameProcess || (state.MQTTTerminalProcessID == sample.ProcessID && (terminalErr != nil || !baseline.timeToFullAt.After(terminalAt))) {
				// An ETA received before this process was identified, or before
				// a same-process stop/resume boundary, belongs to no new episode.
				state.TimeToFullHours = nil
				baseline.timeToFullLive = false
				baseline.timeToFullAt = time.Time{}
				baseline.timeToFullProcessID, baseline.timeToFullStartedAt = 0, time.Time{}
				m.mqttBaselines[carID] = baseline
			}
		}
		state.DatabaseProcessID, state.DatabaseStartedAt = 0, ""
		state.DatabaseTerminalConfirmedAt, state.LastEventSampleAt = "", ""
		state.LastSampleBatteryLevel = nil
		state.MQTTTerminalProcessID, state.MQTTTerminalObservedAt = 0, ""
		if sample := m.databaseObservations[carID]; m.databaseManaged {
			state.DatabaseProcessID = sample.ProcessID
			state.DatabaseStartedAt = sample.StartedAt.Format(time.RFC3339Nano)
		}
		state.Active = true
		state.StartBatteryLevel = *state.BatteryLevel
		state.StartRatedRangeKM = cloneFloat(state.RatedRangeKM)
		if sample := m.databaseObservations[carID]; m.databaseManaged {
			state.StartBatteryLevel = *sample.BatteryLevel
			state.StartRatedRangeKM = cloneFloat(sample.RatedRangeKM)
		}
		state.ObservationMode = "mqtt_live_transition"
		state.SessionStartedAt = observedAt.Format(time.RFC3339)
		if m.mqttManaged && (!baseline.liveState || !baseline.seenNotCharging) {
			state.ObservationMode = "recovered_current_snapshot"
			state.SessionStartedAt = "" // physical/source start time is unknown
		}
		state.SessionID = chargingSessionID(m.installationID, carID, observedAt)
		state.Sequence = 0
		state.StartDelivered = false
		state.LastQueuedAt = ""
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		event := m.makeEventLocked(carID, &state, "charging_started", observedAt)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		return &event
	}

	if !isCharging && wasActive {
		// A live healthy heartbeat proves transport, not the age of retained
		// terminal scalars received after reconnect. Only a real primary
		// state publication can supply an MQTT terminal observation.
		if !allowMQTTTerminal || (m.mqttManaged && (!baseline.stateLive || !baseline.chargeLive)) {
			_ = m.saveLocked()
			return nil
		}
		state.Active = false
		state.MQTTTerminalProcessID = state.DatabaseProcessID
		state.MQTTTerminalObservedAt = observedAt.Format(time.RFC3339Nano)
		if timer := m.pending[carID]; timer != nil {
			timer.Stop()
			delete(m.pending, carID)
		}
		event := m.makeEventLocked(carID, &state, "charging_ended", observedAt)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		return &event
	}

	m.store.Cars[carID] = state
	_ = m.saveLocked()
	if state.Active {
		if !state.StartDelivered {
			if !m.chargingDataPublishableLocked(carID, state, observedAt, false) {
				return nil
			}
			if pushRegistry != nil && pushRegistry.hasPendingChargingStart(carID, state.SessionID, observedAt) {
				return nil // Durable outbox owns this exact still-fresh start.
			}
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
	return nil
}

func (m *chargingNotificationMonitor) scheduleUpdateLocked(carID int, observedAt time.Time) {
	if m.mqttManaged && !m.chargingMQTTReadyLocked(carID) {
		return
	}
	state := m.store.Cars[carID]
	if !state.StartDelivered || state.SessionID == "" || state.BatteryLevel == nil || !m.chargingDataPublishableLocked(carID, state, observedAt, true) {
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
	if !state.Active || !state.StartDelivered || state.BatteryLevel == nil || !m.chargingDataPublishableLocked(carID, state, time.Now().UTC(), true) || (m.mqttManaged && !m.chargingMQTTReadyLocked(carID)) {
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
	mode := state.ObservationMode
	if m.mqttManaged && (!m.mqttBaselines[carID].liveState || !m.mqttBaselines[carID].seenNotCharging) {
		mode = "recovered_current_snapshot"
	}
	state.Sequence++
	state.LastQueuedAt = observedAt.Format(time.RFC3339)
	power := cloneFloat(state.ChargerPowerKW)
	dataObservedAt := observedAt
	if m.databaseManaged && eventType != "charging_ended" {
		if sample := m.databaseObservations[carID]; sample.LastSampleAt != nil {
			dataObservedAt = *sample.LastSampleAt
			state.LastEventSampleAt = dataObservedAt.Format(time.RFC3339Nano)
			power = cloneFloat(sample.PowerKW)
			mode = "database_sample_confirmed"
		}
	}
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
	currentRange := cloneFloat(state.RatedRangeKM)
	if sample := m.databaseObservations[carID]; m.databaseManaged && sample.ProcessID == state.DatabaseProcessID && sample.StartedAt.Format(time.RFC3339Nano) == state.DatabaseStartedAt {
		// A terminal confirms lifecycle, not every numerical field. When its
		// final row lacks SOC, keep only a previously source-confirmed value;
		// never stamp an unclocked MQTT SOC as the database terminal reading.
		battery = state.StartBatteryLevel
		if state.LastSampleBatteryLevel != nil {
			battery = *state.LastSampleBatteryLevel
		}
		if sample.BatteryLevel != nil {
			battery = *sample.BatteryLevel
			value := battery
			state.LastSampleBatteryLevel = &value
		}
		currentRange = cloneFloat(sample.RatedRangeKM)
		addedPercent = battery - state.StartBatteryLevel
		if addedPercent < 0 {
			addedPercent = 0
		}
	}
	var addedRange *float64
	if state.StartRatedRangeKM != nil && currentRange != nil {
		value := *currentRange - *state.StartRatedRangeKM
		if value < 0 {
			value = 0
		}
		addedRange = &value
	}
	var remaining *int
	var estimated *int64
	if state.TimeToFullHours != nil && (!m.databaseManaged || m.chargingDataPublishableLocked(carID, *state, observedAt, false)) {
		seconds := int(*state.TimeToFullHours * 3600)
		// Retained duration has no sample timestamp; a healthy receipt cannot
		// rebase it into a newly predicted completion time.
		baseline := m.mqttBaselines[carID]
		if !m.mqttManaged || (baseline.timeToFullLive && (!m.databaseManaged || (baseline.timeToFullProcessID == state.DatabaseProcessID && baseline.timeToFullStartedAt.Format(time.RFC3339Nano) == state.DatabaseStartedAt && !baseline.timeToFullAt.After(observedAt) && observedAt.Sub(baseline.timeToFullAt) <= chargingSampleMaximumAge))) {
			remaining = &seconds
			base := observedAt
			if m.mqttManaged {
				base = m.mqttBaselines[carID].timeToFullAt
			}
			value := base.Add(time.Duration(seconds) * time.Second).Unix()
			estimated = &value
		}
	}
	if eventType == "charging_ended" {
		// Terminal cards may retain last-known SOC/range for context, but
		// cannot claim current power or an active completion countdown.
		power, remaining, estimated = nil, nil, nil
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
		RatedRangeKM:        currentRange,
		AddedRangeKM:        addedRange,
		PowerKW:             power,
		RemainingSeconds:    remaining,
		EstimatedCompleteAt: estimated,
		ObservedAt:          dataObservedAt.Format(time.RFC3339Nano),
		ObservationMode:     mode,
	}
}

func (m *chargingNotificationMonitor) chargingMQTTReadyLocked(carID int) bool {
	b := m.mqttBaselines[carID]
	s := m.store.Cars[carID]
	consistent := (s.VehicleState == "charging") == (s.ChargingState == "charging")
	return b.stateReceived && b.chargeReceived && b.batteryReceived && b.rangeReceived && consistent && !b.unhealthy && (b.liveState || b.healthy)
}

func chargingVehicleStateKnown(value string) bool {
	switch value {
	case "charging", "driving", "online", "asleep", "offline":
		return true
	}
	return false
}

func (m *chargingNotificationMonitor) enqueue(event chargingLiveActivityEvent) {
	select {
	case m.queue <- event:
	default:
		log.Printf("[error] charging event queue is full; event=%s", event.EventID)
	}
}

// Replay an in-progress charge only to the installation/vehicle scope that
// just enabled Lock Screen cards. This repairs off→on without waiting for the
// next charge and avoids duplicating cards for already-enabled vehicles.
func (m *chargingNotificationMonitor) replayActiveStarts(installationID string) int {
	return m.replayActiveStartsMatching(installationID, nil)
}

func (m *chargingNotificationMonitor) replayActiveStartsMatching(
	installationID string,
	shouldReplay func(carID int) bool,
) int {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return 0
	}
	m.mu.Lock()
	events := make([]chargingLiveActivityEvent, 0, len(m.store.Cars))
	now := time.Now().UTC()
	for carID, state := range m.store.Cars {
		if shouldReplay != nil && !shouldReplay(carID) {
			continue
		}
		if m.mqttManaged && !m.chargingMQTTReadyLocked(carID) {
			continue
		}
		if !state.Active || state.SessionID == "" || state.BatteryLevel == nil || !m.chargingDataPublishableLocked(carID, state, now, false) {
			continue
		}
		event := m.makeEventLocked(carID, &state, "charging_started", now)
		event.targetInstallationID = installationID
		m.store.Cars[carID] = state
		events = append(events, event)
	}
	_ = m.saveLocked()
	m.mu.Unlock()
	for _, event := range events {
		m.enqueue(event)
	}
	return len(events)
}

func (m *chargingNotificationMonitor) deliveryWorker() {
	for event := range m.queue {
		m.deliverWithRetry(event)
	}
}

// Serialize charging HTTP starts/updates and terminal cleanup across the normal
// worker and durable outbox. The network never holds m.mu, and waiting consumes
// the same bounded request context rather than indefinitely blocking cleanup.
func (m *chargingNotificationMonitor) acquireChargingDelivery(ctx context.Context) (func(), error) {
	m.mu.Lock()
	if m.deliveryGate == nil {
		m.deliveryGate = make(chan struct{}, 1)
	}
	gate := m.deliveryGate
	m.mu.Unlock()
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *chargingNotificationMonitor) chargingEventCurrentLocked(carID int, state carChargingState, observedAt string) bool {
	if !m.databaseManaged {
		return true // MQTT-only compatibility constructors, never production setup.
	}
	now := time.Now().UTC()
	at, err := time.Parse(time.RFC3339Nano, observedAt)
	return err == nil && !at.After(now) && now.Sub(at) <= chargingSampleMaximumAge &&
		state.Active && m.mqttManaged && m.chargingMQTTReadyLocked(carID) && mqttCarEligible(carID) &&
		m.chargingDataPublishableLocked(carID, state, now, false)
}

func (m *chargingNotificationMonitor) chargingEventCurrent(event chargingLiveActivityEvent) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.store.Cars[event.CarID]
	return (!m.databaseManaged || (state.SessionID != "" && state.SessionID == event.SessionID)) &&
		m.chargingEventCurrentLocked(event.CarID, state, event.ObservedAt)
}

func (m *chargingNotificationMonitor) chargingPayloadCurrent(sub pushSubscriber, metadata pushPayloadMetadata) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.store.Cars[metadata.CarID]
	return (!m.databaseManaged || (state.SessionID != "" && scopedLiveActivitySessionID(state.SessionID, sub.SourceID, "charge") == metadata.SessionID)) &&
		m.chargingEventCurrentLocked(metadata.CarID, state, metadata.ObservedAt)
}

func (m *chargingNotificationMonitor) deliverWithRetry(event chargingLiveActivityEvent) {
	delays := []time.Duration{0, 5 * time.Second, 30 * time.Second}
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		outcome, err := m.deliverOutcome(event)
		if err != nil {
			m.mu.Lock()
			m.lastError = err.Error()
			m.mu.Unlock()
			log.Printf("[warn] charging relay event=%s attempt=%d: %v", event.EventID, attempt+1, err)
			continue
		}
		if outcome != pushDeliveryAPNsAccepted {
			// Queued work remains owned by the durable outbox. Expired or
			// ineligible work was not sent. Neither proves push-to-start worked.
			return
		}
		m.mu.Lock()
		m.store.Delivered[event.EventID] = event.ObservedAt
		state := m.store.Cars[event.CarID]
		if event.Type == "charging_started" && state.Active && state.SessionID == event.SessionID {
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
	_, err := m.deliverOutcome(event)
	return err
}

func (m *chargingNotificationMonitor) deliverOutcome(event chargingLiveActivityEvent) (pushDeliveryOutcome, error) {
	if event.Type != "charging_ended" && !m.chargingEventCurrent(event) {
		return pushDeliveryExpired, nil // Ended/replaced/stale sessions are not retryable starts.
	}
	subs := []pushSubscriber{}
	if pushRegistry != nil {
		subs = pushRegistry.matching(event.CarID, func(s pushSubscriber) bool {
			return pushSubscriberWantsEvent(s, event.CarID, "charging_live_activity", event.Type == "charging_ended", event.SessionID) &&
				(event.targetInstallationID == "" || s.InstallationID == event.targetInstallationID)
		})
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
		return pushDeliveryExpired, nil
	}
	var last error
	ok := 0
	accepted, queued := false, false
	for _, sub := range subs {
		copy := event
		copy.InstallationID = sub.InstallationID
		copy.SourceID = sub.SourceID
		copy.SessionID = scopedLiveActivitySessionID(event.SessionID, sub.SourceID, "charge")
		copy.EventID = targetScopedPushEventID(event.EventID, sub.InstallationID, sub.SourceID, event.Type)
		payload, err := json.Marshal(copy)
		if err != nil {
			last = err
			continue
		}
		outcome := pushDeliveryAPNsAccepted
		if pushRegistry != nil {
			result := pushRegistry.deliverJSONResult(sub, payload)
			outcome, err = result.Outcome, result.Err
		} else {
			outcome, err = m.deliverLegacy(sub, payload)
		}
		if err != nil {
			last = err
			log.Printf("[warn] charging fan-out installation=%s: %v", sub.InstallationID[:8], err)
			continue
		}
		ok++
		accepted = accepted || outcome == pushDeliveryAPNsAccepted
		queued = queued || outcome == pushDeliveryQueued || outcome == pushDeliveryAwaitingToken
	}
	if last != nil {
		return pushDeliveryFailed, last
	}
	if ok == 0 {
		return pushDeliveryFailed, fmt.Errorf("no charging live-activity subscribers")
	}
	if accepted {
		return pushDeliveryAPNsAccepted, nil
	}
	if queued {
		return pushDeliveryQueued, nil
	}
	return pushDeliveryExpired, nil
}

func (m *chargingNotificationMonitor) reconcileChargingDeliveryResolution(resolution pushDeliveryResolution) {
	if resolution.EventType != "charging_started" || resolution.Outcome != string(pushDeliveryAPNsAccepted) || pushRegistry == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.store.Cars[resolution.CarID]
	if !state.Active || state.SessionID == "" || state.StartDelivered {
		return
	}
	matches := pushRegistry.matching(resolution.CarID, func(sub pushSubscriber) bool {
		return sub.InstallationID == resolution.InstallationID && sub.wantsChargingLiveActivity(resolution.CarID) &&
			scopedLiveActivitySessionID(state.SessionID, sub.SourceID, "charge") == resolution.SessionID
	})
	if len(matches) == 0 {
		return
	}
	state.StartDelivered = true
	state.LastQueuedAt = ""
	m.store.Cars[resolution.CarID] = state
	now := time.Now().UTC()
	m.lastEventAt, m.lastError = &now, ""
	_ = m.saveLocked()
	m.scheduleUpdateLocked(resolution.CarID, now)
}

func (m *chargingNotificationMonitor) deliverLegacy(sub pushSubscriber, payload []byte) (pushDeliveryOutcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	release, err := m.acquireChargingDelivery(ctx)
	if err != nil {
		return pushDeliveryFailed, err
	}
	defer release()
	var metadata pushPayloadMetadata
	if json.Unmarshal(payload, &metadata) != nil {
		return pushDeliveryFailed, fmt.Errorf("invalid charging payload")
	}
	if metadata.Type != "charging_ended" && !m.chargingPayloadCurrent(sub, metadata) {
		return pushDeliveryExpired, nil
	}
	signature := hmac.New(sha256.New, relaySecretBytes(sub.RelaySecret))
	_, _ = signature.Write(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, officialSoftwarePushRelayURL, bytes.NewReader(payload))
	if err != nil {
		return pushDeliveryFailed, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-My-T-Installation", sub.InstallationID)
	request.Header.Set("X-My-T-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
	response, err := m.httpClient.Do(request)
	if err != nil {
		return pushDeliveryFailed, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return pushDeliveryFailed, fmt.Errorf("relay returned HTTP %d", response.StatusCode)
	}
	return pushDeliveryAPNsAccepted, nil
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
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < minimum || parsed > maximum {
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
