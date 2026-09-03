package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testNavigationMonitor(t *testing.T) *navigationNotificationMonitor {
	t.Helper()
	d := t.TempDir()
	return &navigationNotificationMonitor{
		statePath:      filepath.Join(d, "state.json"),
		historyPath:    filepath.Join(d, "history.json"),
		queue:          make(chan navigationLiveActivityEvent, 64),
		priorityQueue:  make(chan navigationLiveActivityEvent, 64),
		pending:        map[int]*time.Timer{},
		store:          navigationNotificationStore{Cars: map[int]carNavigationState{}, Delivered: map[string]string{}},
		history:        navigationPushHistoryStore{},
		installationID: "install-test",
		enabled:        true,
	}
}

func TestStagedNextStopCommitsNewLegFromOldSnapshot(t *testing.T) {
	for i := 0; i < 3; i++ {
		m := testNavigationMonitor(t)
		now := time.Now().UTC()
		m.observe(1, "state", "driving", now)
		m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0.1,"minutes_to_arrival":1}`, now)
		start := <-m.queue
		oldSession := start.SessionID
		m.observe(1, "active_route", `{"destination":"Stop B"}`, now.Add(time.Second))
		staged := m.store.Cars[1]
		if staged.SessionID != oldSession || staged.Destination != "Stop A" {
			t.Fatalf("name-only B rewrote committed identity: dest=%q session=%q", staged.Destination, staged.SessionID)
		}
		if staged.PendingDestination != "Stop B" || staged.LegPhase != navigationLegPhasePendingNext {
			t.Fatalf("incomplete B was not staged: %+v", staged)
		}
		m.observe(1, "active_route", `{"destination":"Stop B","miles_to_arrival":12,"minutes_to_arrival":25}`, now.Add(2*time.Second))
		end := <-m.priorityQueue
		next := <-m.queue
		after := m.store.Cars[1]
		if after.SessionID == oldSession {
			t.Fatal("B with miles kept A's session identity")
		}
		if after.Destination != "Stop B" {
			t.Fatalf("committed destination=%q", after.Destination)
		}
		if end.Destination != "Stop A" || end.EndReason != "redirected" || end.SessionID != oldSession {
			t.Fatalf("old terminal used the wrong snapshot: %+v", end)
		}
		if end.RemainingDistanceKM == nil || *end.RemainingDistanceKM > 0.3 {
			t.Fatalf("old terminal remaining leaked B metrics: %+v", end.RemainingDistanceKM)
		}
		if next.Destination != "Stop B" || next.SessionID == oldSession {
			t.Fatalf("new start=%+v", next)
		}
		if end.Revision == 0 || next.Revision <= end.Revision {
			t.Fatalf("revision was not monotonic: end=%d start=%d", end.Revision, next.Revision)
		}
		m.stop()
	}
}

func TestNearbyDistinctStopStartsNewSession(t *testing.T) {
	if !navigationDestinationChangeStartsNewSession("Stop A", "Stop B", floatPointer(0.1), floatPointer(0.6)) {
		t.Fatal("distinct stop 500 m farther must start a new session")
	}
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0.062137,"minutes_to_arrival":1}`, now)
	start := <-m.queue
	m.observe(1, "active_route", `{"destination":"Stop B","miles_to_arrival":0.372822,"minutes_to_arrival":2}`, now.Add(time.Second))
	end := <-m.priorityQueue
	next := <-m.queue
	if m.store.Cars[1].SessionID == start.SessionID {
		t.Fatal("nearby distinct stop reused the old session")
	}
	if end.Destination != "Stop A" || next.Destination != "Stop B" {
		t.Fatalf("leg labels end=%q start=%q", end.Destination, next.Destination)
	}
}

func TestRedirectEndUsesImmutableOldLegMetrics(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0.1,"minutes_to_arrival":1}`, now)
	<-m.queue
	m.observe(1, "active_route", `{"destination":"Stop B","miles_to_arrival":12,"minutes_to_arrival":25}`, now.Add(time.Second))
	end := <-m.priorityQueue
	if end.Destination != "Stop A" || end.EndReason != "redirected" {
		t.Fatalf("unexpected end: %+v", end)
	}
	if end.RemainingDistanceKM == nil || *end.RemainingDistanceKM > 0.3 {
		t.Fatalf("redirected end used next-stop remaining: %+v", end.RemainingDistanceKM)
	}
}

func TestStreetGeofenceAliasKeepsSession(t *testing.T) {
	if navigationDestinationChangeStartsNewSession("20 Hi Mount Dr", "Home", floatPointer(20.4), floatPointer(20.1)) {
		t.Fatal("street↔geofence alias must keep the session")
	}
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"20 Hi Mount Dr","miles_to_arrival":12.5,"location":{"latitude":43.85,"longitude":-79.33}}`, now)
	start := <-m.queue
	m.observe(1, "active_route", `{"destination":"Home","miles_to_arrival":12.4,"location":{"latitude":43.8501,"longitude":-79.3301}}`, now.Add(time.Second))
	if m.store.Cars[1].SessionID != start.SessionID || m.store.Cars[1].Destination != "Home" {
		t.Fatalf("alias did not keep session/label: %+v", m.store.Cars[1])
	}
	select {
	case event := <-m.priorityQueue:
		t.Fatalf("alias queued an end: %+v", event)
	default:
	}
}

func TestSameNameFartherStopStartsNewSession(t *testing.T) {
	if !navigationDestinationChangeStartsNewSession("Starbucks", "Starbucks", floatPointer(0.1), floatPointer(19.3)) {
		t.Fatal("same-name remaining jump must start a new leg")
	}
}

func TestSameDestinationCoordinatesPreventRerouteLegSplit(t *testing.T) {
	committed := carNavigationState{Destination: "Home", RemainingDistanceKM: floatPointer(1),
		DestinationLatitude: floatPointer(37.78), DestinationLongitude: floatPointer(-122.41)}
	candidate := navigationRouteCandidate{Destination: "Home", RemainingKM: floatPointer(8),
		Latitude: floatPointer(37.78), Longitude: floatPointer(-122.41)}
	if classifyNavigationLegChange(committed, candidate) != navigationLegKeep {
		t.Fatal("a detour to the same confirmed destination must keep its leg")
	}
	candidate.Destination = "Home street address"
	if classifyNavigationLegChange(committed, candidate) != navigationLegAlias {
		t.Fatal("confirmed coordinate alias must not split the leg")
	}
}

func TestOrdinaryForwardProgressAndPartialZeroDoNotSplitOrArrive(t *testing.T) {
	if navigationDestinationChangeStartsNewSession("Home", "Home", floatPointer(0.5), floatPointer(0.3)) {
		t.Fatal("near-arrival forward progress split the leg")
	}
	if navigationDestinationChangeStartsNewSession("Home", "Home", floatPointer(20), floatPointer(10)) {
		t.Fatal("ordinary progress split the leg")
	}
	zero := 0
	if navigationRemainingLooksArrived(floatPointer(10), &zero) {
		t.Fatal("partial zero minutes falsely declared arrival")
	}
}

func TestRestoreWithoutFreshMQTTCannotAnnounceArrival(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	before := time.Now().UTC().Add(-time.Minute)
	zero := 0
	m.store.Cars[1] = carNavigationState{Active: true, SessionID: "orphan-leg", Destination: "Home",
		RemainingDistanceKM: floatPointer(10), RemainingMinutes: &zero, LastObservedAt: before.Format(time.RFC3339)}
	m.reconcileRestoredSessionsNow(time.Now().UTC())
	end := <-m.priorityQueue
	if end.EndReason != "stale" || navigationEndReason(end) == "arrived" {
		t.Fatalf("restore fabricated arrival: %+v", end)
	}
	if navigationEndReason(navigationLiveActivityEvent{RemainingDistanceKM: floatPointer(10), RemainingMinutes: &zero}) == "arrived" {
		t.Fatal("legacy terminal fallback used conflicting zero minutes")
	}
}

func TestCoordinateFarStopStartsNewSessionEvenWithSimilarRemaining(t *testing.T) {
	committed := carNavigationState{
		Destination:          "Cafe A",
		RemainingDistanceKM:  floatPointer(1.0),
		DestinationLatitude:  floatPointer(37.7800),
		DestinationLongitude: floatPointer(-122.4100),
	}
	candidate := navigationRouteCandidate{
		Destination: "Cafe B",
		RemainingKM: floatPointer(1.05),
		Latitude:    floatPointer(37.7900),
		Longitude:   floatPointer(-122.4100),
	}
	if classifyNavigationLegChange(committed, candidate) != navigationLegNew {
		t.Fatal("far coordinates must start a new session")
	}
}

func TestPushEventOmitsDestinationCoordinates(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Home","miles_to_arrival":3,"location":{"latitude":43.85,"longitude":-79.33}}`, now)
	event := <-m.queue
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(payload)
	if strings.Contains(raw, "latitude") || strings.Contains(raw, "longitude") || strings.Contains(raw, "43.85") {
		t.Fatalf("push payload leaked destination coordinates: %s", raw)
	}
	if event.SourceID != "" {
		t.Fatal("unscoped makeEvent must leave source_id empty until fan-out")
	}
	if event.CarID != 1 || event.SessionID == "" {
		t.Fatalf("identity fields missing: %+v", event)
	}
}

func TestMissingDestinationCoordinateIsNotInterpretedAsEquator(t *testing.T) {
	for _, raw := range []string{
		`{"destination":"Home","location":{"latitude":null,"longitude":-122}}`,
		`{"destination":"Home","location":{"latitude":37}}`,
	} {
		candidate := parseActiveRouteCandidate(raw)
		if candidate.Latitude != nil || candidate.Longitude != nil {
			t.Fatal("partial coordinates must remain unknown, not become a new geographic destination")
		}
	}
}

func TestZeroRemainingWhileDrivingIsNotArrival(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":1,"minutes_to_arrival":3}`, now)
	<-m.queue
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0,"minutes_to_arrival":0}`, now.Add(time.Second))
	if !m.store.Cars[1].Active || len(m.priorityQueue) != 0 {
		t.Fatal("0 km/0 min while driving must not emit a terminal event")
	}
}

func TestParkingEvidenceConfirmsArrivalFromOldSnapshot(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0.1,"minutes_to_arrival":1}`, now)
	start := <-m.queue
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0,"minutes_to_arrival":0}`, now.Add(time.Second))
	m.observe(1, "state", "online", now.Add(2*time.Second))
	end := <-m.priorityQueue
	if m.store.Cars[1].Active {
		t.Fatal("parking evidence left the session active")
	}
	if end.SessionID != start.SessionID || end.Destination != "Stop A" || end.EndReason != "arrived" {
		t.Fatalf("unexpected arrival: %+v", end)
	}
	if end.LegPhase != navigationLegPhaseConfirmedArrived && end.LegPhase != navigationLegPhaseEnded {
		t.Fatalf("leg_phase=%q", end.LegPhase)
	}
}

func TestOfflineDoesNotInventArrival(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0,"minutes_to_arrival":0}`, now)
	<-m.queue
	m.observe(1, "state", "offline", now.Add(time.Second))
	if !m.store.Cars[1].Active || len(m.priorityQueue) != 0 {
		t.Fatal("offline/unknown must preserve the session without inventing arrival")
	}
	m.observe(1, "state", "asleep", now.Add(2*time.Second))
	if !m.store.Cars[1].Active || len(m.priorityQueue) != 0 {
		t.Fatal("asleep must not fake arrival")
	}
}

func TestTrafficStopWithRemainingDistanceStaysActive(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":2,"minutes_to_arrival":6}`, now)
	<-m.queue
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":2,"minutes_to_arrival":6}`, now.Add(time.Second))
	if !m.store.Cars[1].Active || len(m.priorityQueue) != 0 {
		t.Fatal("in-progress remaining must not be treated as arrival")
	}
}
