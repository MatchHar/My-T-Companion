# My T Companion 1.10.28

## Relay security hardening

- Every software, charging, navigation, and lock-secure delivery is pinned to
  the official My T relay endpoint; a persisted URL is no longer trusted for
  outbound requests.
- Relay clients refuse HTTP redirects, preventing authorization material from
  being forwarded to a different origin.
- Navigation notification history remains bounded by the same compile-time
  limit used at runtime.

## Documentation

- English, Simplified Chinese, and Traditional Chinese compatibility guidance
  now points to the App Store listing as the authoritative My T version source.

No database migration or re-pairing is required.
