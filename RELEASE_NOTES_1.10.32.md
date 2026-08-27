# My T Companion 1.10.32

## Destination-navigation Live Activity progress

- Keep refreshing verified driven distance throughout an active navigation
  session, even after the TeslaMate drive ID and start name are already known.
- Ignore stationary or incomplete odometer samples at or below 0.05 km so a
  remote Live Activity does not become permanently verified at zero progress.
- Deliver the first meaningful driven-distance update immediately, then retain
  the existing ten-second push throttle for later changes.
- Avoid repeating start-address database lookups after the authoritative start
  name has been resolved.

No database migration, push re-pairing, or configuration change is required.
