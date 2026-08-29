# My T Companion 1.10.33

## Immediate authoritative trip origin

- When a navigation Live Activity starts before TeslaMate has finalized the
  active drive, send another update immediately after the authoritative trip
  origin is first resolved.
- Cancel a stale throttled update before sending that first corrected origin,
  so the Lock Screen does not keep a missing or older start label.
- Keep the existing immediate first verified-distance delivery and the normal
  ten-second throttle for later navigation changes.

No database migration, push re-pairing, or configuration change is required.
