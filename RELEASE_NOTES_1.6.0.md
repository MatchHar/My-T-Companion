# My T Companion 1.6.0

This feature release adds the VPS side of My T charging Live Activities.

## Added

- Detects genuine charging start, update, and end changes from TeslaMate MQTT.
- Sends signed, privacy-minimal events to the official My T APNs relay.
- Supports automatic Live Activity presentation while My T is not open.
- Reports true start/current/target battery percentage, current rated range,
  true rated-range gain, charging power, remaining duration, and completion
  time.
- Coalesces frequent MQTT changes: routine charging updates use a 45-second
  minimum interval, while charging at 50 kW or above uses 15 seconds.
- Exposes authenticated status at
  `/api/v1/notifications/charging-live-activity/status`.

## Privacy and data boundaries

Charging Live Activity events contain only the fields needed to render the
card. They do not contain VIN, location, route history, TeslaMate credentials,
or kWh. Rated-range gain is sent only when genuine TeslaMate readings exist;
the service does not estimate missing kilometers.

## Compatibility

- Existing Parking Monitor, parking-history, current-drive, and vehicle
  software notification routes remain compatible.
- The charging card requires a compatible My T build that registers
  ActivityKit push-to-start and per-activity update tokens.
- TeslaMate remains the source of truth and is not modified.

## Upgrade

```sh
sudo MY_T_VERSION=1.6.0 /opt/my-t-companion/update.sh
```

The updater creates a recoverable backup before replacing the installation.
