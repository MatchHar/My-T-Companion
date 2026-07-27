# Security policy

## Supported versions

Security fixes are provided for the latest published release. Version 1.4.1 is
currently being validated privately before public availability.

## Deployment requirements

- Bind the service only to `127.0.0.1`.
- Put every data endpoint behind HTTPS and the same authentication boundary as
  the existing TeslaMate API.
- Confirm an unauthenticated request to `/api/ping` is rejected before enabling
  authentication reuse.
- Keep PostgreSQL on a private Docker network.
- Do not remove `PGOPTIONS=-c default_transaction_read_only=on`.
- Keep the container hardening options from the supplied Compose file.
- Back up TeslaMate and test restoration independently of this add-on.

## Reporting a vulnerability

Do not open a public issue containing credentials, server addresses, vehicle
locations, VINs, or database extracts. Use GitHub's private vulnerability
reporting feature after the repository becomes public and the setting is
enabled. Until then, contact the repository owner through GitHub without
including production secrets or unredacted vehicle data.

Include the companion version, TeslaMate version, reverse proxy type, and
redacted reproduction steps. Never attach `.env` or raw production logs.
