package main

import "time"

const lockSecureSettle = 3 * time.Second
const lockSecureEpisodeLifetime = 30 * time.Second
const lockSecureHeartbeatLifetime = 90 * time.Second

// Connection-local evidence. Never restore this from disk or promote retained
// replay/HTTP receipt into a new physical observation. Unchanged MQTT values do
// not repeat, so presence is invalidated by context changes, not a short TTL.
type lockSecureEvidence struct {
	PresenceLive       bool
	PresenceReceivedAt time.Time
	LastHealthyAt      time.Time
	LockAt             time.Time
	StableSince        time.Time
}

// Added to the existing authenticated per-car overlay, not a new public route.
// received_at is explicitly MQTT receipt time, never a vehicle measurement time.
func (m *lockSecureNotificationMonitor) presenceSnapshot(carID int, now time.Time) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.store.Cars[carID]
	e := m.evidence[carID]
	current := m.connected && e.PresenceLive && state.UserPresent != nil &&
		(state.State == "online" || state.State == "charging" || state.State == "driving") &&
		!e.LastHealthyAt.IsZero() && now.Sub(e.LastHealthyAt) >= 0 && now.Sub(e.LastHealthyAt) <= lockSecureHeartbeatLifetime
	result := map[string]any{"car_id": carID, "value": state.UserPresent,
		"context_current": current, "source": "teslamate_mqtt", "snapshot_at": now.Format(time.RFC3339Nano)}
	if !e.PresenceReceivedAt.IsZero() {
		result["received_at"] = e.PresenceReceivedAt.Format(time.RFC3339Nano)
	}
	return result
}

func (e *lockSecureEvidence) observe(previous, current lockSecureCarState, field string, retained bool, now time.Time) {
	if retained {
		e.LockAt = time.Time{}
		if field == "is_user_present" {
			e.PresenceLive = false
		}
		return
	}
	if field == "healthy" {
		if current.Healthy != nil && *current.Healthy {
			e.LastHealthyAt = now
		} else {
			e.LastHealthyAt = time.Time{}
			e.PresenceLive = false
			e.LockAt = time.Time{}
		}
		return
	}
	if field == "display_name" {
		return
	}
	e.StableSince = now
	switch field {
	case "is_user_present":
		e.PresenceLive = current.UserPresent != nil
		e.PresenceReceivedAt = now
		if current.UserPresent == nil || *current.UserPresent {
			e.LockAt = time.Time{}
		}
	case "locked":
		if current.Locked == nil || !*current.Locked {
			e.LockAt = time.Time{}
		} else if previous.Locked != nil && !*previous.Locked {
			e.LockAt = now
		}
	case "state":
		if current.State != "online" && current.State != "charging" && current.State != "driving" {
			e.PresenceLive = false
			e.LockAt = time.Time{}
		}
		if current.State == "driving" {
			e.LockAt = time.Time{}
		}
	case "shift_state":
		if current.ShiftState != "P" {
			e.LockAt = time.Time{}
		}
	case "doors_open", "trunk_open", "frunk_open":
		var value *bool
		switch field {
		case "doors_open":
			value = current.DoorsOpen
		case "trunk_open":
			value = current.TrunkOpen
		case "frunk_open":
			value = current.FrunkOpen
		}
		if value == nil || *value {
			e.PresenceLive = false
			e.LockAt = time.Time{}
		}
	}
}

func (e lockSecureEvidence) ready(state lockSecureCarState, now time.Time) bool {
	return e.PresenceLive && !e.LockAt.IsZero() && !e.LastHealthyAt.IsZero() &&
		now.Sub(e.LockAt) >= 0 && now.Sub(e.LockAt) <= lockSecureEpisodeLifetime &&
		now.Sub(e.LastHealthyAt) >= 0 && now.Sub(e.LastHealthyAt) <= lockSecureHeartbeatLifetime &&
		now.Sub(e.StableSince) >= lockSecureSettle && lockSecureIsSecure(state)
}

// Caller holds m.mu. A reconnect is a new evidence generation; persisted values
// may remain useful for UI/history but are not authorization to send a new alert.
func (m *lockSecureNotificationMonitor) resetEvidenceLocked() {
	for _, timer := range m.pending {
		timer.Stop()
	}
	m.pending = map[int]*time.Timer{}
	m.evidence = map[int]lockSecureEvidence{}
	for id, state := range m.store.Cars {
		m.store.Cars[id] = lockSecureCarState{DisplayName: state.DisplayName, LastPushedAt: state.LastPushedAt}
	}
}
