package main

import (
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var lockSecureMQTTFields = []string{"locked", "is_user_present", "state", "shift_state", "doors_open", "trunk_open", "frunk_open", "display_name", "healthy"}
var lowBatteryMQTTFields = []string{"display_name", "state", "shift_state", "charging_state", "battery_level"}

func resubscribeFleet(client mqtt.Client, fields []string, handler mqtt.MessageHandler) bool {
	if client == nil || !client.IsConnected() {
		return false
	}
	token := client.SubscribeMultiple(teslaMateMQTTTopics(fields), handler)
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		// Do not include broker details, payloads or credentials in diagnostics.
		log.Printf("[warn] fleet baseline subscription did not complete")
		requestMQTTBaselineRetry()
		return false
	}
	return client.IsConnected()
}
func (m *lockSecureNotificationMonitor) refreshFleetSubscription() {
	m.mu.Lock()
	client := m.client
	epoch := mqttConnectionEpoch("lock_secure")
	m.mu.Unlock()
	if resubscribeFleet(client, lockSecureMQTTFields, m.handleMQTTMessage) {
		m.mu.Lock()
		if m.client != client || !client.IsConnected() || !recordMQTTConnection("lock_secure", true, epoch) {
			m.mu.Unlock()
			return
		}
		m.connected = true
		if m.lastError == "MQTT lock-secure subscription timed out" {
			m.lastError = ""
		}
		m.mu.Unlock()
	}
}
func (m *lockSecureNotificationMonitor) reconcileFleet(cars map[int]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.store.Cars {
		if !cars[id] {
			delete(m.store.Cars, id)
			delete(m.evidence, id)
			if timer := m.pending[id]; timer != nil {
				timer.Stop()
				delete(m.pending, id)
			}
		}
	}
}
func (m *lowBatteryNotificationMonitor) refreshFleetSubscription() {
	m.mu.Lock()
	client := m.client
	epoch := mqttConnectionEpoch("low_battery")
	m.mu.Unlock()
	if resubscribeFleet(client, lowBatteryMQTTFields, m.handleMQTTMessage) {
		m.mu.Lock()
		if m.client != client || !client.IsConnected() || !recordMQTTConnection("low_battery", true, epoch) {
			m.mu.Unlock()
			return
		}
		m.connected = true
		if m.lastError == "MQTT low-battery subscription timed out" {
			m.lastError = ""
		}
		m.mu.Unlock()
	}
}
func (m *lowBatteryNotificationMonitor) reconcileFleet(cars map[int]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.store.Cars {
		if !cars[id] {
			delete(m.store.Cars, id)
			if timer := m.timers[id]; timer != nil {
				timer.Stop()
				delete(m.timers, id)
			}
		}
	}
}
func (s *friendTeslaMateSource) reconcileFleet(cars map[int]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.navigation {
		if !cars[int(id)] {
			delete(s.navigation, id)
		}
	}
}
