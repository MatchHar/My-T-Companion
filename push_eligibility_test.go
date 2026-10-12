package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func pushEligibilityFixture(t *testing.T) (*pushSubscriberRegistry, pushSubscriber) {
	t.Helper()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(t.TempDir(), "software.json"))
	registry := newPushSubscriberRegistry()
	id, secret, relay := testInstallation()
	on := true
	if _, err := registry.upsert(pushPairRequest{InstallationID: id, SourceID: "source-a", RelayURL: relay, RelaySecret: secret,
		SoftwareUpdate: &on, ChargingLiveActivity: &on, NavigationLiveActivity: &on}); err != nil {
		t.Fatal(err)
	}
	return registry, registry.store.Subscribers[0]
}

func eligibilityPayload(t *testing.T, sub pushSubscriber, eventType, sessionID string) []byte {
	t.Helper()
	payload, err := json.Marshal(pushPayloadMetadata{EventID: "eligibility-" + eventType, InstallationID: sub.InstallationID,
		SourceID: sub.SourceID, CarID: 2, Type: eventType, SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func disablePushFixture(t *testing.T, registry *pushSubscriberRegistry, sub pushSubscriber, scope string) {
	t.Helper()
	off := false
	req := pushPairRequest{InstallationID: sub.InstallationID, SourceID: sub.SourceID, RelayURL: sub.RelayURL, RelaySecret: sub.RelaySecret}
	switch scope {
	case "server-category":
		req.SoftwareUpdate, req.ChargingLiveActivity, req.NavigationLiveActivity = &off, &off, &off
	case "car-override":
		values := []vehiclePushPreferences{{CarID: 2}}
		req.VehiclePreferences = &values
	case "source-changed":
		req.SourceID = "source-b"
	case "paused":
		if _, err := registry.pause(sub.InstallationID); err != nil {
			t.Fatal(err)
		}
		return
	case "unpaired":
		if _, err := registry.unsubscribe(sub.InstallationID); err != nil {
			t.Fatal(err)
		}
		return
	case "fleet-disabled":
		mqttSourceRuntime.Load().applyFleet(map[int]bool{2: false}, time.Now().UTC())
		return
	case "fleet-removed":
		mqttSourceRuntime.Load().applyFleet(map[int]bool{}, time.Now().UTC())
		return
	case "fleet-stale":
		runtime := mqttSourceRuntime.Load()
		runtime.mu.Lock()
		runtime.lastFleetSuccess = time.Now().UTC().Add(-mqttFleetGrace - time.Second)
		runtime.mu.Unlock()
		return
	}
	if _, err := registry.upsert(req); err != nil {
		t.Fatal(err)
	}
}

func TestPushEligibilityRechecksInitialAndQueuedDelivery(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, scope := range []string{"server-category", "car-override", "source-changed", "paused", "unpaired", "fleet-disabled", "fleet-removed", "fleet-stale"} {
			t.Run(map[bool]string{false: "initial/", true: "retry/"}[queued]+scope, func(t *testing.T) {
				registry, staleSub := pushEligibilityFixture(t)
				switch scope {
				case "fleet-disabled", "fleet-removed", "fleet-stale":
					runtime := newTeslaMateMQTTRuntime(nil)
					runtime.applyFleet(map[int]bool{2: true}, time.Now().UTC())
					previous := mqttSourceRuntime.Swap(runtime)
					t.Cleanup(func() { mqttSourceRuntime.Store(previous) })
				}
				posts := 0
				registry.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					posts++
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
				})}
				payload := eligibilityPayload(t, staleSub, "update_available", "")
				if queued {
					if result := registry.deliverJSONResult(staleSub, payload); result.Outcome != pushDeliveryQueued {
						t.Fatalf("expected queue: %+v", result)
					}
				}
				disablePushFixture(t, registry, staleSub, scope)
				if queued {
					registry.retryDue(time.Now().UTC().Add(time.Minute))
				} else if result := registry.deliverJSONResult(staleSub, payload); result.Outcome != pushDeliveryExpired || result.Err != nil {
					t.Fatalf("expected suppressed ineligible event: %+v", result)
				}
				wantPosts := 0
				if queued {
					wantPosts = 1
				}
				if posts != wantPosts || len(registry.store.Outbox) != 0 {
					t.Fatalf("ineligible event sent/retained: posts=%d want=%d outbox=%d", posts, wantPosts, len(registry.store.Outbox))
				}
			})
		}
	}
}

func TestPushEligibilityPreservesOnlyBoundTerminalCleanup(t *testing.T) {
	for _, kind := range []string{"charging", "navigation"} {
		t.Run(kind, func(t *testing.T) {
			registry, sub := pushEligibilityFixture(t)
			posts := 0
			transport := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				posts++
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
			})}
			registry.http = transport
			if got := registry.deliverJSONResult(sub, eligibilityPayload(t, sub, kind+"_started", "known-session")); got.Outcome != pushDeliveryAPNsAccepted {
				t.Fatalf("start not accepted: %+v", got)
			}
			disablePushFixture(t, registry, sub, "car-override")
			// Restart must retain only the original source/car/session binding.
			registry = newPushSubscriberRegistry()
			registry.http = transport
			if got := registry.deliverJSONResult(sub, eligibilityPayload(t, sub, kind+"_ended", "known-session")); got.Outcome != pushDeliveryAPNsAccepted {
				t.Fatalf("bound terminal cleanup blocked: %+v", got)
			}
			for _, session := range []string{"unknown-session", ""} {
				if got := registry.deliverJSONResult(sub, eligibilityPayload(t, sub, kind+"_ended", session)); got.Outcome != pushDeliveryExpired {
					t.Fatalf("unbound terminal cleanup admitted: %+v", got)
				}
			}
			disablePushFixture(t, registry, sub, "source-changed")
			if got := registry.deliverJSONResult(sub, eligibilityPayload(t, sub, kind+"_ended", "known-session")); got.Outcome != pushDeliveryExpired {
				t.Fatalf("old source cleanup admitted: %+v", got)
			}
			if posts != 2 {
				t.Fatalf("expected only start and exact cleanup; posts=%d", posts)
			}
		})
	}
}

func TestPushEligibilityRetryRechecksBetweenItems(t *testing.T) {
	registry, sub := pushEligibilityFixture(t)
	posts := 0
	registry.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		posts++
		if posts <= 2 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
		}
		disablePushFixture(t, registry, sub, "server-category")
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	for _, kind := range []string{"update_available", "update_installed"} {
		registry.deliverJSONResult(sub, eligibilityPayload(t, sub, kind, ""))
	}
	registry.retryDue(time.Now().UTC().Add(time.Minute))
	if posts != 3 || len(registry.store.Outbox) != 0 {
		t.Fatalf("second queued item used stale preference snapshot: posts=%d outbox=%d", posts, len(registry.store.Outbox))
	}
}

func TestPushEligibilityQueuedTerminalCleanupSurvivesPreferenceOff(t *testing.T) {
	for _, kind := range []string{"charging", "navigation"} {
		t.Run(kind, func(t *testing.T) {
			registry, sub := pushEligibilityFixture(t)
			posts := 0
			registry.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				posts++
				if posts == 2 {
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
				}
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
			})}
			registry.deliverJSONResult(sub, eligibilityPayload(t, sub, kind+"_started", "known-session"))
			if got := registry.deliverJSONResult(sub, eligibilityPayload(t, sub, kind+"_ended", "known-session")); got.Outcome != pushDeliveryQueued {
				t.Fatalf("terminal not queued: %+v", got)
			}
			disablePushFixture(t, registry, sub, "server-category")
			runtime := newTeslaMateMQTTRuntime(nil)
			runtime.applyFleet(map[int]bool{2: false}, time.Now().UTC())
			previous := mqttSourceRuntime.Swap(runtime)
			t.Cleanup(func() { mqttSourceRuntime.Store(previous) })
			registry.retryDue(time.Now().UTC().Add(time.Minute))
			if posts != 3 || len(registry.store.Outbox) != 0 {
				t.Fatalf("exact terminal cleanup was lost after preference/fleet disable: posts=%d outbox=%d", posts, len(registry.store.Outbox))
			}
		})
	}
}
