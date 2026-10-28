# 兼容性与发布验证

[English](COMPATIBILITY.md) · 简体中文 · [繁體中文](COMPATIBILITY.zh-Hant.md)

1.5.0 加入可选的 TeslaMate MQTT 软件更新观察与签名中继投递。1.7.1
加入目的地导航实时活动投递，并保留充电实时活动及已修补的构建依赖，API
和部署方式没有改变。未配对推送时，停车与导航功能仍可使用。

1.10.47 与 TeslaMate 4.2.0、上一稳定版 4.1.1 以及 TeslaMateAPI 1.25.0
兼容。分车推送覆盖使用通知事件中已有的 TeslaMate 车辆 ID，不需要数据库迁移、
更换 Tesla 令牌或唤醒车辆。现有安装开始时仍沿用 1.10.36 的全部车辆行为；请求
未带新可选字段时会保留已有覆盖，不会让无法显示它们的旧版 App 静默清除设置。

1.10.47 修复 HostBox 启用朋友同行后，My T 建立邀请失败：GET `/status` 已成功，
POST `/invitations` 因独立只读连接池用了 `postgres:?host=...` 连不上 TeslaMate
数据库而返回 503。常规升级不改变停车、导航、配对与通知。启用朋友同行仍需
`FRIEND_TOGETHER_ENABLED=true`、独立 HTTPS 访客域名，以及车主
`/api/v1/friend-together/*`。不需要迁移数据库、配对、Tesla 令牌、通知偏好或
已有历史，并继续兼容现有 My T 客户端。

## 1.10.65 的足迹停车及 GPS 证据

所有者认证的 `road_parking_v1` 批量读取已保存行程边界，不更改 TeslaMate 结构、通知设置或配对，也不请求车辆。新显示需要兼容 My T 8.05 Build796 或之后；此处发布不代表公开 App Store 已上架。可选 GPS 证据只在样本完整且有已核对市界时确认途经城市；缺失或不支持的数据维持未知。旧客户端不受影响。见[只读接口说明](ROAD_PARKING_API.md)。使用既有验证更新器或独立签名 HostBox 目录更新，并保留上一份已验证版本以便回退。

## 1.10.51 胎压历史

可选胎压历史接口读取 TeslaMate 已有的 `positions` 记录。所需字段已在
TeslaMate 4.2.0 / TeslaMateAPI 1.25.0 / PostgreSQL 18 上核对；这不代表下方
完整矩阵中的所有版本均已验证此功能。能力发现会检查实际安装的数据库结构，
缺字段时报告不支持，不伪造数据或修改数据库。数据库读取失败不等于空历史。

My T 胎压历史页面需等待兼容的 App 构建发布。旧客户端的停车、导航、充电、
配对与通知功能不因此要求升级。自定义代理需将
`/api/v1/cars/{id}/tire-pressure-history` 转发到现有车主验证的 Companion
路径；安装器与附带的 Caddy/Nginx 示例已包含该路径。

历史范围取决于数据源已保留的记录；记录时间不是胎压传感器测量时间，外温
不是轮胎温度。不新增采样、唤醒车辆、外部天气服务或数据库结构/索引迁移。
31 天限制、分页、单位、缺值及服务重启后的客户端处理见
[接口合同](docs/tire-pressure-history.md)。

使用现有的已验证版本安装器/更新器，或在独立签名目录更新后使用 HostBox。
保留上一版本的已验证安装包和更新器备份以便回滚；回退 Companion 不会删除
TeslaMate 历史。版本发布、线上接口验收与签名目录推广是独立步骤，本地测试
通过不代表这些步骤已经完成。

## Companion 1.10.54 TeslaMate 4.3 兼容性

本节功能由 Companion `1.10.54` 引入；既有自托管的 Companion 1.10.53 服务
只有升级后才会改变。
动态车队／基线恢复、通知政策和逐字段接收／ETA 限制见
[1.10.54 README 章节](README.zh-Hans.md#companion-11054-teslamate-43-加固)与
[数据生命周期](DATA_LIFECYCLE.zh-Hans.md#companion-11054-观察生命周期)。
不代表新车、强制 MQTT 重连或 APNs 已完成现场验收。

`MQTT_NAMESPACE` 必须完全一致：空值保留 `teslamate/cars/{id}/{field}`，
非空使用 `teslamate/{namespace}/cars/{id}/{field}`。0–128 个字符必须全部
属于 ASCII `[A-Za-z0-9_.-]`，不去空白或规范化。运行时及安装器拒绝空白、
斜线、通配符、引号和 `$`；此字符集避免 dotenv／Compose 展开改变来源隔离。

已审查 [4.3.0 官方发布](https://github.com/teslamate-org/teslamate/releases/tag/v4.3.0)
及 [4.2.0 → 4.3.0 源码差异](https://github.com/teslamate-org/teslamate/compare/v4.2.0...v4.3.0)，
两版相关历史表迁移没有变化。这不代表 TeslaMate 数据库成为版本化公开接口，
也不代表完整矩阵或所有部署已完成线上验收。Companion 的 `states`、
`current_drive`、`friend_source`、`mqtt_fleet` SQL 能力现按实际字段、运算符的有时限只读探测判定，不以
TeslaMate 版本白名单阻止功能；胎压历史继续使用既有独立探测。

已验证身份的能力响应新增 `sql_features` 下这四组能力，分别包含 `status`（`available`、
`unsupported_schema`、`unknown`）、`supported`（`true`、`false`、`null`）
及已净化的 `error`。失败只移除受影响的能力字符串。状态/当前行程请求遇到缺表、缺字段或不兼容
运算符返回 HTTP 501 `states_unsupported` / `current_drive_unsupported`；
连接、权限或超时错误返回 HTTP 503 `states_unavailable` /
`current_drive_unavailable`，两者均包含 `feature`。未知车辆返回 HTTP 404
`car_not_found`；已存在但没有历史或当前行程的车辆仍返回有效空结果。
接口不会泄露原始 SQL 错误或凭据。

朋友同行探测使用实际独立连接池与车辆归属、最新位置、轨迹查询；停用或无
可用连接池时报告结构支持未知，不宣称可用。`mqtt_fleet` 检查实际
`cars.settings_id → car_settings.id` 关联及布尔值 `car_settings.enabled`
记录开关。车辆清单结构不可用时，不宣称依赖即时 MQTT 的功能和
`friend_together_v1` 可用；SQL 状态/行程历史、已存导航历史与推送偏好设置
仍独立可用。结构探测不代表车辆遥测新鲜或朋友同行服务完全就绪。四组探测
并行执行，每组最多两秒，且遵守更早的请求取消。

主只读 SQL 连接池有独立上限，朋友同行保留独立连接池。两者固定 UTC 会话，
避免无时区的历史时间被服务器本地时区重新解释；App `TZ` 只格式化响应。
显式配置无效时
启动失败且不会回显配置值；时长使用 Go 格式，如 `5m` 或 `30m`。

| 环境变量 | 默认值 | 有效范围 |
| --- | --- | --- |
| `DATABASE_MAX_OPEN_CONNS` | `8` | 1–64 |
| `DATABASE_MAX_IDLE_CONNS` | `2` | 0–64，且不超过 max-open |
| `DATABASE_CONN_MAX_IDLE_TIME` | `5m` | 1s–24h |
| `DATABASE_CONN_MAX_LIFETIME` | `30m` | 1s–24h |

安装器写入 `.env` 前要求标准十进制连接数，以及使用 `ns/us/ms/s/m/h` 的
ASCII 时长分量（例如 `1h30m` 或 `1.5s`）。时长分量最多 14 位整数及 9 位
小数，且不得超过该单位的纳秒精度。正负号、前导零、空白及 shell 字符会被
拒绝而非规范化；直接配置运行环境仍可使用上述范围内的 Go 时长语法。

已验证身份的 `database_pool` 诊断提供 max-open 上限、当前使用量及累计等待
次数/时间。采用 [Go SQL 连接池控制](https://go.dev/doc/database/manage-connections)，
不修改 PostgreSQL 服务端上限、数据库结构或索引。

状态历史未传 `limit` 和 `cursor` 时保留原本完整响应。可选
`limit=1..2000` 启用游标分页（仅传游标时默认 1000），能力名为
`state_history_keyset_pagination_v1`。`meta.state_history_page` 返回
`limit`、`returned_count`、`has_more`、`next_cursor`。用相同车辆、
`startDate`、`endDate` 和返回的不透明游标继续读取，直到 `has_more=false`。
按 `(start_date,id)` 排序，相同状态时间戳不会丢失记录。重叠区间保留真实
起点，包括 16 天或更长的停车；不新增日期范围上限，也不静默截断历史。
游标由进程签名，重启或切换服务器后 HTTP 400 `invalid_cursor` 表示需重读
该窗口并去重。各页为实时读取而非数据库快照；开放区间变化后应重读窗口。
状态及位置源时间保留小数秒，旧位置不会伪装成当前观察；缺失遥测仍为 null，
不会变成零。

本地驱动/HTTP 测试覆盖连接池上限、结构分类、取消、空值、小数秒、旧/相同
时间戳、未知车辆与分页，但不等同于执行 PostgreSQL 查询。独立可选测试
`TestSQLLocalPostgresTemporalStatesAndPagination` 只通过既有严格回环地址
`MYT_FRIEND_LOCAL_POSTGRES_URL` 夹具使用临时表（数据库名
`myt_friend_fixture`，必须显式端口）。未明确准备夹具时测试报告跳过，实际
该次本地 PostgreSQL 执行仍未验证；另行执行的合成夹具以对应版本 CI 结果
为准。本地或 CI 测试都不证明正式部署、真车行为或 APNs 投递。Companion
不迁移 TeslaMate 表／索引，发布版本也不会部署服务。

## 必要基础环境

- 运行 Docker Engine 与 Docker Compose v2 的 Linux 主机。
- 已存在且健康的 TeslaMate Docker Compose 部署。
- PostgreSQL 可从 TeslaMate Docker 网络访问；默认服务名为 `database`，安装程序也会检查 `db`、`postgres` 及容器标签。
- 无需依赖 TeslaMate `.env` 文件也能取得 `DATABASE_PASS`，来源可以是 Compose 配置、容器环境变量、Shell 环境或可选 `.env`。
- TeslaMate 数据库包含 `cars`、`states`、`positions` 和 `drives`。
- 现有 API 使用 Bearer Token、Basic Authentication、`X-API-Token` 或 Cloudflare Access Service Token 验证。
- Gateway 已覆盖停车、导航、能力发现及 `/api/v1/notifications/*` 路径，包括实时活动状态。

## 发布测试矩阵

每个稳定版本都必须记录以下结果：

| 范围 | 必测场景 |
| --- | --- |
| 主机 | Ubuntu 22.04 与 24.04，amd64 |
| 架构 | amd64 与 arm64 镜像构建 |
| TeslaMate | 当前稳定版及上一稳定版 |
| 代理 | 系统 Caddy 自动安装；Nginx/Traefik 手动说明 |
| 验证 | Bearer、Basic、X-API-Token、Cloudflare Access |
| 生命周期 | 全新安装、重复安装/更新、失败回滚、卸载 |
| 停车 | 休眠/唤醒/再休眠、开放休眠、停车中充电、缺失遥测、跨午夜 |
| 导航 | 无行程、等待首个点、增量点、分页、Drive ID 变化 |
| 失败 | 数据库不可用、Token 错误、公开 `/api/ping`、端口占用、未知 Compose 布局 |

## 已知发布候选限制

- 自动反向代理编辑只支持系统 Caddy 服务。
- 其他代理布局必须提供并验证 `MY_T_BASE_URL`；只在本机可用且未经验证的服务会报告为未完成，而不是成功。
- 默认 TeslaMate 目录为 `/opt/teslamate`；其他布局使用 `TESLAMATE_DIR`。
- 默认数据库 Compose 服务名为 `database`。
- 旧式历史状态请求为兼容性仍完整回传；客户端应限定时间窗口，并对大型历史
  使用上述可选游标分页。查询失败返回错误，不会把部分数据作为成功结果。
