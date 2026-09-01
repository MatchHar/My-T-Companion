# My T Companion 1.10.39

## Complete multi-server push isolation

- Charging Live Activities, destination-navigation Live Activities, trip
  alerts, and Lock Secure notifications now carry the saved My T source ID.
- Remote charging and navigation session IDs are derived from both the original
  Companion session and its saved source, so two TeslaMate servers with the
  same local car ID cannot address one ActivityKit session.
- Relay event IDs are also source-scoped while remaining deterministic for
  retries to the same phone.

Existing pairings keep working. Pairing refreshes performed by a compatible My
T build add the source ID automatically. No TeslaMate database migration, Tesla
token change, vehicle wake, or VPS reconfiguration is required.
