# 相容性與版本驗證

[English](COMPATIBILITY.md) · [简体中文](COMPATIBILITY.zh-Hans.md) · 繁體中文

1.5.0 加入選用的 TeslaMate MQTT 軟體更新觀察與簽章中繼傳送。1.7.1
加入目的地導航即時動態傳送，並保留充電即時動態及已修補的建置相依套件，API
與部署方式沒有改變。未配對推播時，停車與導航功能仍可使用。

1.10.32 與 TeslaMate 4.2.0、上一個穩定版 4.1.1 及 TeslaMateAPI 1.25.0
相容。導航進度持續更新沿用現有訂閱裝置登記與 TeslaMate 行程位置，
不需要資料庫遷移或重新配對。

## 必要基礎環境

- 執行 Docker Engine 與 Docker Compose v2 的 Linux 主機。
- 已存在且運作正常的 TeslaMate Docker Compose 部署。
- PostgreSQL 可從 TeslaMate Docker 網路存取；預設服務名稱為 `database`，安裝程式也會檢查 `db`、`postgres` 與容器標籤。
- 不依賴 TeslaMate `.env` 檔案也能取得 `DATABASE_PASS`，來源可為 Compose 設定、容器環境變數、Shell 環境或選用 `.env`。
- TeslaMate 資料庫包含 `cars`、`states`、`positions` 與 `drives`。
- 現有 API 使用 Bearer Token、Basic Authentication、`X-API-Token` 或 Cloudflare Access Service Token 驗證。
- Gateway 已涵蓋停車、導航、能力探索及 `/api/v1/notifications/*` 路徑，包括即時動態狀態。

## 版本測試矩陣

每個穩定版本都必須記錄以下結果：

| 範圍 | 必測情境 |
| --- | --- |
| 主機 | Ubuntu 22.04 與 24.04，amd64 |
| 架構 | amd64 與 arm64 映像建置 |
| TeslaMate | 目前穩定版及上一個穩定版 |
| 代理 | 系統 Caddy 自動安裝；Nginx/Traefik 手動說明 |
| 驗證 | Bearer、Basic、X-API-Token、Cloudflare Access |
| 生命週期 | 全新安裝、重複安裝/更新、失敗回復、解除安裝 |
| 停車 | 休眠/喚醒/再休眠、開放休眠、停車中充電、缺少遙測、跨午夜 |
| 導航 | 無行程、等待第一個點、增量點、分頁、Drive ID 變更 |
| 失敗 | 資料庫無法使用、Token 錯誤、公開 `/api/ping`、連接埠占用、未知 Compose 配置 |

## 已知候選版本限制

- 自動編輯反向代理只支援系統 Caddy 服務。
- 其他代理配置必須提供並驗證 `MY_T_BASE_URL`；僅限本機且未經驗證的服務會回報為未完成，不會誤報成功。
- 預設 TeslaMate 目錄為 `/opt/teslamate`；其他配置請使用 `TESLAMATE_DIR`。
- 預設資料庫 Compose 服務名稱為 `database`。
- 歷史狀態回應目前尚未分頁；用戶端應要求有時間範圍的停車時段。
