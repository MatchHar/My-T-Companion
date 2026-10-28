# My T Companion 1.10.65

- Add owner-only `road_parking_v1`: batch parking places and paged arrival/departure history from recorded TeslaMate drive boundaries. Stops under five minutes are excluded; range totals count only overlapping known time. Unknown departures or inconsistent positions never receive an estimated duration.
- Keep place grouping bounded, separate vehicle identities, and authenticate pagination by car, range, place and data revision. Requests are read-only and do not wake vehicles or send controls.
- Add opt-in timestamped GPS samples to footprint responses so a compatible My T update can qualify traversed cities against verified administrative boundaries. Coverage-cell intersections alone do not establish a visit.
- Older clients keep their existing responses. No database migration, pairing change or notification change.

These new screens require the compatible My T 8.05 update (build 796 or later); release of this Companion does not claim that App build is already available on the public App Store. Unsupported boundaries or incomplete evidence remain unavailable. Update VM102 through HostBox after its signed catalog is promoted; keep the previous verified archive/updater backup for rollback.
