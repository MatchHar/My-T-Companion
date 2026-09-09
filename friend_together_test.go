package main

import (
	"strings"
	"testing"

	ft "mycarmate-states-api/internal/friendtogether"
)

type friendMQTTTestMessage struct {
	retained  bool
	duplicate bool
	topic     string
	payload   []byte
}

func (m friendMQTTTestMessage) Duplicate() bool   { return m.duplicate }
func (m friendMQTTTestMessage) Qos() byte         { return 1 }
func (m friendMQTTTestMessage) Retained() bool    { return m.retained }
func (m friendMQTTTestMessage) Topic() string     { return m.topic }
func (m friendMQTTTestMessage) MessageID() uint16 { return 1 }
func (m friendMQTTTestMessage) Payload() []byte   { return m.payload }
func (m friendMQTTTestMessage) Ack()              {}

func TestFriendRetainedNavigationNeverBecomesFresh(t *testing.T) {
	s := &friendTeslaMateSource{navigation: map[int64]friendNavigationSample{}}
	message := friendMQTTTestMessage{retained: true, topic: "teslamate/cars/1/active_route", payload: []byte(`{"miles_to_arrival":1,"minutes_to_arrival":2,"location":{"latitude":43,"longitude":-79}}`)}
	s.handleNavigationMessage(message, 1000)
	if len(s.navigation) != 0 {
		t.Fatal("retained bootstrap gained observation time")
	}
	message.retained = false
	s.handleNavigationMessage(message, 2000)
	if !s.navigation[1].active || s.navigation[1].observation.ObservedAtMS != 2000 {
		t.Fatal("missing genuine observation")
	}
	message.duplicate = true
	s.handleNavigationMessage(message, 2500)
	if s.navigation[1].observation.ObservedAtMS != 2000 {
		t.Fatal("QoS redelivery became fresh")
	}
	message.duplicate = false
	message.payload = []byte(`null`)
	s.handleNavigationMessage(message, 1500)
	if !s.navigation[1].active {
		t.Fatal("older callback withdrew newer navigation")
	}
	message.topic = "teslamate/cars/1/display_name"
	s.handleNavigationMessage(message, 3000)
	if s.navigation[1].lastObservedMS != 2000 {
		t.Fatal("unrelated field changed navigation time")
	}
}

func TestFriendNavigationSourceRevisionAndWithdrawal(t *testing.T) {
	s := &friendTeslaMateSource{navigation: map[int64]friendNavigationSample{}}
	route := []byte(`{"destination":"Do not share private name","miles_to_arrival":1.5,"minutes_to_arrival":4,"error":null,"location":{"latitude":43.1,"longitude":-79.2}}`)
	s.observeNavigation(1, route, 1000)
	first := s.navigation[1]
	if !first.active || first.revision != 1 || first.observation.ObservedAtMS != 1000 || *first.observation.RemainingDistanceKM != 1.5*1.609344 {
		t.Fatal("initial route")
	}
	s.observeNavigation(1, route, 2000)
	if s.navigation[1].revision != 1 {
		t.Fatal("same destination should keep revision")
	}
	s.observeNavigation(1, []byte(`null`), 3000)
	if s.navigation[1].active || s.navigation[1].observation != nil {
		t.Fatal("withdrawal")
	}
	s.observeNavigation(1, route, 4000)
	if s.navigation[1].revision != 2 {
		t.Fatal("restart route must increment")
	}
	s.observeNavigation(1, []byte(`{"miles_to_arrival":0,"minutes_to_arrival":0,"location":{"latitude":43.2,"longitude":-79.2}}`), 5000)
	if s.navigation[1].revision != 3 || *s.navigation[1].observation.RemainingMinutes != 0 {
		t.Fatal("reroute and zero")
	}
	if _, ok := s.navigation[2]; ok {
		t.Fatal("car scope")
	}
}
func TestFriendNavigationRejectsPartialInvalidAndBounds(t *testing.T) {
	for _, raw := range []string{`{"miles_to_arrival":1,"minutes_to_arrival":2}`, `{"error":"stale","miles_to_arrival":1,"minutes_to_arrival":2,"location":{"latitude":43,"longitude":-79}}`, `{"miles_to_arrival":-1,"minutes_to_arrival":2,"location":{"latitude":43,"longitude":-79}}`, `{"miles_to_arrival":1,"minutes_to_arrival":2,"location":{"latitude":99,"longitude":-79}}`} {
		s := &friendTeslaMateSource{navigation: map[int64]friendNavigationSample{1: {active: true, revision: 7, observation: &ft.NavigationObservation{Revision: 7}}}}
		s.observeNavigation(1, []byte(raw), 1000)
		if s.navigation[1].active || s.navigation[1].revision != 7 {
			t.Fatal("invalid route kept live")
		}
	}
}

func TestFriendDatabaseDSNMatchesWorkingPool(t *testing.T) {
	t.Setenv("DATABASE_HOST", "database")
	t.Setenv("DATABASE_PORT", "5432")
	t.Setenv("DATABASE_USER", "teslamate")
	t.Setenv("DATABASE_PASS", "unit-test-pass")
	t.Setenv("DATABASE_NAME", "teslamate")
	t.Setenv("DATABASE_SSL", "disable")
	t.Setenv("DATABASE_TIMEOUT", "10")
	dsn := friendDatabaseDSN()
	if strings.HasPrefix(dsn, "postgres:") {
		t.Fatal("friend pool must use the keyword DSN that already works for parking")
	}
	for _, part := range []string{"host='database'", "port='5432'", "user='teslamate'", "dbname='teslamate'", "application_name=my-t-companion-friends"} {
		if !strings.Contains(dsn, part) {
			t.Fatalf("missing %s", part)
		}
	}
}
