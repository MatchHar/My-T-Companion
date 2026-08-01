# My T Companion 1.10.4

- Records navigation push sessions (destination, distance, duration) for App history via `GET /api/v1/cars/{id}/navigation/push-history`.
- Capability: `navigation_push_history`.
- Navigation end events include real trip timing: `trip_started_at`, `trip_ended_at`, `duration_minutes`, and driven distance for authentic Live Activity end frames.
- Continues to use only `https://push.my-tesla.app/v1/events` as the trusted push relay.
- Includes 1.10.3 history API and 1.10.2 domain/unpair hardening.
