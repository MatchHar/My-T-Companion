package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const defaultParkingEventRetentionDays = 365

type parkingObservedEvent struct {
	ID              string   `json:"id"`
	CarID           int      `json:"car_id"`
	Type            string   `json:"type"`
	Field           string   `json:"field"`
	Value           string   `json:"value"`
	ObservedAt      string   `json:"observed_at"`
	BatteryLevel    *int     `json:"battery_level,omitempty"`
	RatedRangeKM    *float64 `json:"rated_battery_range_km,omitempty"`
	ChargingState   string   `json:"charging_state,omitempty"`
	ChargeCable     string   `json:"charge_cable,omitempty"`
	ObservationMode string   `json:"observation_mode"`
}

type parkingEventCarState struct {
	Values       map[string]string `json:"values"`
	BatteryLevel *int              `json:"battery_level,omitempty"`
	RatedRangeKM *float64          `json:"rated_battery_range_km,omitempty"`
}

type parkingEventStore struct {
	Cars   map[int]parkingEventCarState `json:"cars"`
	Events []parkingObservedEvent       `json:"events"`
}

type parkingEventMonitor struct {
	mu            sync.RWMutex
	statePath     string
	retentionDays int
	mqttBroker    string
	mqttClientID  string
	mqttUsername  string
	mqttPassword  string
	client        mqtt.Client
	connected     bool
	lastError     string
	store         parkingEventStore
}

func newParkingEventMonitorFromEnvironment() *parkingEventMonitor {
	retention := defaultParkingEventRetentionDays
	if parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PARKING_EVENT_RETENTION_DAYS"))); err == nil &&
		parsed >= 30 && parsed <= 3650 {
		retention = parsed
	}
	monitor := &parkingEventMonitor{
		statePath:     getenv("PARKING_EVENT_STATE_PATH", "/data/parking-events.json"),
		retentionDays: retention,
		mqttBroker:    getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883"),
		mqttClientID:  getenv("MQTT_CLIENT_ID", "my-t-companion") + "-parking-events",
		mqttUsername:  strings.TrimSpace(os.Getenv("MQTT_USERNAME")),
		mqttPassword:  strings.TrimSpace(os.Getenv("MQTT_PASSWORD")),
		store: parkingEventStore{
			Cars:   map[int]parkingEventCarState{},
			Events: []parkingObservedEvent{},
		},
	}
	monitor.load()
	return monitor
}

func (m *parkingEventMonitor) start() {
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
		log.Printf("[warn] parking-event MQTT disconnected: %v", err)
	})
	options.SetOnConnectHandler(func(client mqtt.Client) {
		topics := map[string]byte{}
		for _, field := range parkingEventMQTTFields {
			topics["teslamate/cars/+/"+field] = 1
		}
		token := client.SubscribeMultiple(topics, m.handleMQTTMessage)
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			m.mu.Lock()
			m.connected = true
			m.lastError = ""
			m.mu.Unlock()
			log.Printf("[info] subscribed to TeslaMate parking-event MQTT topics")
			return
		}
		err := token.Error()
		if err == nil {
			err = fmt.Errorf("MQTT parking-event subscription timed out")
		}
		m.mu.Lock()
		m.connected = false
		m.lastError = err.Error()
		m.mu.Unlock()
	})
	m.client = mqtt.NewClient(options)
	token := m.client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		m.mu.Lock()
		m.lastError = "MQTT parking-event connection timed out"
		m.mu.Unlock()
		return
	}
	if err := token.Error(); err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("[warn] TeslaMate parking-event MQTT initial connection: %v", err)
	}
}

func (m *parkingEventMonitor) stop() {
	if m.client != nil && m.client.IsConnected() {
		m.client.Disconnect(250)
	}
}

var parkingEventMQTTFields = []string{
	"battery_level",
	"rated_battery_range_km",
	"plugged_in",
	"charging_state",
	"conn_charge_cable",
	"locked",
	"sentry_mode",
	"windows_open",
	"doors_open",
	"trunk_open",
	"frunk_open",
	"is_climate_on",
	"is_preconditioning",
	"battery_heater",
	"charge_port_door_open",
}

func (m *parkingEventMonitor) handleMQTTMessage(_ mqtt.Client, message mqtt.Message) {
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

func (m *parkingEventMonitor) observe(carID int, field, rawValue string, observedAt time.Time) {
	value := strings.ToLower(normalizedMQTTValue(rawValue))
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.store.Cars[carID]
	if state.Values == nil {
		state.Values = map[string]string{}
	}
	switch field {
	case "battery_level":
		state.BatteryLevel = parseBoundedInt(value, 0, 100)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		return
	case "rated_battery_range_km":
		state.RatedRangeKM = parseBoundedFloat(value, 0, 1000)
		m.store.Cars[carID] = state
		_ = m.saveLocked()
		return
	}

	previous, initialized := state.Values[field]
	state.Values[field] = value
	m.store.Cars[carID] = state

	// Retained MQTT values arrive at every subscription. The first observation
	// establishes a baseline only; it must never be presented as a transition.
	if !initialized || previous == value {
		_ = m.saveLocked()
		return
	}
	eventType := parkingEventType(field, value)
	if eventType == "" {
		_ = m.saveLocked()
		return
	}
	m.pruneLocked(observedAt)
	event := parkingObservedEvent{
		ID:              fmt.Sprintf("%d-%d-%s", carID, observedAt.UnixNano(), eventType),
		CarID:           carID,
		Type:            eventType,
		Field:           field,
		Value:           value,
		ObservedAt:      observedAt.Format(time.RFC3339Nano),
		BatteryLevel:    cloneParkingInt(state.BatteryLevel),
		RatedRangeKM:    cloneFloat(state.RatedRangeKM),
		ChargingState:   state.Values["charging_state"],
		ChargeCable:     state.Values["conn_charge_cable"],
		ObservationMode: "teslamate_mqtt_first_observed",
	}
	m.store.Events = append(m.store.Events, event)
	_ = m.saveLocked()
}

func parkingEventType(field, value string) string {
	switch field {
	case "plugged_in":
		if value == "true" {
			return "plug_connected"
		}
		if value == "false" {
			return "plug_disconnected"
		}
	case "charging_state":
		switch value {
		case "charging":
			return "charging_started"
		case "complete":
			return "charging_completed"
		case "stopped":
			return "charging_stopped"
		case "nopower":
			return "charging_no_power"
		}
	case "locked":
		if value == "true" {
			return "vehicle_locked"
		}
		if value == "false" {
			return "vehicle_unlocked"
		}
	case "sentry_mode":
		return boolEvent(value, "sentry_enabled", "sentry_disabled")
	case "windows_open":
		return boolEvent(value, "windows_opened", "windows_closed")
	case "doors_open":
		return boolEvent(value, "doors_opened", "doors_closed")
	case "trunk_open":
		return boolEvent(value, "trunk_opened", "trunk_closed")
	case "frunk_open":
		return boolEvent(value, "frunk_opened", "frunk_closed")
	case "is_climate_on":
		return boolEvent(value, "climate_started", "climate_stopped")
	case "is_preconditioning":
		return boolEvent(value, "preconditioning_started", "preconditioning_stopped")
	case "battery_heater":
		return boolEvent(value, "battery_heating_started", "battery_heating_stopped")
	case "charge_port_door_open":
		return boolEvent(value, "charge_port_opened", "charge_port_closed")
	}
	return ""
}

func boolEvent(value, trueEvent, falseEvent string) string {
	if value == "true" {
		return trueEvent
	}
	if value == "false" {
		return falseEvent
	}
	return ""
}

func (m *parkingEventMonitor) events(carID int, startDate, endDate time.Time) []parkingObservedEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []parkingObservedEvent{}
	for _, event := range m.store.Events {
		if event.CarID != carID {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, event.ObservedAt)
		if err == nil && !at.Before(startDate) && at.Before(endDate) {
			result = append(result, event)
		}
	}
	return result
}

func (m *parkingEventMonitor) pruneLocked(now time.Time) {
	cutoff := now.AddDate(0, 0, -m.retentionDays)
	kept := m.store.Events[:0]
	for _, event := range m.store.Events {
		at, err := time.Parse(time.RFC3339Nano, event.ObservedAt)
		if err == nil && !at.Before(cutoff) {
			kept = append(kept, event)
		}
	}
	m.store.Events = kept
}

func (m *parkingEventMonitor) load() {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var stored parkingEventStore
	if json.Unmarshal(data, &stored) == nil {
		if stored.Cars != nil {
			m.store.Cars = stored.Cars
		}
		if stored.Events != nil {
			m.store.Events = stored.Events
		}
	}
}

func (m *parkingEventMonitor) saveLocked() error {
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

func cloneParkingInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
