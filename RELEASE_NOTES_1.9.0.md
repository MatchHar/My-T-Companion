# My T Companion 1.9.0

This release adds future-only parking-event observation for compatible My T
builds.

## What changed

- Records genuine TeslaMate MQTT transitions for:
  - cable connected and disconnected;
  - charging started, stopped, completed, or connected with no power;
  - vehicle locked/unlocked, Sentry enabled/disabled, and doors, windows,
    trunk, or frunk opened/closed;
  - climate, preconditioning, battery heating, and charge-port door changes.
- Adds authenticated date-range retrieval at
  `/api/v1/cars/{id}/parking-events`.
- Attaches battery percentage, rated range, charging state, and cable type only
  when TeslaMate genuinely reports those values.
- Keeps events for 365 days by default. `PARKING_EVENT_RETENTION_DAYS` may be
  set from 30 to 3650 days.

## Truthfulness and privacy

- The first retained value after install or restart establishes a baseline and
  is never recorded as a transition.
- Events begin only after 1.9.0 is running. Old cable-insertion times are not
  inferred from charging-session starts.
- A timestamp means the time TeslaMate/Companion first observed the new state,
  not a guaranteed physical-action timestamp.
- Events remain in the user's own Companion data volume. They are not sent to
  the My T notification relay.
- TeslaMate PostgreSQL access remains read-only and no TeslaMate tables are
  created or modified.

## Compatibility

My T detects these additions through feature-level capabilities. Users without
the Companion, or with an older Companion, keep ordinary TeslaMate/Tessie
parking and charging history without errors. Upgrade the App separately when a
My T release explicitly lists parking-event timeline support.
