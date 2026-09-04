# My T Companion

[English](README.md) · [简体中文](README.zh-Hans.md) · [繁體中文](README.zh-Hant.md)

> [![最新穩定版](https://img.shields.io/github/v/release/MatchHar/My-T-Companion?display_name=tag&sort=semver)](https://github.com/MatchHar/My-T-Companion/releases/latest)
>
> 上方徽章及[最新穩定版](https://github.com/MatchHar/My-T-Companion/releases/latest)連結會自動更新。車輛軟體原生推送、充電鎖屏即時活動及導航即時活動均為選用功能，在相容的 My T 版本
> 提供安全配對前保持關閉。
>
> **My T：** App Store 公開可下載版本以
> [Apple 產品頁](https://apps.apple.com/app/id6780299502)為準。
> 較新版本可能正在審查中，本儲存庫不寫死版本號，也不聲明非公開審查版本。
> 停車流水、觀測事件、軌跡及目的地行程記錄在可存取 `/api/v1/capabilities` 時
> 可用；推播及即時動態仍需完成配對。詳見
> [My T 功能可用性](https://github.com/MatchHar/My-T-App/blob/main/docs/FEATURE_AVAILABILITY.md)。

安全配對後，每支 iPhone 可以個別開啟「上鎖且車內無人」通知。伺服器上的選擇是
所有車輛的預設；相容的 My T 版本可依車輛名稱，為每一類通知分別建立覆寫，新加入
車輛會自動沿用預設。在 My T 切換目前選擇的車輛不會改變伺服器端通知範圍。
Companion 只會傳送簽署事件；可見訊息及提示音由
My T 在手機上處理。匯入音訊、檔名及靜音／預設選擇均不會傳送到 Companion 或
推播中繼。

每支 iPhone 也可以個別開啟低電量提醒；該偏好預設涵蓋其所配對 TeslaMate
伺服器上的全部車輛，也可為單一車輛覆寫。任一車輛停妥、未充電且電量嚴格低於 20% 時提醒一次，低於
10% 時可再發一次較強提醒，電量達到 25% 後才重新布防。第一個完整
MQTT 保留快照只建立基線，不會在安裝或重啟時誤報。通知可選擇「知道了」或「4 小時
後提醒」；操作狀態按手機保存在使用者自己的 VPS，充電會取消待提醒。Companion
只讀 TeslaMate 已回報的資料，不輪詢 Tesla，也不會喚醒車輛。

**本元件專為
[My T iPhone App 開發，可於 App Store 下載](https://apps.apple.com/app/id6780299502)。**
如果您是從 TeslaMate 專案找到這裡，請先透過此連結確認並下載配套的 My T App。

**需要部署伺服器堆疊？[從 App Store 下載 HostBox](https://apps.apple.com/app/id6798103086)。** [產品說明與上線影片](https://my-tesla.app/hostbox/zh-hant/)介紹如何直接在 iPhone 引導部署 VPS。

## 導航鎖屏即時活動

1.7.0 讀取 TeslaMate MQTT 真實回報的目的地導航及車輛行駛狀態。相容的 My T
版本即使沒有開啟，也可以自動開始、更新及結束鎖屏／靈動島導航卡片。

卡片使用真實目的地、剩餘里程／時間、預計到達、車輛預測到達電量，以及由目前
TeslaMate 行程驗證的進度。中繼不會接收座標、軌跡、VIN、TeslaMate 憑證或車輛
歷史；缺少資料不會估算。

未部署本元件時，My T 的 App 內目的地卡片、車輛即時位置及速度仍可正常使用；
真實起點、已行駛軌跡、全程進度及主動鎖屏推送需要部署並完成配對。

狀態介面：`GET /api/v1/notifications/navigation-live-activity/status`。

## 充電鎖屏即時活動

1.6.1 讀取 TeslaMate MQTT 真實回報的充電狀態、電量百分比、額定續航、充電上限、
功率與剩餘時間。相容的 My T 版本即使沒有開啟，也可以自動顯示並更新鎖屏／靈動島
充電卡片。

一般更新最短間隔為 45 秒，功率達到 50 kW 時縮短為 15 秒，並會合併短時間內的多項變化。簽署事件只包含卡片所需
欄位，不包含 VIN、位置、路線、TeslaMate 憑證或 kWh。續航增加只在 TeslaMate
具有真實起始與目前 `rated_battery_range_km` 時運算；缺少時不估算公里數。

狀態介面：`GET /api/v1/notifications/charging-live-activity/status`。

## iPhone 原生車輛軟體更新通知

1.5.0 訂閱 TeslaMate MQTT 中車輛真實回報的 `update_available`、
`update_version` 和目前版本變化，不猜測可用版本、不存取 Tesla，也不喚醒車輛。

此功能預設關閉。My T 配對將提供安裝 ID、HTTPS 中繼位址和每次安裝獨立的密鑰，
三項必須同時設定。事件使用 HMAC-SHA256 簽署，並在獨立 Docker 資料卷中保存
去重狀態。推送內容不包含 VIN、位置、TeslaMate 憑證、資料庫密碼、電池、路線或
行駛歷史。Apple APNs 私鑰絕不會放入本公開專案或使用者 VPS。

狀態介面：`GET /api/v1/notifications/software-update/status`。
使用者在 My T 中啟用通知後，App 會透過現有已驗證連線自動寫入配對：
`POST /api/v1/notifications/software-update/pair`。為防止伺服器端請求偽造
（SSRF），元件只接受 My T 官方中繼位址，不允許設定任意伺服器。

從 1.10.37 起，相容的 App 可加入選用 vehicle_preferences 陣列。每項包含一個
正數 TeslaMate car_id 和完整的分類開關；未列出的車輛沿用頂層所有車輛預設。
省略欄位會保留已有覆寫，避免舊版 App 清除自己無法顯示的設定；明確傳送空陣列才
清除所有覆寫。

之所以需要官方中繼，是因為任意使用者 VPS 沒有 App 持有的 APNs 憑證，不能直接
向 Apple Push Notification service 傳送 My T 通知。中繼只儲存投遞所需的 APNs
裝置權杖與不透明安裝 ID，並只接收上文所述的最少軟體更新事件。每個 VPS 使用
自己的獨立簽章，一個安裝不能代替另一個安裝傳送通知。

完成推送配對後，Companion 亦會提交供營運統計使用的匿名車輛清單。它在自己的
資料卷中產生並保存隨機命名空間，再為該已配對伺服器上的每輛 TeslaMate 車輛
透過 HMAC 衍生一個穩定別名。中繼只保留別名及首次／最後出現時間，不會把別名
與安裝 ID 一起保存，也不會接收原始車輛 ID、車輛名稱、VIN、伺服器位址、Apple
ID、手機身分、位置、路線、遙測、軟體版本或通知內容。同一輛車在一個 Companion
配對多支 iPhone 時仍只計算一次。

My T 的完整產品介紹、TeslaMateAPI 部署、連線安全及故障排查，請查看
[My T 公開文件倉庫](https://github.com/MatchHar/My-T-App)。

My T Companion 是部署於 TeslaMate 伺服器的選用獨立元件，為 My T
提供完整的車輛狀態歷史、已保留的停車 MQTT 事件觀測（插槍、充電、保全、空調等）
及可靠的即時行駛軌跡。停車增強與即時導航共用同一個容器、驗證方式、安裝指令
及更新流程。

元件唯讀現有 TeslaMate PostgreSQL 資料庫，不修改 TeslaMate、不建立資料表，
也不會複製、刪除或改寫資料庫歷史。從 1.9.2 開始，元件會在自己的資料卷保存
TeslaMate 未長期保存的真實 MQTT 狀態變化，例如開始充電前已插槍。安裝或重新
啟動後的第一個保留值只建立基線，不會產生假事件。事件時間代表「TeslaMate／
Companion 首次觀測時間」，不冒充更精確的實體操作時間。停車事件預設長期保留，
並以最新 50,000 筆作為容量保護；導航及推送等暫時狀態按各自期限自動清理。
詳細分類請參閱[資料生命週期](DATA_LIFECYCLE.zh-Hant.md)。

若中繼暫時無法使用，或 ActivityKit 尚未登記該工作階段權杖，Companion 只會在
使用者自己的 VPS 上，以 `0600` 權限暫存完成重試所需的最少簽署事件。重試記錄
依事件類型在 10 分鐘至 24 小時內到期，總數上限為 256；投遞成功、暫停或解除
配對時立即刪除，不會進入開發者中繼的投遞稽核。

請在 **TeslaMate 已部署並正常運作之後** 安裝此元件。它不能取代 TeslaMate
或 TeslaMateAPI。

### 部署方式：HostBox 或自建

| 路徑 | 對象 | 說明 |
| --- | --- | --- |
| **[HostBox](https://apps.apple.com/app/id6798103086)**（多數手機使用者推薦） | App Store 上的 iOS 部署 App | 安裝 TeslaMate + API + 本元件，盡量配置**統一入口**（臨時 IP 的 edge，或 Tunnel 路徑）、MQTT `host.docker.internal` 與健康檢查。完成後在 My T 只填**一個** base_url + Token；詳見[產品說明](https://my-tesla.app/hostbox/zh-hant/)。 |
| **本倉庫自建** | 自己跑 Docker / `install.sh` | 依下方校驗版安裝，並配置閘道分流（見連接埠說明）。My T 連線規則相同。 |

HostBox 是**部署層**；My T 是看車 App。二者不互相替代。

## 為什麼 My T 需要這個補充元件

[My T](https://apps.apple.com/app/id6780299502) 是用於查看使用者自建
TeslaMate 資料的 iPhone App。一般行程、充電、統計及目前車輛資訊繼續由標準
TeslaMate/TeslaMateAPI 提供，此元件只補充三個手機端無法可靠還原的情境：

1. **停車增強**：需要完整的 `online`、`offline`、`asleep` 狀態順序，
   以及每次狀態切換前後的真實電量與續航。App 關閉期間發生的事件無法由手機
   事後重建。
2. **正在行駛時的即時導航**：需要 TeslaMate 儲存的最早真實 GPS 點及後續
   增量軌跡。My T 開啟時看到的位置不能被當作真實行程起點。
3. **停車事件**（插槍／拔槍、充電、哨兵、鎖車／門、空調）：需要持續運作的
   觀測服務；iOS 在 My T 被暫停後不能可靠保存這些事件。Companion 1.9.2+ 會
   保留 TeslaMate 未作為歷史保存的真實 MQTT 變化。

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
| 插槍、安全及空調事件 | My T 關閉後沒有持續歷史 | 保存部署後的真實 MQTT 變化；只顯示實際回報的電量與續航 |
| 正在行駛地圖 | 沒有真實起點時只顯示車輛位置及速度 | 顯示不可變真實起點及增量真實軌跡 |

此元件完全為選用。My T 會自動偵測；使用者不需要在 App 內新增伺服器、帳號或車輛連線。

## TeslaMate 部署於家庭或私人內網

本元件可以讀取部署於內網的 TeslaMate 資料庫，但 My T 必須透過**同一個統一
存取位址**連線 TeslaMateAPI 與本元件。My T 會在已設定的 TeslaMate 伺服器
位址偵測 `/api/v1/capabilities`，不需要、也不會另外設定元件位址。

| 內網架構 | 使用結果 |
| --- | --- |
| TeslaMateAPI 與本元件經由同一個 Caddy/Nginx/Traefik 位址轉送 | 支援 |
| My T 直接連線「主機上的 TeslaMateAPI 連接埠」（示例常寫 8080 或 8081），沒有統一反向代理 | TeslaMate 基礎功能可用，但無法存取本元件 |
| 透過 VPN、Tailscale 或其他私人網路存取同一個反向代理位址 | 支援 |

**連接埠說明：** 容器內 TeslaMateAPI 固定為 **8080**；對應到主機時可用 **8080 或 8081** 等（HostBox 預設主機 **8081**）。Companion 主機 **8083** 僅本機，不要在 My T 再填第二個位址。

元件的 `8083` 會刻意維持只綁定 `127.0.0.1`，不能直接開放至內網或公網。
原本直接使用「主機 API 連接埠、無反代」的使用者，需要先增加**統一反向代理（Gateway）**：將 Companion 路徑轉送至 `127.0.0.1:8083`，其餘 TeslaMateAPI 轉送至內部 API 位址，然後在 My T 中只使用這個**統一 base_url**。倉庫內的
`Caddyfile.snippet` 已列出所需介面。

安裝程式可以自動處理能夠識別的系統 Caddy，也可以處理與 Companion 共用已驗證
Docker 網路的 TeslaMate 容器版 Caddy。系統閘道轉送至 `127.0.0.1:8083`；容器版
Caddy 則在不放寬主機 8083 本機綁定的前提下直連 `companion:8080`。Nginx、
Traefik、無法識別的容器代理或自訂內網閘道仍需要管理員手動加入路由；只安裝
容器、未設定統一入口時，My T 不會顯示增強功能。

### 建議順序（方法不變，只是常見三階段）

不改變既有部署方法，多數人會按此順序：

1. **TeslaMate + TeslaMateAPI** — My T 填 `http://IP:主機API埠`（或已有 HTTPS 反代）。基礎看車可用。
2. **Companion** — **仍用同一個** My T base_url。必須有統一入口（系統 Caddy、`Caddyfile.snippet`、或安裝程式 edge）把擴充路徑轉到 `127.0.0.1:8083`，其餘 `/api/*` 轉官方 API。Token 不變。
3. **Cloudflare Tunnel（可選安全）** — My T 改填 `https://你的域名`。Tunnel 成為新的統一入口；分流概念與 Caddy 相同（`Caddyfile.snippet` 路徑 → `8083`，其餘 `api/*` → 官方 API 主機埠）。

#### 從臨時 IP / edge 切換到 Cloudflare Tunnel

若第 2 步用了**佔用公網 API 埠的 edge**（API 常被挪到本機 `18081`），第 3 步必須**收口**，只留一個公網入口：

| 要做 | 原因 |
| --- | --- |
| Tunnel 擴充路徑 → `http://127.0.0.1:8083` | 與 `Caddyfile.snippet` 一致 |
| Tunnel 其餘 `api/*` → **TeslaMateAPI 實際監聽的埠**（常見是恢復庫存 `127.0.0.1:8081` 或 `8080`，不要指向已拆除的 edge） | 指錯會導致 My T **HTTP 502** |
| 停掉臨時 IP 的 edge，避免繼續佔用 `8081` | 兩套入口搶埠 |
| My T 改用 `https://域名`（不要再用 `http://IP:8081`） | 只換外層入口；Token 不變 |

Tunnel 完成後的乾淨目標（「一個 base_url」契約）：

```text
https://域名
  ├─ 擴充路徑  → 127.0.0.1:8083
  └─ 其餘 /api/* → 127.0.0.1:<官方API主機埠>   # 例如 8081
```

## 資料如何流動

```text
車輛 → TeslaMate → PostgreSQL
                       │ Docker 內網唯讀連線
                       ▼
               My T Companion
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
| Companion 反向代理路由 | 是 | 透過 My T 原有伺服器位址提供能力偵測、停車狀態／事件、目前行駛及通知介面 |
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
| 1.10.43 | 停車事件依序快速接收、合併持久化，並在服務正常停止時完整寫入 |
| 1.10.39 | 充電、導航與鎖車安心推播加入資料來源隔離，並避免即時動態工作階段衝突 |
| 1.10.38 | 四門獨立歷史與狀態、進行中停車觀測、軟體更新來源隔離及路由修復 |
| 1.10.37 | 每支 iPhone 的所有車輛預設與依類別分車覆寫；暫停手機容量恢復 |
| 1.10.36 | 伺服器範圍的所有車輛推播訂閱，並自動遷移舊的目前車輛篩選 |
| 1.10.33 | 導航卡片開始後，TeslaMate 首次解析出真實行程起點時立即傳送修正，不等待一般節流計時 |
| 1.10.32 | 持續刷新已驗證的目的地導航行駛距離，並立即向遠端即時動態傳送首次有效進度 |
| 1.10.31 | 依 Companion 而非手機計算的匿名車輛數量及首次／最後出現時間回報 |
| 1.10.30 | 以 TeslaMate 行程起點為準，並在單台 iPhone 開啟功能時定向補發進行中的充電或導航即時動態 |
| 1.10.29 | 即時讀取 TeslaMate 版本，並標示來源與偵測時間；僅在即時讀取失敗時退回安裝資訊 |
| 1.0.0 | 唯讀停車狀態歷史介面 |
| 1.1.0 | 沿用現有 TeslaMate API 驗證邊界 |
| 1.2.0 | 包含觀測時間及新鮮度限制的電量與額定續航資料 |
| 1.3.0 | 不可變的真實行程起點與增量軌跡分頁 |
| 1.4.0 | 能力偵測、容器與資料庫加固、安全安裝及解除安裝 |
| 1.4.1 | 校驗版更新、回復備份、統一入口驗證及更完整的內網/代理說明 |
| 1.5.0 | 原生軟體更新推送、持久去重、簽署中繼及 App 自動安全配對 |
| 1.5.1 | 修補 MQTT 與 Go 網路依賴，不改變 API 或部署方式 |
| 1.9.3 | 長期容量受控的停車事件、重複配對穩定性、日誌輪替與資源限制 |
| 1.10.0 | 備份／還原流程及有界暫時推播狀態 |
| 1.10.2 | App 相容性資訊、正式推播網域與安全解除配對 |
| 1.10.4 | 帶真實行程時間的目的地導航工作階段歷史 |
| 1.10.5 | 推播歷史路由與死鎖修正 |
| 1.10.6 | 行駛中更改目的地時拆分為完整獨立工作階段 |
| 1.10.7 | 記錄真實起點名稱，用於「起點 → 目的地」標題 |
| 1.10.8 | 更可靠的 `start_name`（圍欄黏性 + 行程地址回填），起點 → 目的地 |
| 1.10.10 | 修正能力及診斷介面的運行版本報告 |
| 1.10.19 | 已儲存 MQTT 設定與實際 Docker 拓撲不一致時仍可可靠升級 |
| 1.10.28 | 固定使用官方中繼、拒絕重新導向、限制導航歷史容量，並同步三語相容性說明 |
| 1.10.27 | 每支 iPhone 個別的持久推播佇列、偏好設定、失效裝置隔離，以及更嚴格的資料庫／網路逾時 |

完整改動請查看 [CHANGELOG.md](CHANGELOG.md) 或
[最新 GitHub Release](https://github.com/MatchHar/My-T-Companion/releases/latest)。

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

安裝使用固定 GitHub Release，不直接執行會變動的 `main` 分支。已安裝 GitHub CLI 時：

```sh
version="$(gh release view -R MatchHar/My-T-Companion --json tagName --jq '.tagName | ltrimstr("v")')"; workdir="$(mktemp -d)" && gh release download "v$version" -R MatchHar/My-T-Companion -D "$workdir" && (cd "$workdir" && sha256sum -c "my-t-companion-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-companion-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-companion-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

官方壓縮檔及校驗檔亦附有 GitHub 建置來源證明。安裝前可獨立驗證已下載檔案：

```sh
gh attestation verify my-t-companion-X.Y.Z.tar.gz \
  --repo MatchHar/My-T-Companion \
  --signer-workflow MatchHar/My-T-Companion/.github/workflows/release.yml
```

未安裝 GitHub CLI 時：

```sh
version="$(curl -fsSL https://api.github.com/repos/MatchHar/My-T-Companion/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')"; test -n "$version" && workdir="$(mktemp -d)" && base="https://github.com/MatchHar/My-T-Companion/releases/download/v$version" && curl -fL "$base/my-t-companion-$version.tar.gz" -o "$workdir/my-t-companion-$version.tar.gz" && curl -fL "$base/my-t-companion-$version.tar.gz.sha256" -o "$workdir/my-t-companion-$version.tar.gz.sha256" && (cd "$workdir" && sha256sum -c "my-t-companion-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-companion-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-companion-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

只有本機服務和 My T 統一入口都驗證成功，安裝程式才報告完整成功。手動使用
Nginx、Traefik 或無法識別的容器代理時，必須加入並驗證倉庫提供的路由。安裝程式還會：

- 自動偵測 TeslaMate 資料庫容器及 Docker 網路。
- 沿用現有資料庫密碼與 API 驗證，不要求在 My T 新增第二組帳號。
- 支援 Bearer Token、Basic Auth、`X-API-Token` 與 Cloudflare Access
  Service Token。
- 修改反向代理前建立備份。
- 檢查現有 `/api/ping` 是否受到驗證保護；若公開可存取則拒絕安裝。
- 自動處理可識別的系統 Caddy，或共用已驗證 TeslaMate 網路的容器化 Caddy。
- 對無法安全識別的 Nginx、Traefik、其他容器代理或自訂配置停止自動修改，
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
- 保存未來觀測到的四門、四窗獨立開關記錄，並在不喚醒休眠車輛的
  情況下提供最後觀測到的車鎖、車門及車窗狀態。

## API 介面

- `GET /api/v1/capabilities`
- `GET /api/v1/cars/{car_id}/states?startDate=...&endDate=...`
- `GET /api/v1/cars/{car_id}/parking-events?startDate=...&endDate=...`
- `GET /api/v1/cars/{car_id}/companion-status`
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

1.7.1 預設安全措施：

- 容器以非 root 使用者 UID 10001 執行。
- 根檔案系統唯讀。
- 移除全部 Linux capabilities。
- 啟用 `no-new-privileges`。
- PostgreSQL 工作階段強制設定
  `PGOPTIONS=-c default_transaction_read_only=on`。
- 健康檢查同時驗證服務及資料庫連線。
- HTTP 服務設定讀取、寫入、請求標頭及閒置逾時。
- API Token 使用固定時間比較。

詳細要求請閱讀[安全性政策](SECURITY.zh-Hant.md)。

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

以下永久指令會跟隨 GitHub 的**最新穩定 Release**（不包含草稿及預發布版），
下載對應的固定版本、校驗 SHA-256、備份目前安裝，再執行可重複執行的安裝程式：

```sh
sudo /opt/my-t-companion/update.sh
```

My T 亦可能顯示類似
`sudo MY_T_VERSION=<已驗證版本> /opt/my-t-companion/update.sh`
的指定版本指令。這是刻意設計：App 固定至該 App 版本已驗證相容的最新
Companion；永久指令則供明確希望跟隨伺服器最新穩定版的管理員使用。
可信任的部署工具也可設定
`MY_T_EXPECTED_SHA256=<簽章目錄中的摘要>`；即使 Release 清單也同時被變更，
更新程式仍會拒絕與簽章目錄不一致的安裝包。

元件沒有獨立資料庫或資料遷移。更新不會改變 TeslaMate 歷史資料。

## 解除安裝或回復

由安裝程式管理的部署可執行：

```sh
sudo /opt/my-t-companion/uninstall.sh
```

解除安裝程式會停止並移除獨立 Companion 容器，以及安裝程式建立的 Caddy 路由；
`/opt/my-t-companion` 會保留以便復原。手動設定的反向代理路由需要手動刪除。
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

- [相容性與版本驗證](COMPATIBILITY.zh-Hant.md)
- [安全性政策](SECURITY.zh-Hant.md)
- [支援](SUPPORT.zh-Hant.md)
- [資料生命週期](DATA_LIFECYCLE.zh-Hant.md)
- [版本更新記錄](CHANGELOG.md)
- [MIT License](LICENSE)

## 專案、授權與品牌邊界

本儲存庫原始碼依 [MIT License](LICENSE) 公開，可按授權自行託管、修改及再散布。
My T iOS 原始碼、開發者營運的中繼實作與 APNs 提供者憑證、HostBox App 與部署
原始碼，以及 HostBox 目錄簽署私鑰，均為不包含在本儲存庫內的獨立私有元件。

MIT 授權不會令衍生版本成為 My T 或 HostBox 官方版本。再散布的衍生版本應採用
清楚不同的產品品牌，不得以暗示官方認可或官方建置的方式使用 My T／HostBox 的
名稱、圖示或整體呈現。

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
