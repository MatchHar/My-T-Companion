package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/lib/pq"
)

// Embedded unused interface methods intentionally panic if the code starts a
// connection; these tests implement subscription only, with no transport.
type notificationSubscriptionClient struct {
	mqtt.Client
	err         error
	calls       int
	onSubscribe func(mqtt.MessageHandler)
	onWait      func()
}

func (*notificationSubscriptionClient) IsConnected() bool { return true }
func (c *notificationSubscriptionClient) SubscribeMultiple(_ map[string]byte, handler mqtt.MessageHandler) mqtt.Token {
	c.calls++
	if c.onSubscribe != nil {
		c.onSubscribe(handler)
	}
	return notificationSubscriptionToken{err: c.err, onWait: c.onWait}
}

type notificationSubscriptionToken struct {
	err    error
	onWait func()
}

func (notificationSubscriptionToken) Wait() bool { return true }
func (t notificationSubscriptionToken) WaitTimeout(time.Duration) bool {
	if t.onWait != nil {
		t.onWait()
	}
	return true
}
func (notificationSubscriptionToken) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}
func (t notificationSubscriptionToken) Error() error { return t.err }

func TestLowBatteryDueSnoozeWaitsThroughUnknownReconnect(t *testing.T) {
	m, events := newLowBatteryTestMonitor(t, 1)
	t.Cleanup(m.stop)
	at := time.Now().UTC()
	installationID := pushRegistry.store.Subscribers[0].InstallationID
	state := lowBatteryTestState(19)
	state.Initialized, state.Latched, state.EpisodeID = true, true, "existing-episode"
	state.Actions[installationID] = lowBatteryActionState{Action: "snooze_4h", SnoozeUntil: at.Add(-time.Minute).Format(time.RFC3339)}
	m.store.Cars[1] = state
	m.resetObservationBaseline()
	m.processDueReminders(at)
	if got := m.store.Cars[1].Actions[installationID]; got.Action != "snooze_4h" || got.SnoozeUntil == "" || len(*events) != 0 {
		t.Fatalf("unknown reconnect consumed or sent due reminder: %+v events=%+v", got, *events)
	}
	for field, value := range map[string]string{"state": "asleep", "charging_state": "disconnected", "battery_level": "19"} {
		m.observeMessage(1, field, value, at, true)
	}
	m.evaluate(1, at)
	if len(*events) != 0 {
		t.Fatal("recovered baseline created a new low alert")
	}
	m.processDueReminders(at.Add(time.Minute))
	m.processDueReminders(at.Add(2 * time.Minute))
	if len(*events) != 1 || (*events)[0].Type != "vehicle_low_battery_reminder" || (*events)[0].EpisodeID != "existing-episode" {
		t.Fatalf("expected one preserved due reminder: %+v", *events)
	}
}

func TestLowBatteryStaleFanOutCannotSurviveFleetReenableOrDisconnect(t *testing.T) {
	m, events := newLowBatteryTestMonitor(t, 1)
	t.Cleanup(m.stop)
	at := time.Now().UTC()
	old := lowBatteryTestState(19)
	old.Initialized, old.Latched, old.EpisodeID = true, true, "old-episode"
	m.store.Cars[1] = old
	m.reconcileFleet(map[int]bool{1: false})
	current := lowBatteryTestState(19)
	current.Initialized, current.Latched, current.EpisodeID = true, true, "new-episode"
	m.store.Cars[1] = current
	m.fanOut(1, old, "vehicle_low_battery", at, "")
	if len(*events) != 0 {
		t.Fatal("pre-removal event dispatched against re-enabled car")
	}
	m.resetObservationBaseline()
	m.fanOut(1, current, "vehicle_low_battery", at, "")
	if len(*events) != 0 {
		t.Fatal("disconnected evidence dispatched old event")
	}
	m.store.Cars[1] = current
	m.fanOut(1, current, "vehicle_low_battery", at, "")
	if len(*events) != 1 {
		t.Fatal("valid current episode was suppressed")
	}
}

func TestFriendQueuedNavigationCannotSurviveDisconnectGeneration(t *testing.T) {
	t.Setenv("MQTT_NAMESPACE", "")
	s := &friendTeslaMateSource{navigation: map[int64]friendNavigationSample{}}
	message := friendMQTTTestMessage{topic: "teslamate/cars/1/active_route", payload: []byte(`{"miles_to_arrival":1,"minutes_to_arrival":2,"location":{"latitude":43,"longitude":-79}}`)}
	oldGeneration, accepting := s.beginNavigationConnection()
	if !accepting {
		t.Fatal("could not begin test connection")
	}
	oldHandler := s.navigationMessageHandler()
	oldHandler(nil, message)
	if !s.navigation[1].active {
		t.Fatal("current live observation rejected")
	}
	s.invalidateNavigationConnection()
	oldHandler(nil, message)
	s.handleNavigationMessage(message, time.Now().UnixMilli())
	if s.navigation[1].active || s.navigation[1].observation != nil {
		t.Fatal("queued callback revived disconnected source")
	}
	if _, accepting := s.beginNavigationConnection(); !accepting {
		t.Fatal("could not begin reconnected source")
	}
	oldHandler(nil, message)
	if s.navigation[1].active {
		t.Fatal("previous connection callback entered new generation")
	}
	if s.finishNavigationSubscription(oldGeneration, false) {
		t.Fatal("old subscription failure invalidated new generation")
	}
	newHandler := s.navigationMessageHandler()
	newHandler(nil, message)
	if !s.navigation[1].active {
		t.Fatal("new generation live observation rejected")
	}
	s.stop()
	newHandler(nil, message)
	_, accepting = s.beginNavigationConnection()
	if s.navigation[1].active || accepting {
		t.Fatal("stopped source accepted queued/reconnect callback")
	}
}

func TestFriendDatabaseKeywordEscapingAndUTCParseWithoutConnection(t *testing.T) {
	password := `synthetic p'a\ss word`
	t.Setenv("DATABASE_HOST", "database")
	t.Setenv("DATABASE_PORT", "5432")
	t.Setenv("DATABASE_USER", "teslamate")
	t.Setenv("DATABASE_PASS", password)
	t.Setenv("DATABASE_NAME", "teslamate")
	t.Setenv("DATABASE_SSL", "disable")
	t.Setenv("DATABASE_TIMEOUT", "10")
	for _, makeDSN := range []func() string{friendDatabaseDSN, mainDatabaseDSN} {
		dsn := makeDSN()
		if _, err := pq.NewConnector(dsn); err != nil {
			t.Fatalf("keyword DSN rejected by pq without dialing: %v", err)
		}
		config, err := pq.NewConfig(dsn)
		if err != nil || config.Password != password {
			t.Fatalf("keyword DSN did not preserve synthetic password: err=%v", err)
		}
		if !strings.Contains(dsn, "-c timezone=UTC") {
			t.Fatal("read session lost UTC option")
		}
	}
	if !strings.Contains(friendDatabaseDSN(), "-c default_transaction_read_only=on") {
		t.Fatal("friend session lost read-only option")
	}
}

func TestSoftwareFleetSubscriptionRecoversFailedInitialSUBACK(t *testing.T) {
	runtime := installMQTTTestRuntime(t, map[int]bool{2: true})
	m, events := softwareTestMonitor(t)
	client := &notificationSubscriptionClient{err: errors.New("synthetic SUBACK failure")}
	m.client = client
	m.refreshFleetSubscription()
	if m.connected || client.calls != 1 {
		t.Fatal("failed subscription marked connected or skipped recovery")
	}
	client.err = nil
	client.onSubscribe = func(handler mqtt.MessageHandler) {
		handler(nil, friendMQTTTestMessage{retained: true, topic: "teslamate/cars/2/software_update", payload: []byte(`{"installed_version":"2026.1","latest_version":"2026.2"}`)})
	}
	m.refreshFleetSubscription()
	if !m.connected || client.calls != 2 || !runtime.connections["software"].Connected {
		t.Fatal("successful retry did not recover initial SUBACK failure")
	}
	if m.carState(2).UpdateAvailabilityStatus != "available" {
		t.Fatal("retained baseline was not recovered")
	}
	expectNoSoftwareEvent(t, m, events)
}

func TestSoftwareLateSubscriptionCannotReviveInvalidatedConnection(t *testing.T) {
	runtime := installMQTTTestRuntime(t, map[int]bool{2: true})
	m, _ := softwareTestMonitor(t)
	client := &notificationSubscriptionClient{}
	m.client = client
	client.onSubscribe = func(mqtt.MessageHandler) {
		m.mu.Lock()
		m.resetObservationBaselineLocked()
		m.client = nil
		m.mu.Unlock()
	}
	m.refreshFleetSubscription()
	if m.connected || runtime.connections["software"].Connected {
		t.Fatal("late SUBACK revived invalidated software connection")
	}
}

func TestFriendFleetSubscriptionRecoversWithoutResettingWorkingState(t *testing.T) {
	runtime := installMQTTTestRuntime(t, map[int]bool{1: true})
	s := &friendTeslaMateSource{navigation: map[int64]friendNavigationSample{}}
	s.invalidateNavigationConnection() // same state as a failed initial SUBACK
	client := &notificationSubscriptionClient{err: errors.New("synthetic SUBACK failure")}
	s.client = client
	s.refreshFleetSubscription()
	if s.mqttAccepting || client.calls != 1 {
		t.Fatal("failed recovery accepted messages")
	}
	client.err = nil
	client.onSubscribe = func(handler mqtt.MessageHandler) {
		handler(nil, friendMQTTTestMessage{topic: "teslamate/cars/1/active_route", payload: []byte(`{"miles_to_arrival":1,"minutes_to_arrival":2,"location":{"latitude":43,"longitude":-79}}`)})
	}
	s.refreshFleetSubscription()
	if !s.mqttAccepting || !s.navigation[1].active || !runtime.connections["friend_navigation"].Connected {
		t.Fatal("friend initial SUBACK recovery failed")
	}
	generation := s.mqttGeneration
	client.onSubscribe = nil
	s.refreshFleetSubscription()
	if generation != s.mqttGeneration || !s.navigation[1].active {
		t.Fatal("new fleet baseline reset working friend observation")
	}
	client.err = errors.New("synthetic resubscribe failure")
	s.refreshFleetSubscription()
	if generation != s.mqttGeneration || !s.navigation[1].active {
		t.Fatal("failed new-car resubscription erased working state")
	}
}

func TestNotificationLateSUBACKCannotReviveSameClientConnection(t *testing.T) {
	for _, kind := range []string{"software", "friend_navigation", "lock_secure", "low_battery"} {
		t.Run(kind, func(t *testing.T) {
			runtime := installMQTTTestRuntime(t, map[int]bool{1: true})
			client := &notificationSubscriptionClient{}
			var refresh func()
			var connected func() bool
			var disconnect func()
			switch kind {
			case "software":
				m := &softwareNotificationMonitor{client: client, connected: true}
				refresh = m.refreshFleetSubscription
				connected = func() bool { return m.connected }
				disconnect = func() {
					m.mu.Lock()
					m.connected = false
					// Leave the local generation unchanged to independently exercise
					// the source epoch guard, not the existing generation guard.
					recordMQTTConnection(kind, false)
					m.mu.Unlock()
				}
			case "friend_navigation":
				s := &friendTeslaMateSource{client: client, mqttManaged: true, mqttAccepting: true, navigation: map[int64]friendNavigationSample{}}
				refresh = s.refreshFleetSubscription
				connected = func() bool { return s.mqttAccepting }
				disconnect = s.invalidateNavigationConnection
			case "lock_secure":
				m := &lockSecureNotificationMonitor{client: client, connected: true}
				refresh = m.refreshFleetSubscription
				connected = func() bool { return m.connected }
				disconnect = func() {
					m.mu.Lock()
					m.connected = false
					recordMQTTConnection(kind, false)
					m.mu.Unlock()
				}
			case "low_battery":
				m := &lowBatteryNotificationMonitor{client: client, connected: true}
				refresh = m.refreshFleetSubscription
				connected = func() bool { return m.connected }
				disconnect = func() {
					m.mu.Lock()
					m.connected = false
					recordMQTTConnection(kind, false)
					m.mu.Unlock()
				}
			}
			epoch := beginMQTTConnection(kind)
			if !recordMQTTConnection(kind, true, epoch) {
				t.Fatal("could not establish synthetic connection")
			}
			// Complete the old token only after ConnectionLost. IsConnected stays
			// true to model the same Paho client already reconnecting underneath it.
			client.onWait = disconnect
			refresh()
			if connected() || runtime.connections[kind].Connected || mqttConnectionEpoch(kind) == epoch {
				t.Fatal("late successful SUBACK revived the disconnected same client")
			}
			client.onWait = nil
			refresh()
			if !connected() || !runtime.connections[kind].Connected {
				t.Fatal("current epoch subscription did not recover after stale result")
			}
		})
	}
}

func TestNotificationFailedRefreshKeepsReplacementState(t *testing.T) {
	for _, kind := range []string{"software", "lock_secure", "low_battery"} {
		t.Run(kind, func(t *testing.T) {
			runtime := installMQTTTestRuntime(t, map[int]bool{1: true})
			oldClient := &notificationSubscriptionClient{err: errors.New("synthetic old SUBACK failure")}
			replacement := &notificationSubscriptionClient{}
			var refresh func()
			var current func() (bool, string)
			switch kind {
			case "software":
				m := &softwareNotificationMonitor{client: oldClient}
				refresh = m.refreshFleetSubscription
				current = func() (bool, string) { return m.connected, m.lastError }
				oldClient.onWait = func() {
					m.mu.Lock()
					m.client, m.connected, m.lastError = replacement, true, "replacement-state"
					epoch := beginMQTTConnection(kind)
					recordMQTTConnection(kind, true, epoch)
					m.mu.Unlock()
				}
			case "lock_secure":
				m := &lockSecureNotificationMonitor{client: oldClient}
				refresh = m.refreshFleetSubscription
				current = func() (bool, string) { return m.connected, m.lastError }
				oldClient.onWait = func() {
					m.mu.Lock()
					m.client, m.connected, m.lastError = replacement, true, "replacement-state"
					epoch := beginMQTTConnection(kind)
					recordMQTTConnection(kind, true, epoch)
					m.mu.Unlock()
				}
			case "low_battery":
				m := &lowBatteryNotificationMonitor{client: oldClient}
				refresh = m.refreshFleetSubscription
				current = func() (bool, string) { return m.connected, m.lastError }
				oldClient.onWait = func() {
					m.mu.Lock()
					m.client, m.connected, m.lastError = replacement, true, "replacement-state"
					epoch := beginMQTTConnection(kind)
					recordMQTTConnection(kind, true, epoch)
					m.mu.Unlock()
				}
			}
			beginMQTTConnection(kind)
			refresh()
			connected, lastError := current()
			if !connected || lastError != "replacement-state" || !runtime.connections[kind].Connected {
				t.Fatal("old failed SUBACK clobbered replacement connection state")
			}
		})
	}
}

func TestFriendSUBACKCannotMutateReplacementClient(t *testing.T) {
	runtime := installMQTTTestRuntime(t, map[int]bool{1: true})
	oldClient, replacement := &notificationSubscriptionClient{}, &notificationSubscriptionClient{}
	s := &friendTeslaMateSource{client: replacement, mqttManaged: true, mqttAccepting: true, mqttGeneration: 7, navigation: map[int64]friendNavigationSample{1: {active: true}}}
	epoch := beginMQTTConnection("friend_navigation")
	recordMQTTConnection("friend_navigation", true, epoch)
	// Keep generation/epoch unchanged to independently exercise client identity.
	for _, success := range []bool{false, true} {
		if s.finishNavigationSubscriptionForClient(oldClient, 7, success, epoch) {
			t.Fatal("old client subscription completion was accepted")
		}
		if !s.mqttAccepting || s.mqttGeneration != 7 || !s.navigation[1].active || !runtime.connections["friend_navigation"].Connected || mqttConnectionEpoch("friend_navigation") != epoch {
			t.Fatal("old client completion altered replacement source state")
		}
	}
}
