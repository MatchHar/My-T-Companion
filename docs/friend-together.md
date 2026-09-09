# Friend Together — optional default-off feature

## English

This release adds temporary, mutually approved sharing of one selected
vehicle between independent TeslaMate owners. It is OFF by default. It does not
send continuous location to the developer's push Worker, share Tesla/VPS
credentials, expose other vehicles/history, wake vehicles or call Tesla.

The owner must separately configure a dedicated publicly reachable HTTPS share
origin and route only `/friend/v1/*` to Companion, preserving Host. The existing
owner `/api/v1/friend-together/*` endpoints remain under normal owner
authentication and must not be included in that public guest proxy rule. Do not
disable site-wide Basic/Cloudflare Access, expose8083 or publish DB/MQTT ports.
This source change does not configure a hostname, DNS, proxy, firewall or HostBox.
The shipped owner Caddy/nginx matchers include `/api/v1/friend-together/*`;
an existing installation must adopt that matcher on its authenticated API site
before owner controls can reach Companion. The LAN sample only illustrates
routing; the App's release owner transport still requires HTTPS. The dedicated
guest proxy must preserve the configured public Host (DNS case and explicit
default`:443` are equivalent); rewriting it to an upstream/local host is rejected.

Only after release and an authorized ingress review: runtime settings are
`FRIEND_TOGETHER_ENABLED=true`, `FRIEND_TOGETHER_GUEST_ORIGIN=https://share.example.com`
and a private `FRIEND_TOGETHER_STATE_PATH` (default
`/data/friend-together/state.json`). Example hostname is documentation-only.
Unset/false leaves the feature disabled. Invalid configuration/durable state
fails closed and does not advertise `friend_together_v1`.

Sharing requires explicit location consent; navigation, battery and the
post-consent current trajectory are independent optional categories. Precision
can reveal home addresses even if private place names are omitted. Road queries
on the phone send endpoints to Apple Maps. The temporary invitation expires in
ten minutes and grants last at most eight hours after owner approval. Closing
the map is not stopping a share. Both peers approve their own one-way grants.
Owner and recipient stop controls revoke their own grant; a network/storage
failure must not be reported as a completed stop. Copied information cannot be
recalled. Existing grant/counter state is deliberately not resumed after server
restart: all prior shares are ended and fresh invitations are required.
After terminal metadata cleanup, an authenticated owner cancellation stays
idempotent. A guest401/404 must not be treated as successful cancellation:
the server may no longer possess the token hash needed to authenticate the old
request. Keep stop unconfirmed unless a valid stop acknowledgement is received,
or independently establish absolute expiry against the original pinned HTTPS
server's nonce clock **and matching issuer_id**. A same-host different issuer
must not confirm expiry. Use the known grant expiry; if approval outcome was never read,
the conservative latest possible expiry is invitation expiry plus its declared
sharing duration. Expiry is not a remote cancellation acknowledgement and must
be labelled accordingly. Never use a wrong-host response or phone clock alone.

GPS/speed/battery use original read-only TeslaMate position timestamps. Optional
navigation uses non-retained `active_route` observations, separate from unrelated
topic receipt times. Missing data stays missing. Trajectory responses contain
only the latest500 eligible points of the current drive, not eight hours of
recoverable history; clients must split gaps and not draw a straight bridge.
This is not a guarantee that brief upstream vehicle actions or all GPS samples
were recorded. No cross-owner APNs/Live Activity feature is implied.

The source uses a separate two-connection read-only pool, two-second query
deadlines, at most two simultaneous reads and at most one read attempt per grant
per second. Slow database/network I/O does not hold the sharing-control mutex;
revocation and permission revisions are rechecked after a read. Latest-position
queries start at consent, and trajectories stop at the selected position time.
State is as-of that position, not a fresh GPS claim for an asleep/parked vehicle.

Local synthetic proof: `go test -race ./...` then
`go run ./cmd/friend-together-fixture -listen 127.0.0.1:18961 -lane 0`.
A second instance can use port18962/lane1; each owns syntheticcar1. Fixture owner
header is the deliberately public constant `Bearer myt-local-fixture-only`.
It binds literal loopback only, uses a fresh temporary journal and never reads
TeslaMate/MQTT. Do not use its HTTP exception or dummy token in production.

The separate opt-in SQL test uses `MYT_FRIEND_LOCAL_POSTGRES_URL` pointing only to
literal-loopback PostgreSQL, explicit port, database `myt_friend_fixture`, without
URL query parameters. It creates temporary synthetic relations only. Run
`go test -run TestFriendLocalPostgresSchemaAndSource -v .`. Missing local PostgreSQL
is an explicit skipped SQL gate, not proof of SQL execution or production plans.

Before release: physical two-owner acceptance, ingress isolation, privacy copy,
bounded/rate testing, signed HostBox catalog/CLI clean-install, upgrade/rollback
and documentation still need their normal separate release/deployment gates.

## 繁體中文

這是已發布但預設關閉的功能，朋友同行預設關閉。每位車主分別選擇
自己的車、分享資料與期限，並確認對方加入；收到邀請不等於可以讀取位置。
位置分享是精確位置，可能透露住址。導航、電量及同意後的軌跡可分別選擇。
不分享 Tesla／VPS 帳密、其他車輛、過去行程、VIN、門窗鎖或控制權。

需要另行配置 HTTPS 分享子網域，只公開 `/friend/v1/*`，車主的
`/api/v1/friend-together/*` 與原本 API 仍保留認證。不會自動修改 DNS、
防火牆、HostBox 或 Cloudflare，也不能取消整站保護或公開8083／資料庫／MQTT。
既有安裝須更新車主 API 的代理比對規則，將 `/api/v1/friend-together/*` 交給
Companion，同時保留原有認證；訪客路由仍只設於獨立分享網域。分享代理必須保留
公開 Host，不能改成本機上游名稱；省略或明列 HTTPS 預設443埠均可。
邀請10分鐘有效，分享最多8小時。關閉地圖不等於停止；停止要收到伺服器確認。
伺服器重啟後本版會結束原有分享，需要重新邀請，不宣稱無縫恢復。
到期資料清理後，車主取消仍可安全重試；訪客401／404不能直接當作停止成功。
須保留未確認狀態，或另行向原本受信任 HTTPS 伺服器取得 nonce，確認 issuer_id 相符，
再核對嚴格期限並顯示「期限已屆」，
不能只靠手機時間、錯誤網域回應或把到期稱為伺服器已確認取消。

GPS與電量保留 TeslaMate 原始觀測時間，不用刷新時間假裝即時。導航不把保留的
舊 MQTT 資料當新觀測。軌跡一次最多目前行程的500個同意後觀測點，不等於完整
8小時歷史；缺口不能畫直線補齊。中央推送 Worker 不接收連續 GPS；手機計算
道路路線時仍會把起終點交給 Apple Maps。本機測試不代表真車背景或推送驗收。

同行讀取使用獨立、最多兩條連線的唯讀資料庫連線池及2秒查詢限制；同時最多
兩次讀取，每項授權最快每秒一次。慢查詢／網路不會佔住分享控制鎖；回傳前會
再確認停止、到期及權限。車況依原始位置時間判定，休眠／停車不會假裝持續有新
GPS。沒有本機 PostgreSQL 時，SQL 實際執行測試會明確跳過，不代表正式資料庫驗證。

## 简体中文

这是已发布但默认关闭的功能，朋友同行默认关闭。每位车主分别选择
自己的车、分享数据与期限，并确认对方加入；收到邀请不等于可以读取位置。
位置分享是精确位置，可能透露住址。导航、电量及同意后的轨迹可分别选择。
不分享 Tesla／VPS 账号密码、其他车辆、过去行程、VIN、门窗锁或控制权。

需要另行配置 HTTPS 分享子域名，只公开 `/friend/v1/*`，车主的
`/api/v1/friend-together/*` 与原有 API 仍保留认证。不会自动修改 DNS、
防火墙、HostBox 或 Cloudflare，也不能取消整站保护或公开8083／数据库／MQTT。
既有安装须更新车主 API 的代理匹配规则，将 `/api/v1/friend-together/*` 交给
Companion，同时保留原有认证；访客路由仍只设于独立分享域名。分享代理必须保留
公开 Host，不能改成本地上游名称；省略或明列 HTTPS 默认443端口均可。
邀请10分钟有效，分享最多8小时。关闭地图不等于停止；停止须收到服务器确认。
服务器重启后本版会结束原有分享，需要重新邀请，不宣称无缝恢复。
到期数据清理后，车主取消仍可安全重试；访客401／404不能直接当作停止成功。
须保留未确认状态，或另行向原本可信 HTTPS 服务器取得 nonce，确认 issuer_id 相符，
再核对严格期限并显示“期限已到”，
不能只靠手机时间、错误域名响应或把到期称为服务器已确认取消。

GPS与电量保留 TeslaMate 原始观测时间，不用刷新时间假装实时。导航不把保留的
旧 MQTT 数据当新观测。轨迹一次最多当前行程的500个同意后观测点，不等于完整
8小时历史；缺口不能画直线补齐。中央推送 Worker 不接收连续 GPS；手机计算
道路路线时仍会把起终点交给 Apple Maps。本地测试不代表真车后台或推送验收。

同行读取使用独立、最多两条连接的只读数据库连接池及2秒查询限制；同时最多
两次读取，每项授权最快每秒一次。慢查询／网络不会占用分享控制锁；返回前会
再次确认停止、到期及权限。车况依原始位置时间判断，休眠／停车不会假装持续有新
GPS。没有本地 PostgreSQL 时，SQL 实际执行测试会明确跳过，不代表正式数据库验证。
