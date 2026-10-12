# 相容性與版本驗證

[English](COMPATIBILITY.md) · [简体中文](COMPATIBILITY.zh-Hans.md) · 繁體中文

1.5.0 加入選用的 TeslaMate MQTT 軟體更新觀察與簽章中繼傳送。1.7.1
加入目的地導航即時動態傳送，並保留充電即時動態及已修補的建置相依套件，API
與部署方式沒有改變。未配對推播時，停車與導航功能仍可使用。

1.10.47 與 TeslaMate 4.2.0、上一個穩定版 4.1.1 及 TeslaMateAPI 1.25.0
相容。分車推播覆寫使用通知事件中已有的 TeslaMate 車輛 ID，不需要資料庫遷移、
更換 Tesla 權杖或喚醒車輛。現有安裝開始時仍沿用 1.10.36 的所有車輛行為；請求
未帶新選用欄位時會保留已有覆寫，不會讓無法顯示它們的舊版 App 靜默清除設定。

1.10.47 修復 HostBox 啟用朋友同行後，My T 建立邀請失敗：GET `/status` 已成功，
POST `/invitations` 因獨立唯讀連線池用了 `postgres:?host=...` 連不上 TeslaMate
資料庫而回傳 503。常規升級不改變停車、導航、配對與通知。啟用朋友同行仍需
`FRIEND_TOGETHER_ENABLED=true`、獨立 HTTPS 訪客網域，以及車主
`/api/v1/friend-together/*`。不需要遷移資料庫、配對、Tesla 權杖、通知偏好或
既有歷史，並繼續相容現有 My T 用戶端。

## 1.10.51 胎壓歷史

選用胎壓歷史介面讀取 TeslaMate 既有的 `positions` 紀錄。所需欄位已在
TeslaMate 4.2.0 / TeslaMateAPI 1.25.0 / PostgreSQL 18 上核對；這不代表下方
完整矩陣中的所有版本均已驗證此功能。能力探索會檢查實際安裝的資料庫結構，
缺少欄位時回報不支援，不虛構資料或修改資料庫。資料庫讀取失敗不等於空歷史。

My T 胎壓歷史頁面需等待相容的 App 建置版本發布。舊用戶端的停車、導航、充電、
配對與通知功能不因此要求升級。自訂代理需將
`/api/v1/cars/{id}/tire-pressure-history` 轉送至現有車主驗證的 Companion
路徑；安裝器與附帶的 Caddy/Nginx 範例已包含該路徑。

歷史範圍取決於資料來源已保留的紀錄；紀錄時間不是胎壓感測器測量時間，外溫
不是輪胎溫度。不新增採樣、喚醒車輛、外部天氣服務或資料庫結構/索引遷移。
31 天限制、分頁、單位、缺值及服務重新啟動後的用戶端處理見
[介面契約](docs/tire-pressure-history.md)。

使用現有的已驗證版本安裝器/更新器，或在獨立簽章目錄更新後使用 HostBox。
保留上一版本的已驗證安裝包與更新器備份以便回復；退回 Companion 不會刪除
TeslaMate 歷史。版本發布、線上介面驗收與簽章目錄推廣是獨立步驟，本機測試
通過不代表這些步驟已經完成。

## Companion 1.10.54 TeslaMate 4.3 相容性

本節功能由 Companion `1.10.54` 引入；既有自行託管的 Companion 1.10.53 服務
只有升級後才會改變。
動態車隊／基線恢復、通知政策和逐欄位接收／ETA 限制見
[1.10.54 README 章節](README.zh-Hant.md#companion-11054-teslamate-43-加固)與
[資料生命週期](DATA_LIFECYCLE.zh-Hant.md#companion-11054-觀察生命週期)。
不代表新車、強制 MQTT 重連或 APNs 已完成現場驗收。

`MQTT_NAMESPACE` 必須完全一致：空值保留 `teslamate/cars/{id}/{field}`，
非空使用 `teslamate/{namespace}/cars/{id}/{field}`。0–128 個字元必須全部
屬於 ASCII `[A-Za-z0-9_.-]`，不去空白或正規化。執行時及安裝器拒絕空白、
斜線、萬用字元、引號和 `$`；此字元集避免 dotenv／Compose 展開改變來源隔離。

已審查 [4.3.0 官方發布](https://github.com/teslamate-org/teslamate/releases/tag/v4.3.0)
及 [4.2.0 → 4.3.0 原始碼差異](https://github.com/teslamate-org/teslamate/compare/v4.2.0...v4.3.0)，
兩版相關歷史表遷移沒有變化。這不代表 TeslaMate 資料庫成為版本化公開介面，
也不代表完整矩陣或所有部署已完成線上驗收。Companion 的 `states`、
`current_drive`、`friend_source`、`mqtt_fleet` SQL 能力現按實際欄位、運算子的有時限唯讀探測判定，不以
TeslaMate 版本白名單阻止功能；胎壓歷史繼續使用既有獨立探測。

已驗證身分的能力回應新增 `sql_features` 下這四組能力，分別包含 `status`（`available`、
`unsupported_schema`、`unknown`）、`supported`（`true`、`false`、`null`）
及已淨化的 `error`。失敗只移除受影響的能力字串。狀態/目前行程請求遇到缺表、缺欄位或不相容
運算子回傳 HTTP 501 `states_unsupported` / `current_drive_unsupported`；
連線、權限或逾時錯誤回傳 HTTP 503 `states_unavailable` /
`current_drive_unavailable`，兩者均包含 `feature`。未知車輛回傳 HTTP 404
`car_not_found`；已存在但沒有歷史或目前行程的車輛仍回傳有效空結果。
介面不會洩漏原始 SQL 錯誤或憑證。

朋友同行探測使用實際獨立連線池與車輛歸屬、最新位置、軌跡查詢；停用或無
可用連線池時回報結構支援未知，不宣稱可用。`mqtt_fleet` 檢查實際
`cars.settings_id → car_settings.id` 關聯及布林值 `car_settings.enabled`
記錄開關。車輛清單結構不可用時，不宣稱依賴即時 MQTT 的功能和
`friend_together_v1` 可用；SQL 狀態/行程歷史、已存導航歷史與推播偏好設定
仍獨立可用。結構探測不代表車輛遙測新鮮或朋友同行服務完全就緒。四組探測
並行執行，每組最多兩秒，且遵守更早的請求取消。

主唯讀 SQL 連線池有獨立上限，朋友同行保留獨立連線池。兩者固定 UTC 工作階段，
避免無時區的歷史時間被伺服器本地時區重新解釋；App `TZ` 只格式化回應。
顯式設定無效時
啟動失敗且不會回顯設定值；時長使用 Go 格式，如 `5m` 或 `30m`。

| 環境變數 | 預設值 | 有效範圍 |
| --- | --- | --- |
| `DATABASE_MAX_OPEN_CONNS` | `8` | 1–64 |
| `DATABASE_MAX_IDLE_CONNS` | `2` | 0–64，且不超過 max-open |
| `DATABASE_CONN_MAX_IDLE_TIME` | `5m` | 1s–24h |
| `DATABASE_CONN_MAX_LIFETIME` | `30m` | 1s–24h |

安裝器寫入 `.env` 前要求標準十進位連線數，以及使用 `ns/us/ms/s/m/h` 的
ASCII 時長組件（例如 `1h30m` 或 `1.5s`）。時長組件最多 14 位整數及 9 位
小數，且不得超過該單位的奈秒精度。正負號、前導零、空白及 shell 字元會被
拒絕而非正規化；直接設定執行環境仍可使用上述範圍內的 Go 時長語法。

已驗證身分的 `database_pool` 診斷提供 max-open 上限、目前使用量及累計等待
次數/時間。採用 [Go SQL 連線池控制](https://go.dev/doc/database/manage-connections)，
不修改 PostgreSQL 伺服器上限、資料庫結構或索引。

狀態歷史未傳 `limit` 和 `cursor` 時保留原本完整回應。選用
`limit=1..2000` 啟用游標分頁（僅傳游標時預設 1000），能力名稱為
`state_history_keyset_pagination_v1`。`meta.state_history_page` 回傳
`limit`、`returned_count`、`has_more`、`next_cursor`。用相同車輛、
`startDate`、`endDate` 和回傳的不透明游標繼續讀取，直到 `has_more=false`。
按 `(start_date,id)` 排序，相同狀態時間戳不會遺失紀錄。重疊區間保留真實
起點，包括 16 天或更長的停車；不新增日期範圍上限，也不靜默截斷歷史。
游標由處理程序簽章，重新啟動或切換伺服器後 HTTP 400 `invalid_cursor`
表示需重讀該視窗並去重。各頁為即時讀取而非資料庫快照；開放區間變更後應
重讀視窗。狀態及位置來源時間保留小數秒，舊位置不會偽裝成目前觀察；缺少
遙測仍為 null，不會變成零。

本機驅動/HTTP 測試涵蓋連線池上限、結構分類、取消、空值、小數秒、舊/相同
時間戳、未知車輛與分頁，但不等同於執行 PostgreSQL 查詢。獨立選用測試
`TestSQLLocalPostgresTemporalStatesAndPagination` 只透過既有嚴格回環位址
`MYT_FRIEND_LOCAL_POSTGRES_URL` 測試資料庫使用暫存表（資料庫名稱
`myt_friend_fixture`，必須顯式連接埠）。未明確準備測試資料庫時測試回報略過，
該次本機 PostgreSQL 執行仍未驗證；另行執行的合成測試資料以對應版本 CI
結果為準。本機或 CI 測試都不證明正式部署、真車行為或 APNs 傳送。
Companion 不遷移 TeslaMate 表／索引，發布版本也不會部署服務。

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
- 舊式歷史狀態請求為相容性仍完整回傳；用戶端應限定時間視窗，並對大型歷史
  使用上述選用游標分頁。查詢失敗回傳錯誤，不會把部分資料作為成功結果。
