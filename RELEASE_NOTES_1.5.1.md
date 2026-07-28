# My T VPS Companion 1.5.1

This is a dependency-security maintenance release.

## Changed

- Eclipse Paho MQTT is updated from 1.5.0 to 1.5.1.
- `golang.org/x/net` is updated to 0.55.0.
- The build toolchain is updated to Go 1.25.

## Compatibility

There are no endpoint, pairing, database, reverse-proxy, or configuration
changes. Parking history, live-drive trajectories, and optional native
software-update notification behavior remain compatible with 1.5.0.

## Upgrade

Installer-managed deployments can run:

```sh
sudo MY_T_VERSION=1.5.1 /opt/my-t-parking-monitor/update.sh
```

The updater verifies the release checksum and keeps a recovery backup before
applying the update.
