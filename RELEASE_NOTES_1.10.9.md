# My T Companion 1.10.9

Installer and gateway packaging release (no Go API behavior change).

- Install no longer requires `/opt/teslamate/.env`; secrets resolve from shell,
  `docker compose config`, running containers, then optional `.env`.
- Discover DB host/user/name and MQTT URL when present.
- Prefer a Docker network shared with TeslaMate/API.
- Gateway snippets route all `/api/v1/notifications/*` (Live Activity status +
  software-update pair/status).
