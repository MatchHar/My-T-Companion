# My T Companion 1.10.11

- Smarter MQTT discovery: system mosquitto (host :1883) uses
  `host.docker.internal` + Docker `extra_hosts` without HostBox-only patches.
- Docker network discovery prefers networks shared with API / TeslaMate / MQTT.
- New `myt-doctor.sh` for VPS self-check after install/update.
