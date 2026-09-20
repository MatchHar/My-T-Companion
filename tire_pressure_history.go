package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	tireHistoryCapability   = "tire_pressure_history_v1"
	tireHistoryMaxWindow    = 31 * 24 * time.Hour
	tireHistoryDefaultLimit = 1000
	tireHistoryMaxLimit     = 2000
	tireHistoryQueryTimeout = 8 * time.Second
)

var (
	tireHistoryPath             = regexp.MustCompile(`^/api/v1/cars/(\d+)/tire-pressure-history$`)
	errTireHistoryUnsupported   = errors.New("tire_pressure_history_unsupported")
	errTireHistoryCarNotFound   = errors.New("car_not_found")
	errTireHistoryInvalidCursor = errors.New("invalid_cursor")
	// Cursors belong to this server process. Restarting requires restarting the
	// history request; a cursor from a different server is never accepted, even
	// if both installations use the same API token and local car/position IDs.
	tireHistoryCursorKey = func() []byte {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("unable to initialize history cursor signing")
		}
		return key
	}()
)

// These are database record times, not individual TPMS measurement times.
// No coordinates, retained MQTT observations, or current-state backfill belong
// in this history. Every point keeps the original TeslaMate position identity.
type tirePressurePoint struct {
	PositionID       int64      `json:"position_id"`
	CarID            int        `json:"car_id"`
	RecordedAt       time.Time  `json:"recorded_at"`
	SensorMeasuredAt *time.Time `json:"sensor_measured_at"`
	PressureFL       *float64   `json:"tpms_pressure_fl"`
	PressureFR       *float64   `json:"tpms_pressure_fr"`
	PressureRL       *float64   `json:"tpms_pressure_rl"`
	PressureRR       *float64   `json:"tpms_pressure_rr"`
	OutsideTemp      *float64   `json:"outside_temp"`
	Speed            *float64   `json:"speed"`
	DriveID          *int64     `json:"drive_id"`
}

type tireHistoryCursor struct {
	Version int       `json:"v"`
	CarID   int       `json:"car"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Date    time.Time `json:"date"`
	ID      int64     `json:"id"`
}

type tireHistoryRequest struct {
	CarID    int
	From, To time.Time
	Limit    int
	Cursor   *tireHistoryCursor
}

type tireHistoryData struct {
	CarID         int                 `json:"car_id"`
	From          time.Time           `json:"from"`
	To            time.Time           `json:"to"`
	Points        []tirePressurePoint `json:"points"`
	ReturnedCount int                 `json:"returned_count"`
	HasMore       bool                `json:"has_more"`
	NextCursor    *string             `json:"next_cursor"`
}

const tireHistoryColumns = `id, car_id, date, tpms_pressure_fl, tpms_pressure_fr,
	tpms_pressure_rl, tpms_pressure_rr, outside_temp, speed, drive_id`

const tireHistorySchemaQuery = `SELECT ` + tireHistoryColumns + ` FROM positions LIMIT 0`

// A bounded car/date predicate can use TeslaMate's existing car/date indexes.
// Do not create or alter indexes in the user's TeslaMate database. Actual
// production query plans/coverage need separate read-only acceptance.
const tireHistoryQuery = `SELECT ` + tireHistoryColumns + `
	FROM positions
	WHERE car_id = $1 AND date >= $2 AND date < $3
	  AND (tpms_pressure_fl IS NOT NULL OR tpms_pressure_fr IS NOT NULL
	       OR tpms_pressure_rl IS NOT NULL OR tpms_pressure_rr IS NOT NULL)`

const tireHistoryCarQuery = `SELECT EXISTS (SELECT 1 FROM cars WHERE id = $1)`

func parseTireHistoryRequest(car string, values url.Values, key []byte) (tireHistoryRequest, error) {
	q := tireHistoryRequest{Limit: tireHistoryDefaultLimit}
	carID, err := strconv.ParseInt(car, 10, 32)
	if err != nil || carID <= 0 {
		return q, errors.New("Invalid car id")
	}
	q.CarID = int(carID)
	for name, entries := range values {
		if name != "from" && name != "to" && name != "limit" && name != "cursor" {
			return q, errors.New("Unknown query parameter")
		}
		if len(entries) != 1 || entries[0] == "" {
			return q, errors.New("Invalid query parameter")
		}
	}
	q.From, err = time.Parse(time.RFC3339Nano, values.Get("from"))
	if err != nil {
		return q, errors.New("Invalid from; expected RFC3339")
	}
	q.To, err = time.Parse(time.RFC3339Nano, values.Get("to"))
	if err != nil {
		return q, errors.New("Invalid to; expected RFC3339")
	}
	q.From, q.To = q.From.UTC(), q.To.UTC()
	if !q.To.After(q.From) || q.To.Sub(q.From) > tireHistoryMaxWindow {
		return q, errors.New("Window must be positive and at most 31 days")
	}
	if raw := values.Get("limit"); raw != "" {
		q.Limit, err = strconv.Atoi(raw)
		if err != nil || q.Limit < 1 || q.Limit > tireHistoryMaxLimit {
			return q, errors.New("limit must be between 1 and 2000")
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeTireHistoryCursor(raw, key)
		if err != nil || cursor.Version != 1 || cursor.CarID != q.CarID || !cursor.From.Equal(q.From) || !cursor.To.Equal(q.To) || cursor.ID <= 0 || cursor.Date.Before(q.From) || !cursor.Date.Before(q.To) {
			return q, errTireHistoryInvalidCursor
		}
		q.Cursor = &cursor
	}
	return q, nil
}

func encodeTireHistoryCursor(cursor tireHistoryCursor, key []byte) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeTireHistoryCursor(raw string, key []byte) (tireHistoryCursor, error) {
	var cursor tireHistoryCursor
	if len(raw) > 1024 {
		return cursor, errors.New("oversized cursor")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return cursor, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, errors.New("cursor source/signature mismatch")
	}
	err = json.Unmarshal(payload, &cursor)
	return cursor, err
}

func tireHistorySchemaError(err error) error {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && (pgErr.Code == "42703" || pgErr.Code == "42P01") {
		return errTireHistoryUnsupported
	}
	return err
}

func checkTireHistorySchema(ctx context.Context, database *sql.DB) error {
	if database == nil {
		return errors.New("database unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := database.QueryContext(ctx, tireHistorySchemaQuery)
	if err != nil {
		return tireHistorySchemaError(err)
	}
	defer rows.Close()
	for rows.Next() { // LIMIT 0; consume the result to observe deferred errors.
	}
	return rows.Err()
}

func addTireHistoryCapability(ctx context.Context, payload map[string]any) {
	if payload == nil {
		return
	}
	err := checkTireHistorySchema(ctx, db)
	status := "available"
	if err != nil {
		status = "unavailable"
		if errors.Is(err, errTireHistoryUnsupported) {
			status = "unsupported_schema"
		}
	} else {
		if capabilities, ok := payload["capabilities"].([]string); ok || payload["capabilities"] == nil {
			payload["capabilities"] = append(capabilities, tireHistoryCapability)
		}
	}
	payload["tire_pressure_history"] = map[string]any{
		"supported": err == nil, "status": status, "max_window_days": 31,
		"max_page_limit": tireHistoryMaxLimit, "pressure_unit": "bar", "temperature_unit": "C",
	}
}

func handleTirePressureHistory(w http.ResponseWriter, r *http.Request, car string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid query"})
		return
	}
	q, err := parseTireHistoryRequest(car, values, tireHistoryCursorKey)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	data, err := fetchTireHistory(r.Context(), db, q, tireHistoryCursorKey)
	if err != nil {
		status, code := http.StatusServiceUnavailable, "tire_pressure_history_unavailable"
		if errors.Is(err, errTireHistoryUnsupported) {
			status, code = http.StatusNotImplemented, "tire_pressure_history_unsupported"
		}
		if errors.Is(err, errTireHistoryCarNotFound) {
			status, code = http.StatusNotFound, "car_not_found"
		}
		writeJSON(w, status, map[string]string{"error": code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{
			"generated_at": time.Now().UTC().Format(time.RFC3339Nano),
			"storage_mode": "teslamate_source_of_truth", "retention": "follows_teslamate_database",
			"source_table": "positions", "timestamp_policy": "teslamate_position_recorded_at",
			"pressure_unit": "bar", "temperature_unit": "C", "speed_unit": "km/h",
			"order": "recorded_at_desc_position_id_desc", "window": "from_inclusive_to_exclusive",
			"sensor_measurement_timestamps_available": false,
		},
	})
}

func fetchTireHistory(ctx context.Context, database *sql.DB, q tireHistoryRequest, key []byte) (tireHistoryData, error) {
	data := tireHistoryData{CarID: q.CarID, From: q.From, To: q.To, Points: []tirePressurePoint{}}
	ctx, cancel := context.WithTimeout(ctx, tireHistoryQueryTimeout)
	defer cancel()
	if err := checkTireHistorySchema(ctx, database); err != nil {
		return data, err
	}
	var exists bool
	if err := database.QueryRowContext(ctx, tireHistoryCarQuery, q.CarID).Scan(&exists); err != nil {
		return data, tireHistorySchemaError(err)
	}
	if !exists {
		return data, errTireHistoryCarNotFound
	}
	query := tireHistoryQuery
	args := []any{q.CarID, q.From, q.To}
	if q.Cursor != nil {
		query += ` AND (date, id) < ($4, $5)`
		args = append(args, q.Cursor.Date.UTC(), q.Cursor.ID)
	}
	args = append(args, q.Limit+1)
	query += fmt.Sprintf(` ORDER BY date DESC, id DESC LIMIT $%d`, len(args))
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return data, tireHistorySchemaError(err)
	}
	defer rows.Close()
	for rows.Next() {
		point, err := scanTirePressurePoint(rows)
		if err != nil {
			return data, err
		}
		if len(data.Points) == q.Limit {
			data.HasMore = true
			break
		}
		data.Points = append(data.Points, point)
	}
	if err := rows.Err(); err != nil {
		return data, err
	}
	data.ReturnedCount = len(data.Points)
	if data.HasMore {
		last := data.Points[len(data.Points)-1]
		next, err := encodeTireHistoryCursor(tireHistoryCursor{Version: 1, CarID: q.CarID, From: q.From, To: q.To, Date: last.RecordedAt, ID: last.PositionID}, key)
		if err != nil {
			return data, err
		}
		data.NextCursor = &next
	}
	return data, nil
}

func scanTirePressurePoint(row rowScanner) (tirePressurePoint, error) {
	var point tirePressurePoint
	var fl, fr, rl, rr, outside, speed sql.NullFloat64
	var drive sql.NullInt64
	if err := row.Scan(&point.PositionID, &point.CarID, &point.RecordedAt, &fl, &fr, &rl, &rr, &outside, &speed, &drive); err != nil {
		return point, err
	}
	point.RecordedAt = point.RecordedAt.UTC()
	point.PressureFL, point.PressureFR = finiteTireHistoryValue(fl, true), finiteTireHistoryValue(fr, true)
	point.PressureRL, point.PressureRR = finiteTireHistoryValue(rl, true), finiteTireHistoryValue(rr, true)
	point.OutsideTemp, point.Speed = finiteTireHistoryValue(outside, false), finiteTireHistoryValue(speed, true)
	if drive.Valid && drive.Int64 > 0 {
		point.DriveID = &drive.Int64
	}
	return point, nil
}

func finiteTireHistoryValue(value sql.NullFloat64, nonnegative bool) *float64 {
	if !value.Valid || math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) || (nonnegative && value.Float64 < 0) {
		return nil
	}
	return &value.Float64
}
