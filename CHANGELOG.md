# Changelog

## 1.4.1

- Install and update from immutable, checksummed GitHub Release archives.
- Make the installed updater fetch and back up a real newer release.
- Do not report full success until the unified My T proxy endpoint is ready.
- Add LAN Caddy and host Nginx examples.
- Add CI, release artifact generation, contribution guidance, and safe issue
  templates.

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
