# Companion 1.10.54

## Changes

- Use the Go 1.26.9 security patch and golang.org/x/net 0.60.0 for the release
  build and validation, without changing the Go major version.
- Follow TeslaMate's enabled source fleet dynamically and recover a new car's
  retained MQTT baseline with bounded re-subscription retries. Reject delayed
  subscription results and old-client disconnect callbacks. Use TeslaMate 4.3's
  explicit Reload after account fleet changes; Companion does not call Tesla or
  wake a vehicle.
- Prevent retained parking replay and unknown-to-known recovery from becoming
  new physical actions. Prefer software-update aggregates over mixing separate
  availability/version callbacks; incomplete or unrecognized versions remain
  unconfirmed instead of becoming a false update alert.
- Recheck current installation, source, vehicle and category/per-car preferences
  before initial push delivery and every queued retry. Preserve only the exact
  terminal cleanup of an already authorized charging/navigation Live Activity
  for an active paired recipient. Pending low-battery snoozes survive unknown
  reconnects; the initial complete low-battery snapshot remains silent.
- Recover charging/navigation snapshots without inventing physical start/end
  times. Retained remaining time cannot become a new countdown through a
  `healthy` message. Add authenticated per-field receipt/retained/connection
  diagnostics; receipt time is never a vehicle-source sample timestamp.
- Use one strict `MQTT_NAMESPACE` across monitors and preserve safe namespace
  and SQL pool settings during installation. Bound read-only UTC SQL pools and
  per-feature schema probes; add optional keyset state-history pagination while
  keeping unpaginated legacy requests complete.

## Upgrade and compatibility

Publishing 1.10.54 does not upgrade an existing self-hosted 1.10.53 service.
Install the verified release package separately, preserve the previous verified
package and normal configuration/data backups for rollback, and validate the
service after upgrading. Pairings, per-phone/per-car preferences and stored
history are retained; there is no TeslaMate table/index migration. New cars
continue to inherit server notification defaults; Friend Together grants stay
explicit per car and never expand automatically.

`MQTT_NAMESPACE` must exactly match the TeslaMate source: empty or up to 128
ASCII characters from `[A-Za-z0-9_.-]`, without normalization. Invalid explicit
namespace/pool settings fail rather than silently selecting another source.
Unknown software availability/progress can be `null`; clients must not treat
unknown as false/zero. See [COMPATIBILITY.md](COMPATIBILITY.md) and
[DATA_LIFECYCLE.md](DATA_LIFECYCLE.md) for SQL ranges, pagination and cleanup
authorization limits.

## Verification and limitations

The pre-release local synthetic baseline recorded 533 passing results including
subcases, two opt-in PostgreSQL fixture skips and zero failures. Race detector,
vet and Darwin/Linux cross-build checks passed. Check the matching release CI
for separately executed disposable PostgreSQL fixtures and synthetic broker/
lifecycle tests; such tests do not prove a real deployment or APNs delivery.

Retained values and even live MQTT receipt times do not establish vehicle sample
freshness. Upstream software aggregates can contain remembered values; the
conservative legacy scalar fallback may omit an unconfirmed alert. A failed
fleet read uses its last successful snapshot for at most five minutes, then
ordinary source-dependent work fails closed. Already transmitted requests
cannot be recalled. Real new-car, forced production reconnect/ACL/namespace,
APNs, production load and the full TeslaMate/PostgreSQL version matrix remain
separate acceptance work. Presence detection remains advisory, not proof that
every seat, child or pet is absent.
