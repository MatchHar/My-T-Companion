# My T Parking Monitor 1.7.0

This release adds proactive destination-navigation Live Activities while
preserving the companion's existing parking, drive-trajectory, charging, and
vehicle-software notification features.

## Added

- Genuine TeslaMate MQTT `active_route` and vehicle-state monitoring.
- Automatic navigation Live Activity start, update, and end events for
  compatible My T builds.
- Remaining distance/time, estimated arrival, vehicle-reported arrival
  battery, and read-only PostgreSQL-verified drive progress.
- Authenticated delivery status at
  `/api/v1/notifications/navigation-live-activity/status`.

## Privacy and fallback

- The relay receives destination and visible navigation summary/progress only.
  Coordinates, trajectory, VIN, TeslaMate credentials, and vehicle history are
  not sent to the relay.
- My T remains usable without this companion. Its ordinary destination card,
  live vehicle position, and speed continue to work; complete route history
  and proactive Lock Screen/Dynamic Island delivery require a paired companion.
- Missing TeslaMate route data is omitted rather than estimated.

## Upgrade

```sh
sudo MY_T_VERSION=1.7.0 /opt/my-t-parking-monitor/update.sh
```
