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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Official Cloudflare-hosted product push host (primary).
const officialSoftwarePushRelayURL = "https://push.my-tesla.app/v1/events"
const officialVehicleRegistrationURL = "https://push.my-tesla.app/v1/vehicle-registrations"

func isTrustedSoftwarePushRelayURL(raw string) bool {
	return strings.TrimSpace(raw) == officialSoftwarePushRelayURL
}

func newPushRelayHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		// The only accepted destination is the official HTTPS endpoint. Do not
		// follow redirects to another host even if that endpoint is ever
		// misconfigured or compromised.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (m *softwareNotificationMonitor) disable() error {
	m.stop()
	m.mu.Lock()
	m.enabled = false
	m.installationID = ""
	m.relayURL = ""
	m.relaySecret = ""
	m.lastError = ""
	m.mu.Unlock()
	err := os.Remove(m.pairingPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type softwareNotificationEvent struct {
	EventID        string `json:"event_id"`
	InstallationID string `json:"installation_id"`
	CarID          int    `json:"car_id"`
	VehicleName    string `json:"vehicle_name,omitempty"`
	Type           string `json:"type"`
	CurrentVersion string `json:"current_version,omitempty"`
	UpdateVersion  string `json:"update_version,omitempty"`
	ObservedAt     string `json:"observed_at"`
}

type carSoftwareState struct {
	DisplayName     string `json:"display_name,omitempty"`
	Version         string `json:"version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateVersion   string `json:"update_version,omitempty"`
	DownloadPercent int    `json:"download_percent,omitempty"`
	InstallPercent  int    `json:"install_percent,omitempty"`
}

type softwareNotificationStore struct {
	Cars      map[int]carSoftwareState `json:"cars"`
	Delivered map[string]string        `json:"delivered"`
}

type softwarePushPairing struct {
	InstallationID string `json:"installation_id"`
	RelayURL       string `json:"relay_url"`
	RelaySecret    string `json:"relay_secret"`
}

type softwareNotificationMonitor struct {
	mu             sync.Mutex
	store          softwareNotificationStore
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
	inFlight       map[string]bool
	enabled        bool
	connected      bool
	lastEventAt    *time.Time
	lastError      string
	started        bool
}

func newSoftwareNotificationMonitorFromEnvironment() *softwareNotificationMonitor {
	installationID := strings.TrimSpace(os.Getenv("PUSH_INSTALLATION_ID"))
	relayURL := strings.TrimSpace(os.Getenv("PUSH_RELAY_URL"))
	relaySecret := strings.TrimSpace(os.Getenv("PUSH_RELAY_SECRET"))
	monitor := &softwareNotificationMonitor{
		statePath:      getenv("PUSH_STATE_PATH", "/data/software-notifications.json"),
		installationID: installationID,
		relayURL:       relayURL,
		relaySecret:    relaySecret,
		mqttBroker:     getenv("MQTT_BROKER_URL", "tcp://mosquitto:1883"),
		mqttClientID:   getenv("MQTT_CLIENT_ID", "my-t-companion"),
		mqttUsername:   strings.TrimSpace(os.Getenv("MQTT_USERNAME")),
		mqttPassword:   strings.TrimSpace(os.Getenv("MQTT_PASSWORD")),
		httpClient:     newPushRelayHTTPClient(12 * time.Second),
		inFlight:       map[string]bool{},
		store: softwareNotificationStore{
			Cars:      map[int]carSoftwareState{},
			Delivered: map[string]string{},
		},
	}
	monitor.enabled = installationID != "" && relayURL != "" && relaySecret != ""
	monitor.load()
	monitor.loadPairing()
	return monitor
}

func (m *softwareNotificationMonitor) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	enabled := m.enabled
	m.mu.Unlock()
	if !enabled {
		log.Printf("[info] vehicle software push disabled; relay pairing is not configured")
		return
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
		log.Printf("[warn] TeslaMate MQTT disconnected: %v", err)
	})
	options.SetOnConnectHandler(func(client mqtt.Client) {
		token := client.SubscribeMultiple(map[string]byte{
			"teslamate/cars/+/display_name":     1,
			"teslamate/cars/+/version":          1,
			"teslamate/cars/+/update_available": 1,
			"teslamate/cars/+/update_version":   1,
			"teslamate/cars/+/download_perc":    1,
			"teslamate/cars/+/install_perc":     1,
		}, m.handleMQTTMessage)
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			m.mu.Lock()
			m.connected = true
			m.lastError = ""
			m.mu.Unlock()
			log.Printf("[info] subscribed to TeslaMate software update MQTT topics")
			return
		}
		err := token.Error()
		if err == nil {
			err = fmt.Errorf("MQTT subscription timed out")
		}
		m.mu.Lock()
		m.connected = false
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("[error] TeslaMate MQTT subscribe: %v", err)
	})

	client := mqtt.NewClient(options)
	m.mu.Lock()
	m.client = client
	m.mu.Unlock()
	token := client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		m.mu.Lock()
		m.lastError = "MQTT initial connection timed out"
		m.mu.Unlock()
		return
	}
	if err := token.Error(); err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("[warn] TeslaMate MQTT initial connection: %v", err)
	}
}

func (m *softwareNotificationMonitor) configure(pairing softwarePushPairing) error {
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
	relayURL, err := url.Parse(pairing.RelayURL)
	if err != nil || !isTrustedSoftwarePushRelayURL(relayURL.String()) {
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
	path := m.pairingPath()
	data, err := json.Marshal(pairing)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0700)
	}
	if err == nil {
		err = os.WriteFile(path+".tmp", data, 0600)
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		m.mu.Unlock()
		return err
	}
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

func (m *softwareNotificationMonitor) loadPairing() {
	data, err := os.ReadFile(m.pairingPath())
	if err != nil {
		return
	}
	var pairing softwarePushPairing
	if json.Unmarshal(data, &pairing) != nil || pairing.InstallationID == "" ||
		pairing.RelayURL == "" || pairing.RelaySecret == "" {
		return
	}
	m.installationID = pairing.InstallationID
	m.relayURL = pairing.RelayURL
	m.relaySecret = pairing.RelaySecret
	m.enabled = true
}

func (m *softwareNotificationMonitor) pairingPath() string {
	return filepath.Join(filepath.Dir(m.statePath), "software-push-pairing.json")
}

func (m *softwareNotificationMonitor) stop() {
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

func (m *softwareNotificationMonitor) handleMQTTMessage(_ mqtt.Client, message mqtt.Message) {
	parts := strings.Split(message.Topic(), "/")
	if len(parts) != 4 || parts[0] != "teslamate" || parts[1] != "cars" {
		return
	}
	carID, err := strconv.Atoi(parts[2])
	if err != nil || carID <= 0 {
		return
	}
	field := parts[3]
	switch field {
	case "display_name", "version", "update_available", "update_version", "download_perc", "install_perc":
	default:
		return
	}
	m.observe(carID, field, strings.TrimSpace(string(message.Payload())), time.Now().UTC())
}

func (m *softwareNotificationMonitor) observe(carID int, field, value string, observedAt time.Time) {
	m.mu.Lock()
	state := m.store.Cars[carID]
	previous := state
	switch field {
	case "display_name":
		state.DisplayName = normalizedMQTTValue(value)
	case "version":
		state.Version = normalizedMQTTValue(value)
	case "update_available":
		state.UpdateAvailable = strings.EqualFold(value, "true") || value == "1"
	case "update_version":
		state.UpdateVersion = normalizedMQTTValue(value)
	case "download_perc":
		state.DownloadPercent, _ = strconv.Atoi(value)
	case "install_perc":
		state.InstallPercent, _ = strconv.Atoi(value)
	}
	m.store.Cars[carID] = state
	_ = m.saveLocked()

	var eventType, eventVersion string
	if state.UpdateAvailable && state.UpdateVersion != "" &&
		(!previous.UpdateAvailable || previous.UpdateVersion != state.UpdateVersion) {
		eventType, eventVersion = "update_available", state.UpdateVersion
	} else if field == "version" && previous.Version != "" && state.Version != "" &&
		previous.Version != state.Version {
		eventType, eventVersion = "update_installed", state.Version
	}
	if eventType == "" {
		m.mu.Unlock()
		return
	}
	originID := fmt.Sprintf("%d:%s:%s", carID, eventType, eventVersion)
	if _, delivered := m.store.Delivered[originID]; delivered {
		m.mu.Unlock()
		return
	}
	if m.inFlight[originID] {
		m.mu.Unlock()
		return
	}
	m.inFlight[originID] = true
	m.mu.Unlock()

	go m.fanOutSoftware(originID, carID, state, eventType, eventVersion, observedAt)
}

func (m *softwareNotificationMonitor) fanOutSoftware(
	originID string,
	carID int,
	state carSoftwareState,
	eventType, eventVersion string,
	observedAt time.Time,
) {
	defer func() {
		m.mu.Lock()
		delete(m.inFlight, originID)
		m.mu.Unlock()
	}()
	subs := []pushSubscriber{}
	if pushRegistry != nil {
		subs = pushRegistry.matching(carID, func(s pushSubscriber) bool { return s.wantsSoftwareUpdate(carID) })
	} else if m.installationID != "" {
		subs = []pushSubscriber{{
			InstallationID: m.installationID,
			RelayURL:       m.relayURL,
			RelaySecret:    m.relaySecret,
			SoftwareUpdate: true,
			Status:         pushStatusActive,
		}}
	}
	deliveredAll := len(subs) > 0
	for _, sub := range subs {
		event := m.makeEvent(sub.InstallationID, carID, state, eventType, eventVersion, observedAt)
		if err := m.deliverTo(sub, *event); err != nil {
			log.Printf("[warn] software fan-out installation=%s: %v", sub.InstallationID[:8], err)
			deliveredAll = false
			continue
		}
	}
	if !deliveredAll {
		return
	}
	m.mu.Lock()
	m.store.Delivered[originID] = observedAt.Format(time.RFC3339)
	m.lastError = ""
	now := time.Now().UTC()
	m.lastEventAt = &now
	_ = m.saveLocked()
	m.mu.Unlock()
}

func (m *softwareNotificationMonitor) makeEvent(
	installationID string,
	carID int,
	state carSoftwareState,
	eventType, eventVersion string,
	observedAt time.Time,
) *softwareNotificationEvent {
	idInput := fmt.Sprintf("%s:%d:%s:%s", installationID, carID, eventType, eventVersion)
	idHash := sha256.Sum256([]byte(idInput))
	return &softwareNotificationEvent{
		EventID:        hex.EncodeToString(idHash[:16]),
		InstallationID: installationID,
		CarID:          carID,
		VehicleName:    state.DisplayName,
		Type:           eventType,
		CurrentVersion: state.Version,
		UpdateVersion:  state.UpdateVersion,
		ObservedAt:     observedAt.Format(time.RFC3339),
	}
}

func (m *softwareNotificationMonitor) deliverTo(sub pushSubscriber, event softwareNotificationEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if pushRegistry != nil {
		return pushRegistry.deliverJSON(sub, payload)
	}
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

func relaySecretBytes(value string) []byte {
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) >= 16 {
		return decoded
	}
	return []byte(value)
}

func (m *softwareNotificationMonitor) carState(carID int) carSoftwareState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.Cars[carID]
}

func (m *softwareNotificationMonitor) status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := map[string]any{
		"enabled":          m.enabled,
		"mqtt_connected":   m.connected,
		"delivery_mode":    "developer_operated_apns_relay",
		"privacy_mode":     "no_vin_location_or_teslamate_credentials",
		"tracked_cars":     len(m.store.Cars),
		"delivered_events": len(m.store.Delivered),
	}
	if m.lastEventAt != nil {
		result["last_delivered_at"] = m.lastEventAt.Format(time.RFC3339)
	}
	if m.lastError != "" {
		result["last_error"] = m.lastError
	}
	return result
}

func (m *softwareNotificationMonitor) load() {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var stored softwareNotificationStore
	if json.Unmarshal(data, &stored) == nil {
		if stored.Cars != nil {
			m.store.Cars = stored.Cars
		}
		if stored.Delivered != nil {
			m.store.Delivered = stored.Delivered
		}
		pruneTimestampMap(m.store.Delivered, time.Now().UTC(), softwareDeliveredRetention, softwareDeliveredMaximum)
	}
}

func (m *softwareNotificationMonitor) saveLocked() error {
	pruneTimestampMap(m.store.Delivered, time.Now().UTC(), softwareDeliveredRetention, softwareDeliveredMaximum)
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

func normalizedMQTTValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "nil", "null", "unknown":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}

func collapsedDisplayName(value string) string {
	return strings.Join(strings.Fields(normalizedMQTTValue(value)), " ")
}
