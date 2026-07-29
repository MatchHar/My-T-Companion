# My T Companion 1.10.0

- Parking plug, charging, security and climate observations now default to
  long-term retention instead of 365 days.
- A 50,000-event capacity guard preserves the newest genuine observations and
  prevents unbounded storage growth.
- Temporary navigation, charging and push-deduplication state now has explicit
  age and count limits.
- New backup, restore and storage-status commands support safe VPS migration.
- TeslaMate PostgreSQL remains read-only and is never pruned by Companion.

This release is backward compatible with My T. Updating is recommended, not
required for ordinary TeslaMate features.
