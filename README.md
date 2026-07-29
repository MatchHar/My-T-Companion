# My T Companion

[English](README.md) · [简体中文](README.zh-Hans.md) · [繁體中文](README.zh-Hant.md)

> Current release: `1.10.0`. Native vehicle software push, charging Live
> Activities, and navigation Live Activities are optional and remain disabled
> until a compatible My T build supplies a secure relay pairing.
>
> **App Store My T 3.10** does not expose Companion screens. **TestFlight /
> pre-release My T 3.20+** supports this companion. Parking timeline, observed
> events, and trajectories work when the App can reach `/api/v1/capabilities`;
> push and Live Activities still need pairing. See the
> [My T feature availability](https://github.com/MatchHar/My-T-App/blob/main/docs/FEATURE_AVAILABILITY.md)
> notes.

**This companion is built specifically for the
[My T iPhone app — download it from the App Store](https://apps.apple.com/us/app/my-t/id6780299502).**
Install My T first if you are looking for the app that uses these enhanced
TeslaMate features.

For the complete My T product overview, TeslaMateAPI setup, connection
security, and troubleshooting, see the
[My T documentation repository](https://github.com/MatchHar/My-T-App).
For help with this component, see [SUPPORT.md](SUPPORT.md).

This optional, standalone service adds complete TeslaMate vehicle-state history,
retained parking-event observation (plug, charging, security, climate), and
reliable live-drive trajectories to My T.
Parking monitoring and live navigation use the same container, authentication,
installer, and update command. It reads the existing TeslaMate PostgreSQL
database without changing TeslaMate or creating tables.

TeslaMate remains the source of truth. Database-backed history is never
duplicated, deleted, or rewritten. Starting with 1.9.2, the companion also
stores a small local event log for genuine MQTT transitions that TeslaMate does
not preserve historically, such as a cable being connected before charging.
The first retained value after install or restart is baseline-only and never
becomes an event. Event timestamps mean “first observed by
TeslaMate/Companion,” not a promised physical-action timestamp. The event log
is kept long-term by default, with a 50,000-event capacity guard, in the
existing companion data volume. Temporary navigation and push-delivery state
expires independently. See [DATA_LIFECYCLE.md](DATA_LIFECYCLE.md).

Install this add-on **after TeslaMate is already deployed and working**. It is
not a replacement for TeslaMate or TeslaMateAPI.

## Why My T needs this companion

[My T](https://apps.apple.com/us/app/my-t/id6780299502) is an iPhone client for viewing
data stored on the user's own TeslaMate server. Standard TeslaMate and
TeslaMateAPI endpoints remain sufficient for most trips, charging sessions,
statistics, and current vehicle information.

Three My T experiences need more precise server-side data:

1. **Long-term My T Companion** needs the complete sequence of recorded
   `online`, `offline`, and `asleep` intervals, plus real battery/range samples
   on both sides of each transition. A phone cannot reconstruct events that
   happened while the app was closed.
2. **Live navigation during an active drive** needs TeslaMate's immutable first
   GPS point and incremental trajectory points. The position visible when My T
   opens must never be presented as the true trip start.
3. **Parking events** (plug/unplug, charging, Sentry, lock/doors, climate)
   need an always-running observer. iOS cannot reliably collect them while My T
   is suspended. Companion 1.9.2+ retains genuine MQTT transitions that
   TeslaMate does not keep as history.

This companion provides only those missing read-only capabilities. It keeps
TeslaMate as the source of truth and lets My T automatically enable enhanced
views when `/api/v1/capabilities` is available.

### Feature behavior

| My T feature | Without the companion | With the companion |
| --- | --- | --- |
| Trips, charging, statistics | Normal TeslaMate API data | Unchanged |
| Basic parking history | Available from ordinary trip/parking records | Unchanged |
| Sleep and wake timeline | May be incomplete; My T does not estimate missing events | Full TeslaMate-recorded state sequence |
| Parking battery/range change | Shown only when real observations already exist | Real transition-boundary observations within 30 minutes |
| Charging while parked | Existing charging sessions remain visible | Charging can be placed alongside the state timeline |
| Plug/security/climate events | No durable history while My T is closed | Long-term genuine MQTT transitions, with battery/range only when reported |
| Active-drive map | Real current position and speed only when the true route start is unavailable | Immutable true start plus incremental real trajectory |

The component is optional. My T detects it automatically; users do not add a
second server, account, or vehicle connection in the app.

## TeslaMate on a home or private LAN

The companion works with a TeslaMate database hosted on a LAN, but My T must
reach TeslaMateAPI and the companion through **one unified base URL**. My T
checks `/api/v1/capabilities` on the same server address already configured for
TeslaMate; it does not require or expose a second companion address.

| LAN setup | Result |
| --- | --- |
| TeslaMateAPI and companion are routed through the same Caddy/Nginx/Traefik address | Supported |
| My T connects directly to `http://LAN-IP:8081` with no reverse proxy | Basic TeslaMate features work, but the companion is not reachable |
| Access through a VPN, Tailscale, or another private network using one reverse-proxy address | Supported |

Port `8083` intentionally remains bound to `127.0.0.1` and must not be exposed
directly to the LAN or Internet. For a direct-`8081` installation, first add a
reverse proxy that sends the supplied companion routes to `127.0.0.1:8083` and all
ordinary TeslaMateAPI routes to `127.0.0.1:8081`, then use that proxy address in
My T. The supplied `Caddyfile.snippet` contains the required companion routes.

The installer can update a recognized system Caddy configuration automatically.
For Nginx, Traefik, containerized Caddy, or a custom LAN gateway, it deliberately
leaves the proxy unchanged and requires the administrator to add the routes.
Installation of the container alone does not make the enhanced features
reachable from My T.

## How data flows

```text
Vehicle → TeslaMate → PostgreSQL
                         │ read-only Docker network
                         ▼
               My T Companion
                         │ existing HTTPS/API authentication
                         ▼
                      My T App
```

- Tesla account authorization remains entirely inside TeslaMate.
- My T Companion never connects to Tesla or wakes the vehicle.
- My T does not send vehicle history through a developer-owned cloud service.
- Data travels between the user's own VPS and iPhone through the user's
  existing secured API hostname or private network.

## What deployment creates

| Item | Created or changed? | Purpose |
| --- | --- | --- |
| A standalone Docker service and container | Yes | Runs the read-only companion API |
| A loopback listener on `127.0.0.1:8083` | Yes | Keeps the service behind the existing protected reverse proxy |
| Companion reverse-proxy routes | Yes | Makes capability, parking-state/event, current-drive, and notification endpoints available through the My T base URL |
| Installer configuration and recovery backups | Yes | Supports repeatable updates, rollback, and uninstall |
| A new database or TeslaMate table | No | TeslaMate PostgreSQL remains the only source of truth |
| A duplicate vehicle-history store | No | The companion queries data only when My T requests it |
| TeslaMate data changes or vehicle commands | No | Database sessions are read-only and the service never connects to Tesla |

The service reads only the TeslaMate `states`, `drives`, and `positions` data
needed for its endpoints. It returns derived JSON state intervals, nearby real
battery/range observations, the current drive's immutable first point, and
incremental trajectory points. It does not run a background collector or define
its own retention period; available history follows the TeslaMate database.

## Capability by version

| Companion version | Capability added |
| --- | --- |
| 1.0.0 | Read-only parking state-history endpoint |
| 1.1.0 | Existing TeslaMate API authentication boundary |
| 1.2.0 | Timestamped, freshness-limited battery and rated-range observations |
| 1.3.0 | Immutable current-drive start and incremental trajectory paging |
| 1.4.0 | Capability discovery, hardened container/database access, safe install and uninstall |
| 1.4.1 | Checksummed release updates, rollback backups, unified-route verification, and broader LAN/proxy guidance |
| 1.5.0 | MQTT software-update detection, persistent deduplication, signed relay delivery, and authenticated status |
| 1.5.1 | Patched MQTT and Go networking dependencies; no API or deployment changes |
| 1.6.1 | Automatic charging Live Activities with true percentage/range, power, and completion updates |
| 1.7.0 | Proactive destination-navigation Live Activities with verified drive progress |
| 1.7.1 | Full-snapshot token recovery, immediate catch-up, and trailing navigation updates |

## Navigation Live Activities

Version 1.7.0 observes TeslaMate's genuine MQTT active route and driving state.
A compatible My T build can automatically start, update, and end a destination
card on the Lock Screen and Dynamic Island while the App is not open.

The card receives destination, remaining distance/time, estimated arrival,
vehicle-reported arrival battery, and progress verified against the current
TeslaMate drive. The relay does not receive coordinates, trajectory, VIN,
TeslaMate credentials, or vehicle history. Missing values are omitted rather
than estimated.

Without this companion, My T's ordinary in-App destination card, live vehicle
position, and speed continue to work. The genuine route start, traveled
trajectory, complete-route progress, and proactive Live Activity require a
paired companion.

Authenticated status:

```text
GET /api/v1/notifications/navigation-live-activity/status
```

## Charging Live Activities

Version 1.6.1 observes genuine TeslaMate MQTT charging state, battery
percentage, rated range, charge limit, power, and remaining time. A compatible
My T build can therefore show and update a Lock Screen/Dynamic Island charging
card even when the App is not open.

Routine updates are coalesced to a minimum 45-second interval; charging at
50 kW or above uses a 15-second interval. The signed
payload contains only the fields required by the card. It excludes VIN,
location, routes, TeslaMate credentials, and kWh. Range gain is calculated only
when genuine TeslaMate start/current `rated_battery_range_km` observations are
available; missing kilometers are omitted rather than estimated.

Authenticated status:

```text
GET /api/v1/notifications/charging-live-activity/status
```

## Native iPhone vehicle software notifications

Version 1.5.0 observes TeslaMate's genuine MQTT `update_available`,
`update_version`, and installed-version fields. It does not guess availability,
contact Tesla, or wake the vehicle.

Push is off by default. A compatible My T build will provide an opaque
installation ID, the official HTTPS relay URL, and a unique per-installation
secret. All three must be configured together.
Events are HMAC-SHA256 signed and deduplicated across container restarts.
The App writes the pairing through the user's existing authenticated connection:

```text
POST /api/v1/notifications/software-update/pair
```

For SSRF protection, the Companion accepts only My T's official relay URL.

Payloads never contain VIN, location, TeslaMate credentials, database
passwords, battery data, routes, or driving history. The APNs signing key is not
part of this repository and must never be copied to a user's VPS. Pairing is
automatic after the user enables notifications in My T; parking and live
navigation continue to work normally if push remains disabled.

The official relay is used because Apple Push Notification service does not
accept notifications directly from an arbitrary user VPS without an App-owned
APNs credential. It stores the APNs device token and an opaque installation
identifier needed for delivery, and receives only the privacy-minimal
software-update event documented above. Each VPS signs its own events; one
installation cannot send notifications for another installation.

Authenticated status:

```text
GET /api/v1/notifications/software-update/status
```

See [CHANGELOG.md](CHANGELOG.md) for complete changes and
[RELEASE_NOTES_1.10.0.md](RELEASE_NOTES_1.10.0.md) for the current release.

## Who should install it

Install it when all of the following are true:

- You use My T with a self-hosted TeslaMate connection.
- You want reliable long-term parking sleep/wake review or the complete
  active-drive trajectory.
- You control the TeslaMate Docker host and can run a `sudo` command.
- Your TeslaMate API is already protected by HTTPS/VPN and authentication.

You do not need it when using My T only with Tessie, when basic
trip/charge/statistics views are enough, or when you cannot administer the
TeslaMate server. Installing it does not improve TeslaMate collection quality;
it can only return observations TeslaMate actually stored.

## Verified release install

Installation uses a numbered GitHub Release rather than the mutable `main`
branch. With GitHub CLI installed:

```sh
version=1.10.0; workdir="$(mktemp -d)" && gh release download "v$version" -R MatchHar/My-T-Companion -D "$workdir" && (cd "$workdir" && sha256sum -c "my-t-companion-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-companion-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-companion-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

Without GitHub CLI:

```sh
version=1.10.0; workdir="$(mktemp -d)" && base="https://github.com/MatchHar/My-T-Companion/releases/download/v$version" && curl -fL "$base/my-t-companion-$version.tar.gz" -o "$workdir/my-t-companion-$version.tar.gz" && curl -fL "$base/my-t-companion-$version.tar.gz.sha256" -o "$workdir/my-t-companion-$version.tar.gz.sha256" && (cd "$workdir" && sha256sum -c "my-t-companion-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-companion-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-companion-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

Full success is reported only after both the local service and the unified My T
proxy route are verified. Manual Nginx, Traefik, and containerized-proxy setups
must add and verify the supplied routes.

The installer keeps My T connection setup unchanged. My T Companion reuses the
authentication already accepted by the existing TeslaMate API reverse proxy,
including Bearer token, Basic authentication, `X-API-Token`, and Cloudflare
Access service-token headers. There is no second credential to enter in My T.
The installer detects the existing API hostname and uses its normal protected
`/api/ping` route to validate requests. Future updates use the installed,
checksummed updater described below.

The command is interactive only when the server needs `sudo` authentication.
It detects the TeslaMate database container/network, reuses the existing
database and API credentials, and creates backups before proxy changes.

The installer automatically edits a system Caddy configuration when it can
identify the existing protected API route. Nginx, Traefik, containerized Caddy,
and custom TeslaMate layouts require the supplied routes to be added manually.
The installer stops with an actionable error instead of guessing.

## What it provides

- Real `online`, `offline`, `asleep`, and other state intervals recorded by
  TeslaMate.
- Real battery percentage and rated-range observations immediately before a
  state begins and after it ends.
- Boundary telemetry includes its real observation timestamp and is returned
  only when sampled within 30 minutes of the state transition. Older values are
  left unknown rather than being presented as sleep/wake consumption.
- A capability endpoint so My T can distinguish “not deployed” from “no events”.
- No estimated wake events or estimated battery consumption.
- PostgreSQL sessions are forced into read-only transaction mode, even though
  the existing TeslaMate database credential is reused.
- The current TeslaMate drive ID, its immutable earliest real GPS point, and
  timestamped real trajectory points.
- Incremental trajectory paging with `afterPointId`, so a phone can resume the
  same drive after reopening without repeatedly downloading the full route.
- Explicit `waiting_for_positions` when TeslaMate has opened a drive but has
  not stored a valid point yet. The service never substitutes the phone-open
  vehicle location as the trip start.

## Endpoints

- `GET /api/v1/capabilities`
- `GET /api/v1/cars/{car_id}/states?startDate=...&endDate=...`
- `GET /api/v1/cars/{car_id}/parking-events?startDate=...&endDate=...`
- `GET /api/v1/cars/{car_id}/navigation/current-drive?afterPointId=0&limit=5000`
- `GET /api/healthz`

All data and capability endpoints require the same authentication used by the
TeslaMate API connection. `/api/healthz` contains no vehicle data and is
available only on the loopback-bound service port.

## Deployment

### Prerequisites

- A working Docker Compose TeslaMate installation.
- The TeslaMate PostgreSQL service is named `database`. If yours uses a
  different service name, change `DATABASE_HOST`.
- The TeslaMate database password is available as `DATABASE_PASS`.
- My T connects to the API with a bearer token. Set the same value as
  `MY_T_API_TOKEN`; use a long random value and never commit it.
- A reverse proxy already protects the TeslaMate API with HTTPS.

### Install after TeslaMate

1. Copy this directory to `/opt/teslamate/my-t-companion`.
2. Add these non-secret values to `/opt/teslamate/.env`:

   ```dotenv
   # Reuse the existing DATABASE_PASS used by TeslaMate.
   MY_T_API_TOKEN=replace-with-a-long-random-token
   TZ=UTC
   ```

3. Merge `docker-compose.snippet.yml` under `services:` in TeslaMate's
   `docker-compose.override.yml`.
4. Route `/api/v1/cars/{id}/states`,
   `/api/v1/cars/{id}/navigation/current-drive`, and
   `/api/v1/capabilities` to local port `8083`, before the general TeslaMate API
   route. A ready-to-copy Caddy example is included in `Caddyfile.snippet`.
5. Validate and start:

   ```sh
   cd /opt/teslamate
   docker compose config -q
   docker compose up -d --build mycarmate-states-api
   ```

Keep port `8083` bound to `127.0.0.1`. Do not expose it directly to the public
Internet.

The installed container runs as an unprivileged user with a read-only root
filesystem, all Linux capabilities removed, `no-new-privileges`, and an
authenticated reverse-proxy boundary.

### Verify

```sh
curl --fail http://127.0.0.1:8083/api/healthz
curl --fail \
  -H "Authorization: Bearer ${MY_T_API_TOKEN}" \
  http://127.0.0.1:8083/api/v1/capabilities
```

The first response should report `OK`. The second should include
`parking_state_history`, `state_boundary_battery`, and
`state_boundary_rated_range`, and `current_drive_trajectory`.

### Update

The permanent command below follows GitHub's **latest stable Release** (drafts
and prereleases are excluded), downloads that numbered archive, verifies its
SHA-256 manifest, and backs up the existing installation before applying it:

```sh
sudo /opt/my-t-companion/update.sh
```

My T may instead show a version-pinned command such as
`sudo MY_T_VERSION=1.10.0 /opt/my-t-companion/update.sh`. That is intentional:
the App pins the newest Companion version verified with that App build, while
the permanent command is for administrators who explicitly want the newest
stable server release.

The service has no private SQL database or migration. Its bounded local state
volume keeps parking observations and push-delivery state; updating it does
not alter TeslaMate data.

### Remove or roll back

For installer-managed deployments:

```sh
sudo /opt/my-t-companion/uninstall.sh
```

The command removes the standalone container and installer-owned Caddy routes,
but preserves `/opt/my-t-companion` for recovery. Manually configured
custom reverse proxies must have the supplied companion routes removed manually.
TeslaMate continues to operate normally because this add-on is independent.

## Supported environments

See [COMPATIBILITY.md](COMPATIBILITY.md) for the tested release matrix. The
installer intentionally fails closed on unknown layouts. A manual Compose and
reverse-proxy deployment remains available for advanced installations.

## Security

Review [SECURITY.md](SECURITY.md) before exposing the API through a public
hostname. Never include `.env`, database passwords, API tokens, Cloudflare
service tokens, or private keys in an issue.

## Data meaning

For an `offline` interval, `start_telemetry` is the last observation at or
before sleep and `end_telemetry` is the first observation when TeslaMate sees
the car again. Their difference is therefore an observed parking consumption,
not a calculated estimate. An ongoing sleep has no end observation until the
vehicle wakes.

## App behavior when not installed

My T checks `/api/v1/capabilities`. If the add-on is unavailable, the app keeps
the normal TeslaMate features working and explains that complete long-term
parking wake history requires this optional VPS deployment. It does not invent
missing events or battery consumption.

The live-navigation module follows the same rule. Without this capability, My T
can fall back to the standard TeslaMate API, but it must label a route as
partial when the real first point is unavailable. It must not promote the
vehicle location observed when the app opens into a “true start”.

## Scope and independence

This is an independent companion project designed for My T. It is not
affiliated with or endorsed by Tesla, Inc., the TeslaMate maintainers, or the
TeslaMateAPI maintainers. It does not contain Tesla credentials, issue vehicle
commands, wake a vehicle, or replace the official TeslaMate deployment.

## Frequently asked questions

### Does it change or copy my TeslaMate history?

No. Queries run in PostgreSQL read-only transaction mode. Companion has no SQL
database or migrations. Its background MQTT observer writes only the bounded
local event state described in [DATA_LIFECYCLE.md](DATA_LIFECYCLE.md).

### Can it recover old wake events?

It can show old events that still exist in TeslaMate's `states` table. It
cannot recover observations that TeslaMate never recorded or that were removed
by the user's retention policy.

### Will it wake the car or increase battery drain?

No. It reads the database only and never calls Tesla vehicle-control APIs.

### Must I change My T connection settings?

Normally no. The installer reuses the existing TeslaMate API hostname and
authentication. My T detects the capability endpoint automatically.

### Can I uninstall it without damaging TeslaMate?

Yes. The companion is a separate Compose project. The uninstall script removes
its container and installer-managed proxy routes while preserving TeslaMate and
its database.
