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
