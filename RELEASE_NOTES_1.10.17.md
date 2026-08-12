# My T Companion 1.10.17

## Verified release synchronization

- Rebuilds the public package from the fully audited current source so it now
  includes transient GitHub/CDN download retries and the latest verified Go
  dependency updates.
- Uses Go 1.26.5 / Alpine 3.24 with `lib/pq` 1.12.3 and `x/net` 0.56.0.
- Publishes only the exact 1.10.17 archive and SHA-256 checksum. Release
  automation now fails instead of replacing or accepting a conflicting
  same-named asset.
- Keeps the verified pre-update backup and automatic rollback lifecycle.

There is no API or stored-data migration. This release remains backward
compatible with older My T versions.

## Upgrade

```sh
sudo MY_T_VERSION=1.10.17 /opt/my-t-companion/update.sh
```
