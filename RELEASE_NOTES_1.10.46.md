# My T Companion 1.10.46

- Fix HostBox and CLI upgrades of 1.10.45. The installer now copies
  `internal/friendtogether` into the Docker build context, so
  `COPY internal ./internal` no longer fails with `"/internal": not found`.
- Keep Friend Together off by default. Parking, navigation, pairing, APNs, and
  existing capabilities stay unchanged on a normal upgrade.
- 1.10.45 rolled back automatically on failed builds; this release is the
  upgrade path from that rollback.

No database, Tesla token, or stored-data migration is required.
