# My T Companion 1.6.1

This release includes the charging Live Activity feature from 1.6.0 and fixes
the verified-release installer so the new charging monitor source is copied
into the installation directory before the container is built.

## Added

- Genuine TeslaMate MQTT charging start, update, and end events.
- Remote Lock Screen and Dynamic Island charging updates for compatible My T
  builds.
- True battery percentage, rated range, range gain, power, remaining time, and
  completion time without estimated kilometers or kWh.
- 15-second minimum update interval at 50 kW or above and 45 seconds otherwise.

## Fixed

- The installer now validates and installs `charging_notification.go`.
- Existing deployments still receive a recoverable backup and automatic
  rollback if an update fails.

## Upgrade

```sh
sudo MY_T_VERSION=1.6.1 /opt/my-t-companion/update.sh
```
