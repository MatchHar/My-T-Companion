# My T Companion 1.10.15

## 修復（HostBox / 更新編譯）

- Dockerfile **明確列出**生產用 Go 原始檔（含 `lock_secure_notification.go`）。
- 安裝前檢查缺檔會直接失敗，並印出建置目錄檔案列表。
- 使用 `docker compose build --progress=plain`，方便在 HostBox 彈窗看到完整編譯錯誤。
- 執行記憶體上限調至 **512m**。

含 1.10.13–1.10.14 鎖車安心功能；**不影響**舊版 My T。

## 升級

```sh
sudo MY_T_VERSION=1.10.15 /opt/my-t-companion/update.sh
```

或 HostBox → 元件版本 → 更新到 **1.10.15**。
