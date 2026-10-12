package main

// Shared source isolation and receipt diagnostics. These clocks describe local
// MQTT receipt only: TeslaMate scalar topics have no vehicle sample timestamp.
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const mqttMaximumCars = 256
const mqttMaximumFields = 96
const mqttFleetGrace = 5 * time.Minute
const mqttFleetQuery = `SELECT c.id, cs.enabled FROM cars c LEFT JOIN car_settings cs ON cs.id = c.settings_id ORDER BY c.id LIMIT 257`

type mqttFieldReceipt struct {
	ReceivedAt        time.Time  `json:"received_at"`
	LastLiveReceiptAt *time.Time `json:"last_live_receipt_at,omitempty"`
	Retained          bool       `json:"retained"`
	ValueStatus       string     `json:"value_status"`
	ConnectionEpoch   uint64     `json:"connection_epoch"`
	SourceObservedAt  *time.Time `json:"source_observed_at"`
}
type mqttConnectionReceipt struct {
	Connected bool   `json:"connected"`
	Epoch     uint64 `json:"epoch"`
}
type mqttCarReceipt struct {
	FirstReceivedAt time.Time
	LastReceivedAt  time.Time
	Fields          map[string]mqttFieldReceipt
}
type teslaMateMQTTRuntime struct {
	mu               sync.RWMutex
	database         *sql.DB
	cars             map[int]bool
	receipts         map[int]*mqttCarReceipt
	connections      map[string]mqttConnectionReceipt
	lastFleetSuccess time.Time
	lastFleetAttempt time.Time
	baselineRetryAt  time.Time
	fleetError       string
	refreshCh        chan struct{}
	handlers         map[string]mqttFleetMonitor
}

type mqttFleetMonitor struct {
	refresh   func()
	reconcile func(map[int]bool)
}

var mqttSourceRuntime atomic.Pointer[teslaMateMQTTRuntime]

func mqttNamespace() (string, error) {
	namespace := os.Getenv("MQTT_NAMESPACE")
	// This exact alphabet also survives installer dotenv/Compose expansion.
	// Never silently normalize to another source (including outer whitespace).
	if len(namespace) > 128 || strings.IndexFunc(namespace, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-')
	}) >= 0 {
		return "", fmt.Errorf("invalid MQTT_NAMESPACE: use at most 128 ASCII letters, digits, dots, underscores or hyphens")
	}
	return namespace, nil
}
func teslaMateMQTTTopics(fields []string) map[string]byte {
	namespace, err := mqttNamespace()
	if err != nil {
		return map[string]byte{}
	}
	prefix := "teslamate/"
	if namespace != "" {
		prefix += namespace + "/"
	}
	topics := make(map[string]byte, len(fields))
	for _, field := range fields {
		if field != "" && !strings.ContainsAny(field, "/+#") {
			topics[prefix+"cars/+/"+field] = 1
		}
	}
	return topics
}
func parseTeslaMateMQTTTopic(topic string) (int, string, bool) {
	namespace, err := mqttNamespace()
	if err != nil {
		return 0, "", false
	}
	prefix := "teslamate/"
	if namespace != "" {
		prefix += namespace + "/"
	}
	prefix += "cars/"
	if !strings.HasPrefix(topic, prefix) {
		return 0, "", false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 2 || len(parts[1]) == 0 || len(parts[1]) > 80 || strings.ContainsAny(parts[1], "+#\x00") {
		return 0, "", false
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 || strconv.Itoa(id) != parts[0] {
		return 0, "", false
	}
	return id, parts[1], true
}
func acceptTeslaMateMQTTMessage(message mqtt.Message, monitor ...string) (int, string, bool) {
	id, field, ok := parseTeslaMateMQTTTopic(message.Topic())
	if !ok || message.Duplicate() || len(message.Payload()) > 16<<10 || !mqttCarEligible(id) {
		return 0, "", false
	}
	if runtime := mqttSourceRuntime.Load(); runtime != nil {
		kind := "source"
		if len(monitor) > 0 {
			kind = monitor[0]
		}
		runtime.record(id, kind, field, message, time.Now().UTC())
	}
	return id, field, true
}
func mqttCarEligible(id int) bool {
	if id <= 0 {
		return false
	}
	runtime := mqttSourceRuntime.Load()
	if runtime == nil {
		return true
	} // isolated constructors/tests, before runtime installation
	runtime.mu.RLock()
	enabled, exists := runtime.cars[id]
	fresh := mqttFleetSnapshotUsable(runtime.lastFleetSuccess, time.Now())
	runtime.mu.RUnlock()
	if !exists || !fresh {
		select {
		case runtime.refreshCh <- struct{}{}:
		default:
		}
	}
	return exists && enabled && fresh
}

func mqttFleetSnapshotUsable(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= mqttFleetGrace
}

// Percentages have no vehicle-source timestamp either. Return null for absent,
// invalid, disconnected or previous-connection receipts, never a fabricated 0.
func mqttSoftwarePercent(carID int, field string, value int) any {
	r := mqttSourceRuntime.Load()
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	car, connection := r.receipts[carID], r.connections["software"]
	if !r.cars[carID] || !mqttFleetSnapshotUsable(r.lastFleetSuccess, time.Now()) || !connection.Connected || car == nil {
		return nil
	}
	receipt, exists := car.Fields["software."+field]
	if !exists || receipt.ValueStatus != "known" || receipt.ConnectionEpoch != connection.Epoch {
		return nil
	}
	return value
}

// A successful SUBACK belongs to the connection on which it was requested.
// The client object survives Paho reconnects, so pointer identity alone cannot
// prevent a late result from reviving a disconnected or newer connection.
func recordMQTTConnection(kind string, connected bool, expectedEpoch ...uint64) bool {
	if runtime := mqttSourceRuntime.Load(); runtime != nil {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		c := runtime.connections[kind]
		if connected && len(expectedEpoch) > 0 && c.Epoch != expectedEpoch[0] {
			return false
		}
		if !connected {
			c.Epoch++
		}
		c.Connected = connected
		runtime.connections[kind] = c
	}
	return true
}

func beginMQTTConnection(kind string) uint64 {
	if runtime := mqttSourceRuntime.Load(); runtime != nil {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		c := runtime.connections[kind]
		c.Epoch++
		c.Connected = false
		runtime.connections[kind] = c
		return c.Epoch
	}
	return 0
}

func mqttConnectionEpoch(kind string) uint64 {
	if runtime := mqttSourceRuntime.Load(); runtime != nil {
		runtime.mu.RLock()
		defer runtime.mu.RUnlock()
		return runtime.connections[kind].Epoch
	}
	return 0
}

func registerMQTTFleetMonitor(kind string, refresh func(), reconcile func(map[int]bool)) {
	if r := mqttSourceRuntime.Load(); r != nil {
		r.mu.Lock()
		if r.handlers == nil {
			r.handlers = map[string]mqttFleetMonitor{}
		}
		r.handlers[kind] = mqttFleetMonitor{refresh: refresh, reconcile: reconcile}
		cars := make(map[int]bool, len(r.cars))
		for id, enabled := range r.cars {
			cars[id] = enabled
		}
		known := !r.lastFleetSuccess.IsZero()
		r.mu.Unlock()
		if reconcile != nil && known {
			reconcile(cars)
		}
	}
}

func refreshMQTTFleetSubscriptions() {
	r := mqttSourceRuntime.Load()
	if r == nil {
		return
	}
	r.mu.RLock()
	handlers := make([]mqttFleetMonitor, 0, len(r.handlers))
	for _, handler := range r.handlers {
		handlers = append(handlers, handler)
	}
	r.mu.RUnlock()
	var group sync.WaitGroup
	for _, handler := range handlers {
		if handler.refresh != nil {
			group.Add(1)
			go func(refresh func()) { defer group.Done(); refresh() }(handler.refresh)
		}
	}
	group.Wait()
}

// Subscription failure must not strand a new car until another fleet change.
// Keep one bounded retry timestamp, not a goroutine/timer for every car/topic.
func requestMQTTBaselineRetry() {
	if r := mqttSourceRuntime.Load(); r != nil {
		r.mu.Lock()
		if r.baselineRetryAt.IsZero() {
			r.baselineRetryAt = time.Now().UTC().Add(30 * time.Second)
		}
		r.mu.Unlock()
	}
}

func (r *teslaMateMQTTRuntime) retryBaselines(now time.Time) {
	r.mu.Lock()
	due := !r.baselineRetryAt.IsZero() && !now.Before(r.baselineRetryAt)
	if due {
		r.baselineRetryAt = time.Time{}
	}
	r.mu.Unlock()
	if due {
		refreshMQTTFleetSubscriptions()
	}
}
func mqttReceiptValueStatus(field string, payload []byte) string {
	raw := normalizedMQTTValue(string(payload))
	if raw == "" {
		return "unknown"
	}
	if field == "active_route" || field == "software_update" {
		if !json.Valid(payload) {
			return "invalid"
		}
		return "received_json" // syntax is not a claim of semantic completeness
	}
	switch field {
	case "locked", "doors_open", "trunk_open", "frunk_open", "sentry_mode", "plugged_in", "healthy", "windows_open", "service_mode", "is_user_present", "is_climate_on", "is_preconditioning", "battery_heater", "charge_port_door_open", "driver_front_door_open", "driver_rear_door_open", "passenger_front_door_open", "passenger_rear_door_open", "driver_front_window_open", "driver_rear_window_open", "passenger_front_window_open", "passenger_rear_window_open", "sun_roof_installed":
		if parseMQTTBool(raw) == nil {
			return "invalid"
		}
	case "battery_level", "download_perc", "install_perc":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100 {
			return "invalid"
		}
	case "rated_battery_range_km", "speed", "odometer", "latitude", "longitude", "elevation", "power":
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return "invalid"
		}
		if field == "rated_battery_range_km" && (n < 0 || n > 1000) {
			return "invalid"
		}
	}
	return "known"
}
func (r *teslaMateMQTTRuntime) record(id int, kind, field string, message mqtt.Message, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	car := r.receipts[id]
	if car == nil {
		if len(r.receipts) >= mqttMaximumCars {
			return
		}
		car = &mqttCarReceipt{FirstReceivedAt: at, Fields: map[string]mqttFieldReceipt{}}
		r.receipts[id] = car
	}
	key := kind + "." + field
	previous, exists := car.Fields[key]
	if !exists && len(car.Fields) >= mqttMaximumFields {
		return
	}
	receipt := mqttFieldReceipt{ReceivedAt: at, Retained: message.Retained(), ValueStatus: mqttReceiptValueStatus(field, message.Payload()), ConnectionEpoch: r.connections[kind].Epoch}
	if !message.Retained() && !message.Duplicate() {
		receipt.LastLiveReceiptAt = &at
	} else {
		receipt.LastLiveReceiptAt = previous.LastLiveReceiptAt
	}
	car.LastReceivedAt = at
	car.Fields[key] = receipt
}
func newTeslaMateMQTTRuntime(database *sql.DB) *teslaMateMQTTRuntime {
	return &teslaMateMQTTRuntime{database: database, cars: map[int]bool{}, receipts: map[int]*mqttCarReceipt{}, connections: map[string]mqttConnectionReceipt{}, refreshCh: make(chan struct{}, 1)}
}
func startTeslaMateMQTTRuntime(database *sql.DB) func() {
	r := newTeslaMateMQTTRuntime(database)
	mqttSourceRuntime.Store(r)
	r.refreshFleet(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-r.refreshCh:
			}
			r.mu.RLock()
			tooSoon := time.Since(r.lastFleetAttempt) < time.Second
			r.mu.RUnlock()
			if !tooSoon {
				r.refreshFleet(ctx)
			}
			if ctx.Err() == nil {
				r.retryBaselines(time.Now().UTC())
			}
		}
	}()
	return func() { cancel(); <-done; mqttSourceRuntime.CompareAndSwap(r, nil) }
}
func (r *teslaMateMQTTRuntime) refreshFleet(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	r.mu.Lock()
	r.lastFleetAttempt = time.Now().UTC()
	r.mu.Unlock()
	if r.database == nil {
		r.fleetFailure("fleet_read_unavailable")
		return
	}
	rows, err := r.database.QueryContext(ctx, mqttFleetQuery)
	if err != nil {
		r.fleetFailure("fleet_read_unavailable")
		return
	}
	defer rows.Close()
	cars := map[int]bool{}
	for rows.Next() {
		var id int
		var enabled sql.NullBool
		if rows.Scan(&id, &enabled) != nil || id <= 0 {
			r.fleetFailure("fleet_read_invalid")
			return
		}
		cars[id] = enabled.Valid && enabled.Bool
		if len(cars) > mqttMaximumCars {
			r.fleetFailure("fleet_capacity_exceeded")
			return
		}
	}
	if rows.Err() != nil {
		r.fleetFailure("fleet_read_unavailable")
		return
	}
	r.applyFleet(cars, time.Now().UTC())
}
func (r *teslaMateMQTTRuntime) fleetFailure(code string) {
	r.mu.Lock()
	r.fleetError = code
	r.mu.Unlock()
}
func (r *teslaMateMQTTRuntime) applyFleet(cars map[int]bool, at time.Time) {
	r.mu.Lock()
	added := false
	for id, enabled := range cars {
		if enabled && !r.cars[id] {
			added = true
		}
	}
	wasInitialized := !r.lastFleetSuccess.IsZero()
	r.cars = cars
	r.lastFleetSuccess = at
	r.fleetError = ""
	for id := range r.receipts {
		if !cars[id] {
			delete(r.receipts, id)
		}
	}
	handlers := make([]mqttFleetMonitor, 0, len(r.handlers))
	for _, handler := range r.handlers {
		handlers = append(handlers, handler)
	}
	copyCars := make(map[int]bool, len(cars))
	for id, enabled := range cars {
		copyCars[id] = enabled
	}
	r.mu.Unlock()
	for _, handler := range handlers {
		if handler.reconcile != nil {
			handler.reconcile(copyCars)
		}
	}
	// The first new-car publications can precede this refresh. Re-subscription
	// recovers their retained baselines instead of waiting forever for unchanged
	// scalar fields to publish again. Never infer a fresh action from that replay.
	if added && wasInitialized {
		refreshMQTTFleetSubscriptions()
	}
}
func mqttSourceDiagnostics(carID int) map[string]any {
	r := mqttSourceRuntime.Load()
	if r == nil {
		return map[string]any{"status": "not_started", "source_observed_at": nil}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.cars[carID]
	enabled := r.cars[carID]
	fleetUsable := mqttFleetSnapshotUsable(r.lastFleetSuccess, time.Now())
	state := "waiting_for_fleet"
	if !r.lastFleetSuccess.IsZero() {
		switch {
		case !exists:
			state = "not_in_source_fleet"
		case !enabled:
			state = "logging_disabled"
		default:
			state = "waiting_for_mqtt"
		}
	}
	if !r.lastFleetSuccess.IsZero() && !fleetUsable {
		state = "stale_fleet"
	} else if r.lastFleetSuccess.IsZero() && r.fleetError != "" {
		state = "fleet_unavailable"
	}
	connections := make(map[string]mqttConnectionReceipt, len(r.connections))
	for key, c := range r.connections {
		connections[key] = c
	}
	out := map[string]any{
		"status": state, "car_id": carID, "source_fleet_known": exists, "logging_enabled": enabled,
		"fleet_last_success_at": optionalMQTTTime(r.lastFleetSuccess), "fleet_last_error": emptyToNil(r.fleetError),
		"fleet_snapshot_usable": fleetUsable,
		"source_observed_at":    nil, "receipt_is_vehicle_sample_time": false,
		"connections": connections, "notification_default_policy": "inherit_server_defaults_unless_vehicle_override",
		"friend_grant_policy": "explicit_car_id_only",
	}
	if car := r.receipts[carID]; car != nil {
		fields := make(map[string]mqttFieldReceipt, len(car.Fields))
		for key, v := range car.Fields {
			fields[key] = v
		}
		missing := []string{}
		for _, field := range []string{"battery_level", "rated_battery_range_km", "locked"} {
			v, ok := fields["parking."+field]
			if !ok || v.ValueStatus != "known" || v.ConnectionEpoch != r.connections["parking"].Epoch {
				missing = append(missing, field)
			}
		}
		out["first_received_at"] = car.FirstReceivedAt
		out["last_received_at"] = car.LastReceivedAt
		out["fields"] = fields
		out["core_missing_fields"] = missing
		out["core_baseline_complete"] = len(missing) == 0
		out["core_current_connection_complete"] = len(missing) == 0 && r.connections["parking"].Connected && enabled && fleetUsable
		out["status"] = "receiving"
		if len(missing) == 0 {
			out["status"] = "baseline_received"
		}
		if !r.connections["parking"].Connected {
			out["status"] = "last_known_only"
		}
		if !enabled || !fleetUsable {
			out["status"] = state
		}
	}
	return out
}
func optionalMQTTTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at
}
func mqttFleetDiagnostics() map[string]any {
	r := mqttSourceRuntime.Load()
	if r == nil {
		return map[string]any{"status": "not_started"}
	}
	r.mu.RLock()
	lastSuccess, lastError := r.lastFleetSuccess, r.fleetError
	ids := make([]int, 0, len(r.cars))
	for id := range r.cars {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	sort.Ints(ids)
	cars := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		cars = append(cars, mqttSourceDiagnostics(id))
	}
	namespace, _ := mqttNamespace()
	status := "available"
	usable := mqttFleetSnapshotUsable(lastSuccess, time.Now())
	if lastSuccess.IsZero() {
		status = "waiting_for_fleet"
		if lastError != "" {
			status = "fleet_unavailable"
		}
	} else if !usable {
		status = "stale_fleet"
	}
	return map[string]any{"namespace": namespace, "maximum_tracked_cars": mqttMaximumCars, "cars": cars,
		"status": status, "fleet_last_success_at": optionalMQTTTime(lastSuccess), "fleet_last_error": emptyToNil(lastError), "fleet_snapshot_usable": usable}
}
