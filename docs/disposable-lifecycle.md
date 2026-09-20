# Disposable 1.10.50 → 1.10.51 lifecycle acceptance

`Companion disposable lifecycle` runs only on an empty GitHub-hosted Ubuntu
runner, with no repository/account secrets, production data, Tesla credentials,
or production Docker socket. Its synthetic stack uses actual PostgreSQL,
Mosquitto and Caddy. A minimal TeslaMate-compatible schema and authenticated API
stub replace TeslaMate ingestion/API behavior; this is not a complete upstream
compatibility matrix or a real-car test. Runtime containers use an internal-only
Docker network. The only subscriber is synthetic and paused; relay pairing and
outbox delivery remain off.

The script downloads published immutable 1.10.50 and 1.10.51 archives, verifies
their published digests/checksums and GitHub release-workflow attestations, then
executes their **unaltered** installers/updaters. Its gates cover:

1. Clean 1.10.51 installation, real authenticated Caddy/Go/Postgres tire-history
   pagination, and uninstall stopping Companion while retaining configuration
   and its durable volume.
2. Clean 1.10.50 with synthetic subscriber preferences, parking/navigation
   history, other state files, and a real generated Friend Together identity.
3. A one-shot failure at the candidate's Docker-build step, after new source
   files were copied. Every other Docker call is real. The actual prior
   installer must restore health and the exact prior source/configuration tree;
   neither new tire-history source file may remain.
4. Digest-pinned upgrade, repeated latest-stable updater, retained configuration
   and full durable-volume inventory/content, plus real HTTP and SQL checks.
5. **Backup-assisted healthy rollback:** restore the complete saved 1.10.50
   source/configuration and Caddy configuration, retaining the current durable
   volume; run the real pinned 1.10.50 updater and verify health and exact source.
   This does not claim that the overwrite-only updater alone removes newer
   files during a healthy downgrade. Return to pinned 1.10.51 afterward.

All durable JSON is compared canonically; only Friend Together's documented
restart-updated `last_time_ms` is excluded, with a separate nondecreasing-clock
check. Its issuer ID and all other fields remain covered. Synthetic source
database contents and unrelated stack container identities are also checked.
No old durable snapshot is restored to make a preservation check pass.

Evidence contains only synthetic test logs and gate results. The failure case
proves recovery from a pre-build installation failure, not every possible crash,
proxy failure, power loss, or rollback of active Friend Together grants (which
are intentionally invalidated on restart). Docker-Caddy routing is exercised;
system-Caddy, Nginx and API-port-edge installations need their own acceptance.

This is deliberately version-pinned. It fails closed if latest stable no longer
resolves to 1.10.51; update the fixture deliberately for a later release. Do not
run it on VM102 or attempt to bypass the hosted-runner/empty-Docker guards.
