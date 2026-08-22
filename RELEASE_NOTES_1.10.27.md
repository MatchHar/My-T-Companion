# My T Companion 1.10.27

## Reliable per-iPhone push delivery

- Transient relay failures and pending ActivityKit session tokens now enter a
  bounded durable retry queue on the user's VPS. One successful iPhone can no
  longer hide another iPhone's failed delivery.
- Invalid relay installations are paused individually; pause and unpair remove
  that phone's pending rows.
- Lock-secure preferences and status are installation-specific.

## Database and concurrency hardening

- Corrects the PostgreSQL connection timeout to 10 seconds, adds a 15-second
  read-only statement timeout, and gives requests bounded query contexts.
- Navigation database enrichment runs outside the MQTT state mutex, so a slow
  database cannot stall vehicle-state processing.
- Revalidates stored relay URLs, installation IDs, and secrets on startup.

The retry queue is permissioned `0600`, capped at 256 events, and expires each
row after 10 minutes to 24 hours depending on event type. It is removed on
delivery, pause, unpair, or expiry and remains only on the user-controlled VPS.

Also includes the installer reliability fixes committed after 1.10.26.
