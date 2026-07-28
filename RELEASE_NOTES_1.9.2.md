# My T Companion 1.9.2

This patch release fixes the Caddy route migration in 1.9.1.

## Fix

- Creates the temporary Caddy route file before writing the new
  `/api/v1/cars/{id}/parking-events` route.
- Adds a regression test for the upgrade-specific initialization order.
- Retains updater rollback protection if a build, health check, proxy
  validation, or reload fails.

Version 1.9.2 includes all genuine future parking-event features from 1.9.0 and
the complete installer source-copy fix from 1.9.1. Users should install 1.9.2.
