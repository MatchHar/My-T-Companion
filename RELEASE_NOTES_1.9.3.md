# My T Companion 1.9.3

This maintenance release fixes repeated App pairing and adds deployment
resource safeguards.

## MQTT stability

- Replaying the same valid pairing no longer creates duplicate software,
  charging, or navigation MQTT clients.
- A genuinely changed pairing cleanly disconnects the old client before the
  replacement starts.
- Charging and navigation keep one delivery worker for the lifetime of the
  process.
- A regression test replays the same pairing three times across all push
  monitors.

## Deployment safeguards

- The container is limited to 1 CPU, 256 MB memory, and 128 processes.
- Docker JSON logs rotate at 10 MB and retain three files.
- Existing pairing, notification, parking-event, and Live Activity state remain
  in the persistent Companion data volume during upgrades.
