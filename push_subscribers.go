package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
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
)

const (
	maxPushSubscribers          = 8
	maxPushOutboxItems          = 256
	maxVehicleRegistrationItems = 32
)

type pushSubscriberStatus string

const (
	pushStatusActive pushSubscriberStatus = "active"
	pushStatusPaused pushSubscriberStatus = "paused"
)

type pushSubscriber struct {
	InstallationID         string               `json:"installation_id"`
	RelayURL               string               `json:"relay_url"`
	RelaySecret            string               `json:"relay_secret"`
	Status                 pushSubscriberStatus `json:"status"`
	SoftwareUpdate         bool                 `json:"software_update"`
	LockSecure             bool                 `json:"lock_secure"`
	ChargingLiveActivity   bool                 `json:"charging_live_activity"`
	NavigationLiveActivity bool                 `json:"navigation_live_activity"`
	NavigationTripAlerts   bool                 `json:"navigation_trip_alerts"`
	CarIDs                 []int                `json:"car_ids,omitempty"`
	UpdatedAt              string               `json:"updated_at,omitempty"`
	LastSeenAt             string               `json:"last_seen_at,omitempty"`
}

func (s pushSubscriber) wantsCar(carID int) bool {
	if len(s.CarIDs) == 0 {
		return true
	}
	for _, id := range s.CarIDs {
		if id == carID {
			return true
		}
	}
	return false
}

func (s pushSubscriber) pairing() softwarePushPairing {
	return softwarePushPairing{
		InstallationID: s.InstallationID,
		RelayURL:       s.RelayURL,
		RelaySecret:    s.RelaySecret,
	}
}

type pushSubscriberStore struct {
	Subscribers      []pushSubscriber `json:"subscribers"`
	Outbox           []queuedPush     `json:"outbox,omitempty"`
	VehicleNamespace string           `json:"vehicle_namespace,omitempty"`
}

type vehicleRegistrationReport struct {
	InstallationID string   `json:"installation_id"`
	VehicleAliases []string `json:"vehicle_aliases"`
}

type queuedPush struct {
	ID             string          `json:"id"`
	InstallationID string          `json:"installation_id"`
	Payload        json.RawMessage `json:"payload"`
	EventType      string          `json:"event_type"`
	Attempts       int             `json:"attempts"`
	CreatedAt      string          `json:"created_at"`
	NextAttemptAt  string          `json:"next_attempt_at"`
	ExpiresAt      string          `json:"expires_at"`
}

type pushPayloadMetadata struct {
	EventID string `json:"event_id"`
	Type    string `json:"type"`
}

type pushSubscriberRegistry struct {
	mu       sync.Mutex
	path     string
	store    pushSubscriberStore
	http     *http.Client
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newPushSubscriberRegistry() *pushSubscriberRegistry {
	base := filepath.Dir(getenv("PUSH_STATE_PATH", "/data/software-notifications.json"))
	r := &pushSubscriberRegistry{
		path: filepath.Join(base, "push-subscribers.json"),
		http: newPushRelayHTTPClient(12 * time.Second),
		store: pushSubscriberStore{
			Subscribers: []pushSubscriber{},
			Outbox:      []queuedPush{},
		},
		stopCh: make(chan struct{}),
	}
	r.load()
	r.migrateLegacyPairingLocked()
	if r.ensureVehicleNamespaceLocked() {
		if err := r.saveLocked(); err != nil {
			log.Printf("[warn] vehicle namespace save: %v", err)
		}
	}
	return r
}

func (r *pushSubscriberRegistry) load() {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return
	}
	var stored pushSubscriberStore
	if json.Unmarshal(data, &stored) != nil {
		return
	}
	if stored.Subscribers == nil {
		stored.Subscribers = []pushSubscriber{}
	}
	if stored.Outbox == nil {
		stored.Outbox = []queuedPush{}
	}
	validSubscribers := stored.Subscribers[:0]
	validIDs := map[string]bool{}
	for _, sub := range stored.Subscribers {
		if !validPushSubscriber(sub) {
			log.Printf("[warn] discarded invalid stored push subscriber")
			continue
		}
		validSubscribers = append(validSubscribers, sub)
		validIDs[sub.InstallationID] = true
	}
	stored.Subscribers = validSubscribers
	now := time.Now().UTC()
	validOutbox := stored.Outbox[:0]
	for _, item := range stored.Outbox {
		expires, err := time.Parse(time.RFC3339, item.ExpiresAt)
		if err == nil && expires.After(now) && validIDs[item.InstallationID] && len(item.Payload) <= 16<<10 {
			validOutbox = append(validOutbox, item)
		}
	}
	stored.Outbox = validOutbox
	r.store = stored
}

func (r *pushSubscriberRegistry) start() {
	r.wg.Add(2)
	go r.retryLoop()
	go r.vehicleRegistrationLoop()
}

func (r *pushSubscriberRegistry) stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}

func (r *pushSubscriberRegistry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r.store, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func (r *pushSubscriberRegistry) ensureVehicleNamespaceLocked() bool {
	decoded, err := hex.DecodeString(r.store.VehicleNamespace)
	if err == nil && len(decoded) == 32 {
		return false
	}
	random := make([]byte, 32)
	if _, err := cryptorand.Read(random); err != nil {
		log.Printf("[warn] vehicle namespace generation: %v", err)
		return false
	}
	r.store.VehicleNamespace = hex.EncodeToString(random)
	return true
}

func (r *pushSubscriberRegistry) migrateLegacyPairingLocked() {
	if len(r.store.Subscribers) > 0 {
		return
	}
	pairingPath := filepath.Join(filepath.Dir(r.path), "software-push-pairing.json")
	data, err := os.ReadFile(pairingPath)
	if err != nil {
		return
	}
	var pairing softwarePushPairing
	if json.Unmarshal(data, &pairing) != nil || pairing.InstallationID == "" {
		return
	}
	lockEnabled := false
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(r.path), "lock-secure-prefs.json")); err == nil {
		var prefs lockSecurePrefs
		if json.Unmarshal(raw, &prefs) == nil {
			lockEnabled = prefs.Enabled
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	r.store.Subscribers = []pushSubscriber{{
		InstallationID:         pairing.InstallationID,
		RelayURL:               pairing.RelayURL,
		RelaySecret:            pairing.RelaySecret,
		Status:                 pushStatusActive,
		SoftwareUpdate:         true,
		LockSecure:             lockEnabled,
		ChargingLiveActivity:   true,
		NavigationLiveActivity: true,
		UpdatedAt:              now,
		LastSeenAt:             now,
	}}
	if err := r.saveLocked(); err != nil {
		log.Printf("[warn] push subscriber migration save: %v", err)
		return
	}
	log.Printf("[info] migrated legacy software-push-pairing.json into push-subscribers.json")
}

type pushPairRequest struct {
	InstallationID         string `json:"installation_id"`
	RelayURL               string `json:"relay_url"`
	RelaySecret            string `json:"relay_secret"`
	Status                 string `json:"status,omitempty"`
	Mode                   string `json:"mode,omitempty"`
	SoftwareUpdate         *bool  `json:"software_update,omitempty"`
	LockSecure             *bool  `json:"lock_secure,omitempty"`
	ChargingLiveActivity   *bool  `json:"charging_live_activity,omitempty"`
	NavigationLiveActivity *bool  `json:"navigation_live_activity,omitempty"`
	NavigationTripAlerts   *bool  `json:"navigation_trip_alerts,omitempty"`
	CarIDs                 []int  `json:"car_ids,omitempty"`
}

func (r *pushSubscriberRegistry) upsert(req pushPairRequest) (map[string]any, error) {
	if len(req.InstallationID) != 48 {
		return nil, fmt.Errorf("missing pairing values")
	}
	if _, err := hex.DecodeString(req.InstallationID); err != nil {
		return nil, fmt.Errorf("invalid installation ID")
	}
	secret, err := hex.DecodeString(req.RelaySecret)
	if err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("invalid relay secret")
	}
	if !isTrustedSoftwarePushRelayURL(req.RelayURL) {
		return nil, fmt.Errorf("untrusted relay URL")
	}
	status := pushStatusActive
	if strings.EqualFold(strings.TrimSpace(req.Status), string(pushStatusPaused)) {
		status = pushStatusPaused
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode != "replace" {
		mode = "join"
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	idx := r.indexLocked(req.InstallationID)
	known := idx >= 0
	if !known && len(r.store.Subscribers) >= maxPushSubscribers {
		return nil, fmt.Errorf("subscriber_limit")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var sub pushSubscriber
	if known {
		sub = r.store.Subscribers[idx]
	} else {
		sub = pushSubscriber{
			InstallationID: req.InstallationID,
		}
	}
	sub.RelayURL = req.RelayURL
	sub.RelaySecret = req.RelaySecret
	sub.Status = status
	sub.UpdatedAt = now
	sub.LastSeenAt = now
	if req.SoftwareUpdate != nil {
		sub.SoftwareUpdate = *req.SoftwareUpdate
	} else if !known {
		// Legacy POST /pair with only the three pairing fields: treat as on.
		sub.SoftwareUpdate = true
		sub.ChargingLiveActivity = true
		sub.NavigationLiveActivity = true
	}
	if req.LockSecure != nil {
		sub.LockSecure = *req.LockSecure
	}
	if req.ChargingLiveActivity != nil {
		sub.ChargingLiveActivity = *req.ChargingLiveActivity
	}
	if req.NavigationLiveActivity != nil {
		sub.NavigationLiveActivity = *req.NavigationLiveActivity
	}
	if req.NavigationTripAlerts != nil {
		sub.NavigationTripAlerts = *req.NavigationTripAlerts
	}
	if req.CarIDs != nil {
		sub.CarIDs = append([]int(nil), req.CarIDs...)
	}

	if mode == "replace" {
		kept := make([]pushSubscriber, 0, 1)
		for _, existing := range r.store.Subscribers {
			if existing.InstallationID == req.InstallationID {
				continue
			}
			log.Printf("[info] push replace removed installation=%s", existing.InstallationID[:8])
		}
		kept = append(kept, sub)
		r.store.Subscribers = kept
		r.removeOutboxExceptLocked(req.InstallationID)
	} else if known {
		r.store.Subscribers[idx] = sub
	} else {
		r.store.Subscribers = append(r.store.Subscribers, sub)
	}

	if err := r.saveLocked(); err != nil {
		return nil, err
	}
	r.syncLegacyPairingLocked()
	return r.snapshotLocked(req.InstallationID), nil
}

func (r *pushSubscriberRegistry) pause(installationID string) (map[string]any, error) {
	if strings.TrimSpace(installationID) == "" {
		return nil, fmt.Errorf("missing_installation")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.indexLocked(installationID)
	if idx < 0 {
		return r.snapshotLocked(installationID), nil
	}
	r.store.Subscribers[idx].Status = pushStatusPaused
	r.store.Subscribers[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	r.removeOutboxForLocked(installationID)
	if err := r.saveLocked(); err != nil {
		return nil, err
	}
	r.syncLegacyPairingLocked()
	return r.snapshotLocked(installationID), nil
}

func (r *pushSubscriberRegistry) unsubscribe(installationID string) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(installationID) == "" {
		if len(r.store.Subscribers) > 1 {
			return nil, fmt.Errorf("need_installation_id")
		}
		r.store.Subscribers = nil
		r.store.Outbox = nil
		if err := r.saveLocked(); err != nil {
			return nil, err
		}
		r.syncLegacyPairingLocked()
		return r.snapshotLocked(""), nil
	}
	next := r.store.Subscribers[:0]
	for _, sub := range r.store.Subscribers {
		if sub.InstallationID != installationID {
			next = append(next, sub)
		}
	}
	r.store.Subscribers = next
	r.removeOutboxForLocked(installationID)
	if err := r.saveLocked(); err != nil {
		return nil, err
	}
	r.syncLegacyPairingLocked()
	return r.snapshotLocked(installationID), nil
}

func (r *pushSubscriberRegistry) setLockSecure(installationID string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.indexLocked(installationID)
	if idx < 0 {
		return fmt.Errorf("not_paired")
	}
	r.store.Subscribers[idx].LockSecure = enabled
	r.store.Subscribers[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return r.saveLocked()
}

func (r *pushSubscriberRegistry) snapshot(installationID string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(installationID)
}

func (r *pushSubscriberRegistry) snapshotLocked(installationID string) map[string]any {
	self := "absent"
	var prefs *pushSubscriber
	others := 0
	active := 0
	for i := range r.store.Subscribers {
		sub := r.store.Subscribers[i]
		if sub.Status == pushStatusActive {
			active++
		}
		if installationID != "" && sub.InstallationID == installationID {
			self = string(sub.Status)
			copy := sub
			prefs = &copy
			continue
		}
		if sub.Status == pushStatusActive {
			others++
		}
	}
	result := map[string]any{
		"push_subscribers":   true,
		"self_status":        self,
		"subscriber_count":   len(r.store.Subscribers),
		"active_count":       active,
		"other_active":       others,
		"pending_deliveries": len(r.store.Outbox),
		"known":              self != "absent",
	}
	if prefs != nil {
		result["software_update"] = prefs.SoftwareUpdate
		result["lock_secure"] = prefs.LockSecure
		result["charging_live_activity"] = prefs.ChargingLiveActivity
		result["navigation_live_activity"] = prefs.NavigationLiveActivity
		result["navigation_trip_alerts"] = prefs.NavigationTripAlerts
		result["car_ids"] = prefs.CarIDs
	}
	return result
}

func (r *pushSubscriberRegistry) indexLocked(id string) int {
	for i, sub := range r.store.Subscribers {
		if sub.InstallationID == id {
			return i
		}
	}
	return -1
}

func (r *pushSubscriberRegistry) syncLegacyPairingLocked() {
	path := filepath.Join(filepath.Dir(r.path), "software-push-pairing.json")
	var primary *pushSubscriber
	for i := range r.store.Subscribers {
		sub := r.store.Subscribers[i]
		if sub.Status == pushStatusActive {
			primary = &r.store.Subscribers[i]
			break
		}
	}
	if primary == nil {
		_ = os.Remove(path)
		return
	}
	data, err := json.Marshal(primary.pairing())
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func (r *pushSubscriberRegistry) matching(carID int, pred func(pushSubscriber) bool) []pushSubscriber {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]pushSubscriber, 0, len(r.store.Subscribers))
	for _, sub := range r.store.Subscribers {
		if sub.Status != pushStatusActive {
			continue
		}
		if !sub.wantsCar(carID) {
			continue
		}
		if pred != nil && !pred(sub) {
			continue
		}
		out = append(out, sub)
	}
	return out
}

func (r *pushSubscriberRegistry) deliverJSON(sub pushSubscriber, payload []byte) error {
	if !validPushSubscriber(sub) {
		return fmt.Errorf("invalid stored push subscriber")
	}
	var metadata pushPayloadMetadata
	if len(payload) == 0 || len(payload) > 16<<10 || json.Unmarshal(payload, &metadata) != nil ||
		strings.TrimSpace(metadata.EventID) == "" || strings.TrimSpace(metadata.Type) == "" {
		return fmt.Errorf("invalid push event payload")
	}
	status, err := r.postJSON(sub, payload)
	if err == nil && status >= 200 && status < 300 {
		return nil
	}
	if err != nil || retryableRelayStatus(status) {
		if queueErr := r.enqueue(sub, metadata, payload); queueErr != nil {
			if err != nil {
				return fmt.Errorf("relay delivery failed (%v); durable queue failed: %w", err, queueErr)
			}
			return queueErr
		}
		return nil
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		r.pauseInvalidSubscriber(sub.InstallationID)
	}
	return fmt.Errorf("relay returned HTTP %d", status)
}

func (r *pushSubscriberRegistry) postJSON(sub pushSubscriber, payload []byte) (int, error) {
	signature := hmac.New(sha256.New, relaySecretBytes(sub.RelaySecret))
	_, _ = signature.Write(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, officialSoftwarePushRelayURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-My-T-Installation", sub.InstallationID)
	request.Header.Set("X-My-T-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
	client := r.http
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func (r *pushSubscriberRegistry) reportVehicleRegistrations(installationID string) error {
	if db == nil {
		return fmt.Errorf("database unavailable")
	}
	r.mu.Lock()
	namespace := r.store.VehicleNamespace
	subscribers := append([]pushSubscriber(nil), r.store.Subscribers...)
	r.mu.Unlock()

	var authSubscriber *pushSubscriber
	for index := range subscribers {
		if subscribers[index].Status != pushStatusActive {
			continue
		}
		if installationID != "" && subscribers[index].InstallationID == installationID {
			copy := subscribers[index]
			authSubscriber = &copy
			break
		}
		if authSubscriber == nil {
			copy := subscribers[index]
			authSubscriber = &copy
		}
	}
	if authSubscriber == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(
		ctx,
		"SELECT id FROM cars WHERE id > 0 ORDER BY id LIMIT $1",
		maxVehicleRegistrationItems,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	aliases := make([]string, 0)
	for rows.Next() {
		var carID int
		if err := rows.Scan(&carID); err != nil {
			return err
		}
		if !anyActiveSubscriberWantsCar(subscribers, carID) {
			continue
		}
		alias, err := anonymousVehicleAlias(namespace, carID)
		if err != nil {
			return err
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(aliases) == 0 {
		return nil
	}
	payload, err := json.Marshal(vehicleRegistrationReport{
		InstallationID: authSubscriber.InstallationID,
		VehicleAliases: aliases,
	})
	if err != nil {
		return err
	}
	if len(payload) > 16<<10 {
		return fmt.Errorf("vehicle registration payload too large")
	}
	status, err := r.postVehicleRegistrations(*authSubscriber, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("vehicle registration relay returned HTTP %d", status)
	}
	return nil
}

func anyActiveSubscriberWantsCar(subscribers []pushSubscriber, carID int) bool {
	for _, sub := range subscribers {
		if sub.Status == pushStatusActive && sub.wantsCar(carID) {
			return true
		}
	}
	return false
}

func anonymousVehicleAlias(namespace string, carID int) (string, error) {
	key, err := hex.DecodeString(namespace)
	if err != nil || len(key) != 32 || carID <= 0 {
		return "", fmt.Errorf("invalid vehicle alias input")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("my-t-vehicle-v1:" + strconv.Itoa(carID)))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (r *pushSubscriberRegistry) postVehicleRegistrations(sub pushSubscriber, payload []byte) (int, error) {
	if !validPushSubscriber(sub) {
		return 0, fmt.Errorf("invalid stored push subscriber")
	}
	signature := hmac.New(sha256.New, relaySecretBytes(sub.RelaySecret))
	_, _ = signature.Write(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		officialVehicleRegistrationURL,
		bytes.NewReader(payload),
	)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-My-T-Installation", sub.InstallationID)
	request.Header.Set("X-My-T-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
	client := r.http
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func (r *pushSubscriberRegistry) enqueue(sub pushSubscriber, metadata pushPayloadMetadata, payload []byte) error {
	now := time.Now().UTC()
	id := sub.InstallationID + ":" + metadata.EventID
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.store.Outbox {
		if r.store.Outbox[index].ID == id {
			r.store.Outbox[index].Payload = append(json.RawMessage(nil), payload...)
			r.store.Outbox[index].EventType = metadata.Type
			return r.saveLocked()
		}
	}
	if len(r.store.Outbox) >= maxPushOutboxItems {
		return fmt.Errorf("push_outbox_full")
	}
	r.store.Outbox = append(r.store.Outbox, queuedPush{
		ID:             id,
		InstallationID: sub.InstallationID,
		Payload:        append(json.RawMessage(nil), payload...),
		EventType:      metadata.Type,
		CreatedAt:      now.Format(time.RFC3339),
		NextAttemptAt:  now.Add(2 * time.Second).Format(time.RFC3339),
		ExpiresAt:      now.Add(pushEventTTL(metadata.Type)).Format(time.RFC3339),
	})
	return r.saveLocked()
}

func (r *pushSubscriberRegistry) retryLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.retryDue(time.Now().UTC())
		}
	}
}

func (r *pushSubscriberRegistry) vehicleRegistrationLoop() {
	defer r.wg.Done()
	initial := time.NewTimer(30 * time.Second)
	defer initial.Stop()
	select {
	case <-r.stopCh:
		return
	case <-initial.C:
		if err := r.reportVehicleRegistrations(""); err != nil {
			log.Printf("[warn] anonymous vehicle statistics: %v", err)
		}
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			if err := r.reportVehicleRegistrations(""); err != nil {
				log.Printf("[warn] anonymous vehicle statistics: %v", err)
			}
		}
	}
}

func (r *pushSubscriberRegistry) retryDue(now time.Time) {
	r.mu.Lock()
	items := append([]queuedPush(nil), r.store.Outbox...)
	subs := append([]pushSubscriber(nil), r.store.Subscribers...)
	r.mu.Unlock()
	byID := make(map[string]pushSubscriber, len(subs))
	for _, sub := range subs {
		if sub.Status == pushStatusActive {
			byID[sub.InstallationID] = sub
		}
	}
	for _, item := range items {
		next, nextErr := time.Parse(time.RFC3339, item.NextAttemptAt)
		expires, expiresErr := time.Parse(time.RFC3339, item.ExpiresAt)
		if expiresErr != nil || !expires.After(now) {
			r.removeOutboxItem(item.ID)
			continue
		}
		if nextErr == nil && next.After(now) {
			continue
		}
		sub, ok := byID[item.InstallationID]
		if !ok {
			r.removeOutboxItem(item.ID)
			continue
		}
		status, err := r.postJSON(sub, item.Payload)
		if err == nil && status >= 200 && status < 300 {
			r.removeOutboxItem(item.ID)
			continue
		}
		if status == http.StatusNotFound || status == http.StatusGone {
			r.pauseInvalidSubscriber(item.InstallationID)
			continue
		}
		if err == nil && !retryableRelayStatus(status) {
			log.Printf("[warn] dropped permanent relay failure installation=%s status=%d", shortInstallationID(item.InstallationID), status)
			r.removeOutboxItem(item.ID)
			continue
		}
		r.rescheduleOutboxItem(item.ID, now)
	}
}

func (r *pushSubscriberRegistry) rescheduleOutboxItem(id string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.store.Outbox {
		if r.store.Outbox[index].ID != id {
			continue
		}
		r.store.Outbox[index].Attempts++
		attempt := r.store.Outbox[index].Attempts
		delay := time.Duration(1<<min(attempt, 8)) * 2 * time.Second
		if delay > 15*time.Minute {
			delay = 15 * time.Minute
		}
		r.store.Outbox[index].NextAttemptAt = now.Add(delay).Format(time.RFC3339)
		_ = r.saveLocked()
		return
	}
}

func (r *pushSubscriberRegistry) removeOutboxItem(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.store.Outbox[:0]
	for _, item := range r.store.Outbox {
		if item.ID != id {
			next = append(next, item)
		}
	}
	r.store.Outbox = next
	_ = r.saveLocked()
}

func (r *pushSubscriberRegistry) pauseInvalidSubscriber(installationID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index := r.indexLocked(installationID); index >= 0 {
		r.store.Subscribers[index].Status = pushStatusPaused
		r.store.Subscribers[index].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	r.removeOutboxForLocked(installationID)
	r.syncLegacyPairingLocked()
	_ = r.saveLocked()
}

func (r *pushSubscriberRegistry) removeOutboxForLocked(installationID string) {
	next := r.store.Outbox[:0]
	for _, item := range r.store.Outbox {
		if item.InstallationID != installationID {
			next = append(next, item)
		}
	}
	r.store.Outbox = next
}

func (r *pushSubscriberRegistry) removeOutboxExceptLocked(installationID string) {
	next := r.store.Outbox[:0]
	for _, item := range r.store.Outbox {
		if item.InstallationID == installationID {
			next = append(next, item)
		}
	}
	r.store.Outbox = next
}

func retryableRelayStatus(status int) bool {
	return status == 0 || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500
}

func pushEventTTL(eventType string) time.Duration {
	switch eventType {
	case "charging_updated", "navigation_updated":
		return 10 * time.Minute
	case "charging_started", "navigation_started":
		return 30 * time.Minute
	case "destination_trip_started", "destination_trip_arrived", "vehicle_lock_secure":
		return time.Hour
	case "charging_ended", "navigation_ended":
		return 2 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func validPushSubscriber(sub pushSubscriber) bool {
	if len(sub.InstallationID) != 48 || !isTrustedSoftwarePushRelayURL(sub.RelayURL) {
		return false
	}
	if _, err := hex.DecodeString(sub.InstallationID); err != nil {
		return false
	}
	secret, err := hex.DecodeString(sub.RelaySecret)
	return err == nil && len(secret) == 32 && (sub.Status == pushStatusActive || sub.Status == pushStatusPaused)
}

func shortInstallationID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func installationIDFromRequest(r *http.Request) string {
	id := strings.TrimSpace(r.Header.Get("X-My-T-Installation"))
	if id != "" {
		return id
	}
	return strings.TrimSpace(r.URL.Query().Get("installation_id"))
}
