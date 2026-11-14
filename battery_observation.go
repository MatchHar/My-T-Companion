package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

const batteryObservationReadTimeout = 2 * time.Second

// The fields below come from one persisted vehicle observation. In particular,
// SOC is never taken from the independently updated legacy-stream summary.
const batteryObservationQuery = `
SELECT source, car_id, date, battery_level, usable_battery_level,
       rated_battery_range_km, ideal_battery_range_km
FROM (
  (SELECT 'teslamate.positions' AS source, car_id, date, battery_level,
          usable_battery_level, rated_battery_range_km, ideal_battery_range_km
   FROM positions
   WHERE car_id = $1 AND battery_level BETWEEN 0 AND 100 AND date <= $2
     AND (usable_battery_level IS NULL OR
          (usable_battery_level BETWEEN 0 AND 100 AND usable_battery_level <= battery_level + 2))
   ORDER BY date DESC, id DESC LIMIT 1)
  UNION ALL
  (SELECT 'teslamate.charges' AS source, cp.car_id, c.date, c.battery_level,
          c.usable_battery_level, c.rated_battery_range_km, c.ideal_battery_range_km
   FROM charges c JOIN charging_processes cp ON cp.id = c.charging_process_id
   WHERE cp.car_id = $1 AND c.battery_level BETWEEN 0 AND 100 AND c.date <= $2
     AND (c.usable_battery_level IS NULL OR
          (c.usable_battery_level BETWEEN 0 AND 100 AND c.usable_battery_level <= c.battery_level + 2))
   ORDER BY c.date DESC, c.id DESC LIMIT 1)
) AS samples
ORDER BY date DESC, source DESC
LIMIT 1`

type batteryDatabaseObservation struct {
	CarID              int       `json:"car_id"`
	Source             string    `json:"source"`
	ObservedAt         time.Time `json:"observed_at"`
	CheckedAt          time.Time `json:"checked_at"`
	BatteryLevel       int       `json:"battery_level"`
	UsableBatteryLevel *int      `json:"usable_battery_level"`
	RatedRangeKM       *float64  `json:"rated_battery_range_km"`
	IdealRangeKM       *float64  `json:"ideal_battery_range_km"`
}

func readBatteryDatabaseObservation(ctx context.Context, database *sql.DB, carID int, checkedAt time.Time) (*batteryDatabaseObservation, error) {
	if database == nil || carID <= 0 || checkedAt.IsZero() {
		return nil, errors.New("battery observation source unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, batteryObservationReadTimeout)
	defer cancel()
	var observation batteryDatabaseObservation
	var usable sql.NullInt64
	var rated, ideal sql.NullFloat64
	err := database.QueryRowContext(ctx, batteryObservationQuery, carID, checkedAt.UTC()).Scan(
		&observation.Source, &observation.CarID, &observation.ObservedAt, &observation.BatteryLevel, &usable, &rated, &ideal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read battery observation: %w", err)
	}
	if (observation.Source != "teslamate.positions" && observation.Source != "teslamate.charges") ||
		observation.CarID != carID || observation.ObservedAt.IsZero() || observation.ObservedAt.After(checkedAt) ||
		observation.BatteryLevel < 0 || observation.BatteryLevel > 100 {
		return nil, errors.New("invalid battery observation")
	}
	if usable.Valid {
		if usable.Int64 < 0 || usable.Int64 > 100 || usable.Int64 > int64(observation.BatteryLevel+2) {
			return nil, errors.New("invalid usable battery observation")
		}
		value := int(usable.Int64)
		observation.UsableBatteryLevel = &value
	}
	for _, value := range []sql.NullFloat64{rated, ideal} {
		if value.Valid && (math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) || value.Float64 < 0) {
			return nil, errors.New("invalid battery range observation")
		}
	}
	if rated.Valid {
		observation.RatedRangeKM = &rated.Float64
	}
	if ideal.Valid {
		observation.IdealRangeKM = &ideal.Float64
	}
	observation.ObservedAt = observation.ObservedAt.UTC()
	observation.CheckedAt = checkedAt.UTC()
	return &observation, nil
}
