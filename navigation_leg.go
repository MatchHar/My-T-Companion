package main

import (
	"encoding/json"
	"math"
	"strings"
)

const (
	navigationLegPhaseActive           = "active"
	navigationLegPhasePendingNext      = "pending_next"
	navigationLegPhaseConfirmedArrived = "confirmed_arrived"
	navigationLegPhaseEnded            = "ended"

	navigationAliasCoordinateMeters = 150.0
	navigationAliasRemainingFloorKM = 0.15
	navigationAliasRemainingShare   = 0.08
	navigationAliasRemainingCapKM   = 3.0
	navigationArrivalDistanceKM     = 0.35
	navigationArrivalMinutes        = 2
)

type navigationLegDecision int

const (
	navigationLegKeep navigationLegDecision = iota
	navigationLegAlias
	navigationLegStage
	navigationLegNew
)

type navigationRouteCandidate struct {
	Destination      string
	RemainingKM      *float64
	RemainingMinutes *int
	ArrivalBattery   *int
	Latitude         *float64
	Longitude        *float64
	Invalid          bool
}

func parseActiveRouteCandidate(raw string) navigationRouteCandidate {
	var route activeRouteMQTT
	if json.Unmarshal([]byte(raw), &route) != nil || route.Error != nil ||
		strings.TrimSpace(route.Destination) == "" {
		return navigationRouteCandidate{Invalid: true}
	}
	candidate := navigationRouteCandidate{
		Destination:    strings.TrimSpace(route.Destination),
		ArrivalBattery: nil,
	}
	if route.MilesToArrival != nil {
		km := *route.MilesToArrival * 1.609344
		candidate.RemainingKM = &km
	}
	if route.MinutesToArrival != nil {
		minutes := int(*route.MinutesToArrival + 0.5)
		candidate.RemainingMinutes = &minutes
	}
	if route.EnergyAtArrival != nil && *route.EnergyAtArrival >= 0 && *route.EnergyAtArrival <= 100 {
		candidate.ArrivalBattery = route.EnergyAtArrival
	}
	if route.Location != nil {
		lat := route.Location.Latitude
		lon := route.Location.Longitude
		if validRouteCoordinate(lat, lon) {
			candidate.Latitude = cloneFloat(lat)
			candidate.Longitude = cloneFloat(lon)
		}
	}
	return candidate
}

func classifyNavigationLegChange(committed carNavigationState, candidate navigationRouteCandidate) navigationLegDecision {
	if candidate.Invalid || strings.TrimSpace(candidate.Destination) == "" {
		return navigationLegKeep
	}
	if strings.TrimSpace(committed.Destination) == "" {
		return navigationLegKeep
	}
	sameName := navigationDestinationEqual(committed.Destination, candidate.Destination)
	coordsClose, coordsFar, coordsKnown := navigationDestinationCoordinatesRelation(
		committed.DestinationLatitude,
		committed.DestinationLongitude,
		candidate.Latitude,
		candidate.Longitude,
	)
	remainingNew := navigationRemainingDeltaStartsNewLeg(committed.RemainingDistanceKM, candidate.RemainingKM)

	if sameName {
		if coordsKnown && coordsClose {
			return navigationLegKeep
		}
		if coordsFar || navigationRemainingLooksLikeNewSameNameStop(committed.RemainingDistanceKM, candidate.RemainingKM) {
			return navigationLegNew
		}
		return navigationLegKeep
	}
	if coordsKnown && coordsClose {
		return navigationLegAlias
	}
	if coordsFar {
		return navigationLegNew
	}
	if committed.RemainingDistanceKM == nil || candidate.RemainingKM == nil {
		return navigationLegStage
	}
	if remainingNew {
		return navigationLegNew
	}
	return navigationLegAlias
}

func navigationRemainingDeltaStartsNewLeg(previous, current *float64) bool {
	if previous == nil || current == nil {
		return false
	}
	prev := math.Max(0, *previous)
	curr := math.Max(0, *current)
	delta := math.Abs(prev - curr)
	tolerance := math.Max(navigationAliasRemainingFloorKM, math.Max(prev, curr)*navigationAliasRemainingShare)
	if tolerance > navigationAliasRemainingCapKM {
		tolerance = navigationAliasRemainingCapKM
	}
	return delta > tolerance
}

func navigationRemainingLooksLikeNewSameNameStop(previous, current *float64) bool {
	if previous == nil || current == nil {
		return false
	}
	// Remaining shrinking is ordinary progress, not a new stop with the same label.
	if *current <= *previous+navigationAliasRemainingFloorKM {
		return false
	}
	return navigationRemainingDeltaStartsNewLeg(previous, current)
}

func navigationDestinationCoordinatesRelation(lat1, lon1, lat2, lon2 *float64) (close, far, known bool) {
	if !validRouteCoordinate(lat1, lon1) || !validRouteCoordinate(lat2, lon2) {
		return false, false, false
	}
	meters := destinationCoordinateDistanceMeters(*lat1, *lon1, *lat2, *lon2)
	if meters <= navigationAliasCoordinateMeters {
		return true, false, true
	}
	return false, true, true
}

func validRouteCoordinate(lat, lon *float64) bool {
	if lat == nil || lon == nil {
		return false
	}
	if *lat == 0 && *lon == 0 {
		return false
	}
	return *lat >= -90 && *lat <= 90 && *lon >= -180 && *lon <= 180
}

func destinationCoordinateDistanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthMeters = 6371000.0
	toRad := math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthMeters * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func navigationVehicleStateIsParkingEvidence(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "online", "charging":
		return true
	default:
		return false
	}
}

func navigationVehicleStateIsTransientUnknown(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "offline", "asleep", "unavailable", "unknown":
		return true
	default:
		return false
	}
}

func navigationRemainingLooksArrived(distanceKM *float64, minutes *int) bool {
	// A recalculation may briefly publish zero minutes beside many kilometres.
	// When both measurements exist they must agree, rather than letting either
	// partial zero turn parking/cancellation into a false arrival.
	if distanceKM != nil && minutes != nil {
		return *distanceKM <= navigationArrivalDistanceKM && *minutes <= navigationArrivalMinutes
	}
	if distanceKM != nil && *distanceKM <= navigationArrivalDistanceKM {
		return true
	}
	if minutes != nil && *minutes <= navigationArrivalMinutes {
		return true
	}
	return false
}

func cloneCarNavigationState(state carNavigationState) carNavigationState {
	cloned := state
	cloned.RemainingDistanceKM = cloneFloat(state.RemainingDistanceKM)
	cloned.RemainingMinutes = cloneInt(state.RemainingMinutes)
	cloned.ArrivalBatteryLevel = cloneInt(state.ArrivalBatteryLevel)
	cloned.DrivenDistanceKM = cloneFloat(state.DrivenDistanceKM)
	cloned.DestinationLatitude = cloneFloat(state.DestinationLatitude)
	cloned.DestinationLongitude = cloneFloat(state.DestinationLongitude)
	cloned.PendingRemainingKM = cloneFloat(state.PendingRemainingKM)
	cloned.PendingRemainingMinutes = cloneInt(state.PendingRemainingMinutes)
	cloned.PendingArrivalBattery = cloneInt(state.PendingArrivalBattery)
	cloned.PendingLatitude = cloneFloat(state.PendingLatitude)
	cloned.PendingLongitude = cloneFloat(state.PendingLongitude)
	return cloned
}

func applyRouteCandidate(state *carNavigationState, candidate navigationRouteCandidate) {
	if candidate.Invalid {
		return
	}
	if candidate.Destination != "" {
		state.Destination = candidate.Destination
	}
	if candidate.RemainingKM != nil {
		state.RemainingDistanceKM = cloneFloat(candidate.RemainingKM)
	}
	if candidate.RemainingMinutes != nil {
		state.RemainingMinutes = cloneInt(candidate.RemainingMinutes)
	}
	if candidate.ArrivalBattery != nil {
		state.ArrivalBatteryLevel = cloneInt(candidate.ArrivalBattery)
	}
	if validRouteCoordinate(candidate.Latitude, candidate.Longitude) {
		state.DestinationLatitude = cloneFloat(candidate.Latitude)
		state.DestinationLongitude = cloneFloat(candidate.Longitude)
	}
	clearPendingCandidate(state)
	if state.Active {
		state.LegPhase = navigationLegPhaseActive
	}
}

func stagePendingCandidate(state *carNavigationState, candidate navigationRouteCandidate) {
	state.PendingDestination = candidate.Destination
	state.PendingRemainingKM = cloneFloat(candidate.RemainingKM)
	state.PendingRemainingMinutes = cloneInt(candidate.RemainingMinutes)
	state.PendingArrivalBattery = cloneInt(candidate.ArrivalBattery)
	state.PendingLatitude = cloneFloat(candidate.Latitude)
	state.PendingLongitude = cloneFloat(candidate.Longitude)
	if state.Active {
		state.LegPhase = navigationLegPhasePendingNext
	}
}

func clearPendingCandidate(state *carNavigationState) {
	state.PendingDestination = ""
	state.PendingRemainingKM = nil
	state.PendingRemainingMinutes = nil
	state.PendingArrivalBattery = nil
	state.PendingLatitude = nil
	state.PendingLongitude = nil
}

func clearCommittedRoute(state *carNavigationState) {
	state.Destination = ""
	state.RemainingDistanceKM = nil
	state.RemainingMinutes = nil
	state.ArrivalBatteryLevel = nil
	state.DestinationLatitude = nil
	state.DestinationLongitude = nil
	clearPendingCandidate(state)
}
