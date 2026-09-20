# Tire-pressure history API (Companion 1.10.51)

`GET /api/v1/cars/{id}/tire-pressure-history` uses the same owner authentication
as existing Companion car routes. It reads existing TeslaMate `positions` only.
It does not poll/wake the car, subscribe to TPMS, retain new records, query a
weather service, or return coordinates. Availability requires a published and
installed Companion 1.10.51 or later, a supported source schema, and a compatible
My T build. Preparing this API does not add tire-history UI to older App builds.

## Request and capability

- `from` and `to`: required RFC3339 instants, normalized to UTC. The interval is
  `[from, to)`, positive and at most 31 × 24 hours. Local calendar/DST choices
  belong to the client; send explicit instants.
- `limit`: default 1000; integer 1–2000.
- `cursor`: optional opaque `next_cursor` from the preceding page. Keep exactly
  the same car/window for the entire traversal. Unknown/duplicate parameters
  are rejected.
- `/api/v1/capabilities` includes `tire_pressure_history_v1` only after a read-only
  schema check. The separate `tire_pressure_history` object reports `supported`,
  `status` (`available`, `unsupported_schema`, `unavailable`), `max_window_days`,
  `max_page_limit`, `pressure_unit` and `temperature_unit`. Old Companion servers
  omit the token; clients must show unsupported, not an empty completed history.

## Response

`data` contains `car_id`, `from`, `to`, `points`, `returned_count`, `has_more`,
and nullable `next_cursor`. `points` are ordered newest first by
`(recorded_at, position_id)`, including multiple records at the same timestamp
and repeated identical pressure readings. An existing car with no tire records
returns `points: []`, `returned_count: 0`, `has_more: false`, `next_cursor: null`.
An unknown car returns HTTP 404 `{"error":"car_not_found"}`.

Each point contains:

| Field | Meaning |
| --- | --- |
| `position_id`, `car_id` | Original local TeslaMate record/vehicle IDs |
| `recorded_at` | Original `positions.date`, in UTC with source subsecond precision |
| `sensor_measured_at` | Always JSON null: this source has no TPMS measurement clock |
| `tpms_pressure_fl`, `tpms_pressure_fr`, `tpms_pressure_rl`, `tpms_pressure_rr` | Nullable source pressure values, in bar |
| `outside_temp` | Nullable recorded outside temperature, in degrees Celsius; not tire temperature |
| `speed` | Nullable recorded speed, km/h |
| `drive_id` | Nullable original drive association; absence does not prove parked state |

`meta` declares `pressure_unit: "bar"`, `temperature_unit: "C"`,
`speed_unit: "km/h"`, `timestamp_policy: "teslamate_position_recorded_at"`,
`sensor_measurement_timestamps_available: false`, `source_table: "positions"`,
`storage_mode: "teslamate_source_of_truth"`, retention, order, window semantics,
and response-generation time. `generated_at` is never a measurement timestamp.

Positions with all four source pressures null are omitted. A source zero stays
zero so it is not confused with a missing column/value; clients must not present
zero as a normal usable pressure or make a tire diagnosis from it. Nonfinite
values and negative pressure/speed become null; finite negative outside
temperatures remain valid. Missing wheels/context are never filled from other
records. Repeated values do not prove a new sensor measurement. Clients should
key/cache records by authenticated source identity + car ID + position ID, and
discard delayed responses when the selected source/car/window changes.

## Pagination, errors and compatibility

The query requests `limit + 1` rows and uses `(date, id)` keyset pagination, so
equal timestamps do not drop or repeat records. `next_cursor` is present only
when `has_more` is true. Cursors are signed with an ephemeral server-process
key and bind the car and exact window; they cannot cross independent servers.
After Companion restarts, a saved cursor is invalid. HTTP 400
`{"error":"invalid_cursor"}` instructs clients to discard that traversal's
partial page accumulation and restart the same window once without a cursor.
Do not loop retries or merge old/new traversals and claim complete coverage.

Other malformed requests return HTTP 400, missing/invalid auth returns 401,
and methods other than GET return 405. A missing selected source column/table
returns HTTP 501 `{"error":"tire_pressure_history_unsupported"}`. Database
outage, timeout or other read failure returns HTTP 503
`{"error":"tire_pressure_history_unavailable"}`; this is not empty history.
All endpoint responses include `Cache-Control: no-store`. SQL errors/secrets
are not returned. The total read deadline is 8 seconds, schema check at most
2 seconds, and queries use the existing read-only database connection.

Pagination does not lock or snapshot the TeslaMate database: newly backfilled
or amended historical rows may require a fresh complete traversal. No database
schema/index changes are made. Production coverage, actual query plans and
30-day performance need separate acceptance on the selected installation.
