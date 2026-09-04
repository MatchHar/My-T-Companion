# Compatibility and release validation

English · [简体中文](COMPATIBILITY.zh-Hans.md) · [繁體中文](COMPATIBILITY.zh-Hant.md)

Version `1.5.0` added optional TeslaMate MQTT software-update observation and
signed relay delivery. Version `1.7.1` adds destination-navigation Live Activity delivery and retains charging Live Activity delivery plus patched build dependencies
without changing APIs or deployment. Parking and navigation remain usable
without pairing.

The 1.10.43 release is compatible with TeslaMate 4.2.0 and the previous 4.1.1
stable line, together with TeslaMateAPI 1.25.0. Per-vehicle push overrides use
TeslaMate car IDs already present in notification events and require no
database migration, Tesla token change, or vehicle wake. Existing installations
start with the all-vehicle behavior from 1.10.36. Requests that omit the new
optional field preserve any stored overrides instead of erasing settings an
older App cannot display.

Version 1.10.43 changes only the internal parking-event persistence path. It
requires no database, API, pairing, proxy, Tesla token, or stored-data
migration and remains compatible with existing My T clients.

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
