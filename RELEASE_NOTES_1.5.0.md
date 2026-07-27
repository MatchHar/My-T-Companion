# My T VPS Companion 1.5.0

This release adds the VPS half of native My T vehicle software-update
notifications.

- Observes genuine TeslaMate MQTT software-update fields.
- Detects newly available versions and completed installed-version changes.
- Persists deterministic event IDs across container restarts.
- Sends privacy-minimal HMAC-SHA256 signed events only to configured HTTPS
  relays.
- Exposes authenticated delivery status without exposing secrets.
- Accepts automatic pairing from My T through the existing authenticated API.
- Restricts delivery to the official My T HTTPS relay to prevent SSRF.

Push remains disabled unless all pairing values are configured. Existing
Parking Monitor and live-navigation features continue working unchanged. The
public repository and user VPS never contain the Apple APNs signing key.
