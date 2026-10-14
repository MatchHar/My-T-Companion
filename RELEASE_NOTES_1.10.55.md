# Companion 1.10.55

## Changes

- Passively read bounded TeslaMate charging-process and persisted charge samples
  every30 seconds, using the existing read-only database connection. Bind active
  charging cards to the exact process ID and start time. Repeated HTTP/MQTT
  replay cannot make an old power/SOC/range sample current.
- Reconcile a fresh DB result even when MQTT readiness arrived first, without
  waiting for an unrelated next message. Keep existing readiness, membership,
  45/15-second delivery throttles and source-clock checks.
- Bind each live ETA receipt to its already-known fresh process. Do not lend old
  remaining time to a new process or to a later stop/resume of the same process.
- Before initial and durable retry delivery, recheck the exact current charging
  source/session and sample freshness. Bounded, cancellable serialization keeps
  terminal cleanup after an already-in-flight start. A queued or ineligible start
  is not delivery success; exact actual acceptance resolves delivery bookkeeping.
- Add optional owner-authenticated `data.charging_observation` diagnostics with
  fresh/data_gap/terminal_confirmed/no_session/unknown status and source clocks.
  App foreground charge reads remain separate; this is not FSD identification.

## Upgrade and rollback

Publishing does not upgrade self-hosted services. Install only the verified
immutable55 package after checking the current deployment. Preserve the exact
previous verified54 package/image, source/configuration and normal data backups.
Validate health, version, read-only diagnostics and durable inventories after
upgrading; use the tested backup-assisted rollback if acceptance fails. Do not
rewind durable notification or charging history while rolling back source.

Pairings, settings and history are retained. No TeslaMate table/index migration,
TeslaMate/API/Relay upgrade, vehicle wake or notification test is required by
this release. Unsupported/missing charge schema stays unknown, not completed.

## Verification and limits

The accepted local five-file baseline passed three real isolated PostgreSQL18.6
gates, full tests (288 top-level/594 including subcases), full race, vet and build
without test skips. The final versioned candidate must pass its own CI and exact
package lifecycle gates; baseline evidence is not final package acceptance.

Upstream-unrecorded physical stop/sleep times cannot be recovered. Offline is
not confirmed asleep. Database end_date is the logger's recorded end, not proof
of the physical stop time. Real APNs, physical vehicles, load and the complete
TeslaMate/PostgreSQL schema matrix remain separately unverified.
