# My T Companion 1.10.19

This release fixes upgrades on servers whose saved MQTT settings no longer
match the live Docker topology.

- Ignores a stale `host.docker.internal` MQTT address when Mosquitto is a
  Docker Compose service and nothing listens on the host's port 1883.
- Uses the live Docker Mosquitto service and shared TeslaMate network instead.
- Preserves explicit external MQTT hosts and genuine host-based Mosquitto
  installations.
- Keeps checksum verification, pre-update backup, automatic rollback, and the
  complete HostBox/Companion readiness checks.
- No API or stored-data migration; backward compatible with previous My T
  versions.

Update an existing installation:

```bash
sudo MY_T_VERSION=1.10.19 /opt/my-t-companion/update.sh
```
