# My T Companion 1.10.30

## Correct destination-trip origins

- Destination notifications now prefer the matching TeslaMate drive start
  address. A geofence remembered from an earlier trip can no longer make every
  journey appear to start at Home.
- Existing navigation history is updated when the authoritative drive address
  arrives.

## Live Activity catch-up

- Enabling charging or destination Live Activities during an active session
  now replays the current start only to that iPhone.
- Ordinary trip alerts are not duplicated, and other paired phones do not
  receive the catch-up event.

No database migration or push re-pairing is required.
