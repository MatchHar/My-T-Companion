# My T Companion 1.10.38

## Individual door history and last-observed status

- Capture future open and close transitions for the driver-front,
  driver-rear, passenger-front, and passenger-rear doors separately.
- Keep the aggregate door event as a backward-compatible fallback. A compatible
  My T build keeps the named rows and suppresses only the matching same-burst
  aggregate duplicate.
- Return the last observed vehicle lock, aggregate door, and four named-door
  values through `companion-status`, alongside the existing four windows.

## More reliable parking and server routing

- Return the latest real battery and rated-range sample for an active online
  parking interval when it is observed within the existing 30-minute boundary.
- Repair the `companion-status` route during upgrades across system Caddy,
  Docker Caddy, the local API edge, and unified LAN configurations. Companion
  remains bound to loopback; this does not expose port 8083 publicly.
- Scope software-update events to the paired saved-source identifier so the
  same TeslaMate-local car ID on different servers cannot collide or open the
  wrong saved connection in My T.

No TeslaMate database migration, Tesla token change, re-pairing, or vehicle
wake is required. Existing history is unchanged: individual door transitions
that happened before this version cannot be reconstructed. Older My T builds
continue to receive the aggregate fallback.
