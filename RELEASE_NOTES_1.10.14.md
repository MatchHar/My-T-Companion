# My T Companion 1.10.14

## Fix

- **HostBox / `update.sh` build failure on 1.10.13:** installer used a hardcoded
  list of Go sources and did not copy `lock_secure_notification.go` into
  `/opt/my-t-companion`. Docker then failed with:

  ```
  undefined: lockSecureNotificationMonitor
  undefined: newLockSecureNotificationMonitorFromEnvironment
  undefined: lockSecurePutBody
  ```

- `install.sh` now copies every `*.go` file from the release package.

## Compatibility

- Same additive lock-secure feature as 1.10.13.
- Does **not** change App requirements (`minimum_version` 3.10 /
  `recommended_version` 3.30). Previous My T builds keep working.

## Upgrade

```sh
sudo MY_T_VERSION=1.10.14 /opt/my-t-companion/update.sh
# or
sudo /opt/my-t-companion/update.sh
```

In HostBox, run Companion update again to 1.10.14.
