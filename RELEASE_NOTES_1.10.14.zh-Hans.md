# My T Companion 1.10.14

## 修复

- **HostBox / 更新 1.10.13 编译失败：** 安装脚本只按固定文件列表复制 `.go`，漏了
  `lock_secure_notification.go`，导致 Docker 构建报错
  `undefined: lockSecureNotificationMonitor` 等。
- 现已改为复制发布包内全部 `*.go` 文件。

## 兼容性

- 功能与 1.10.13 相同（可选锁车安心推送）。
- **不影响**既有 My T 版本（最低 3.10 / 建议 3.30 未改）。

## 升级

```sh
sudo MY_T_VERSION=1.10.14 /opt/my-t-companion/update.sh
```

或在 HostBox 中再次更新 Companion 到 **1.10.14**。
