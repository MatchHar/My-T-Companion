# My T VPS Companion

[English](README.md) · [简体中文](README.zh-Hans.md) · [繁體中文](README.zh-Hant.md)

> 目前原始碼版本：`1.5.0`。車輛軟體原生推送為選用功能，在 My T 提供安全配對前
> 保持關閉。
>
> App Store 目前公開版本為 My T 3.10，尚未提供停車監控整合。提前安裝本元件
> 不會顯示相關頁面；請等待 My T 版本說明明確列出支援後再作日常使用。

**本元件專為
[My T iPhone App 開發，可於 App Store 下載](https://apps.apple.com/cn/app/my-t/id6780299502)。**
如果您是從 TeslaMate 專案找到這裡，請先透過此連結確認並下載配套的 My T App。

## iPhone 原生車輛軟體更新通知

1.5.0 訂閱 TeslaMate MQTT 中車輛真實回報的 `update_available`、
`update_version` 和目前版本變化，不猜測可用版本、不存取 Tesla，也不喚醒車輛。

此功能預設關閉。My T 配對將提供安裝 ID、HTTPS 中繼位址和每次安裝獨立的密鑰，
三項必須同時設定。事件使用 HMAC-SHA256 簽署，並在獨立 Docker 資料卷中保存
去重狀態。推送內容不包含 VIN、位置、TeslaMate 憑證、資料庫密碼、電池、路線或
行駛歷史。Apple APNs 私鑰絕不會放入本公開專案或使用者 VPS。

狀態介面：`GET /api/v1/notifications/software-update/status`。

My T 的完整產品介紹、TeslaMateAPI 部署、連線安全及故障排查，請查看
[My T 公開文件倉庫](https://github.com/MatchHar/My-T-App)。

My T VPS Companion 是部署於 TeslaMate 伺服器的選用獨立元件，為 My T
提供完整的車輛狀態歷史與可靠的即時行駛軌跡。長期停車監控與即時導航共用
同一個容器、驗證方式、安裝指令及更新流程。

元件只讀取現有 TeslaMate PostgreSQL 資料庫，不修改 TeslaMate、不建立資料表，
也不會複製、刪除或改寫車輛歷史。TeslaMate 資料庫始終是唯一資料來源，歷史
保留時間取決於使用者自己的 TeslaMate 資料庫及備份策略。

請在 **TeslaMate 已部署並正常運作之後** 安裝此元件。它不能取代 TeslaMate
或 TeslaMateAPI。

## 為什麼 My T 需要這個補充元件

[My T](https://apps.apple.com/cn/app/my-t/id6780299502) 是用於查看使用者自建
TeslaMate 資料的 iPhone App。一般行程、充電、統計及目前車輛資訊繼續由標準
TeslaMate/TeslaMateAPI 提供，此元件只補充兩個手機端無法可靠還原的情境：

1. **長期停車監控**：需要完整的 `online`、`offline`、`asleep` 狀態順序，
   以及每次狀態切換前後的真實電量與續航。App 關閉期間發生的事件無法由手機
   事後重建。
2. **正在行駛時的即時導航**：需要 TeslaMate 儲存的最早真實 GPS 點及後續
   增量軌跡。My T 開啟時看到的位置不能被當作真實行程起點。

VPS Companion 只提供這些缺少的唯讀能力。TeslaMate 仍是唯一資料來源；
My T 偵測到 `/api/v1/capabilities` 後會自動啟用增強顯示。

### My T 功能對照

| My T 功能 | 未部署元件 | 已部署元件 |
| --- | --- | --- |
| 行程、充電、統計 | 使用標準 TeslaMate API，正常可用 | 保持不變 |
| 基礎停車歷史 | 依一般行程及停車記錄顯示 | 保持不變 |
| 休眠與喚醒流水 | 可能不完整，My T 不會估算缺失事件 | 顯示 TeslaMate 真實儲存的完整狀態順序 |
| 停車電量與續航變化 | 僅在已有真實資料時顯示 | 顯示切換前後 30 分鐘內的真實邊界觀測 |
| 停車途中充電 | 充電記錄正常可查 | 可與停車狀態流水結合顯示 |
| 正在行駛地圖 | 沒有真實起點時只顯示車輛位置及速度 | 顯示不可變真實起點及增量真實軌跡 |

此元件完全為選用。My T 會自動偵測；使用者不需要在 App 內新增伺服器、帳號或車輛連線。

## TeslaMate 部署於家庭或私人內網

本元件可以讀取部署於內網的 TeslaMate 資料庫，但 My T 必須透過**同一個統一
存取位址**連線 TeslaMateAPI 與本元件。My T 會在已設定的 TeslaMate 伺服器
位址偵測 `/api/v1/capabilities`，不需要、也不會另外設定元件位址。

| 內網架構 | 使用結果 |
| --- | --- |
| TeslaMateAPI 與本元件經由同一個 Caddy/Nginx/Traefik 位址轉送 | 支援 |
| My T 直接連線 `http://內網IP:8081`，沒有反向代理 | TeslaMate 基礎功能可用，但無法存取本元件 |
| 透過 VPN、Tailscale 或其他私人網路存取同一個反向代理位址 | 支援 |

元件的 `8083` 連接埠會刻意維持只綁定 `127.0.0.1`，不能直接開放至內網或公網。
原本直接使用 `8081` 的使用者，需要先增加一個統一反向代理：將三個元件介面
轉送至 `127.0.0.1:8083`，其餘 TeslaMateAPI 請求轉送至
`127.0.0.1:8081`，然後在 My T 中使用這個統一位址。倉庫內的
`Caddyfile.snippet` 已列出所需介面。

安裝程式可以自動處理能夠識別的系統 Caddy。Nginx、Traefik、容器版 Caddy 或
自訂內網閘道需要管理員手動加入路由；只安裝容器、未設定統一入口時，My T
不會顯示增強功能。

## 資料如何流動

```text
車輛 → TeslaMate → PostgreSQL
                       │ Docker 內網唯讀連線
                       ▼
               My T VPS Companion
                       │ 現有 HTTPS/API 驗證
                       ▼
                    My T App
```

- Tesla 帳號授權始終由 TeslaMate 管理。
- VPS Companion 不連線 Tesla，也不會喚醒車輛。
- My T 不會把車輛歷史轉送至開發者營運的雲端。
- 資料只在使用者自己的 VPS 與 iPhone 之間，透過現有安全網域或私人網路傳輸。

## 部署後會建立什麼

| 項目 | 是否建立或修改 | 用途 |
| --- | --- | --- |
| 獨立 Docker 服務與容器 | 是 | 執行唯讀 Companion API |
| `127.0.0.1:8083` 本機監聽連接埠 | 是 | 讓服務始終位於現有受保護的反向代理之後 |
| 三條反向代理路由 | 是 | 透過 My T 原有伺服器位址提供能力偵測、停車狀態及目前行駛介面 |
| 安裝設定與復原備份 | 是 | 支援重複更新、回復及解除安裝 |
| 新資料庫或 TeslaMate 資料表 | 否 | TeslaMate PostgreSQL 始終是唯一資料來源 |
| 重複的車輛歷史儲存 | 否 | 僅在 My T 請求時查詢資料 |
| 修改 TeslaMate 資料或傳送車輛指令 | 否 | 資料庫連線強制唯讀，也不連線 Tesla |

服務只讀取介面所需的 TeslaMate `states`、`drives` 及 `positions` 資料，回傳
整理後的狀態區間、附近的真實電量/續航觀測、目前行程不可變的第一個位置，
以及增量軌跡點。它沒有背景採集器，也不設定獨立保留期限；可查詢多久完全
取決於 TeslaMate 資料庫內的歷史。

## 各版本提供的能力

| Companion 版本 | 新增能力 |
| --- | --- |
| 1.0.0 | 唯讀停車狀態歷史介面 |
| 1.1.0 | 沿用現有 TeslaMate API 驗證邊界 |
| 1.2.0 | 包含觀測時間及新鮮度限制的電量與額定續航資料 |
| 1.3.0 | 不可變的真實行程起點與增量軌跡分頁 |
| 1.4.0 | 能力偵測、容器與資料庫加固、安全安裝及解除安裝 |
| 1.4.1 | 校驗版更新、回復備份、統一入口驗證及更完整的內網/代理說明 |

完整改動請查看 [CHANGELOG.md](CHANGELOG.md)，1.4.1 公開候選說明請查看
[RELEASE_NOTES_1.4.1.md](RELEASE_NOTES_1.4.1.md)。

## 哪些使用者需要安裝

符合以下條件時建議安裝：

- 在 My T 中使用自建 TeslaMate 資料來源。
- 希望查看可靠的長期停車休眠/喚醒歷史，或完整的正在行駛軌跡。
- 可以管理 TeslaMate Docker 主機並執行 `sudo` 指令。
- TeslaMate API 已透過 HTTPS、VPN 及驗證進行保護。

以下情況不需要安裝：

- My T 只連線 Tessie。
- 只需要基礎行程、充電及統計功能。
- 無法管理 TeslaMate 伺服器。

元件不會提高 TeslaMate 的採集能力，只能回傳 TeslaMate 實際儲存的資料。

## 校驗後安裝固定版本

安裝使用固定 GitHub Release，不直接執行會變動的 `main` 分支。私有測試期間：

```sh
version=1.4.1; workdir="$(mktemp -d)" && gh release download "v$version" -R MatchHar/My-T-Parking-Monitor -D "$workdir" && (cd "$workdir" && sha256sum -c "my-t-parking-monitor-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-parking-monitor-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-parking-monitor-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

倉庫及正式 Release 公開後：

```sh
version=1.4.1; workdir="$(mktemp -d)" && base="https://github.com/MatchHar/My-T-Parking-Monitor/releases/download/v$version" && curl -fL "$base/my-t-parking-monitor-$version.tar.gz" -o "$workdir/my-t-parking-monitor-$version.tar.gz" && curl -fL "$base/my-t-parking-monitor-$version.tar.gz.sha256" -o "$workdir/my-t-parking-monitor-$version.tar.gz.sha256" && (cd "$workdir" && sha256sum -c "my-t-parking-monitor-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-parking-monitor-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-parking-monitor-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

只有本機服務和 My T 統一入口都驗證成功，安裝程式才報告完整成功。手動使用
Nginx、Traefik 或容器代理時，必須加入並驗證倉庫提供的路由。安裝程式還會：

- 自動偵測 TeslaMate 資料庫容器及 Docker 網路。
- 沿用現有資料庫密碼與 API 驗證，不要求在 My T 新增第二組帳號。
- 支援 Bearer Token、Basic Auth、`X-API-Token` 與 Cloudflare Access
  Service Token。
- 修改反向代理前建立備份。
- 檢查現有 `/api/ping` 是否受到驗證保護；若公開可存取則拒絕安裝。
- 自動處理可識別的系統 Caddy 設定。
- 對無法安全識別的 Nginx、Traefik、容器化 Caddy 或自訂配置停止自動修改，
  並提示手動加入 `Caddyfile.snippet` 內的路由。

## 提供的功能

- 顯示 TeslaMate 真實記錄的 `online`、`offline`、`asleep` 等狀態區間。
- 顯示狀態開始前及結束後的真實電量百分比與額定續航。
- 邊界資料包含真實觀測時間；僅使用狀態切換前後 30 分鐘內的採樣。
- 沒有真實採樣時顯示未知，不估算喚醒事件、電量或續航消耗。
- 提供能力偵測介面，讓 App 區分「未部署」及「已部署但沒有事件」。
- 即時計算仍在進行的停車狀態，建議 App 每 30 秒重新整理。
- 提供目前 TeslaMate 行程 ID、不可變的最早真實 GPS 點及帶時間的軌跡點。
- 支援 `afterPointId` 增量分頁，避免手機反覆下載完整行駛軌跡。
- TeslaMate 已建立行程但尚未儲存有效 GPS 點時，明確回傳
  `waiting_for_positions`。
- 不會把 App 開啟時看到的車輛位置當作真實行程起點。

## API 介面

- `GET /api/v1/capabilities`
- `GET /api/v1/cars/{car_id}/states?startDate=...&endDate=...`
- `GET /api/v1/cars/{car_id}/navigation/current-drive?afterPointId=0&limit=5000`
- `GET /api/healthz`

所有車輛資料及能力介面都使用現有 TeslaMate API 驗證。`/api/healthz`
不包含車輛資料，而且服務連接埠只綁定於本機回環位址。

## 安全部署要求

- Linux 主機已安裝 Docker Engine 與 Docker Compose v2。
- TeslaMate Docker Compose 已正常運作。
- PostgreSQL 服務預設名稱為 `database`。
- TeslaMate `.env` 內存在 `DATABASE_PASS`。
- 現有 TeslaMate API 已透過 HTTPS、VPN 或 Cloudflare Access 保護。
- 連接埠 `8083` 必須綁定為 `127.0.0.1:8083`，不能直接開放到公網。

1.4.1 預設安全措施：

- 容器以非 root 使用者 UID 10001 執行。
- 根檔案系統唯讀。
- 移除全部 Linux capabilities。
- 啟用 `no-new-privileges`。
- PostgreSQL 工作階段強制設定
  `PGOPTIONS=-c default_transaction_read_only=on`。
- 健康檢查同時驗證服務及資料庫連線。
- HTTP 服務設定讀取、寫入、請求標頭及閒置逾時。
- API Token 使用固定時間比較。

詳細要求請閱讀 [SECURITY.md](SECURITY.md)。

## 驗證

```sh
curl --fail http://127.0.0.1:8083/api/healthz
curl --fail \
  -H "Authorization: Bearer ${MY_T_API_TOKEN}" \
  http://127.0.0.1:8083/api/v1/capabilities
```

第一條應回傳 `OK`。第二條應包含：

- `parking_state_history`
- `state_boundary_battery`
- `state_boundary_rated_range`
- `current_drive_trajectory`

還應確認未驗證請求被拒絕：

```sh
curl -o /dev/null -w "%{http_code}\n" \
  http://127.0.0.1:8083/api/v1/capabilities
```

預期回傳 `401`。

## 更新

已安裝的更新程式會下載最新固定版本、校驗 SHA-256、備份目前安裝，再執行
可重複執行的安裝程式：

```sh
sudo /opt/my-t-parking-monitor/update.sh
```

指定版本可執行：
`sudo MY_T_VERSION=1.4.1 /opt/my-t-parking-monitor/update.sh`。

元件沒有獨立資料庫或資料遷移。更新不會改變 TeslaMate 歷史資料。

## 解除安裝或回復

由安裝程式管理的部署可執行：

```sh
sudo /opt/my-t-parking-monitor/uninstall.sh
```

解除安裝程式會停止並移除獨立 Companion 容器，以及安裝程式建立的 Caddy 路由；
`/opt/my-t-parking-monitor` 會保留以便復原。手動設定的反向代理路由需要手動刪除。
解除安裝不影響 TeslaMate 主服務及其資料庫。

## 資料含義

對於 `offline` 或 `asleep` 區間：

- `start_telemetry` 是車輛休眠前最後一次真實觀測。
- `end_telemetry` 是 TeslaMate 在車輛恢復後記錄的第一次真實觀測。

兩者差值是「觀測到的停車消耗」，不是估算值。正在進行的休眠在車輛真正喚醒前
不會產生結束觀測。

## 未安裝時的 App 行為

My T 透過 `/api/v1/capabilities` 自動偵測元件。未安裝或無法使用時：

- 基礎 TeslaMate 行程、充電及停車記錄繼續正常運作。
- App 會說明完整休眠、喚醒、電量與續航流水需要選用 VPS 元件。
- App 不會虛構缺失事件或估算電量消耗。
- 即時導航沒有足夠真實軌跡點時，只顯示真實車輛位置及速度，不偽造起點或路線。

## 相容性與版本記錄

- [相容性與發布驗證](COMPATIBILITY.md)
- [安全政策](SECURITY.md)
- [版本更新記錄](CHANGELOG.md)
- [MIT License](LICENSE)

## 專案範圍與獨立性聲明

這是專為 My T 設計的獨立補充專案，與 Tesla, Inc.、TeslaMate 官方維護者及
TeslaMateAPI 維護者不存在隸屬或官方認可關係。元件不儲存 Tesla 帳號憑證、
不傳送車輛控制指令、不喚醒車輛，也不取代 TeslaMate 官方部署。

## 常見問題

### 會修改或複製 TeslaMate 歷史嗎？

不會。所有查詢均在 PostgreSQL 唯讀交易模式下執行。元件沒有自己的資料庫、
資料遷移或背景採集器。

### 可以找回以前缺失的喚醒記錄嗎？

可以顯示仍儲存在 TeslaMate `states` 表中的歷史事件，但無法復原 TeslaMate
從未採集或已依使用者保留策略刪除的資料。

### 會喚醒車輛或增加停車耗電嗎？

不會。元件只讀取資料庫，不呼叫任何車輛控制介面。

### 安裝後需要修改 My T 設定嗎？

通常不需要。安裝程式沿用現有 TeslaMate API 網域及驗證，My T 會自動偵測能力介面。

### 解除安裝會影響 TeslaMate 嗎？

不會。Companion 是獨立 Compose 專案；解除安裝程式只移除自身容器及安裝程式
管理的代理路由，TeslaMate 主服務及資料庫保持不變。
