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
- Historical state responses are not yet paginated; clients should request
  bounded parking-session time windows.
