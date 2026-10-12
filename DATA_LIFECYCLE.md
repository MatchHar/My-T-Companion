# Data lifecycle

English · [简体中文](DATA_LIFECYCLE.zh-Hans.md) · [繁體中文](DATA_LIFECYCLE.zh-Hant.md)

My T Companion separates durable evidence from replaceable operational state.
It never changes or deletes TeslaMate PostgreSQL history.

| Data | Purpose | Default lifecycle |
|---|---|---|
| Parking plug, charging, security and climate transitions | Reconstruct long parking sessions | Long-term; newest 50,000 events |
| Software-update notification IDs | Prevent duplicate alerts | 180 days; newest 1,000 |
| Charging Live Activity delivery IDs | Prevent duplicate remote updates | 14 days; newest 2,000 |
| Navigation Live Activity delivery IDs | Prevent duplicate remote updates | 7 days; newest 2,000 |
| Active charging snapshot | Resume a short interrupted delivery | 48 hours |
| Active navigation snapshot, start and waypoints | Live trip only | 12 hours |
| Push pairing | Private device-to-relay configuration | Until unpaired or replaced |
| Pending push retry | Minimum signed event needed for a failed delivery | 10 minutes–24 hours by event type; newest 256 total |

The navigation watchdog checks active sessions every 15 minutes. A session
older than 12 hours is ended through the normal terminal-event path so its
Live Activity is dismissed instead of leaving an immortal snapshot.

Set `PARKING_EVENT_RETENTION_DAYS=0` for the default long-term policy. An
administrator may instead select 30–3650 days. `PARKING_EVENT_MAX_EVENTS`
defaults to 50,000 and may be set from 1,000–500,000. Capacity pruning always
removes the oldest valid observations first.

## Companion 1.10.54 observation lifecycle

Companion `1.10.54` adds the observation and delivery safeguards below. Existing
self-hosted Companion 1.10.53 services remain unchanged until upgraded; the
existing lifecycle records above remain intact.

The enabled source fleet refreshes every 30 seconds; unknown MQTT IDs trigger
bounded, rate-limited refreshes and newly enabled cars recover retained baselines
by re-subscription. Removed/disabled cars lose active cached observations and
pending ordinary delivery, not historical evidence. The last successful fleet
snapshot is usable for at most five minutes after a failure; older inventory
fails closed. New-car server notification defaults and explicit per-car Friend
grants are unchanged.

| Local operational state | Lifecycle and meaning |
| --- | --- |
| Fleet inventory and per-field MQTT receipt diagnostics | Process-local, bounded and rebuilt; not a historical vehicle-sample clock |
| Connection-local transition/software/ETA baselines | Reset on reconnect; retained replay and unknown recovery establish baselines, not new actions |
| Exact Live Activity cleanup authorization | Source/car/category/session/installation scoped; up to 48 hours and newest 1,024 bindings in the private pairing store |

Receipt time, retained flags, last-live receipt and connection epoch explain
what Companion received, never when the vehicle sampled it. `healthy` cannot
refresh unrelated fields. Absolute ETA requires the duration/route's own live
receipt anchor; reconnect/recovery does not create a fresh countdown from an
undated retained value. Software aggregates keep installed/latest versions
together; incomplete/incomparable aggregates are unconfirmed. The legacy scalar
fallback is conservative and may miss an alert when live corroboration is absent.

Every initial push and retry re-evaluates current installation/source/car and
category preferences. Revoked/disabled ordinary work is discarded, but an active
paired recipient may receive only the exact terminal cleanup for its previously
authorized charging/navigation session. This exception cannot start a card,
send an ordinary alert or expand a Friend grant; transmitted requests cannot be
recalled. Cleanup bindings are not telemetry history and follow pairing-data
backup handling below. SQL remains UTC-session read-only with a bounded pool;
no PostgreSQL table, index, retention or stored timestamp is changed. See
[COMPATIBILITY.md](COMPATIBILITY.md) for pool/probe and history-pagination limits.

## Backup and migration

```sh
sudo /opt/my-t-companion/backup.sh
sudo /opt/my-t-companion/restore.sh /var/backups/my-t-companion/BACKUP.tar.gz
sudo /opt/my-t-companion/storage-status.sh
```

The default encrypted-at-rest responsibility belongs to the VPS operator:
archives are permissioned `0600`, checksummed, and only the newest 12 are kept.
Backups include durable parking evidence and software-notification
deduplication. They exclude temporary charging/navigation state and push
pairing secrets. Use `backup.sh --include-pairing` only for a trusted migration.

Pending push retries are stored only in the Companion data volume on the
user-controlled VPS, inside the same `0600` pairing store. They are not an
operator audit log, are removed after successful delivery, pause, unpair, or
expiry, and are excluded unless pairing data is explicitly included in a
trusted migration backup.

This backup is not a TeslaMate backup. Back up and restore TeslaMate PostgreSQL
with TeslaMate's documented process.
