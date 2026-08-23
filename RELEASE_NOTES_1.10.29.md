# My T Companion 1.10.29

## Correct TeslaMate version reporting

- Companion now reads the current TeslaMate version from the live internal
  TeslaMate settings page. An old `TESLAMATE_VERSION` saved during installation
  can no longer override a successful live probe after TeslaMate is upgraded.
- The capabilities response includes `teslamate_version_source` and
  `teslamate_version_checked_at`, allowing My T to distinguish current data
  from installation fallback metadata.
- Install and update flows derive the fallback version from the running
  TeslaMate container before consulting an older saved value.

## Release and delivery hardening

- The new version probe is included in installer, Docker build, and release
  completeness checks.
- Trusted deployment tools may verify the release archive against the digest
  in the separately signed HostBox catalog.
- Navigation sessions left active for more than 12 hours now end through the
  normal terminal-event path.
- Release tags include vulnerability checks, reproducible archives, checksums,
  and build provenance.

No database migration or push re-pairing is required.
