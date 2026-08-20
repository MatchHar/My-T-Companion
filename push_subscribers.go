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
	"strings"
	"sync"
	"time"
)

const maxPushSubscribers = 8

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
	Subscribers []pushSubscriber `json:"subscribers"`
}

type pushSubscriberRegistry struct {
	mu       sync.Mutex
	path     string
	store    pushSubscriberStore
	http     *http.Client
}

func newPushSubscriberRegistry() *pushSubscriberRegistry {
	base := filepath.Dir(getenv("PUSH_STATE_PATH", "/data/software-notifications.json"))
	r := &pushSubscriberRegistry{
		path: filepath.Join(base, "push-subscribers.json"),
		http: &http.Client{Timeout: 12 * time.Second},
		store: pushSubscriberStore{
			Subscribers: []pushSubscriber{},
		},
	}
	r.load()
	r.migrateLegacyPairingLocked()
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
	r.store = stored
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
		"push_subscribers":  true,
		"self_status":       self,
		"subscriber_count":  len(r.store.Subscribers),
		"active_count":      active,
		"other_active":      others,
		"known":             self != "absent",
	}
	if prefs != nil {
		result["software_update"] = prefs.SoftwareUpdate
		result["lock_secure"] = prefs.LockSecure
		result["charging_live_activity"] = prefs.ChargingLiveActivity
		result["navigation_live_activity"] = prefs.NavigationLiveActivity
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
	signature := hmac.New(sha256.New, relaySecretBytes(sub.RelaySecret))
	_, _ = signature.Write(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.RelayURL, bytes.NewReader(payload))
	if err != nil {
		return err
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
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("relay returned HTTP %d", response.StatusCode)
	}
	return nil
}

func installationIDFromRequest(r *http.Request) string {
	id := strings.TrimSpace(r.Header.Get("X-My-T-Installation"))
	if id != "" {
		return id
	}
	return strings.TrimSpace(r.URL.Query().Get("installation_id"))
}
