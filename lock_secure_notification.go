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

// Allowed APNs sound names. `none` omits aps.sound; the imported CAF is stored
// by My T in its Library/Sounds directory before it can be selected.
var lockSecureSoundWhitelist = map[string]bool{
	"none":                    true,
	"default":                 true,
	"lock_secure_user.caf":    true,
	"lock_secure_chime.caf":   true,
	"lock_secure_bell.caf":    true,
	"lock_secure_soft.caf":    true,
	"lock_secure_ack.caf":     true,
	"lock_secure_confirm.caf": true,
	"lock_secure_sent.caf":    true,
}

type lockSecurePrefs struct {
	Enabled   bool   `json:"enabled"`
	Sound     string `json:"sound"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type lockSecureCarState struct {
	DisplayName  string `json:"display_name,omitempty"`
	Locked       *bool  `json:"locked,omitempty"`
	UserPresent  *bool  `json:"is_user_present,omitempty"`
	State        string `json:"state,omitempty"`
	ShiftState   string `json:"shift_state,omitempty"`
	DoorsOpen    *bool  `json:"doors_open,omitempty"`
	TrunkOpen    *bool  `json:"trunk_open,omitempty"`
	FrunkOpen    *bool  `json:"frunk_open,omitempty"`
	LastSecure   bool   `json:"last_secure"`
	Initialized  bool   `json:"initialized"`
	LastPushedAt string `json:"last_pushed_at,omitempty"`
}

type lockSecureStore struct {
	Cars      map[int]lockSecureCarState `json:"cars"`
	Delivered map[string]string          `json:"delivered"`
}

type lockSecurePushEvent struct {
	EventID        string `json:"event_id"`
	InstallationID string `json:"installation_id"`
	CarID          int    `json:"car_id"`
	VehicleName    string `json:"vehicle_name,omitempty"`
	Type           string `json:"type"`
	Sound          string `json:"sound,omitempty"`
	ObservedAt     string `json:"observed_at"`
}

type lockSecureNotificationMonitor struct {
	mu             sync.Mutex
	prefsPath      string
	statePath      string
	prefs          lockSecurePrefs
	store          lockSecureStore
	installationID string
	relayURL       string
	relaySecret    string
	mqttBroker     string
	mqttClientID   string
	mqttUsername   string
	mqttPassword   string
	client         mqtt.Client
	httpClient     *http.Client
	inFlight       map[string]bool
	paired         bool
	connected      bool
	lastError      string
	lastEventAt    *time.Time
	started        bool
}

func newLockSecureNotificationMonitorFromEnvironment() *lockSecureNotificationMonitor {
	base := filepath.Dir(getenv("PUSH_STATE_PATH", "/data/software-notifications.json"))
	m := &lockSecureNotificationMonitor{
		prefsPath:    filepath.Join(base, "lock-secure-prefs.json"),
		statePath:    filepath.Join(base, "lock-secure-state.json"),
		mqttBroker:   getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883"),
		mqttClientID: getenv("MQTT_CLIENT_ID", "my-t-companion") + "-lock-secure",
		mqttUsername: strings.TrimSpace(os.Getenv("MQTT_USERNAME")),
		mqttPassword: strings.TrimSpace(os.Getenv("MQTT_PASSWORD")),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		inFlight:     map[string]bool{},
		store: lockSecureStore{
			Cars:      map[int]lockSecureCarState{},
			Delivered: map[string]string{},
		},
		prefs: lockSecurePrefs{
			Enabled: false,
			Sound:   "default",
		},
	}
	m.loadPrefs()
	m.loadState()
	m.loadPairingFromDisk()
	return m
}

func (m *lockSecureNotificationMonitor) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	m.connectMQTT()
}

func (m *lockSecureNotificationMonitor) stop() {
	m.mu.Lock()
	client := m.client
	m.client = nil
	m.connected = false
	m.started = false
	m.mu.Unlock()
	if client != nil && client.IsConnected() {
		client.Disconnect(250)
	}
}

func (m *lockSecureNotificationMonitor) configure(pairing softwarePushPairing) error {
	if len(pairing.InstallationID) != 48 {
		return fmt.Errorf("missing pairing values")
	}
	if _, err := hex.DecodeString(pairing.InstallationID); err != nil {
		return err
	}
	if _, err := hex.DecodeString(pairing.RelaySecret); err != nil {
		return err
	}
	if !isTrustedSoftwarePushRelayURL(pairing.RelayURL) {
		return fmt.Errorf("untrusted relay")
	}
	m.mu.Lock()
	m.installationID = pairing.InstallationID
	m.relayURL = pairing.RelayURL
	m.relaySecret = pairing.RelaySecret
	m.paired = true
	m.lastError = ""
	m.mu.Unlock()
	return nil
}

func (m *lockSecureNotificationMonitor) disablePairing() {
	m.mu.Lock()
	m.paired = false
	m.installationID = ""
	m.relayURL = ""
	m.relaySecret = ""
	m.prefs.Enabled = false
	m.prefs.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_ = m.savePrefsLocked()
	m.mu.Unlock()
}

func (m *lockSecureNotificationMonitor) loadPairingFromDisk() {
	path := filepath.Join(filepath.Dir(m.statePath), "software-push-pairing.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pairing softwarePushPairing
	if json.Unmarshal(data, &pairing) != nil {
		return
	}
	_ = m.configure(pairing)
}

func (m *lockSecureNotificationMonitor) connectMQTT() {
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
		log.Printf("[warn] lock-secure MQTT disconnected: %v", err)
	})
	options.SetOnConnectHandler(func(client mqtt.Client) {
		fields := []string{
			"locked", "is_user_present", "state", "shift_state",
			"doors_open", "trunk_open", "frunk_open", "display_name",
		}
		topics := map[string]byte{}
		for _, field := range fields {
			topics["teslamate/cars/+/"+field] = 1
		}
		token := client.SubscribeMultiple(topics, m.handleMQTTMessage)
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			m.mu.Lock()
			m.connected = true
			m.lastError = ""
			m.mu.Unlock()
			log.Printf("[info] subscribed to TeslaMate lock-secure MQTT topics")
			return
		}
		err := token.Error()
		if err == nil {
			err = fmt.Errorf("MQTT lock-secure subscription timed out")
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
		m.lastError = "MQTT lock-secure connection timed out"
		m.mu.Unlock()
		return
	}
	if err := token.Error(); err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("[warn] lock-secure MQTT initial connection: %v", err)
	}
}

func (m *lockSecureNotificationMonitor) handleMQTTMessage(_ mqtt.Client, message mqtt.Message) {
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

func parseMQTTBool(value string) *bool {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "true", "1":
		t := true
		return &t
	case "false", "0":
		t := false
		return &t
	default:
		return nil
	}
}

func (m *lockSecureNotificationMonitor) observe(carID int, field, raw string, observedAt time.Time) {
	m.mu.Lock()
	state := m.store.Cars[carID]
	switch field {
	case "display_name":
		state.DisplayName = normalizedMQTTValue(raw)
	case "locked":
		state.Locked = parseMQTTBool(raw)
	case "is_user_present":
		state.UserPresent = parseMQTTBool(raw)
	case "state":
		state.State = strings.ToLower(normalizedMQTTValue(raw))
	case "shift_state":
		state.ShiftState = strings.ToUpper(normalizedMQTTValue(raw))
	case "doors_open":
		state.DoorsOpen = parseMQTTBool(raw)
	case "trunk_open":
		state.TrunkOpen = parseMQTTBool(raw)
	case "frunk_open":
		state.FrunkOpen = parseMQTTBool(raw)
	default:
		m.mu.Unlock()
		return
	}
	secure := lockSecureIsSecure(state)
	wasSecure := state.LastSecure
	wasInitialized := state.Initialized
	if state.Locked != nil && state.UserPresent != nil {
		state.Initialized = true
	}
	state.LastSecure = secure
	m.store.Cars[carID] = state
	_ = m.saveStateLocked()
	// Retained MQTT values describe the current snapshot, not necessarily a new
	// lock transition. Establish a per-car baseline before emitting events.
	if !wasInitialized {
		m.mu.Unlock()
		return
	}

	shouldPush := m.prefs.Enabled && m.paired && m.installationID != "" &&
		secure && !wasSecure
	if !shouldPush {
		m.mu.Unlock()
		return
	}
	// Cooldown 3 minutes per car.
	if state.LastPushedAt != "" {
		if t, err := time.Parse(time.RFC3339, state.LastPushedAt); err == nil {
			if observedAt.Sub(t) < 3*time.Minute {
				m.mu.Unlock()
				return
			}
		}
	}
	event := m.makeEvent(carID, state, observedAt)
	if _, ok := m.store.Delivered[event.EventID]; ok {
		m.mu.Unlock()
		return
	}
	if m.inFlight[event.EventID] {
		m.mu.Unlock()
		return
	}
	m.inFlight[event.EventID] = true
	state.LastPushedAt = observedAt.Format(time.RFC3339)
	m.store.Cars[carID] = state
	_ = m.saveStateLocked()
	m.mu.Unlock()
	go m.deliverWithRetry(*event)
}

func lockSecureIsSecure(state lockSecureCarState) bool {
	// Hard prerequisites: locked + no one inside (both must be known true/false).
	if state.Locked == nil || !*state.Locked {
		return false
	}
	if state.UserPresent == nil || *state.UserPresent {
		return false
	}
	// Not driving.
	shift := strings.ToUpper(strings.TrimSpace(state.ShiftState))
	if shift == "D" || shift == "R" || shift == "N" {
		return false
	}
	st := strings.ToLower(strings.TrimSpace(state.State))
	if st == "driving" {
		return false
	}
	// Prefer closed doors when known.
	if state.DoorsOpen != nil && *state.DoorsOpen {
		return false
	}
	if state.TrunkOpen != nil && *state.TrunkOpen {
		return false
	}
	if state.FrunkOpen != nil && *state.FrunkOpen {
		return false
	}
	return true
}

func normalizeLockSecureSound(sound string) string {
	s := strings.TrimSpace(sound)
	if s == "" {
		return "default"
	}
	if lockSecureSoundWhitelist[s] {
		return s
	}
	return "default"
}

func (m *lockSecureNotificationMonitor) makeEvent(
	carID int,
	state lockSecureCarState,
	observedAt time.Time,
) *lockSecurePushEvent {
	// Edge identity: car + day-minute of first secure transition.
	bucket := observedAt.UTC().Format("200601021504")
	idInput := fmt.Sprintf("%s:%d:vehicle_lock_secure:%s", m.installationID, carID, bucket)
	idHash := sha256.Sum256([]byte(idInput))
	return &lockSecurePushEvent{
		EventID:        hex.EncodeToString(idHash[:16]),
		InstallationID: m.installationID,
		CarID:          carID,
		VehicleName:    state.DisplayName,
		Type:           "vehicle_lock_secure",
		ObservedAt:     observedAt.Format(time.RFC3339),
	}
}

func (m *lockSecureNotificationMonitor) deliverWithRetry(event lockSecurePushEvent) {
	defer func() {
		m.mu.Lock()
		delete(m.inFlight, event.EventID)
		m.mu.Unlock()
	}()
	delays := []time.Duration{0, 5 * time.Second, 30 * time.Second, 2 * time.Minute}
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		if err := m.deliver(event); err != nil {
			m.mu.Lock()
			m.lastError = err.Error()
			m.mu.Unlock()
			log.Printf("[warn] lock-secure push event=%s attempt=%d: %v", event.EventID, attempt+1, err)
			continue
		}
		m.mu.Lock()
		m.store.Delivered[event.EventID] = event.ObservedAt
		m.lastError = ""
		now := time.Now().UTC()
		m.lastEventAt = &now
		_ = m.saveStateLocked()
		m.mu.Unlock()
		log.Printf("[info] lock-secure push delivered event=%s car=%d", event.EventID, event.CarID)
		return
	}
}

func (m *lockSecureNotificationMonitor) deliver(event lockSecurePushEvent) error {
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

func (m *lockSecureNotificationMonitor) status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := map[string]any{
		"enabled":         m.prefs.Enabled,
		"sound":           normalizeLockSecureSound(m.prefs.Sound),
		"confirmed":       m.prefs.Enabled && m.paired,
		"ready":           m.prefs.Enabled && m.paired && m.connected && m.lastError == "",
		"paired":          m.paired,
		"mqtt_connected":  m.connected,
		"capability":      "lock_secure_push",
		"sound_selection": "device_local",
		"sounds": []string{
			"none",
			"default",
			"lock_secure_user.caf",
			"lock_secure_chime.caf",
			"lock_secure_bell.caf",
			"lock_secure_soft.caf",
			"lock_secure_ack.caf",
			"lock_secure_confirm.caf",
			"lock_secure_sent.caf",
		},
		"requires": []string{"locked", "is_user_present_false"},
	}
	if m.prefs.UpdatedAt != "" {
		result["updated_at"] = m.prefs.UpdatedAt
	}
	if m.lastEventAt != nil {
		result["last_delivered_at"] = m.lastEventAt.Format(time.RFC3339)
	}
	if m.lastError != "" {
		result["last_error"] = m.lastError
	}
	return result
}

type lockSecurePutBody struct {
	Enabled *bool  `json:"enabled"`
	Sound   string `json:"sound"`
}

func (m *lockSecureNotificationMonitor) applyPreferences(body lockSecurePutBody) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if body.Enabled != nil && *body.Enabled {
		if !m.paired || m.installationID == "" {
			return nil, fmt.Errorf("not_paired")
		}
		m.prefs.Enabled = true
	} else if body.Enabled != nil {
		m.prefs.Enabled = false
	}
	if strings.TrimSpace(body.Sound) != "" {
		m.prefs.Sound = normalizeLockSecureSound(body.Sound)
	}
	m.prefs.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := m.savePrefsLocked(); err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled":   m.prefs.Enabled,
		"sound":     normalizeLockSecureSound(m.prefs.Sound),
		"confirmed": m.prefs.Enabled && m.paired,
		"paired":    m.paired,
	}, nil
}

func (m *lockSecureNotificationMonitor) loadPrefs() {
	data, err := os.ReadFile(m.prefsPath)
	if err != nil {
		return
	}
	var prefs lockSecurePrefs
	if json.Unmarshal(data, &prefs) == nil {
		m.prefs = prefs
		m.prefs.Sound = normalizeLockSecureSound(m.prefs.Sound)
	}
}

func (m *lockSecureNotificationMonitor) savePrefsLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.prefsPath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(m.prefs)
	if err != nil {
		return err
	}
	temp := m.prefsPath + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, m.prefsPath)
}

func (m *lockSecureNotificationMonitor) loadState() {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var stored lockSecureStore
	if json.Unmarshal(data, &stored) == nil {
		if stored.Cars != nil {
			m.store.Cars = stored.Cars
		}
		if stored.Delivered != nil {
			m.store.Delivered = stored.Delivered
		}
	}
}

func (m *lockSecureNotificationMonitor) saveStateLocked() error {
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
