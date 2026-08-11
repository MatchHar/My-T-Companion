# My T Companion 1.10.15

## Fix (HostBox / update build)

- Docker build now **lists production Go sources explicitly** (including
  `lock_secure_notification.go`) instead of relying only on `COPY *.go`.
- Installer **fails fast** if any required source is missing and prints the
  build-context file list (HostBox only shows the last few KB of SSH output).
- `docker compose build --progress=plain` before `up`, so compile errors are
  visible in HostBox dialogs.
- Runtime memory limit raised to **512m** (extra MQTT monitors).

Includes 1.10.13–1.10.14 lock-secure feature. Still **backward compatible**
with older My T (minimum App 3.10 / recommended 3.30 unchanged).

## Upgrade

```sh
sudo MY_T_VERSION=1.10.15 /opt/my-t-companion/update.sh
```

Or HostBox → 组件版本 → Companion → update to **1.10.15**.

If HostBox still fails, open the dialog fully or SSH and check the plain
`docker compose build` lines; optional:

```sh
sudo tail -100 /var/log/hostbox-companion-install.log
```
