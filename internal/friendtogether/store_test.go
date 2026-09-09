package friendtogether

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// All fixture IDs/coordinates/times are synthetic, unrelated to any vehicle.
const fixtureTime int64 = 1800000000000

func id(n int) string       { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func ptr[T any](value T) *T { return &value }

func fixture(t *testing.T, permissions Permissions) (*Store, *atomic.Int64, GrantSpec, OwnerPrincipal, RecipientPrincipal) {
	t.Helper()
	clock := &atomic.Int64{}
	clock.Store(fixtureTime)
	s := &Store{clock: clock.Load}
	spec := GrantSpec{Binding: Binding{GrantID: id(1), IssuerID: id(2), SessionID: id(3), MemberID: id(4), RecipientID: id(5)},
		Source: SourceKey{SourceID: id(6), CarID: 1}, Permissions: permissions, ExpiresAtMS: fixtureTime + 3600000}
	owner := OwnerPrincipal{IssuerID: spec.Binding.IssuerID, Source: spec.Source}
	recipient := RecipientPrincipal{RecipientID: spec.Binding.RecipientID}
	if _, err := s.Create(owner, spec); err != nil {
		t.Fatal(err)
	}
	return s, clock, spec, owner, recipient
}

func source(spec GrantSpec) SourceSnapshot {
	return SourceSnapshot{Source: spec.Source, State: StateDriving,
		Motion:     &Motion{Latitude: ptr(0.0), Longitude: ptr(0.0), SpeedKPH: ptr(0.0), HeadingDegrees: ptr(0.0), ObservedAtMS: ptr(fixtureTime)},
		Navigation: &NavigationObservation{Revision: 1, Latitude: ptr(1.0), Longitude: ptr(1.0), RemainingDistanceKM: ptr(0.0), RemainingMinutes: ptr(0.0), ObservedAtMS: fixtureTime},
		Battery:    &Battery{Percentage: ptr(0.0), RatedRangeKM: ptr(0.0), ObservedAtMS: fixtureTime},
		Trajectory: []TrajectoryPoint{{Latitude: 0, Longitude: 0, ObservedAtMS: fixtureTime}}}
}

func allPermissions() Permissions {
	return Permissions{Location: true, Navigation: true, Battery: true, Trajectory: true}
}

func TestScopeAndPrincipalsFailClosed(t *testing.T) {
	for _, which := range []string{"grant", "issuer", "session", "member", "recipient", "authenticated recipient", "source", "car", "revision"} {
		t.Run(which, func(t *testing.T) {
			s, _, spec, _, recipient := fixture(t, allPermissions())
			binding, input, revision := spec.Binding, source(spec), int64(1)
			switch which {
			case "grant":
				binding.GrantID = id(99)
			case "issuer":
				binding.IssuerID = id(99)
			case "session":
				binding.SessionID = id(99)
			case "member":
				binding.MemberID = id(99)
			case "recipient":
				binding.RecipientID = id(99)
			case "authenticated recipient":
				recipient.RecipientID = id(99)
			case "source":
				input.Source.SourceID = id(99)
			case "car":
				input.Source.CarID = 2
			case "revision":
				revision = 2
			}
			out, err := s.Project(recipient, binding, revision, input)
			if err == nil || out.SchemaVersion != 0 || out.Motion.Latitude != nil {
				t.Fatal("scope mismatch disclosed a snapshot")
			}
		})
	}
}

func TestTwoIssuersWithSameCarIDRemainIndependent(t *testing.T) {
	s, _, a, _, recipientA := fixture(t, allPermissions())
	b := a
	b.Binding = Binding{GrantID: id(11), IssuerID: id(12), SessionID: id(13), MemberID: id(14), RecipientID: id(15)}
	b.Source.SourceID = id(16)
	if _, err := s.Create(OwnerPrincipal{IssuerID: b.Binding.IssuerID, Source: b.Source}, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Project(recipientA, a.Binding, 1, source(b)); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("cross-car: %v", err)
	}
	for _, spec := range []GrantSpec{a, b} {
		out, err := s.Project(RecipientPrincipal{RecipientID: spec.Binding.RecipientID}, spec.Binding, 1, source(spec))
		if err != nil || out.Binding != spec.Binding || out.Sequence != 1 {
			t.Fatalf("independent scope: %v", err)
		}
	}
}

func TestIndependentOptionalPermissions(t *testing.T) {
	for bits := 0; bits < 8; bits++ {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			p := Permissions{Location: true, Navigation: bits&1 != 0, Battery: bits&2 != 0, Trajectory: bits&4 != 0}
			s, _, spec, _, recipient := fixture(t, p)
			out, err := s.Project(recipient, spec.Binding, 1, source(spec))
			if err != nil {
				t.Fatal(err)
			}
			if (out.Navigation != nil) != p.Navigation || (out.Battery != nil) != p.Battery || (out.Trajectory != nil) != p.Trajectory {
				t.Fatal("permission category mismatch")
			}
			if out.Motion.SpeedKPH == nil || *out.Motion.SpeedKPH != 0 {
				t.Fatal("lost genuine zero")
			}
		})
	}
}

func TestDeniedCategoriesAreFilteredBeforeValidation(t *testing.T) {
	s, _, spec, _, recipient := fixture(t, Permissions{Location: true})
	input := source(spec)
	input.Navigation.ObservedAtMS = math.MaxInt64
	input.Battery.Percentage = ptr(math.NaN())
	input.Trajectory = make([]TrajectoryPoint, MaxSourceTrajectoryPoints+1)
	out, err := s.Project(recipient, spec.Binding, 1, input)
	if err != nil || out.Navigation != nil || out.Battery != nil || out.Trajectory != nil {
		t.Fatalf("unauthorized categories influenced result: %v", err)
	}
}

func TestPreConsentIsSuppressedWithoutInventedTime(t *testing.T) {
	s, _, spec, _, recipient := fixture(t, allPermissions())
	input := source(spec)
	input.Motion.ObservedAtMS = ptr(fixtureTime - 1)
	input.Navigation.ObservedAtMS--
	input.Battery.ObservedAtMS--
	input.Trajectory[0].ObservedAtMS--
	out, err := s.Project(recipient, spec.Binding, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != StateUnknown || out.Motion.ObservedAtMS != nil || out.Motion.Latitude != nil || out.Motion.SpeedKPH != nil || out.Navigation != nil || out.Battery != nil || len(*out.Trajectory) != 0 {
		t.Fatal("preconsent data disclosed")
	}
	input.Motion = nil
	if out, err = s.Project(recipient, spec.Binding, 1, input); err != nil || out.Motion.ObservedAtMS != nil {
		t.Fatal("receipt substituted for source time")
	}
}

func TestTimestampBoundaryAndFutureSamples(t *testing.T) {
	for _, part := range []string{"motion", "navigation", "battery", "trajectory"} {
		for _, observed := range []int64{-1, fixtureTime + 1, MaxTimestampMS + 1, math.MaxInt64} {
			t.Run(fmt.Sprintf("%s/%d", part, observed), func(t *testing.T) {
				s, _, spec, _, recipient := fixture(t, allPermissions())
				input := source(spec)
				switch part {
				case "motion":
					input.Motion.ObservedAtMS = ptr(observed)
				case "navigation":
					input.Navigation.ObservedAtMS = observed
				case "battery":
					input.Battery.ObservedAtMS = observed
				case "trajectory":
					input.Trajectory[0].ObservedAtMS = observed
				}
				if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrInvalid) {
					t.Fatalf("future/invalid accepted: %v", err)
				}
			})
		}
	}
	s, clock, spec, _, recipient := fixture(t, allPermissions())
	clock.Store(fixtureTime + 300000)
	out, err := s.Project(recipient, spec.Binding, 1, source(spec))
	if err != nil || *out.Motion.ObservedAtMS != fixtureTime || out.ServerTimeMS != fixtureTime+300000 {
		t.Fatal("old source became freshly observed")
	}
}

func TestMissingVsZeroExactWireAllowlist(t *testing.T) {
	s, _, spec, _, recipient := fixture(t, allPermissions())
	input := source(spec)
	input.Motion.SpeedKPH = nil
	input.Battery.RatedRangeKM = nil
	out, err := s.Project(recipient, spec.Binding, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Go-generated v1 interoperability fixture: %s", encoded)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	expectKeys(t, object, "schema_version grant_id issuer_id session_id member_id recipient_id revision sequence server_time_ms consent_started_at_ms expires_at_ms state motion navigation battery trajectory")
	for name, keys := range map[string]string{
		"motion":     "latitude longitude speed_kph heading_degrees observed_at_ms",
		"navigation": "revision destination latitude longitude remaining_distance_km remaining_minutes observed_at_ms",
		"battery":    "percentage rated_range_km observed_at_ms",
	} {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(object[name], &nested); err != nil {
			t.Fatal(err)
		}
		expectKeys(t, nested, keys)
	}
	var motion, battery map[string]any
	_ = json.Unmarshal(object["motion"], &motion)
	_ = json.Unmarshal(object["battery"], &battery)
	if motion["speed_kph"] != nil || motion["latitude"] != float64(0) || battery["rated_range_km"] != nil || battery["percentage"] != float64(0) {
		t.Fatal("null and zero conflated")
	}
	if strings.Contains(string(encoded), spec.Source.SourceID) || strings.Contains(string(encoded), `"car_id"`) || strings.Contains(string(encoded), `"vin"`) || strings.Contains(string(encoded), `"odometer"`) || strings.Contains(string(encoded), `"geofence"`) {
		t.Fatal("internal/source fields leaked")
	}
	var points []map[string]json.RawMessage
	if err := json.Unmarshal(object["trajectory"], &points); err != nil {
		t.Fatal(err)
	}
	expectKeys(t, points[0], "latitude longitude observed_at_ms")
}

func expectKeys(t *testing.T, object map[string]json.RawMessage, expected string) {
	t.Helper()
	wanted := strings.Fields(expected)
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(wanted)
	sort.Strings(got)
	if strings.Join(wanted, ",") != strings.Join(got, ",") {
		t.Fatalf("wire allowlist: %v want %v", got, wanted)
	}
}

func TestExpiryRevocationAndRevision(t *testing.T) {
	s, clock, spec, owner, recipient := fixture(t, allPermissions())
	clock.Store(spec.ExpiresAtMS - 1)
	if _, err := s.Project(recipient, spec.Binding, 1, source(spec)); err != nil {
		t.Fatal(err)
	}
	clock.Store(spec.ExpiresAtMS)
	if _, err := s.Project(recipient, spec.Binding, 1, source(spec)); !errors.Is(err, ErrInactive) {
		t.Fatalf("expiry boundary: %v", err)
	}
	clock.Store(fixtureTime)
	if _, err := s.Project(recipient, spec.Binding, 1, source(spec)); !errors.Is(err, ErrInactive) {
		t.Fatal("expired grant revived after clock rollback")
	}

	s, _, spec, owner, recipient = fixture(t, allPermissions())
	badOwner := owner
	badOwner.Source.CarID++
	if _, err := s.Revoke(badOwner, spec.Binding, spec.Source, 1); !errors.Is(err, ErrNotAuthorized) {
		t.Fatal("unrelated owner revoked grant")
	}
	if m, err := s.Revoke(owner, spec.Binding, spec.Source, 1); err != nil || m.Revision != 2 {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.Project(recipient, spec.Binding, 1, source(spec)); !errors.Is(err, ErrRevision) {
		t.Fatal("old revision survived revoke")
	}
	if _, err := s.Project(recipient, spec.Binding, 2, source(spec)); !errors.Is(err, ErrInactive) {
		t.Fatal("current revoked grant survived")
	}
	if _, err := s.Create(owner, spec); !errors.Is(err, ErrNotAuthorized) {
		t.Fatal("revoked ID reassigned")
	}
	if _, err := NewStore().Project(recipient, spec.Binding, 1, source(spec)); !errors.Is(err, ErrNotAuthorized) {
		t.Fatal("restart restored consent implicitly")
	}
}

func TestRestrictionsCannotExpandOrRenewConsent(t *testing.T) {
	s, _, spec, owner, recipient := fixture(t, allPermissions())
	p := Permissions{Location: true, Battery: true}
	m, err := s.Restrict(owner, spec.Binding, spec.Source, 1, p, spec.ExpiresAtMS-1)
	if err != nil || m.Revision != 2 || m.ConsentStartedAtMS != fixtureTime {
		t.Fatalf("restriction: %v", err)
	}
	if _, err := s.Project(recipient, spec.Binding, 1, source(spec)); !errors.Is(err, ErrRevision) {
		t.Fatal("old revision accepted")
	}
	out, err := s.Project(recipient, spec.Binding, 2, source(spec))
	if err != nil || out.Navigation != nil || out.Trajectory != nil || out.Battery == nil {
		t.Fatal("restriction not enforced")
	}
	if _, err := s.Restrict(owner, spec.Binding, spec.Source, 2, allPermissions(), m.ExpiresAtMS); !errors.Is(err, ErrInvalid) {
		t.Fatal("expanded consent")
	}
	if _, err := s.Restrict(owner, spec.Binding, spec.Source, 2, p, spec.ExpiresAtMS); !errors.Is(err, ErrInvalid) {
		t.Fatal("silently renewed consent")
	}
}

func TestNavigationAliasIsOwnerApprovedAndRevisionBound(t *testing.T) {
	s, _, spec, owner, recipient := fixture(t, allPermissions())
	spec.Binding.GrantID = id(10)
	spec.DestinationAlias = &DestinationAlias{NavigationRevision: 1, Value: "Meeting point"}
	if _, err := s.Create(owner, spec); err != nil {
		t.Fatal(err)
	}
	spec.DestinationAlias.Value = "MUTATED"
	out, err := s.Project(recipient, spec.Binding, 1, source(spec))
	if err != nil || out.Navigation.Destination == nil || *out.Navigation.Destination != "Meeting point" {
		t.Fatal("alias was not copied from consent")
	}
	input := source(spec)
	input.Navigation.Revision = 2
	out, err = s.Project(recipient, spec.Binding, 1, input)
	if err != nil || out.Navigation.Destination != nil {
		t.Fatal("old alias followed reroute")
	}
	input.Navigation = nil
	if _, err := s.Project(recipient, spec.Binding, 1, input); err != nil {
		t.Fatal(err)
	}
	input = source(spec)
	input.Navigation.Revision = 2
	if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrOrder) {
		t.Fatal("withdrawn navigation revived")
	}
	input.Navigation.Revision = 3
	if _, err := s.Project(recipient, spec.Binding, 1, input); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidNumericAndStateData(t *testing.T) {
	cases := map[string]func(*SourceSnapshot){
		"partial coordinates": func(s *SourceSnapshot) { s.Motion.Longitude = nil },
		"latitude":            func(s *SourceSnapshot) { s.Motion.Latitude = ptr(90.1) },
		"longitude":           func(s *SourceSnapshot) { s.Motion.Longitude = ptr(-180.1) },
		"speed":               func(s *SourceSnapshot) { s.Motion.SpeedKPH = ptr(324.1) },
		"negative speed":      func(s *SourceSnapshot) { s.Motion.SpeedKPH = ptr(-0.1) },
		"heading360":          func(s *SourceSnapshot) { s.Motion.HeadingDegrees = ptr(360.0) },
		"NaN":                 func(s *SourceSnapshot) { s.Motion.Latitude = ptr(math.NaN()) },
		"infinity":            func(s *SourceSnapshot) { s.Battery.RatedRangeKM = ptr(math.Inf(1)) },
		"SOC":                 func(s *SourceSnapshot) { s.Battery.Percentage = ptr(101.0) },
		"nav revision":        func(s *SourceSnapshot) { s.Navigation.Revision = 0 },
		"nav remaining":       func(s *SourceSnapshot) { s.Navigation.RemainingMinutes = ptr(-1.0) },
		"nav pair":            func(s *SourceSnapshot) { s.Navigation.Longitude = nil },
		"no timestamp":        func(s *SourceSnapshot) { s.Motion.ObservedAtMS = nil },
		"unknown state":       func(s *SourceSnapshot) { s.State = "secret label" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, spec, _, recipient := fixture(t, allPermissions())
			input := source(spec)
			mutate(&input)
			if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid data accepted: %v", err)
			}
		})
	}
}

func TestTrajectoryClippingCapacityAndOrdering(t *testing.T) {
	s, clock, spec, _, recipient := fixture(t, allPermissions())
	clock.Store(fixtureTime + 1000)
	input := source(spec)
	input.Trajectory = nil
	for i := int64(-1); i < 600; i++ {
		input.Trajectory = append(input.Trajectory, TrajectoryPoint{Latitude: 0, Longitude: 0, ObservedAtMS: fixtureTime + i})
	}
	out, err := s.Project(recipient, spec.Binding, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	points := *out.Trajectory
	if len(points) != 500 || cap(points) != 500 || points[0].ObservedAtMS != fixtureTime+100 || points[499].ObservedAtMS != fixtureTime+599 {
		t.Fatal("trajectory clipping/retention bounds")
	}
	input.Trajectory = []TrajectoryPoint{{ObservedAtMS: fixtureTime + 700}, {ObservedAtMS: fixtureTime + 700}}
	if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrOrder) {
		t.Fatal("duplicate times accepted")
	}
	input.Trajectory = []TrajectoryPoint{{ObservedAtMS: fixtureTime + 700}, {ObservedAtMS: fixtureTime + 699}}
	if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrOrder) {
		t.Fatal("reversed trajectory accepted")
	}
	input.Trajectory = make([]TrajectoryPoint, MaxSourceTrajectoryPoints+1)
	if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded input accepted")
	}
}

func TestObservationAndClockRegressionDoNotAdvanceSequence(t *testing.T) {
	for _, component := range []string{"motion", "navigation", "battery", "trajectory", "clock"} {
		t.Run(component, func(t *testing.T) {
			s, clock, spec, _, recipient := fixture(t, allPermissions())
			clock.Store(fixtureTime + 2)
			input := source(spec)
			input.Motion.ObservedAtMS = ptr(fixtureTime + 1)
			input.Navigation.ObservedAtMS++
			input.Battery.ObservedAtMS++
			input.Trajectory[0].ObservedAtMS++
			if _, err := s.Project(recipient, spec.Binding, 1, input); err != nil {
				t.Fatal(err)
			}
			switch component {
			case "motion":
				input.Motion.ObservedAtMS = ptr(fixtureTime)
			case "navigation":
				input.Navigation.ObservedAtMS--
			case "battery":
				input.Battery.ObservedAtMS--
			case "trajectory":
				input.Trajectory[0].ObservedAtMS--
			case "clock":
				clock.Store(fixtureTime + 1)
			}
			if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrOrder) {
				t.Fatalf("regression accepted: %v", err)
			}
			if s.grants[spec.Binding.GrantID].sequence != 1 {
				t.Fatal("rejected projection advanced sequence")
			}
		})
	}
}

func TestReturnedValuesDoNotAliasSourceOrPolicy(t *testing.T) {
	s, _, spec, _, recipient := fixture(t, allPermissions())
	input := source(spec)
	out, err := s.Project(recipient, spec.Binding, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	*input.Motion.Latitude = 50
	*input.Navigation.RemainingMinutes = 50
	*input.Battery.Percentage = 50
	input.Trajectory[0].Latitude = 50
	if *out.Motion.Latitude != 0 || *out.Navigation.RemainingMinutes != 0 || *out.Battery.Percentage != 0 || (*out.Trajectory)[0].Latitude != 0 {
		t.Fatal("snapshot aliases mutable input")
	}
	*out.Motion.ObservedAtMS = fixtureTime + 100
	if _, err := s.Project(recipient, spec.Binding, 1, source(spec)); err != nil {
		t.Fatalf("output mutated policy order: %v", err)
	}
}

func TestGrantValidationLimitsAndTombstones(t *testing.T) {
	s, _, base, owner, _ := fixture(t, allPermissions())
	for i := 2; i <= MaxGrants; i++ {
		spec := base
		spec.Binding.GrantID = id(100 + i)
		if _, err := s.Create(owner, spec); err != nil {
			t.Fatal(err)
		}
	}
	base.Binding.GrantID = id(900)
	if _, err := s.Create(owner, base); !errors.Is(err, ErrCapacity) {
		t.Fatal("store capacity not enforced")
	}
	for _, invalid := range []string{"location", "expiry", "too long", "zero uuid", "malformed uuid", "alias control", "alias length", "alias whitespace", "wrong owner"} {
		t.Run(invalid, func(t *testing.T) {
			s, _, spec, owner, _ := fixture(t, allPermissions())
			spec.Binding.GrantID = id(10)
			switch invalid {
			case "location":
				spec.Permissions.Location = false
			case "expiry":
				spec.ExpiresAtMS = fixtureTime
			case "too long":
				spec.ExpiresAtMS = fixtureTime + MaxGrantLifetimeMS + 1
			case "zero uuid":
				spec.Binding.MemberID = "00000000-0000-0000-0000-000000000000"
			case "malformed uuid":
				spec.Binding.MemberID = "member"
			case "alias control":
				spec.DestinationAlias = &DestinationAlias{1, "A\nB"}
			case "alias length":
				spec.DestinationAlias = &DestinationAlias{1, strings.Repeat("界", 86)}
			case "alias whitespace":
				spec.DestinationAlias = &DestinationAlias{1, "  "}
			case "wrong owner":
				owner.IssuerID = id(99)
			}
			if _, err := s.Create(owner, spec); err == nil {
				t.Fatal("invalid grant accepted")
			}
		})
	}
}

func TestConcurrentProjectionAndRevocation(t *testing.T) {
	s, _, spec, owner, recipient := fixture(t, allPermissions())
	const count = 200
	sequences := make(chan int64, count)
	var wait sync.WaitGroup
	for i := 0; i < count; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			out, err := s.Project(recipient, spec.Binding, 1, source(spec))
			if err != nil {
				t.Error(err)
				return
			}
			sequences <- out.Sequence
		}()
	}
	wait.Wait()
	close(sequences)
	seen := map[int64]bool{}
	for sequence := range sequences {
		if seen[sequence] {
			t.Fatal("duplicate sequence")
		}
		seen[sequence] = true
	}
	if len(seen) != count || !seen[1] || !seen[count] {
		t.Fatal("nonmonotonic sequence allocation")
	}
	if _, err := s.Revoke(owner, spec.Binding, spec.Source, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := s.Project(recipient, spec.Binding, 2, source(spec)); !errors.Is(err, ErrInactive) {
				t.Errorf("post-revoke read: %v", err)
			}
		}()
	}
	wait.Wait()
}

func TestCounterExhaustionFailsClosed(t *testing.T) {
	s, _, spec, owner, recipient := fixture(t, allPermissions())
	g := s.grants[spec.Binding.GrantID]
	g.sequence = math.MaxInt64
	if _, err := s.Project(recipient, spec.Binding, 1, source(spec)); !errors.Is(err, ErrExhausted) {
		t.Fatal("sequence wrapped")
	}
	g.metadata.Revision = math.MaxInt64
	if _, err := s.Restrict(owner, spec.Binding, spec.Source, math.MaxInt64, Permissions{Location: true}, spec.ExpiresAtMS); !errors.Is(err, ErrExhausted) {
		t.Fatal("restriction revision wrapped")
	}
	if metadata, err := s.Revoke(owner, spec.Binding, spec.Source, math.MaxInt64); err != nil || metadata.Revision != math.MaxInt64 {
		t.Fatal("counter exhaustion prevented revocation")
	}
	if _, err := s.Project(recipient, spec.Binding, math.MaxInt64, source(spec)); !errors.Is(err, ErrInactive) {
		t.Fatal("exhausted revoked grant remained active")
	}
}

func TestDestinationIdentityRequiresNewNavigationRevision(t *testing.T) {
	for _, field := range []string{"latitude", "longitude", "coordinate withdrawal"} {
		t.Run(field, func(t *testing.T) {
			s, _, spec, _, recipient := fixture(t, allPermissions())
			input := source(spec)
			if _, err := s.Project(recipient, spec.Binding, 1, input); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "latitude":
				input.Navigation.Latitude = ptr(2.0)
			case "longitude":
				input.Navigation.Longitude = ptr(2.0)
			case "coordinate withdrawal":
				input.Navigation.Latitude, input.Navigation.Longitude = nil, nil
			}
			if _, err := s.Project(recipient, spec.Binding, 1, input); !errors.Is(err, ErrOrder) {
				t.Fatal("same revision destination changed")
			}
			input.Navigation.Revision++
			if _, err := s.Project(recipient, spec.Binding, 1, input); err != nil {
				t.Fatalf("new revision rejected: %v", err)
			}
		})
	}
}
