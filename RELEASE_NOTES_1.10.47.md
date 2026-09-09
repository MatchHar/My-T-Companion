# My T Companion 1.10.47

- Fix Friend Together invitation creation after HostBox enablement. GET
  `/status` already reported ready; POST `/invitations` returned 503
  `source_unavailable` because the extra read-only pool used a `postgres:?host=`
  URL that did not reach TeslaMate Postgres.
- Use the same keyword DSN as parking/notifications, then ping before serving.
- Keep Friend Together off by default. No database migration.
