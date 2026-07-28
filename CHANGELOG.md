# Changelog

## 1.6.0 — 2026-07-28

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
