# Private footprint parking capability (introduced in 1.10.65)

`road_parking_v1` is advertised when the database source is configured.
Owner-only GET `/api/v1/cars/{car}/road-parking` (canonical owner proxy:
`/api/companion/v1/cars/{car}/road-parking`). No vehicle requests or commands.

Optional RFC3339 `from` / `to` use a half-open interval. SQL windows over all
car-bound drives before filtering, so adjacent boundaries outside the interval
remain available. Saved endpoint positions are checked against the following
trip start within100m. Stops under5minutes are excluded. An open stop is not
promoted before5minutes. Charging is not a second stay. Arrival counts use the
actual arrival instant; known duration is the intersection with the interval.
Absent departure or mismatched positions never acquire a fabricated duration.
This is recorded drive-boundary evidence, not proof of uninterrupted telemetry.

Summary returns bounded places, per-car stable IDs, counts, known/unknown stays,
exact representative position and last observed boundaries. A fixed35m seed
prevents chain merging; address/geofence identity also separates adjacent places.
Place IDs are scoped to a query: historical corrections or changed range can
replace an anchor, requiring a fresh summary. Source/car/range caches must be
isolated. More than20,000 rows sets `complete:false`. Queries have an8second
budget. A missing schema is501, missing car404, authentication401.

`place=ID` returns one place and its individual stays (default50, limit1...200).
Every stay carries its actual endpoint position. `next_cursor` is HMAC signed,
bound to car/range/place/data revision. Changes invalidate pagination; restart
from the summary instead of appending stale data. Responses are `no-store`.
No parking values are supplied to My T share images.

Coverage requests can opt into `passport_positions=1`. An additive, versioned
`timestamped_gps_samples_v1` packet contains actual saved positions and dates,
first per200m cell per drive, at most50,000. A truncated packet explicitly sets
`position_samples_complete:false`. Older clients do not request or pay for it.
My T only qualifies sampled positions strictly inside verified municipalities;
coverage-cell intersection alone never produces a visit or first date. Missing
or partial evidence keeps the original endpoint collection.

Local validation uses Go unit tests and an isolated PGlite/Postgres WASM wire
fixture with synthetic data. This verifies SQL semantics and HTTP paging through
lib/pq; it is not a production PostgreSQL query-plan/performance measurement.
No live VM102 database was read or changed.
