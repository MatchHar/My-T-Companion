package main

import (
	"crypto/rand"
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
	lowBatteryEnterPercent  = 20
	lowBatterySeverePercent = 10
	lowBatteryRearmPercent  = 25
	lowBatterySettleDelay   = 5 * time.Second
	lowBatterySnoozeDelay   = 4 * time.Hour
)

type lowBatteryActionState struct {
	Action      string `json:"action"`
	SnoozeUntil string `json:"snooze_until,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type lowBatteryCarState struct {
	DisplayName    string                           `json:"display_name,omitempty"`
	State          string                           `json:"state,omitempty"`
	ShiftState     string                           `json:"shift_state,omitempty"`
	ChargingState  string                           `json:"charging_state,omitempty"`
	BatteryLevel   *int                             `json:"battery_level,omitempty"`
	Initialized    bool                             `json:"initialized"`
	LastEligible   bool                             `json:"last_eligible"`
	Latched        bool                             `json:"latched"`
	SevereLatched  bool                             `json:"severe_latched"`
	EpisodeID      string                           `json:"episode_id,omitempty"`
	Actions        map[string]lowBatteryActionState `json:"actions,omitempty"`
	LastObservedAt string                           `json:"last_observed_at,omitempty"`
}

type lowBatteryStore struct {
	Cars map[int]lowBatteryCarState `json:"cars"`
}

type lowBatteryPushEvent struct {
	EventID        string `json:"event_id"`
	InstallationID string `json:"installation_id"`
	SourceID       string `json:"source_id,omitempty"`
	CarID          int    `json:"car_id"`
	VehicleName    string `json:"vehicle_name,omitempty"`
	Type           string `json:"type"`
	EpisodeID      string `json:"episode_id"`
	BatteryLevel   int    `json:"battery_level"`
	ObservedAt     string `json:"observed_at"`
}

type lowBatteryActionRequest struct {
	InstallationID string `json:"installation_id"`
	CarID          int    `json:"car_id"`
	EpisodeID      string `json:"episode_id"`
	Action         string `json:"action"`
}

type lowBatteryNotificationMonitor struct {
	mu           sync.Mutex
	statePath    string
	store        lowBatteryStore
	mqttBroker   string
	mqttClientID string
	mqttUsername string
	mqttPassword string
	client       mqtt.Client
	timers       map[int]*time.Timer
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
	connected    bool
	lastError    string
	started      bool
}

func newLowBatteryNotificationMonitorFromEnvironment() *lowBatteryNotificationMonitor {
	base := filepath.Dir(getenv("PUSH_STATE_PATH", "/data/software-notifications.json"))
	m := &lowBatteryNotificationMonitor{
		statePath:    filepath.Join(base, "low-battery-state.json"),
		store:        lowBatteryStore{Cars: map[int]lowBatteryCarState{}},
		mqttBroker:   getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883"),
		mqttClientID: getenv("MQTT_CLIENT_ID", "my-t-companion") + "-low-battery",
		mqttUsername: strings.TrimSpace(os.Getenv("MQTT_USERNAME")),
		mqttPassword: strings.TrimSpace(os.Getenv("MQTT_PASSWORD")),
		timers:       map[int]*time.Timer{},
		stopCh:       make(chan struct{}),
	}
	m.loadState()
	return m
}

func (m *lowBatteryNotificationMonitor) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	m.wg.Add(1)
	go m.reminderLoop()
	m.connectMQTT()
}

func (m *lowBatteryNotificationMonitor) stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	m.mu.Lock()
	for _, timer := range m.timers {
		timer.Stop()
	}
	m.timers = map[int]*time.Timer{}
	client := m.client
	m.client = nil
	m.connected = false
	m.started = false
	m.mu.Unlock()
	if client != nil && client.IsConnected() {
		client.Disconnect(250)
	}
	m.wg.Wait()
}

func (m *lowBatteryNotificationMonitor) connectMQTT() {
	options := mqtt.NewClientOptions().
		AddBroker(m.mqttBroker).
		SetClientID(m.mqttClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetOrderMatters(true)
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
		topics := map[string]byte{}
		for _, field := range []string{"display_name", "state", "shift_state", "charging_state", "battery_level"} {
			topics["teslamate/cars/+/"+field] = 1
		}
		token := client.SubscribeMultiple(topics, m.handleMQTTMessage)
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			m.mu.Lock()
			m.connected = true
			m.lastError = ""
			m.mu.Unlock()
			log.Printf("[info] subscribed to TeslaMate low-battery MQTT topics")
			return
		}
		m.mu.Lock()
		m.connected = false
		if err := token.Error(); err != nil {
			m.lastError = err.Error()
		} else {
			m.lastError = "MQTT low-battery subscription timed out"
		}
		m.mu.Unlock()
	})
	m.client = mqtt.NewClient(options)
	token := m.client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		m.mu.Lock()
		m.lastError = "MQTT low-battery connection timed out"
		m.mu.Unlock()
		return
	}
	if err := token.Error(); err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
	}
}

func (m *lowBatteryNotificationMonitor) handleMQTTMessage(_ mqtt.Client, message mqtt.Message) {
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

func (m *lowBatteryNotificationMonitor) observe(carID int, field, raw string, observedAt time.Time) {
	m.mu.Lock()
	state := m.store.Cars[carID]
	if state.Actions == nil {
		state.Actions = map[string]lowBatteryActionState{}
	}
	switch field {
	case "display_name":
		state.DisplayName = collapsedDisplayName(raw)
	case "state":
		state.State = strings.ToLower(normalizedMQTTValue(raw))
	case "shift_state":
		state.ShiftState = strings.ToUpper(normalizedMQTTValue(raw))
	case "charging_state":
		state.ChargingState = strings.ToLower(normalizedMQTTValue(raw))
	case "battery_level":
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 100 {
			m.mu.Unlock()
			return
		}
		state.BatteryLevel = &value
	default:
		m.mu.Unlock()
		return
	}
	state.LastObservedAt = observedAt.Format(time.RFC3339)
	m.store.Cars[carID] = state
	_ = m.saveStateLocked()
	if previous := m.timers[carID]; previous != nil {
		previous.Stop()
	}
	m.timers[carID] = time.AfterFunc(lowBatterySettleDelay, func() {
		m.evaluate(carID, time.Now().UTC())
	})
	m.mu.Unlock()
}

func (m *lowBatteryNotificationMonitor) evaluate(carID int, observedAt time.Time) {
	m.mu.Lock()
	delete(m.timers, carID)
	state, ok := m.store.Cars[carID]
	if !ok || state.BatteryLevel == nil || state.State == "" || state.ChargingState == "" {
		m.mu.Unlock()
		return
	}
	eligible := lowBatteryEligible(state)
	if !state.Initialized {
		state.Initialized = true
		state.LastEligible = eligible
		// The first complete retained MQTT snapshot establishes a baseline only.
		// If the car is already low, latch that episode without notifying so a
		// later retained/topic refresh cannot masquerade as a fresh transition.
		if eligible {
			state.Latched = true
			state.SevereLatched = *state.BatteryLevel < lowBatterySeverePercent
			state.EpisodeID = newLowBatteryEpisodeID(carID, observedAt)
			state.Actions = map[string]lowBatteryActionState{}
		}
		m.store.Cars[carID] = state
		_ = m.saveStateLocked()
		m.mu.Unlock()
		return
	}
	if *state.BatteryLevel >= lowBatteryRearmPercent {
		state.Latched = false
		state.SevereLatched = false
		state.EpisodeID = ""
		state.Actions = map[string]lowBatteryActionState{}
	}
	if lowBatteryIsCharging(state) {
		for installationID, action := range state.Actions {
			if action.Action == "snooze_4h" {
				action.Action = "cancelled"
				action.SnoozeUntil = ""
				action.UpdatedAt = observedAt.Format(time.RFC3339)
				state.Actions[installationID] = action
			}
		}
	}
	eventType := ""
	if eligible && !state.Latched {
		state.Latched = true
		state.SevereLatched = *state.BatteryLevel < lowBatterySeverePercent
		state.EpisodeID = newLowBatteryEpisodeID(carID, observedAt)
		state.Actions = map[string]lowBatteryActionState{}
		if state.SevereLatched {
			eventType = "vehicle_low_battery_severe"
		} else {
			eventType = "vehicle_low_battery"
		}
	} else if eligible && state.Latched && *state.BatteryLevel < lowBatterySeverePercent && !state.SevereLatched {
		state.SevereLatched = true
		for installationID, action := range state.Actions {
			if action.Action == "snooze_4h" {
				action.Action = "superseded"
				action.SnoozeUntil = ""
				action.UpdatedAt = observedAt.Format(time.RFC3339)
				state.Actions[installationID] = action
			}
		}
		eventType = "vehicle_low_battery_severe"
	}
	state.LastEligible = eligible
	m.store.Cars[carID] = state
	_ = m.saveStateLocked()
	m.mu.Unlock()
	if eventType != "" {
		m.fanOut(carID, state, eventType, observedAt, "")
	}
}

func lowBatteryEligible(state lowBatteryCarState) bool {
	if state.BatteryLevel == nil || *state.BatteryLevel >= lowBatteryEnterPercent {
		return false
	}
	shift := strings.ToUpper(strings.TrimSpace(state.ShiftState))
	if shift == "D" || shift == "R" || shift == "N" {
		return false
	}
	vehicleState := strings.ToLower(strings.TrimSpace(state.State))
	if vehicleState == "driving" || vehicleState == "updating" || vehicleState == "charging" {
		return false
	}
	return !lowBatteryIsCharging(state)
}

func lowBatteryIsCharging(state lowBatteryCarState) bool {
	charging := strings.ToLower(strings.TrimSpace(state.ChargingState))
	return charging == "charging" || charging == "starting"
}

func newLowBatteryEpisodeID(carID int, observedAt time.Time) string {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%d-%d", carID, observedAt.Unix())
	}
	return fmt.Sprintf("%d-%d-%s", carID, observedAt.Unix(), hex.EncodeToString(random))
}

func (m *lowBatteryNotificationMonitor) fanOut(carID int, state lowBatteryCarState, eventType string, observedAt time.Time, onlyInstallation string) {
	if pushRegistry == nil || state.BatteryLevel == nil || state.EpisodeID == "" {
		return
	}
	subs := pushRegistry.matching(carID, func(sub pushSubscriber) bool {
		return sub.wantsLowBattery(carID) && (onlyInstallation == "" || sub.InstallationID == onlyInstallation)
	})
	for _, sub := range subs {
		baseID := fmt.Sprintf("%d:%s:%s", carID, state.EpisodeID, eventType)
		event := lowBatteryPushEvent{
			EventID: targetPushEventID(baseID, sub.InstallationID, eventType), InstallationID: sub.InstallationID,
			SourceID: sub.SourceID, CarID: carID, VehicleName: state.DisplayName, Type: eventType,
			EpisodeID: state.EpisodeID, BatteryLevel: *state.BatteryLevel, ObservedAt: observedAt.UTC().Format(time.RFC3339),
		}
		payload, err := json.Marshal(event)
		if err == nil {
			err = pushRegistry.deliverJSON(sub, payload)
		}
		if err != nil {
			log.Printf("[warn] low-battery fan-out installation=%s: %v", shortInstallationID(sub.InstallationID), err)
		}
	}
}

func (m *lowBatteryNotificationMonitor) applyAction(body lowBatteryActionRequest, now time.Time) error {
	if pushRegistry == nil || strings.TrimSpace(body.InstallationID) == "" {
		return fmt.Errorf("not_paired")
	}
	if body.CarID <= 0 || strings.TrimSpace(body.EpisodeID) == "" {
		return fmt.Errorf("invalid_action")
	}
	action := strings.ToLower(strings.TrimSpace(body.Action))
	if action != "ack" && action != "snooze_4h" {
		return fmt.Errorf("invalid_action")
	}
	if len(pushRegistry.matching(body.CarID, func(sub pushSubscriber) bool {
		return sub.InstallationID == body.InstallationID && sub.wantsLowBattery(body.CarID)
	})) == 0 {
		return fmt.Errorf("not_paired")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.store.Cars[body.CarID]
	if !ok || state.EpisodeID != body.EpisodeID || !state.Latched {
		return nil
	}
	if state.Actions == nil {
		state.Actions = map[string]lowBatteryActionState{}
	}
	value := lowBatteryActionState{Action: action, UpdatedAt: now.UTC().Format(time.RFC3339)}
	if action == "snooze_4h" {
		value.SnoozeUntil = now.UTC().Add(lowBatterySnoozeDelay).Format(time.RFC3339)
	}
	state.Actions[body.InstallationID] = value
	m.store.Cars[body.CarID] = state
	return m.saveStateLocked()
}

func (m *lowBatteryNotificationMonitor) reminderLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			m.processDueReminders(now.UTC())
		case <-m.stopCh:
			return
		}
	}
}

func (m *lowBatteryNotificationMonitor) processDueReminders(now time.Time) {
	type dueReminder struct {
		carID          int
		installationID string
		state          lowBatteryCarState
	}
	due := []dueReminder{}
	m.mu.Lock()
	for carID, state := range m.store.Cars {
		for installationID, action := range state.Actions {
			if action.Action != "snooze_4h" || action.SnoozeUntil == "" {
				continue
			}
			until, err := time.Parse(time.RFC3339, action.SnoozeUntil)
			if err != nil || now.Before(until) {
				continue
			}
			action.Action = "completed"
			action.SnoozeUntil = ""
			action.UpdatedAt = now.Format(time.RFC3339)
			state.Actions[installationID] = action
			if state.Latched && lowBatteryEligible(state) {
				due = append(due, dueReminder{carID, installationID, state})
			}
		}
		m.store.Cars[carID] = state
	}
	_ = m.saveStateLocked()
	m.mu.Unlock()
	for _, reminder := range due {
		m.fanOut(reminder.carID, reminder.state, "vehicle_low_battery_reminder", now, reminder.installationID)
	}
}

func (m *lowBatteryNotificationMonitor) loadState() {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var stored lowBatteryStore
	if json.Unmarshal(data, &stored) == nil && stored.Cars != nil {
		for carID, state := range stored.Cars {
			if state.Actions == nil {
				state.Actions = map[string]lowBatteryActionState{}
				stored.Cars[carID] = state
			}
		}
		m.store = stored
	}
}

func (m *lowBatteryNotificationMonitor) saveStateLocked() error {
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

func handleLowBatteryAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}
	if lowBatteryPush == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	defer r.Body.Close()
	var body lowBatteryActionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid body"})
		return
	}
	headerID := installationIDFromRequest(r)
	if body.InstallationID == "" {
		body.InstallationID = headerID
	} else if headerID != "" && body.InstallationID != headerID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation_mismatch"})
		return
	}
	if err := lowBatteryPush.applyAction(body, time.Now().UTC()); err != nil {
		status := http.StatusBadRequest
		if err.Error() == "not_paired" {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": body.Action})
}
