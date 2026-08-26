# Changelog

## 1.10.31

- Report a privacy-minimal anonymous vehicle inventory to the official relay
  after push pairing, shortly after startup, and once daily.
- Derive one stable alias per selected TeslaMate car with HMAC-SHA-256 using a
  random Companion-local namespace. The namespace, raw car ID, vehicle name,
  VIN, server address, installation relationship, location, route, telemetry,
  software version, and notification content never enter this statistic.
- Count the same TeslaMate car once across multiple paired iPhones while keeping
  identical local car IDs on different Companion installations distinct.

## 1.10.30

- Treat the nearest TeslaMate drive start address as authoritative for
  destination-trip notifications, preventing an older home geofence from
  becoming the origin of every trip.
- Replay an already active charging or destination Live Activity to the one
  iPhone that has just enabled the feature, without duplicating ordinary trip
  alerts for other subscribers.
- Keep replay delivery installation-specific and add regression coverage for
  preference transitions, charging replay, navigation replay, and stale
  geofence handling.

## 1.10.29

- Report the live TeslaMate version from its internal settings page instead of
  trusting installation-time metadata that can become stale after an upgrade.
- Expose the version source and observation time so My T can distinguish a
  current probe from a fallback value.
- Make installation and updates derive `TESLAMATE_VERSION` from the running
  TeslaMate container before consulting an older saved value.
- Include the live-version probe in release completeness and container build
  checks, with regression tests for live and fallback paths.
- Let trusted deployment tools provide `MY_T_EXPECTED_SHA256`, so a release
  archive can be checked against a separately signed catalog digest in addition
  to its publisher checksum.
- End active navigation state older than 12 hours through the normal terminal
  event path, matching the documented lifecycle and dismissing orphaned Live
  Activities.
- Add tag-release tests, vulnerability scanning, reproducible source archives,
  checksums, and GitHub artifact provenance.

## 1.10.28

- Pin every software, charging, navigation, and lock-secure delivery to the
  official My T relay endpoint instead of trusting a persisted URL.
- Refuse HTTP redirects in relay clients so authorization material cannot be
  forwarded to a different origin.
- Keep navigation notification history allocation statically bounded, matching
  the existing runtime limit.
- Refresh the English, Simplified Chinese, and Traditional Chinese
  compatibility documentation without hard-coding the current My T App Store
  version.

## 1.10.27

- Add a bounded, permissioned, durable per-iPhone push outbox. A transient
  network/relay failure or pending ActivityKit token is retried without one
  successful subscriber hiding another subscriber's failure.
- Pause only the invalid installation on relay/APNs 404/410 and remove its
  queued events. Pausing or unpairing also removes that phone's retry rows.
- Make lock-secure preferences and status installation-specific; an unknown
  phone now receives `not_paired` instead of silently changing global state.
- Stop all push monitors when every subscriber is paused, not only when the
  subscriber table is empty.
- Correct PostgreSQL `connect_timeout` from 60,000 seconds to 10 seconds, add
  read-only/statement timeouts and request contexts, and move navigation
  enrichment queries outside the MQTT state mutex.
- Validate persisted relay URL/installation/secret values again at startup.
- Installer reliability fixes made after 1.10.26 are included in this release.

## 1.10.26

- Lock-screen Live Activities (charging + destination trip) are one subscriber
  flag pair. Destination start/arrival banners use a new `navigation_trip_alerts`
  flag and are not the lock-screen card.
- Capability: `navigation_trip_alerts`. Older My T builds ignore it.

## 1.10.25

- Docker image copies every `*.go` file so a new source (1.10.24
  `push_subscribers.go`) cannot be omitted from `go build`.
- Installer refuses to start the image build if the Dockerfile would drop a
  production `.go` file. CI and `build-release.sh` run the same check.
- Same features as 1.10.24 (per-iPhone push subscribers). 1.10.24 itself
  cannot be installed on a VPS.

## 1.10.24

- Push subscribers: one iPhone is one `installation_id`. Switching TeslaMate
  servers pauses this phone on the previous VPS instead of wiping everyone.
- Coming back to a server resumes the same row (not a new device).
- Each phone can choose software-update, lock-secure, charging lock-screen,
  and destination-trip Live Activity independently, plus which cars.
- Legacy `POST /pair` still upserts. `DELETE /pair` without an installation
  header is rejected when more than one phone is registered.

## 1.10.23

- MQTT discovery treats a Restarting / Exited Docker Mosquitto as missing.
- If HostBox left `MQTT_BROKER_URL=tcp://mosquitto:1883` but that container
  cannot start (config EACCES) and host `:1883` is listening, install uses
  `host.docker.internal` instead of killing TeslaMateAPI.
- TeslaMate charges and trips still work if Companion is off.

## 1.10.22

- TeslaMate 4.1: live companion-status overlay for per-window, sunroof, service mode, and software download/install percent.

## 1.10.21

- Keeps one navigation Live Activity when Tesla only renames the destination
  (`住家` ↔ street address) or has not published remaining distance yet.
- Collapses extra spaces in vehicle display names so lock-screen cards match
  the App (`Lily's  Car` → `Lily's Car`).
- Backward compatible with previous My T versions; no API or stored-data
  migration.

## 1.10.20

- Stops treating leftover TeslaMateAPI `:18081` as the public My T port during
  install or update (that made the edge and API share one port and 404 on
  `/api/v1/capabilities`).
- If the public-edge capabilities check fails, restores the compose backup and
  continues instead of aborting the whole upgrade.
- Tunnel / HostBox sidecar installs skip the public 8081 edge when loopback
  `:8083` already serves Companion capabilities.
- Backward compatible with previous My T versions; no API or stored-data
  migration.

## 1.10.19

- Ignores a stale `host.docker.internal` MQTT address during install or update
  when Mosquitto is actually a Docker Compose service and no broker listens on
  the host's port 1883.
- Prefers the live Docker Mosquitto service and shared TeslaMate network over
  saved HostBox or prior-install host-MQTT values, while preserving explicit
  external MQTT hosts and genuine host-based brokers.
- Keeps the verified release archive, checksum, backup, rollback, and complete
  readiness checks used by previous 1.10 releases.
- Backward compatible with previous My T versions; no API or stored-data
  migration.

## 1.10.18

- Keeps one navigation session when Tesla changes only the destination label
  (for example, a street address becomes `Home`) while the remaining route is
  effectively unchanged.
- Navigation end events now include an explicit `end_reason`, allowing the push
  relay to distinguish genuine arrival from redirects and cancellations.
- Prevents label-only route updates from generating redundant end/start push
  pairs while preserving genuine mid-drive destination changes.
- Builds with Go 1.26.6 so the released binary includes the current standard
  library security fixes required by the vulnerability gate.
- Backward compatible with previous My T versions; no stored-data migration.

## 1.10.17

- Repackages the audited current `main` line so the public release includes the
  updater's transient-download retries and the patched Go dependencies already
  verified by CI.
- Updates the build image to Go 1.26.5 / Alpine 3.24 and includes `lib/pq`
  1.12.3 plus `x/net` 0.56.0.
- Release publishing now selects only the exact current-version archive and
  checksum, verifies the local checksum, and fails closed if an immutable
  same-named remote asset has a different digest.
- No API or stored-data migration; backward compatible with previous My T
  versions.

## 1.10.16

- Lock-secure monitoring now establishes a retained-MQTT baseline before
  sending alerts, preventing a stale already-locked snapshot from looking like
  a new lock transition after install or restart.
- Status now distinguishes saved configuration from active MQTT readiness and
  advertises the complete App-bundled sound set.
- Lock-secure relay events no longer contain a sound preference. My T 4.13 and
  later apply each iPhone's private, device-local sound after delivery.
- Backward compatible with previous My T versions.

## 1.10.15

- **HostBox build reliability:** Dockerfile lists production Go sources explicitly;
  installer verifies files before build; `docker compose build --progress=plain`.
- Runtime `mem_limit` 256m → **512m**.
- Still backward compatible with previous My T versions.

## 1.10.14

- **Fix HostBox / install image build:** `install.sh` now copies **all** `*.go`
  sources into `/opt/my-t-companion` (was a fixed list). 1.10.13 omitted
  `lock_secure_notification.go`, so `docker build` failed with
  `undefined: lockSecureNotificationMonitor`.
- Still fully backward compatible with previous My T versions (same as 1.10.13).

## 1.10.13

- **Optional lock-secure push** (`vehicle_lock_secure`): when locked and no user
  present, can notify via the existing APNs relay. Off by default; requires push
  pairing and server-confirmed prefs.
- `GET/PUT /api/v1/notifications/lock-secure`; capability `lock_secure_push`.
- **Backward compatible:** no change to existing APIs; `app_compatibility`
  minimum **3.10** / recommended **3.30** unchanged. Older My T ignores the new
  capability and keeps working on the same base URL.

## 1.10.12

- **Fix install crash:** `docker-compose.yml` generation with `extra_hosts` no longer
  glues the next key onto the same line (`go-yaml did not find expected key` at L18).
  HostBox「安装增强」and `install.sh` failed on system-MQTT hosts (1.10.11 regression).

## 1.10.11

- **MQTT discovery (P1):** detect docker mosquitto service **or** host `:1883`
  (HostBox system broker) and set `host.docker.internal` + `extra_hosts` automatically.
- Shared Docker network selection also considers MQTT container when present.
- **`myt-doctor.sh`:** read-only diagnostics (healthz, capabilities, MQTT hosts, edge,
  unified entry). Installed as `/opt/my-t-companion/myt-doctor.sh`.

## 1.10.10

- **Fix reported Companion version:** binary no longer hardcodes `1.10.8`. Version is
  `//go:embed` from the `VERSION` file (Dockerfile copies `VERSION` into the build).
  1.10.9 packages incorrectly still answered `capabilities.version=1.10.8`.
- Capabilities may include `teslamate_version` from env `TESLAMATE_VERSION` (installer
  fills this from the running TeslaMate container image tag) so My T can show TeslaMate
  version without scraping LiveView HTML on HostBox IP/Tunnel/Access setups.

## 1.10.9

- **Installer (P0):** no longer requires TeslaMate `.env`. Secrets are resolved from
  shell env → `docker compose config` → running containers → optional `.env` → prior
  companion install. Clear error if `DATABASE_PASS` is still missing.
- Discover `DATABASE_USER` / `DATABASE_NAME` / `DATABASE_HOST` and `MQTT_BROKER_URL`
  (or `MQTT_HOST`+`MQTT_PORT`) when present; write them into companion compose.
- Prefer a Docker network shared with TeslaMate/API when choosing `TESLAMATE_NETWORK`.
- **Gateway (P0):** `Caddyfile.snippet` / `nginx.snippet.conf` / system-Caddy install
  route all `/api/v1/notifications/*` (software-update **and** charging/navigation
  Live Activity status). LAN example routes aligned.

## 1.10.8

- Harden navigation `start_name` so destination-trip history reliably shows **from → to**:
  sticky last geofence after leaving a fence, open-drive start address, first
  reverse-geocoded position on the open drive, previous completed-drive end place,
  and mid-session backfill when TeslaMate addresses land late.
- Push payload and history continue to carry `start_name` on every navigation event.

## 1.10.7

- Record `start_name` on navigation push history (live geofence or open-drive start label) for App start → destination trip titles.
- Align English, Simplified Chinese, and Traditional Chinese documentation with
  My T 3.32 and the public My-T-App availability/setup repository.
- Correct current install/update examples, security support version, manual
  Compose image tag, and CI image naming to Companion 1.10.7.

## 1.10.6

- Mid-drive destination change ends the previous navigation session as `redirected` and starts a new session for the new destination (closed loops for destination-trip UI).

## 1.10.4

- Persist navigation push sessions and expose `GET /api/v1/cars/{id}/navigation/push-history` (`navigation_push_history`).
- Attach real trip timing on navigation end events (`trip_started_at`, `trip_ended_at`, `duration_minutes`) for authentic Live Activity end frames.
- Includes 1.10.3 history API work and 1.10.2 domain/unpair hardening.

## 1.10.2

- Added localized App compatibility metadata to `/api/v1/capabilities` so My T can safely recommend or require an App update only when the corresponding App Store version is available.
- Made `https://push.my-tesla.app/v1/events` the only trusted push relay endpoint and removed both former relay addresses from the allowlist.

## 1.10.1

- Start destination-navigation Live Activities as soon as a genuine driving
  state and destination are present; distance and ETA may arrive afterward.
- Preserve active navigation sessions across service restarts and end orphaned
  Lock Screen activities when fresh TeslaMate state does not confirm them.
- Deliver navigation end events on an independent worker so ordinary update
  retries cannot delay trip closure.

## 1.10.0

- Keep genuine parking observations long-term by default, bounded to the newest
  50,000 events instead of deleting them after 365 days.
- Bound temporary navigation, charging, and notification delivery state by age
  and entry count.
- Add durable-state backup, verified restore, and storage-status commands for
  clean VPS migration without changing TeslaMate PostgreSQL.

## 1.9.3

- Make repeated App pairing idempotent so existing software, charging, and
  navigation MQTT clients are not recreated with duplicate client IDs.
- Disconnect an existing MQTT client before applying genuinely changed pairing
  credentials, and keep a single charging/navigation delivery worker.
- Add a repeated-pairing regression test covering all three push monitors.
- Bound the Companion container to 1 CPU, 256 MB memory, and 128 processes.
- Rotate Docker JSON logs at 10 MB with three retained files.

## 1.9.2

- Fix the Caddy upgrade path so the new parking-event route is written only
  after the temporary route file has been created.
- Add a regression test for route-file initialization order.

## 1.9.1

- Fix the one-command installer so it copies the new parking-event monitor
  source into the installation directory before building the container.
- Keep 1.9.0 data/API behavior unchanged.

## 1.9.0

- Persist future-only genuine TeslaMate MQTT parking transitions for plug and
  charging, lock/Sentry/openings, climate, preconditioning, battery heating,
  and charge-port state.
- Treat each first retained MQTT value as a baseline so install and restart do
  not fabricate history.
- Add an authenticated date-range parking-event endpoint with 365-day default
  retention and first-observed timestamp semantics.
- Advertise feature-level charging, security, and climate event capabilities
  so older and non-Companion My T installations remain unaffected.

## 1.8.0

- Renamed the product and all pre-release technical identifiers to My T
  Companion.
- Moved the canonical repository and release download paths to
  `MatchHar/My-T-Companion`.
- Added complete trilingual descriptions of parking, navigation, Live Activity,
  and software-notification capabilities.
- Kept TeslaMate PostgreSQL access read-only and preserved all security
  controls from 1.7.1.

## 1.7.1 — 2026-07-28

- Retry push-to-start with the newest complete snapshot after a Live Activity
  token becomes available instead of leaving an active session permanently
  undelivered.
- Require both current battery percentage and current rated range before
  starting a charging Live Activity.
- Immediately catch up with the latest complete charging/navigation snapshot
  after push-to-start succeeds.
- Add trailing coalesced navigation updates so the final change inside the
  15-second window is not lost.
- Shorten blocking start retries so later genuine MQTT readings can recover a
  session after token registration.

## 1.7.0 — 2026-07-28

- Added privacy-minimal TeslaMate MQTT monitoring for genuine active
  destination navigation.
- Added remote navigation Live Activity push-to-start, update, and end events
  for compatible My T builds.
- Added remaining distance/time, arrival battery, and verified current-drive
  progress without transmitting coordinates or trajectory through the relay.
- Preserved the last valid destination while ending an activity and supported
  destination changes during an active drive.
- Added authenticated navigation delivery status while keeping navigation
  optional and preserving the ordinary in-App destination card without VPS.

## 1.6.1 — 2026-07-28

- Added privacy-minimal TeslaMate MQTT monitoring for genuine charging start,
  update, and end events.
- Added ActivityKit push-to-start support so My T can present a charging Live
  Activity while the App is not open.
- Added true battery percentage, rated-range gain, charging power, remaining
  time, and completion-time updates at 45 seconds normally and 15 seconds at
  50 kW or above.
- Added an authenticated charging Live Activity delivery-status endpoint.
- Continued to exclude VIN, location, routes, TeslaMate credentials, and kWh
  from the charging push payload.

## 1.5.1

- Upgrade Eclipse Paho MQTT to 1.5.1.
- Upgrade `golang.org/x/net` to 0.55.0 to address published networking,
  parser, and proxy security advisories.
- Build with Go 1.25. No API, pairing, database, or deployment behavior
  changes.

## 1.5.0

- Subscribe to TeslaMate's genuine MQTT software-update fields.
- Persist per-car state and delivered event IDs across container restarts.
- Send privacy-minimal, HMAC-SHA256 signed events to a configured HTTPS My T
  APNs relay with bounded retry.
- Add authenticated notification status at
  `/api/v1/notifications/software-update/status`.
- Keep push disabled unless installation ID, relay URL, and relay secret are
  configured together. Payloads exclude VIN, location, TeslaMate credentials,
  battery, route, and driving history.

## 1.4.1

### Added

- Immutable, checksummed GitHub Release installation and update flow.
- Installed updater that can select a version and back up the current
  installation before applying it.
- Unified-route verification for the capabilities, parking-state, and
  current-drive endpoints.
- LAN Caddy and host Nginx examples, plus explicit guidance for Traefik,
  containerized Caddy, VPN, and direct-LAN installations.
- CI, deterministic release-archive generation, contribution guidance, safe
  issue templates, and a public-release checklist.

### Changed

- Full install success is reported only when both the loopback service and the
  authenticated My T base URL work.
- Documentation now distinguishes files/routes created by the installer from
  TeslaMate data, which is never copied or modified.

### Security

- Release archives must be verified against their published SHA-256 manifests.
- Update failures retain recovery backups and do not silently replace the
  working installation.

### Compatibility and known limits

- Works with VPS and private-LAN TeslaMate deployments when TeslaMateAPI and
  the companion share one protected reverse-proxy address.
- Nginx, Traefik, containerized Caddy, and custom proxies may require manual
  route configuration.
- The companion cannot recover samples TeslaMate never stored, and it does not
  add Parking Monitor screens to My T versions that do not support them.

## 1.4.0

- Install all three reverse-proxy routes on a clean Caddy deployment.
- Enforce PostgreSQL read-only transactions.
- Run the container as a non-root user with a read-only filesystem, no Linux
  capabilities, and `no-new-privileges`.
- Add database-aware container health checking and HTTP server timeouts.
- Compare direct API tokens in constant time.
- Refuse authentication reuse when the existing `/api/ping` probe is publicly
  accessible.
- Add repeatable uninstall support, baseline unit tests, compatibility matrix,
  and security guidance.
- Unify service, image, Compose, and API release-candidate version metadata.
- Allow `update.sh` to run safely from the installed directory without trying
  to copy source files onto themselves.

## 1.3.0

- Add reliable current-drive trajectory and incremental point paging.
- Preserve immutable first-point semantics for live navigation.

## 1.2.0

- Reject stale parking boundary telemetry and expose observation timestamps.

## 1.1.0

- Reuse the existing TeslaMate API authentication boundary.

## 1.0.0

- Initial private parking state-history implementation.

## 1.10.9
- install.sh: after process start, auto-wire Companion onto the same My T API URL (system Caddy, docker Caddyfile, or host edge on the API port). Verifies /api/v1/capabilities on that URL. Does not modify TeslaMateAPI.
