package friendtogether

import "math"

func (g *grant) project(source SourceSnapshot, now int64) (Snapshot, error) {
	m := g.metadata
	out := Snapshot{SchemaVersion: SchemaVersion, Binding: m.Binding,
		Revision: m.Revision, ServerTimeMS: now, ConsentStartedAtMS: m.ConsentStartedAtMS,
		ExpiresAtMS: m.ExpiresAtMS, State: StateUnknown}
	if source.Motion != nil {
		motion := source.Motion
		if motion.ObservedAtMS == nil {
			if motion.Latitude != nil || motion.Longitude != nil || motion.SpeedKPH != nil || motion.HeadingDegrees != nil || source.State != StateUnknown {
				return Snapshot{}, ErrInvalid
			}
		} else {
			eligible, err := eligibleTime(*motion.ObservedAtMS, m.ConsentStartedAtMS, now)
			if err != nil {
				return Snapshot{}, err
			}
			if eligible {
				if !validCoordinates(motion.Latitude, motion.Longitude) || !bounded(motion.SpeedKPH, 0, 324, true) ||
					!bounded(motion.HeadingDegrees, 0, 360, false) || !validState(source.State) {
					return Snapshot{}, ErrInvalid
				}
				out.State = source.State
				out.Motion = Motion{Latitude: copyPointer(motion.Latitude), Longitude: copyPointer(motion.Longitude),
					SpeedKPH: copyPointer(motion.SpeedKPH), HeadingDegrees: copyPointer(motion.HeadingDegrees), ObservedAtMS: copyPointer(motion.ObservedAtMS)}
			}
		}
	}
	// Permissions are tested before values; excluded data cannot leak into errors
	// or into the DTO, even when the source has old/malformed optional values.
	if m.Permissions.Navigation && source.Navigation != nil {
		n := source.Navigation
		eligible, err := eligibleTime(n.ObservedAtMS, m.ConsentStartedAtMS, now)
		if err != nil {
			return Snapshot{}, err
		}
		if eligible {
			if n.Revision < 1 || !validCoordinates(n.Latitude, n.Longitude) || !nonnegative(n.RemainingDistanceKM) || !nonnegative(n.RemainingMinutes) {
				return Snapshot{}, ErrInvalid
			}
			out.Navigation = &Navigation{Revision: n.Revision, Latitude: copyPointer(n.Latitude), Longitude: copyPointer(n.Longitude),
				RemainingDistanceKM: copyPointer(n.RemainingDistanceKM), RemainingMinutes: copyPointer(n.RemainingMinutes), ObservedAtMS: n.ObservedAtMS}
			if g.alias != nil && g.alias.NavigationRevision == n.Revision {
				out.Navigation.Destination = copyPointer(&g.alias.Value)
			}
		}
	}
	if m.Permissions.Battery && source.Battery != nil {
		b := source.Battery
		eligible, err := eligibleTime(b.ObservedAtMS, m.ConsentStartedAtMS, now)
		if err != nil {
			return Snapshot{}, err
		}
		if eligible {
			if !bounded(b.Percentage, 0, 100, true) || !nonnegative(b.RatedRangeKM) {
				return Snapshot{}, ErrInvalid
			}
			out.Battery = &Battery{Percentage: copyPointer(b.Percentage), RatedRangeKM: copyPointer(b.RatedRangeKM), ObservedAtMS: b.ObservedAtMS}
		}
	}
	if m.Permissions.Trajectory {
		if len(source.Trajectory) > MaxSourceTrajectoryPoints {
			return Snapshot{}, ErrInvalid
		}
		points := make([]TrajectoryPoint, 0, min(len(source.Trajectory), MaxTrajectoryPoints))
		var previous *int64
		for _, point := range source.Trajectory {
			eligible, err := eligibleTime(point.ObservedAtMS, m.ConsentStartedAtMS, now)
			if err != nil {
				return Snapshot{}, err
			}
			if !eligible {
				continue
			}
			if !validCoordinates(&point.Latitude, &point.Longitude) {
				return Snapshot{}, ErrInvalid
			}
			if previous != nil && point.ObservedAtMS <= *previous {
				return Snapshot{}, ErrOrder
			}
			previous = copyPointer(&point.ObservedAtMS)
			points = append(points, point)
		}
		if len(points) > MaxTrajectoryPoints {
			points = points[len(points)-MaxTrajectoryPoints:]
		}
		// Copy to an exactly bounded backing array so discarded history is not
		// retained by the returned snapshot's slice capacity.
		boundedPoints := make([]TrajectoryPoint, len(points))
		copy(boundedPoints, points)
		out.Trajectory = &boundedPoints
	}
	return out, nil
}

func eligibleTime(observed, consent, now int64) (bool, error) {
	if !validTime(observed) || observed > now {
		return false, ErrInvalid
	}
	return observed >= consent, nil
}

func validCoordinates(lat, lon *float64) bool {
	if lat == nil || lon == nil {
		return lat == nil && lon == nil
	}
	return bounded(lat, -90, 90, true) && bounded(lon, -180, 180, true)
}

func bounded(value *float64, minimum, maximum float64, includeMaximum bool) bool {
	if value == nil {
		return true
	}
	v := *value
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= minimum && (v < maximum || includeMaximum && v == maximum)
}

func nonnegative(value *float64) bool { return bounded(value, 0, math.MaxFloat64, true) }

func validState(state State) bool {
	switch state {
	case StateDriving, StateParked, StateCharging, StateAsleep, StateOffline, StateUnknown:
		return true
	default:
		return false
	}
}
