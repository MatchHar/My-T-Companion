# My T Companion 1.10.14

## 修復

- **HostBox / 更新 1.10.13 編譯失敗：** 安裝腳本只按固定檔案列表複製 `.go`，漏了
  `lock_secure_notification.go`，導致 Docker 建置報錯
  `undefined: lockSecureNotificationMonitor` 等。
- 現已改為複製發布包內全部 `*.go` 檔案。

## 相容性

- 功能與 1.10.13 相同（可選鎖車安心推送）。
- **不影響**既有 My T 版本（最低 3.10 / 建議 3.30 未改）。

## 升級

```sh
sudo MY_T_VERSION=1.10.14 /opt/my-t-companion/update.sh
```

或在 HostBox 中再次更新 Companion 到 **1.10.14**。
