package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAttachTripSummaryAddsEnergyAndEfficiencyWithoutCoordinates(t *testing.T) {
	m := &navigationNotificationMonitor{tripEnergyReader: func(id int64) *float64 {
		if id != 42 {
			t.Fatalf("unexpected drive id %d", id)
		}
		v := 2.345
		return &v
	}}
	driven := 12.5
	event := navigationLiveActivityEvent{Type: "destination_trip_arrived", DrivenDistanceKM: &driven, driveID: 42}
	m.attachTripSummary(&event)
	if event.TripEnergyKWh == nil || *event.TripEnergyKWh != 2.35 {
		t.Fatalf("energy = %v", event.TripEnergyKWh)
	}
	if event.TripEfficiencyWhPerKM == nil || *event.TripEfficiencyWhPerKM != 188 {
		t.Fatalf("efficiency = %v", event.TripEfficiencyWhPerKM)
	}
	payload, _ := json.Marshal(event)
	for _, forbidden := range []string{"latitude", "longitude", "drive_id_internal", "driveID"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("payload leaks %q: %s", forbidden, payload)
		}
	}
}

func TestAttachTripSummarySkipsUnknownOrImplausibleEnergy(t *testing.T) {
	bad := 900.0
	m := &navigationNotificationMonitor{tripEnergyReader: func(int64) *float64 { return &bad }}
	event := navigationLiveActivityEvent{driveID: 1}
	m.attachTripSummary(&event)
	if event.TripEnergyKWh != nil {
		t.Fatal("implausible energy must be dropped")
	}
	none := navigationLiveActivityEvent{}
	m.attachTripSummary(&none)
	if none.TripEnergyKWh != nil {
		t.Fatal("no drive id: no summary")
	}
}
