# Disposable 1.10.54 candidate acceptance

This is a destructive acceptance fixture **only for a fresh GitHub-hosted Ubuntu
runner with an empty local Docker daemon**. It must never run on a VPS, developer
Docker daemon, self-hosted runner, or real TeslaMate installation. The script
requires explicit opt-in, root, GitHub-hosted/Linux indicators, a local Unix Docker
socket, unused loopback ports and no inherited operator configuration. Its only
database records, tokens, pairing entries and history are synthetic.

The new workflow runs `scripts/test-candidate-lifecycle.sh`. Root must review its
complete diff before pushing/dispatching it. This fixture does not publish a
release, tag, image, vehicle command, iOS build, or APNs notification.
Automatic PR/main runs apply only while `VERSION=1.10.54`; future versions need
an updated acceptance fixture. Explicit dispatch on another version fails.
Catalog-only changes under `hostbox/` do not repeat this release-source fixture.

## Source identity

- Prior version is immutable stable `1.10.53`, pinned to archive SHA-256
  `3796860cb8b85faa50baa70a2da1e01b033261d699cf446b07f6e3af15c10831`.
  The script checks release immutability/stability, GitHub's asset digest, the
  published checksum and the repository release workflow's artifact attestation.
- `gitarchive` mode (default) requires clean exact `HEAD` with `VERSION=1.10.54`.
  It uses the same reproducible `git archive` format/prefix as the release builder,
  records commit/digest, and serves that archive through a real loopback HTTP
  server using the released updater's explicit release-base override. It does
  **not** claim the unpublished candidate has release provenance/immutability.
- `published` mode additionally fetches immutable stable `1.10.54`, verifies the
  same provenance/checks and requires its archive digest to equal exact `HEAD`.
  Positive updates then download the real GitHub release assets.
- A separately named negative fixture archive adds a deliberate failing `RUN` to
  the candidate Dockerfile. Its independent digest is evidence of fault injection,
  **not a release candidate or publication asset**. Real Docker must build/fail it;
  Docker, database and curl are never mocked or intercepted.

## Acceptance gates

The fixture creates real PostgreSQL 18, Mosquitto and Caddy containers. The API
auth/upstream is explicitly a synthetic Caddy service, not an actual TeslaMateAPI
implementation. SQL tables cover both released53 read paths and54 schema probes,
including `cars.settings_id`, `car_settings.enabled`, states, positions, drives,
charging processes and navigation enrichment references. The application role has
SELECT-only grants and default read-only transactions.

It verifies clean candidate installation, container health/non-root/read-only
rootfs/loopback binding, authenticated Caddy-to-Go SQL reads (including non-empty
states and an ongoing drive with three recorded points), all54 SQL schema
capabilities, a real broker namespace/retained/reconnect/new enabled-car smoke
without Companion restart, and uninstall retention. No push subscriber is active.

Next, the actual released53 updater installs53. Synthetic configuration, paused
pairing, parking history, completed navigation history, dedupe markers and an
operator sentinel are persisted. A real candidate build failure must trigger
the actual released updater's previous-installer recovery, with exact prior
source/configuration and healthy53. Positive53→54, same-version repeat, a
backup-assisted healthy rollback and final re-upgrade/uninstall must preserve
prior configuration values and the entire canonical durable-volume inventory.
New safe configuration keys are allowed; existing values and private file mode
must stay unchanged. Friend Together's monotonic runtime clock is excluded from
canonical content comparison; no vehicle/pairing/history contents are excluded.

Healthy rollback explicitly restores the complete prior source/configuration
snapshot and runs its real installer. It does not pretend old overwrite-only
installers remove54-only files, and never rewinds the durable data volume.
Docker-Caddy route teardown remains separate: uninstall stops Companion and
retains its configuration/volume; fixture cleanup later removes disposable
containers/volumes and firewall rules.

## Network and evidence limits

Only host loopback ports are published. As in the previously green PR62 fixture,
the runtime uses a dedicated bridge with `DOCKER-USER` and `INPUT` rules denying
new external or runner-host connections. It is **not** a literal `internal:true`
network: that topology prevents the unmodified released installer's required
host-loopback probes in this fixture. A bounded TEST-NET egress probe and rule
checks validate the intended runtime boundary. GitHub asset/provenance downloads,
Docker image pulls and build dependency downloads occur outside that bridge; the
whole CI job is not air-gapped. The fixture does not contact real vehicles or APNs.

Artifacts retain gates, exact source identity, image identities, attestation logs
and synthetic install/recovery/container logs for seven days. A syntax/local
static check is not acceptance: only the actual disposable workflow's successful
gates prove Docker install/upgrade/rollback/uninstall. This is not full TeslaMate
4.3 deployment, all-data migration, restart timing, or iOS/relay compatibility
certification.
