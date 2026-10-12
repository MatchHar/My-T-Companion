# Compatibility and release validation

English · [简体中文](COMPATIBILITY.zh-Hans.md) · [繁體中文](COMPATIBILITY.zh-Hant.md)

Version `1.5.0` added optional TeslaMate MQTT software-update observation and
signed relay delivery. Version `1.7.1` adds destination-navigation Live Activity delivery and retains charging Live Activity delivery plus patched build dependencies
without changing APIs or deployment. Parking and navigation remain usable
without pairing.

The 1.10.47 release is compatible with TeslaMate 4.2.0 and the previous 4.1.1
stable line, together with TeslaMateAPI 1.25.0. Per-vehicle push overrides use
TeslaMate car IDs already present in notification events and require no
database migration, Tesla token change, or vehicle wake. Existing installations
start with the all-vehicle behavior from 1.10.36. Requests that omit the new
optional field preserve any stored overrides instead of erasing settings an
older App cannot display.

Version 1.10.47 repairs Friend Together vehicle lookup after HostBox enablement.
GET `/status` already succeeded; POST `/invitations` failed because the extra
read-only pool used a URL DSN that did not reach TeslaMate Postgres. Ordinary
upgrades keep parking, navigation, pairing, and notifications working without
extra configuration. Enabling Friend Together still requires
`FRIEND_TOGETHER_ENABLED=true`, a dedicated HTTPS guest origin for `/friend/v1/*`,
and the owner `/api/v1/friend-together/*` matcher. It requires no database,
pairing, Tesla token, notification-preference, or stored-history migration and
remains compatible with existing My T clients.

## Tire-pressure history in 1.10.51

The optional tire-history API reads existing TeslaMate `positions` records. Its
required columns were checked on TeslaMate 4.2.0 / TeslaMateAPI 1.25.0 / PostgreSQL
18; this is not a claim that every version in the wider matrix below was tested
for this feature. Capability discovery checks the actual installed schema. If a
required column is missing, the feature reports unsupported instead of inventing
data or modifying the database. A database failure is not empty history.

The My T tire-history screen remains unavailable until a compatible App build
ships. Existing clients and their parking, navigation, charging, pairing and
notification behavior do not require an update. Custom proxies must forward
`/api/v1/cars/{id}/tire-pressure-history` through the existing owner-authenticated
Companion route. The installer and supplied Caddy/Nginx examples include it.

History is limited by the source's retained records; record time is not a TPMS
sensor-measurement time, and outside temperature is not tire temperature. No
new sampling, wake request, external weather service or schema/index migration
is introduced. See the [API contract](docs/tire-pressure-history.md) for the
31-day limit, pagination, units, missing values and restart-safe client behavior.

Upgrade with the existing verified-release installer/updater or HostBox after
its separately signed catalog is promoted. Keep the previous verified archive
and updater backup for rollback; reverting Companion does not remove TeslaMate
history. Release publication, live endpoint acceptance and signed-catalog
promotion are separate gates, not implied by local tests.

## Companion 1.10.54 TeslaMate 4.3 compatibility

These features are introduced in Companion `1.10.54`; existing self-hosted
Companion 1.10.53 services change only after an upgrade. Dynamic fleet/baseline recovery, notification
policy and per-field receipt/ETA limitations are described in the
[1.10.54 README section](README.md#companion-11054-teslamate-43-hardening)
and [data lifecycle](DATA_LIFECYCLE.md#companion-11054-observation-lifecycle).
No live new-car, forced MQTT reconnect or APNs acceptance is implied.

`MQTT_NAMESPACE` is exact: empty retains `teslamate/cars/{id}/{field}`; otherwise
use `teslamate/{namespace}/cars/{id}/{field}`. Its 0–128 characters must all be
ASCII `[A-Za-z0-9_.-]`. No trimming/normalization occurs. The runtime and installer
reject whitespace, slashes, wildcards, quotes and `$`; the restricted alphabet
prevents dotenv/Compose expansion from changing source isolation.

The [official 4.3.0 release](https://github.com/teslamate-org/teslamate/releases/tag/v4.3.0)
and [4.2.0 → 4.3.0 source comparison](https://github.com/teslamate-org/teslamate/compare/v4.2.0...v4.3.0)
were audited. The relevant historical table migrations did not change between
these tags. This does not make TeslaMate's database a versioned public API or
constitute live acceptance of every deployment in the matrix. Companion's
`states`, `current_drive`, `friend_source` and `mqtt_fleet` SQL capabilities now depend on bounded, read-only
probes of the installed columns and operators, not a TeslaMate version allowlist.
The existing independent tire-history probe remains in place.

Authenticated capabilities include these four groups under `sql_features`.
Each has `status` (`available`,
`unsupported_schema`, or `unknown`), `supported` (`true`, `false`, or `null`),
and a sanitized `error` code. Only the affected capability strings are removed
on failure. State/current-drive requests with missing relations/columns or incompatible operators yield HTTP 501
`states_unsupported` / `current_drive_unsupported`; connection, permission and
timeout failures yield HTTP 503 `states_unavailable` / `current_drive_unavailable`.
Both responses also identify `feature`. Unknown car IDs yield HTTP 404
`car_not_found`; an existing car with no history/current drive remains a valid
empty result. Raw SQL errors and credentials are never returned to clients.

Friend source probing uses its actual independent pool and the ownership,
latest-position and trajectory query shapes; when disabled or without a pool,
its schema support is unknown rather than claimed available. `mqtt_fleet`
checks the actual `cars.settings_id → car_settings.id` join and boolean
`car_settings.enabled` logging flag. Without a usable fleet schema, active
MQTT-dependent claims and `friend_together_v1` are omitted; SQL state/drive
history, stored navigation history and push-preference configuration remain
independent. A schema probe is not a claim of fresh vehicle telemetry or complete
Friend service readiness. The four feature probes run concurrently, with a
two-second per-feature bound (also respecting earlier request cancellation).

The main read-only SQL pool is independently bounded; Friend Together keeps its
separate pool. Both use UTC sessions so timestamp-without-zone history is not
reinterpreted by a server-local timezone; App `TZ` formats responses only.
Invalid explicit settings stop startup without echoing their
values. Durations use Go notation such as `5m` or `30m`.

| Environment setting | Default | Valid range |
| --- | --- | --- |
| `DATABASE_MAX_OPEN_CONNS` | `8` | 1–64 |
| `DATABASE_MAX_IDLE_CONNS` | `2` | 0–64 and no greater than max-open |
| `DATABASE_CONN_MAX_IDLE_TIME` | `5m` | 1s–24h |
| `DATABASE_CONN_MAX_LIFETIME` | `30m` | 1s–24h |

Before writing `.env`, the installer requires canonical decimal counts and
ASCII duration components using `ns/us/ms/s/m/h` (for example `1h30m` or `1.5s`).
Duration components allow up to 14 integer digits and up to nine fractional
digits, bounded by the unit's nanosecond precision. Signs, leading zeros,
whitespace and shell characters are rejected, not normalized; direct runtime
configuration still accepts Go duration syntax within the ranges above.

Authenticated `database_pool` diagnostics expose the configured max-open bound,
current usage and cumulative connection-wait counts/time. These limits follow
the [Go SQL connection-pool controls](https://go.dev/doc/database/manage-connections);
they do not alter PostgreSQL's server limits, schema or indexes.

State history preserves its existing complete response when neither `limit`
nor `cursor` is supplied. Optional `limit=1..2000` enables keyset pagination
(default 1000 when only a cursor is supplied), advertised as
`state_history_keyset_pagination_v1`. `meta.state_history_page` reports `limit`,
`returned_count`, `has_more` and `next_cursor`. Continue with the same car,
`startDate` and `endDate` and the returned opaque cursor until `has_more=false`.
Ordering uses `(start_date,id)`, so equal state timestamps do not lose rows.
An overlapping interval keeps its original start, even for a 16-day or longer
parking session; there is no new date-window cap or silent record truncation.
The cursor is signed per process: after a restart/server switch, HTTP 400
`invalid_cursor` requires restarting the requested window and deduplicating it.
Pages are live reads, not a database snapshot; re-read the window when an open
interval changes. Source state and position times retain fractional seconds;
an old position is not relabeled as a current observation. Missing telemetry
remains null, not zero.

Local driver/HTTP tests validate pool limits, schema classification, cancellation,
nulls, fractional/old/equal timestamps, unknown cars and pagination. They do not
execute PostgreSQL query semantics. The separate optional test
`TestSQLLocalPostgresTemporalStatesAndPagination` uses only disposable TEMP
tables through the existing strictly loopback `MYT_FRIEND_LOCAL_POSTGRES_URL`
fixture (`myt_friend_fixture` database, explicit port). If that fixture is not
explicitly provisioned, the test reports a skip and actual local PostgreSQL
execution remains unverified in that local run; consult the matching release CI
results for separately executed synthetic fixtures. Neither local nor CI tests
prove production deployment, real-car behavior or APNs delivery. Companion does
not migrate TeslaMate tables/indexes, and publication does not deploy the service.

## Required baseline

- Linux host with Docker Engine and Docker Compose v2.
- An existing, healthy TeslaMate Docker Compose deployment.
- PostgreSQL reachable on the TeslaMate Docker network (default service name
  `database`; installer also probes `db` / `postgres` and container labels).
- `DATABASE_PASS` discoverable without requiring a TeslaMate `.env` file
  (compose config, container env, shell export, or optional `.env`).
- TeslaMate schema containing `cars`, `states`, `positions`, and `drives`.
- Existing API authentication using Bearer token, Basic authentication,
  `X-API-Token`, or Cloudflare Access service-token headers.
- Gateway routes covering parking, navigation, capabilities, and
  `/api/v1/notifications/*` (Live Activity status included).

## Release test matrix

Every stable release must record results for:

| Area | Required cases |
| --- | --- |
| Host | Ubuntu 22.04 and 24.04 on amd64 |
| Architecture | amd64 and arm64 image builds |
| TeslaMate | current stable plus previous stable |
| Proxy | system Caddy automatic install; Nginx/Traefik manual instructions |
| Auth | Bearer, Basic, X-API-Token, Cloudflare Access |
| Lifecycle | clean install, repeat install/update, rollback, uninstall |
| Parking | sleep/wake/sleep, open sleep, charging while parked, missing telemetry, cross-midnight |
| Navigation | no drive, waiting for first point, incremental points, paging, drive ID change |
| Failure | database unavailable, wrong token, public `/api/ping`, occupied port, unknown Compose layout |

## Known release-candidate limits

- Automatic reverse-proxy editing supports a system Caddy service only.
- Other proxy layouts must provide and verify `MY_T_BASE_URL`; an unverified
  local-only service is reported as incomplete rather than successful.
- The default TeslaMate directory is `/opt/teslamate`; override it with
  `TESLAMATE_DIR` for another layout.
- The default database Compose service is `database`.
- Legacy historical-state requests remain unpaginated for compatibility;
  clients should use bounded time windows and opt into the cursor pagination
  above for large histories. Query failures are errors, never partial success.
