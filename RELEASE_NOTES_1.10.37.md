# My T Companion 1.10.37

## Independent notifications for multiple vehicles

- Keep the existing notification choices as defaults for every vehicle on this
  TeslaMate server.
- A compatible My T build can now override Lock Screen cards, destination-trip
  alerts, low-battery alerts, software updates, and Lock Secure Alert for each
  vehicle independently.
- Vehicle selection in My T remains a display choice and never changes which
  vehicles can notify this iPhone. New vehicles inherit the server defaults.
- Enabling Lock Screen cards for one vehicle while it is already charging or
  navigating replays only that vehicle's current card to this iPhone.

## Safer phone-pairing capacity

- Preserve all active phone pairings. At the eight-phone limit, only the oldest
  paused phone may be removed.
- Known paused phones resume in place; paused records expire after 365 days and
  their queued deliveries are removed.

No TeslaMate database migration, Tesla token change, re-pairing, or vehicle
wake is required. Existing installations begin with the 1.10.36 all-vehicle
behavior, and older My T builds cannot silently erase newer per-vehicle choices.
