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

func TestFarDestinationWithoutProgressIsStagedUntilMetricsArrive(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":8,"minutes_to_arrival":20,"location":{"latitude":43.77,"longitude":-79.28}}`, now)
	start := <-m.queue

	m.observe(1, "active_route", `{"destination":"Stop B","location":{"latitude":43.78,"longitude":-79.25}}`, now.Add(time.Second))
	staged := m.store.Cars[1]
	if staged.SessionID != start.SessionID || staged.Destination != "Stop A" {
		t.Fatalf("incomplete far candidate replaced committed leg: %+v", staged)
	}
	if staged.PendingDestination != "Stop B" || staged.LegPhase != navigationLegPhasePendingNext {
		t.Fatalf("incomplete far candidate was not staged: %+v", staged)
	}
	select {
	case event := <-m.priorityQueue:
		t.Fatalf("incomplete far candidate ended the old leg: %+v", event)
	default:
	}

	m.observe(1, "active_route", `{"destination":"Stop B","miles_to_arrival":12,"minutes_to_arrival":25,"location":{"latitude":43.78,"longitude":-79.25}}`, now.Add(2*time.Second))
	end := <-m.priorityQueue
	next := <-m.queue
	if end.Destination != "Stop A" || end.EndReason != "redirected" {
		t.Fatalf("confirmed redirect ended wrong leg: %+v", end)
	}
	if next.Destination != "Stop B" || next.SessionID == start.SessionID {
		t.Fatalf("confirmed route metrics did not start B: %+v", next)
	}
}

func TestTerminalStaleDestinationDoesNotReplaceArrivedLeg(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(2, "state", "driving", now)
	m.observe(2, "active_route", `{"destination":"華泰超市","miles_to_arrival":0,"minutes_to_arrival":0,"location":{"latitude":43.772303,"longitude":-79.281021}}`, now)
	start := <-m.queue

	// TeslaMate's real terminal snapshot: the car is physically at Hua Tai but
	// active_route briefly resurrects Winners with far coordinates and no route
	// distance/time before state changes to online.
	m.observe(2, "active_route", `{"destination":"Winners Scarborough","location":{"latitude":43.77462,"longitude":-79.259131}}`, now.Add(time.Second))
	staged := m.store.Cars[2]
	if staged.SessionID != start.SessionID || staged.Destination != "華泰超市" {
		t.Fatalf("terminal stale destination replaced Hua Tai: %+v", staged)
	}
	if staged.PendingDestination != "Winners Scarborough" {
		t.Fatalf("terminal stale destination was not staged: %+v", staged)
	}

	m.observe(2, "state", "online", now.Add(2*time.Second))
	end := <-m.priorityQueue
	if end.SessionID != start.SessionID || end.Destination != "華泰超市" || end.EndReason != "arrived" {
		t.Fatalf("terminal state did not arrive the committed Hua Tai leg: %+v", end)
	}
	if got := m.store.Cars[2]; got.PendingDestination != "" || got.Active {
		t.Fatalf("terminal state retained pending/still active state: %+v", got)
	}
	select {
	case event := <-m.queue:
		t.Fatalf("terminal stale destination started a false Winners leg: %+v", event)
	default:
	}
	select {
	case event := <-m.priorityQueue:
		t.Fatalf("terminal stale destination emitted an extra end: %+v", event)
	default:
	}
}

func TestTerminalStagedDestinationIsDiscardedWhenRouteClears(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(2, "state", "driving", now)
	m.observe(2, "active_route", `{"destination":"華泰超市","miles_to_arrival":0,"minutes_to_arrival":0,"location":{"latitude":43.772303,"longitude":-79.281021}}`, now)
	start := <-m.queue
	m.observe(2, "active_route", `{"destination":"Winners Scarborough","location":{"latitude":43.77462,"longitude":-79.259131}}`, now.Add(time.Second))
	m.observe(2, "active_route", `{"error":"No active route available"}`, now.Add(2*time.Second))

	end := <-m.priorityQueue
	if end.SessionID != start.SessionID || end.Destination != "華泰超市" || end.EndReason != "arrived" {
		t.Fatalf("route clear did not arrive the last committed leg: %+v", end)
	}
	state := m.store.Cars[2]
	if state.Active || state.Destination != "" || state.PendingDestination != "" {
		t.Fatalf("route clear retained committed/pending destination: %+v", state)
	}
	select {
	case event := <-m.queue:
		t.Fatalf("route clear started a false pending leg: %+v", event)
	default:
	}
}

func TestFarDestinationAlreadyAtArrivalNeedsConfirmation(t *testing.T) {
	zeroKM := 0.0
	zeroMinutes := 0
	oldLatitude := 43.772303
	oldLongitude := -79.281021
	newLatitude := 43.77462
	newLongitude := -79.259131
	committed := carNavigationState{
		Destination:          "華泰超市",
		RemainingDistanceKM:  &zeroKM,
		RemainingMinutes:     &zeroMinutes,
		DestinationLatitude:  &oldLatitude,
		DestinationLongitude: &oldLongitude,
	}
	candidate := navigationRouteCandidate{
		Destination:      "Winners Scarborough",
		RemainingKM:      &zeroKM,
		RemainingMinutes: &zeroMinutes,
		Latitude:         &newLatitude,
		Longitude:        &newLongitude,
	}
	if decision := classifyNavigationLegChange(committed, candidate); decision != navigationLegStage {
		t.Fatalf("far zero-progress destination decision=%v want staged", decision)
	}
}

func TestRepeatedStagedNearStopConfirmsWhileStillDriving(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"Stop A","miles_to_arrival":0,"minutes_to_arrival":0,"location":{"latitude":43.77,"longitude":-79.28}}`, now)
	start := <-m.queue

	nearStop := `{"destination":"Stop B","miles_to_arrival":0.12,"minutes_to_arrival":1,"location":{"latitude":43.771,"longitude":-79.277}}`
	m.observe(1, "active_route", nearStop, now.Add(time.Second))
	if got := m.store.Cars[1]; got.SessionID != start.SessionID || got.PendingObservedAt == "" {
		t.Fatalf("first close-stop sample was not staged: %+v", got)
	}
	m.observe(1, "active_route", nearStop, now.Add(2*time.Second))
	select {
	case event := <-m.priorityQueue:
		t.Fatalf("close-stop candidate confirmed before debounce: %+v", event)
	default:
	}
	m.observe(1, "active_route", nearStop, now.Add(3*time.Second))
	end := <-m.priorityQueue
	next := <-m.queue
	if end.Destination != "Stop A" || next.Destination != "Stop B" || next.SessionID == start.SessionID {
		t.Fatalf("repeated close-stop candidate did not become a real leg: end=%+v next=%+v", end, next)
	}
}

func TestPersistedOldPendingCandidateCannotConfirmAfterRestartGap(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	oldDistance := 0.0
	oldMinutes := 0
	oldLatitude := 43.77
	oldLongitude := -79.28
	pendingLatitude := 43.771
	pendingLongitude := -79.277
	m.store.Cars[1] = carNavigationState{
		VehicleState:            "driving",
		Destination:             "Stop A",
		RemainingDistanceKM:     &oldDistance,
		RemainingMinutes:        &oldMinutes,
		DestinationLatitude:     &oldLatitude,
		DestinationLongitude:    &oldLongitude,
		Active:                  true,
		SessionID:               "old-session",
		SessionStartedAt:        now.Add(-time.Hour).Format(time.RFC3339),
		LegPhase:                navigationLegPhasePendingNext,
		PendingDestination:      "Stop B",
		PendingRemainingKM:      floatPointer(0.2),
		PendingRemainingMinutes: &oldMinutes,
		PendingLatitude:         &pendingLatitude,
		PendingLongitude:        &pendingLongitude,
		PendingObservedAt:       now.Add(-time.Hour).Format(time.RFC3339Nano),
	}

	m.observe(1, "active_route", `{"destination":"Stop B","miles_to_arrival":0.12,"minutes_to_arrival":1,"location":{"latitude":43.771,"longitude":-79.277}}`, now)
	state := m.store.Cars[1]
	if state.SessionID != "old-session" || state.Destination != "Stop A" {
		t.Fatalf("old persisted pending candidate confirmed immediately: %+v", state)
	}
	firstObservedAt, err := time.Parse(time.RFC3339Nano, state.PendingObservedAt)
	if err != nil || firstObservedAt.Before(now.Add(-time.Second)) {
		t.Fatalf("old pending confirmation window was not reset: %q", state.PendingObservedAt)
	}
	select {
	case event := <-m.priorityQueue:
		t.Fatalf("old persisted pending candidate emitted an end: %+v", event)
	default:
	}
}

func TestCommittedNewLegDoesNotInheritOldRouteMetrics(t *testing.T) {
	m := testNavigationMonitor(t)
	defer m.stop()
	now := time.Now().UTC()
	oldDistance := 0.0
	oldMinutes := 0
	oldArrivalBattery := 28
	oldLatitude := 43.772303
	oldLongitude := -79.281021
	state := carNavigationState{
		VehicleState:         "driving",
		Destination:          "Stop A",
		RemainingDistanceKM:  &oldDistance,
		RemainingMinutes:     &oldMinutes,
		ArrivalBatteryLevel:  &oldArrivalBattery,
		DestinationLatitude:  &oldLatitude,
		DestinationLongitude: &oldLongitude,
		Active:               true,
		SessionID:            "old-session",
		SessionStartedAt:     now.Add(-time.Hour).Format(time.RFC3339),
		LegPhase:             navigationLegPhaseActive,
		StartDelivered:       true,
	}
	newMinutes := 25
	candidate := navigationRouteCandidate{
		Destination:      "Stop B",
		RemainingMinutes: &newMinutes,
	}
	_, next, _ := m.commitRedirectedLegLocked(1, &state, cloneCarNavigationState(state), candidate, now)
	if next.RemainingDistanceKM != nil {
		t.Fatalf("new leg inherited old distance: %+v", next.RemainingDistanceKM)
	}
	if next.RemainingMinutes == nil || *next.RemainingMinutes != 25 {
		t.Fatalf("new leg lost its own minutes: %+v", next.RemainingMinutes)
	}
	if next.ArrivalBatteryLevel != nil || state.DestinationLatitude != nil || state.DestinationLongitude != nil {
		t.Fatalf("new leg inherited old arrival/coordinates: next=%+v state=%+v", next, state)
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
