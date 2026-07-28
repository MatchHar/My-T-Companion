# My T Companion 1.9.1

This patch release fixes the 1.9.0 one-command installation package.

## Fix

- The installer now copies `parking_event_monitor.go` into the installed build
  directory before building the container.
- Installation validation requires that source file, preventing an incomplete
  release from reaching the Docker build step.

## Included 1.9 features

Version 1.9.1 includes the future-only genuine TeslaMate MQTT parking-event
monitoring introduced in 1.9.0: plug and charging, lock/Sentry/openings,
climate/preconditioning/battery heating, authenticated date-range retrieval,
first-observed timestamp semantics, and 365-day default retention.

The live updater automatically restores the previous installation if any build
or health check fails. Users should install 1.9.1 rather than 1.9.0.
