# My T Companion 1.10.12

- Fixes install failure when MQTT uses `host.docker.internal`: generated
  `docker-compose.yml` was invalid YAML (`extra_hosts` glued to `environment`).
- Affects HostBox system mosquitto / host broker installs (1.10.11 regression).
