# My T Companion

[English](README.md) · [简体中文](README.zh-Hans.md) · [繁體中文](README.zh-Hant.md)

> [![最新稳定版](https://img.shields.io/github/v/release/MatchHar/My-T-Companion?display_name=tag&sort=semver)](https://github.com/MatchHar/My-T-Companion/releases/latest)
>
> 上方徽章和[最新稳定版](https://github.com/MatchHar/My-T-Companion/releases/latest)链接会自动更新。车辆软件原生推送、充电锁屏实时活动与导航实时活动均为可选功能，在兼容的 My T 版本
> 提供安全配对前保持关闭。
>
> **My T：** App Store 公开可下载版本以
> [Apple 产品页](https://apps.apple.com/us/app/my-t/id6780299502)为准。
> 较新版本可能正在审核中，本仓库不写死版本号，也不声明非公开审核版本。
> 停车流水、观测事件、轨迹及目的地行程记录在可访问 `/api/v1/capabilities` 时
> 可用；推送与实时活动仍需完成配对。详见
> [My T 功能可用性](https://github.com/MatchHar/My-T-App/blob/main/docs/FEATURE_AVAILABILITY.md)。

安全配对后，每台 iPhone 可以独立开启“锁车且车内无人”通知。Companion 只发送
签名事件；可见消息和提示音由 My T 在手机上处理。导入音频、文件名及静音／默认
选择都不会发送到 Companion 或推送中继。

**本组件专为
[My T iPhone App 开发，可在 App Store 下载](https://apps.apple.com/us/app/my-t/id6780299502)。**
如果您是从 TeslaMate 项目找到这里，请先通过此链接确认并下载配套的 My T App。

**需要部署服务器栈？[从 App Store 下载 HostBox](https://apps.apple.com/us/app/hostbox/id6798103086)。** [产品说明与上线视频](https://my-tesla.app/hostbox/)介绍了如何直接在 iPhone 引导部署 VPS。

## 导航锁屏实时活动

1.7.0 读取 TeslaMate MQTT 真实上报的目的地导航与车辆行驶状态。兼容的 My T
版本即使没有打开，也可以自动开始、更新及结束锁屏／灵动岛导航卡片。

卡片使用真实目的地、剩余里程／时间、预计到达、车辆预测到达电量，以及由当前
TeslaMate 行程验证的进度。中继不会接收坐标、轨迹、VIN、TeslaMate 凭据或车辆
历史；缺失数据不会估算。

未部署本组件时，My T 的 App 内目的地卡片、车辆实时位置与速度仍可正常使用；
真实起点、已行驶轨迹、全程进度及主动锁屏推送需要部署并完成配对。

状态接口：`GET /api/v1/notifications/navigation-live-activity/status`。

## 充电锁屏实时活动

1.6.1 读取 TeslaMate MQTT 真实上报的充电状态、电量百分比、额定续航、充电上限、
功率和剩余时间。兼容的 My T 版本即使没有打开，也可以自动显示并更新锁屏／灵动岛
充电卡片。

常规更新最短间隔为 45 秒，功率达到 50 kW 时缩短为 15 秒，并会合并短时间内的多项变化。签名事件只包含卡片所需
字段，不包含 VIN、位置、路线、TeslaMate 凭据或 kWh。续航增加只在 TeslaMate
具有真实起始与当前 `rated_battery_range_km` 时计算；缺失时不估算公里数。

状态接口：`GET /api/v1/notifications/charging-live-activity/status`。

## iPhone 原生车辆软件更新通知

1.5.0 订阅 TeslaMate MQTT 中车辆真实回报的 `update_available`、
`update_version` 和当前版本变化，不猜测可用版本、不访问 Tesla，也不唤醒车辆。

该功能默认关闭。My T 配对将提供安装 ID、HTTPS 中继地址和每次安装独立的密钥，
三项必须同时配置。事件使用 HMAC-SHA256 签名，并在独立 Docker 数据卷中保存
去重状态。推送内容不包含 VIN、位置、TeslaMate 凭据、数据库密码、电池、路线或
行驶历史。Apple APNs 私钥绝不会放入本公开项目或用户 VPS。

状态接口：`GET /api/v1/notifications/software-update/status`。
用户在 My T 中开启通知后，App 会通过现有已认证连接自动写入配对：
`POST /api/v1/notifications/software-update/pair`。为防止服务端请求伪造
（SSRF），组件只接受 My T 官方中继地址，不允许配置任意服务器。

之所以需要官方中继，是因为任意用户 VPS 没有 App 持有的 APNs 凭据，不能直接
向 Apple Push Notification service 发送 My T 通知。中继只保存投递所需的 APNs
设备令牌与不透明安装 ID，并只接收上文所述的最少软件更新事件。每个 VPS 使用
自己的独立签名，一个安装不能替另一个安装发送通知。

My T 的完整产品介绍、TeslaMateAPI 部署、连接安全与故障排查，请查看
[My T 公开文档仓库](https://github.com/MatchHar/My-T-App)。

My T Companion 是部署在 TeslaMate 服务器上的可选独立组件，为 My T
提供完整的车辆状态历史、已保留的停车 MQTT 事件观测（插枪、充电、安防、空调等）
和可靠的实时行驶轨迹。停车增强与实时导航共用同一个容器、认证方式、安装命令
和更新流程。

组件只读现有 TeslaMate PostgreSQL 数据库，不修改 TeslaMate、不创建数据表，
也不会复制、删除或改写数据库历史。从 1.9.2 开始，组件会在自己的数据卷保存
TeslaMate 未长期保存的真实 MQTT 状态变化，例如开始充电前已经插枪。安装或重启
后的首个保留值只建立基线，不会生成假事件。事件时间表示“TeslaMate／Companion
首次观测时间”，不冒充更精确的物理操作时间。停车事件默认长期保留，并以最新
50,000 条作为容量保护；导航和推送等临时状态按各自期限自动清理。详细分类见
[数据生命周期](DATA_LIFECYCLE.zh-Hans.md)。

如果中继暂时不可用，或 ActivityKit 尚未登记该会话令牌，Companion 只会在用户
自己的 VPS 上，以 `0600` 权限暂存完成重试所需的最少签名事件。重试记录按事件
类型在 10 分钟至 24 小时内过期，总数上限为 256；投递成功、暂停或解除配对时
立即删除，不会进入开发者中继的投递审计。

请在 **TeslaMate 已部署并正常运行之后** 安装本组件。它不能替代 TeslaMate
或 TeslaMateAPI。

### 部署方式：HostBox 或自建

| 路径 | 对象 | 说明 |
| --- | --- | --- |
| **[HostBox](https://apps.apple.com/us/app/hostbox/id6798103086)**（多数手机用户推荐） | App Store 上的 iOS 部署 App | 安装 TeslaMate + API + 本组件，尽量配置**统一入口**（临时 IP 的 edge，或 Tunnel 路径）、MQTT `host.docker.internal` 与健康检查。完成后在 My T 只填**一个** base_url + Token；详见[产品说明](https://my-tesla.app/hostbox/)。 |
| **本仓库自建** | 自己跑 Docker / `install.sh` | 按下方校验版安装，并配置网关分流（见端口说明）。My T 连接规则相同。 |

HostBox 是**部署层**；My T 是看车 App。二者不互相替代。

## 为什么 My T 需要这个补充组件

[My T](https://apps.apple.com/us/app/my-t/id6780299502) 是用于查看用户自建
TeslaMate 数据的 iPhone App。普通行程、充电、统计和当前车辆信息继续由标准
TeslaMate/TeslaMateAPI 提供，本组件只补充三个手机端无法可靠还原的场景：

1. **停车增强**：需要完整的 `online`、`offline`、`asleep` 状态顺序，
   以及每次状态切换前后的真实电量和续航。App 关闭期间发生的事件无法由手机
   事后重建。
2. **正在行驶时的实时导航**：需要 TeslaMate 保存的最早真实 GPS 点和后续
   增量轨迹。My T 打开时看到的位置不能冒充真实行程起点。
3. **停车事件**（插枪／拔枪、充电、哨兵、锁车／门、空调）：需要持续运行的
   观测服务；iOS 在 My T 被挂起后不能可靠保存这些事件。Companion 1.9.2+ 会
   保留 TeslaMate 未作为历史保存的真实 MQTT 变化。

VPS Companion 只提供这些缺失的只读能力。TeslaMate 仍是唯一数据来源；
My T 检测到 `/api/v1/capabilities` 后会自动启用增强显示。

### My T 功能对照

| My T 功能 | 未部署组件 | 已部署组件 |
| --- | --- | --- |
| 行程、充电、统计 | 使用标准 TeslaMate API，正常可用 | 保持不变 |
| 基础停车历史 | 根据普通行程和停车记录显示 | 保持不变 |
| 休眠与唤醒流水 | 可能不完整，My T 不会估算缺失事件 | 显示 TeslaMate 真实保存的完整状态顺序 |
| 停车电量与续航变化 | 仅在已有真实数据时显示 | 显示切换前后 30 分钟内的真实边界观测 |
| 停车途中充电 | 充电记录正常可查 | 可与停车状态流水结合显示 |
| 插枪、安全及空调事件 | My T 关闭后没有持续历史 | 保存部署后的真实 MQTT 变化；只显示实际上报的电量与续航 |
| 正在行驶地图 | 没有真实起点时只显示车辆位置和速度 | 显示不可变真实起点及增量真实轨迹 |

本组件完全可选。My T 会自动检测；用户不需要在 App 内新增服务器、账号或车辆连接。

## TeslaMate 部署在家庭或私人内网

本组件可以读取部署在内网的 TeslaMate 数据库，但 My T 必须通过**同一个统一
访问地址**连接 TeslaMateAPI 和本组件。My T 会在已经配置的 TeslaMate 服务器
地址上检测 `/api/v1/capabilities`，不需要、也不会另外配置一个组件地址。

| 内网架构 | 使用结果 |
| --- | --- |
| TeslaMateAPI 与本组件经过同一个 Caddy/Nginx/Traefik 地址转发 | 支持 |
| My T 直接连接「主机上的 TeslaMateAPI 端口」（示例常写 8080 或 8081），没有统一反向代理 | TeslaMate 基础功能可用，但无法访问本组件 |
| 通过 VPN、Tailscale 或其他私人网络访问同一个反向代理地址 | 支持 |

组件的 `8083` 端口会有意保持只绑定 `127.0.0.1`，不能直接开放到内网或公网。
**端口说明：** 容器内 TeslaMateAPI 固定为 **8080**；映射到主机时可用 **8080 或 8081** 等（HostBox 默认主机 **8081**）。Companion 主机端口 **8083** 仅本机，不要在 My T 里再填第二个地址。

原来直接使用「主机 API 端口、无反代」的用户，需要先增加**统一反向代理（Gateway）**：将 Companion 路径转发到 `127.0.0.1:8083`，其余 TeslaMateAPI 请求转发到内部 API 地址，然后在 My T 中只填这个**统一 base_url**。仓库内的 `Caddyfile.snippet` 已列出所需接口。

安装程序可以自动处理能够识别的系统 Caddy。Nginx、Traefik、容器版 Caddy 或
自定义内网网关需要管理员手动加入路由；只安装容器、不配置统一入口时，My T
不会显示增强功能。

### 建议顺序（方法不变，只是常见三阶段）

不改变既有部署方法，多数人会按此顺序：

1. **TeslaMate + TeslaMateAPI** — My T 填 `http://IP:主机API端口`（或已有 HTTPS 反代）。基础看车可用。
2. **Companion** — **仍用同一个** My T base_url。必须有统一入口（系统 Caddy、`Caddyfile.snippet`、或安装程序 edge）把扩展路径转到 `127.0.0.1:8083`，其余 `/api/*` 转官方 API。Token 不变。
3. **Cloudflare Tunnel（可选安全）** — My T 改填 `https://你的域名`。Tunnel 成为新的统一入口；分流概念与 Caddy 相同（`Caddyfile.snippet` 路径 → `8083`，其余 `api/*` → 官方 API 主机端口）。

#### 从临时 IP / edge 切换到 Cloudflare Tunnel

若第 2 步用了**占用公网 API 端口的 edge**（API 常被挪到本机 `18081`），第 3 步必须**收口**，只留一个公网入口：

| 要做 | 原因 |
| --- | --- |
| Tunnel 扩展路径 → `http://127.0.0.1:8083` | 与 `Caddyfile.snippet` 一致 |
| Tunnel 其余 `api/*` → **TeslaMateAPI 实际监听的端口**（常见是恢复库存 `127.0.0.1:8081` 或 `8080`，不要指向已拆除的 edge） | 指错会导致 My T **HTTP 502** |
| 停掉临时 IP 的 edge，避免继续占用 `8081` | 两套入口抢端口 |
| My T 改用 `https://域名`（不要再用 `http://IP:8081`） | 只换外层入口；Token 不变 |

Tunnel 完成后的干净目标（「一个 base_url」契约）：

```text
https://域名
  ├─ 扩展路径  → 127.0.0.1:8083
  └─ 其余 /api/* → 127.0.0.1:<官方API主机端口>   # 例如 8081
```

## 数据如何流动

```text
车辆 → TeslaMate → PostgreSQL
                       │ Docker 内网只读连接
                       ▼
               My T Companion
                       │ 现有 HTTPS/API 认证
                       ▼
                    My T App
```

- Tesla 账号授权始终由 TeslaMate 管理。
- VPS Companion 不连接 Tesla，也不会唤醒车辆。
- My T 不会把车辆历史转发到开发者运营的云端。
- 数据只在用户自己的 VPS 与 iPhone 之间，通过现有安全域名或私人网络传输。

## 部署后会创建什么

| 项目 | 是否创建或修改 | 用途 |
| --- | --- | --- |
| 独立 Docker 服务与容器 | 是 | 运行只读 Companion API |
| `127.0.0.1:8083` 本机监听端口 | 是 | 让服务始终位于现有受保护的反向代理之后 |
| Companion 反向代理路由 | 是 | 通过 My T 原有服务器地址提供能力检测、停车状态／事件、当前行驶及通知接口 |
| 安装配置与恢复备份 | 是 | 支持重复更新、回滚与卸载 |
| 新数据库或 TeslaMate 数据表 | 否 | TeslaMate PostgreSQL 始终是唯一数据来源 |
| 重复的车辆历史存储 | 否 | 仅在 My T 请求时查询数据 |
| 修改 TeslaMate 数据或发送车辆指令 | 否 | 数据库连接强制只读，也不连接 Tesla |

服务只读取接口所需的 TeslaMate `states`、`drives` 和 `positions` 数据，返回
整理后的状态区间、附近的真实电量/续航观测、当前行程不可变的第一个位置，
以及增量轨迹点。它没有后台采集器，也不设定独立保留期限；可查询多久完全
取决于 TeslaMate 数据库中的历史。

## 各版本提供的能力

| Companion 版本 | 新增能力 |
| --- | --- |
| 1.10.33 | 导航卡片开始后，TeslaMate 首次解析出真实行程起点时立即投递修正，不等待普通节流计时 |
| 1.10.32 | 持续刷新已验证的目的地导航行驶距离，并立即向远程实时活动投递首次有效进度 |
| 1.10.31 | 按 Companion 而不是按手机计算的匿名车辆数量及首次／最后出现时间上报 |
| 1.10.30 | 以 TeslaMate 行程起点为准，并在单台 iPhone 开启功能时定向补发进行中的充电或导航实时活动 |
| 1.10.29 | 实时读取 TeslaMate 版本，并标明来源与检测时间；仅在实时读取失败时回退安装信息 |
| 1.0.0 | 只读停车状态历史接口 |
| 1.1.0 | 复用现有 TeslaMate API 认证边界 |
| 1.2.0 | 带观测时间及新鲜度限制的电量和额定续航数据 |
| 1.3.0 | 不可变的真实行程起点与增量轨迹分页 |
| 1.4.0 | 能力检测、容器与数据库加固、安全安装和卸载 |
| 1.4.1 | 校验版更新、回滚备份、统一入口验证及更完整的内网/代理说明 |
| 1.5.0 | 原生软件更新推送、持久去重、签名中继及 App 自动安全配对 |
| 1.5.1 | 修补 MQTT 与 Go 网络依赖，不改变 API 或部署方式 |
| 1.9.3 | 长期容量受控的停车事件、重复配对稳定性、日志轮换与资源限制 |
| 1.10.0 | 备份／恢复流程及有界临时推送状态 |
| 1.10.2 | App 兼容性信息、正式推送域名与安全解绑 |
| 1.10.4 | 带真实行程时间的目的地导航会话历史 |
| 1.10.5 | 推送历史路由与死锁修复 |
| 1.10.6 | 行驶中更改目的地时拆分为完整独立会话 |
| 1.10.7 | 记录真实起点名称，用于“起点 → 目的地”标题 |
| 1.10.8 | 更可靠的 `start_name`（围栏粘性 + 行程地址回填），起点 → 目的地 |
| 1.10.10 | 修正能力与诊断接口中的运行版本报告 |
| 1.10.19 | 已保存 MQTT 设置与实际 Docker 拓扑不一致时仍可可靠升级 |
| 1.10.28 | 固定使用官方中继、拒绝重定向、限制导航历史容量，并同步三语兼容性说明 |
| 1.10.27 | 每台 iPhone 独立的持久推送队列、偏好设置、失效设备隔离，以及更严格的数据库／网络超时 |

完整改动请查看 [CHANGELOG.md](CHANGELOG.md) 或
[最新 GitHub Release](https://github.com/MatchHar/My-T-Companion/releases/latest)。

## 哪些用户需要安装

符合以下条件时建议安装：

- 在 My T 中使用自建 TeslaMate 数据源。
- 希望查看可靠的长期停车休眠/唤醒历史，或完整的正在行驶轨迹。
- 可以管理 TeslaMate Docker 主机并执行 `sudo` 命令。
- TeslaMate API 已通过 HTTPS、VPN 和认证进行保护。

以下情况不需要安装：

- My T 只连接 Tessie。
- 只需要基础行程、充电和统计功能。
- 无法管理 TeslaMate 服务器。

组件不会提高 TeslaMate 的采集能力，只能返回 TeslaMate 实际保存的数据。

## 校验后安装固定版本

安装使用固定 GitHub Release，不直接执行会变化的 `main` 分支。已安装 GitHub CLI 时：

```sh
version="$(gh release view -R MatchHar/My-T-Companion --json tagName --jq '.tagName | ltrimstr("v")')"; workdir="$(mktemp -d)" && gh release download "v$version" -R MatchHar/My-T-Companion -D "$workdir" && (cd "$workdir" && sha256sum -c "my-t-companion-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-companion-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-companion-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

未安装 GitHub CLI 时：

```sh
version="$(curl -fsSL https://api.github.com/repos/MatchHar/My-T-Companion/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')"; test -n "$version" && workdir="$(mktemp -d)" && base="https://github.com/MatchHar/My-T-Companion/releases/download/v$version" && curl -fL "$base/my-t-companion-$version.tar.gz" -o "$workdir/my-t-companion-$version.tar.gz" && curl -fL "$base/my-t-companion-$version.tar.gz.sha256" -o "$workdir/my-t-companion-$version.tar.gz.sha256" && (cd "$workdir" && sha256sum -c "my-t-companion-$version.tar.gz.sha256") && tar -xzf "$workdir/my-t-companion-$version.tar.gz" -C "$workdir" && sudo "$workdir/my-t-companion-$version/install.sh"; status=$?; rm -rf "$workdir"; exit $status
```

只有本地服务和 My T 统一入口都验证成功，安装器才报告完整成功。手动使用
Nginx、Traefik 或容器代理时，必须加入并验证仓库提供的路由。安装器还会：

- 自动检测 TeslaMate 数据库容器和 Docker 网络。
- 复用现有数据库密码与 API 认证，不要求在 My T 中增加第二套账号。
- 支持 Bearer Token、Basic Auth、`X-API-Token` 和 Cloudflare Access
  Service Token。
- 在修改反向代理前创建备份。
- 检查现有 `/api/ping` 是否受到认证保护；如果公开可访问则拒绝安装。
- 自动处理可识别的系统 Caddy 配置。
- 对无法安全识别的 Nginx、Traefik、容器化 Caddy 或自定义布局停止自动修改，
  并提示手动加入 `Caddyfile.snippet` 中的路由。

## 提供的功能

- 显示 TeslaMate 真实记录的 `online`、`offline`、`asleep` 等状态区间。
- 显示状态开始前与结束后的真实电量百分比和额定续航。
- 边界数据携带真实观测时间；仅使用状态切换前后 30 分钟内的采样。
- 没有真实采样时显示未知，不估算唤醒事件、电量或续航消耗。
- 提供能力检测接口，让 App 区分“未部署”和“已部署但没有事件”。
- 实时计算仍在进行的停车状态，建议 App 每 30 秒刷新。
- 提供当前 TeslaMate 行程 ID、不可变的最早真实 GPS 点和带时间的轨迹点。
- 支持 `afterPointId` 增量分页，避免手机反复下载完整行驶轨迹。
- TeslaMate 已建立行程但尚未保存有效 GPS 点时，明确返回
  `waiting_for_positions`。
- 不会把 App 打开时看到的车辆位置伪装成真实行程起点。

## API 接口

- `GET /api/v1/capabilities`
- `GET /api/v1/cars/{car_id}/states?startDate=...&endDate=...`
- `GET /api/v1/cars/{car_id}/parking-events?startDate=...&endDate=...`
- `GET /api/v1/cars/{car_id}/navigation/current-drive?afterPointId=0&limit=5000`
- `GET /api/healthz`

所有车辆数据与能力接口都使用现有 TeslaMate API 认证。`/api/healthz`
不包含车辆数据，并且服务端口只绑定到本机回环地址。

## 安全部署要求

- Linux 主机已安装 Docker Engine 与 Docker Compose v2。
- TeslaMate Docker Compose 已正常运行。
- PostgreSQL 服务默认名为 `database`。
- TeslaMate `.env` 中存在 `DATABASE_PASS`。
- 现有 TeslaMate API 已通过 HTTPS、VPN 或 Cloudflare Access 保护。
- 端口 `8083` 必须绑定为 `127.0.0.1:8083`，不能直接开放到公网。

1.7.1 默认安全措施：

- 容器以非 root 用户 UID 10001 运行。
- 根文件系统只读。
- 移除全部 Linux capabilities。
- 启用 `no-new-privileges`。
- PostgreSQL 会话强制设置
  `PGOPTIONS=-c default_transaction_read_only=on`。
- 健康检查同时验证服务和数据库连接。
- HTTP 服务设置读取、写入、请求头和空闲超时。
- API Token 使用固定时间比较。

详细要求请阅读[安全政策](SECURITY.zh-Hans.md)。

## 验证

```sh
curl --fail http://127.0.0.1:8083/api/healthz
curl --fail \
  -H "Authorization: Bearer ${MY_T_API_TOKEN}" \
  http://127.0.0.1:8083/api/v1/capabilities
```

第一条应返回 `OK`。第二条应包含：

- `parking_state_history`
- `state_boundary_battery`
- `state_boundary_rated_range`
- `current_drive_trajectory`

还应确认未认证请求被拒绝：

```sh
curl -o /dev/null -w "%{http_code}\n" \
  http://127.0.0.1:8083/api/v1/capabilities
```

预期返回 `401`。

## 更新

以下永久命令会跟随 GitHub 的**最新稳定 Release**（不包含草稿和预发布版），
下载对应的固定版本、校验 SHA-256、备份现有安装，再运行可重复执行的安装程序：

```sh
sudo /opt/my-t-companion/update.sh
```

My T 也可能显示类似
`sudo MY_T_VERSION=<已验证版本> /opt/my-t-companion/update.sh`
的指定版本命令。这是有意设计：App 固定到该 App 版本已验证兼容的最新
Companion；永久命令则供明确希望跟随服务器最新稳定版的管理员使用。
可信部署工具还可设置
`MY_T_EXPECTED_SHA256=<签名目录中的摘要>`；即使 Release 清单也同时被更改，
更新程序仍会拒绝与签名目录不一致的安装包。

组件没有独立数据库或数据迁移。更新不会改变 TeslaMate 历史数据。

## 卸载或回滚

安装器管理的部署可执行：

```sh
sudo /opt/my-t-companion/uninstall.sh
```

卸载脚本会停止并移除独立 Companion 容器，以及安装器创建的 Caddy 路由；
`/opt/my-t-companion` 会保留以便恢复。手动配置的反向代理路由需要手动删除。
卸载不影响 TeslaMate 主服务及其数据库。

## 数据含义

对于 `offline` 或 `asleep` 区间：

- `start_telemetry` 是车辆休眠前最后一次真实观测。
- `end_telemetry` 是 TeslaMate 在车辆恢复后记录的第一次真实观测。

两者差值是“观测到的停车消耗”，不是估算值。正在进行的休眠在车辆真正唤醒前
不会产生结束观测。

## 未安装时的 App 行为

My T 通过 `/api/v1/capabilities` 自动检测组件。未安装或不可用时：

- 基础 TeslaMate 行程、充电和停车记录继续正常工作。
- App 会说明完整休眠、唤醒、电量与续航流水需要可选 VPS 组件。
- App 不会虚构缺失事件或估算电量消耗。
- 实时导航没有足够真实轨迹点时，只显示真实车辆位置和速度，不伪造起点或路线。

## 相容性与版本记录

- [兼容性与发布验证](COMPATIBILITY.zh-Hans.md)
- [安全政策](SECURITY.zh-Hans.md)
- [支持](SUPPORT.zh-Hans.md)
- [数据生命周期](DATA_LIFECYCLE.zh-Hans.md)
- [版本更新记录](CHANGELOG.md)
- [MIT License](LICENSE)

## 项目范围与独立性声明

这是专为 My T 设计的独立补充项目，与 Tesla, Inc.、TeslaMate 官方维护者及
TeslaMateAPI 维护者不存在隶属或官方认可关系。组件不保存 Tesla 账号凭证、
不发送车辆控制命令、不唤醒车辆，也不替代 TeslaMate 官方部署。

## 常见问题

### 会修改或复制 TeslaMate 历史吗？

不会。所有查询都在 PostgreSQL 只读事务模式下执行。组件没有自己的数据库、
数据迁移或后台采集器。

### 可以找回以前缺失的唤醒记录吗？

可以显示仍保存在 TeslaMate `states` 表中的历史事件，但无法恢复 TeslaMate
从未采集或已经按用户保留策略删除的数据。

### 会唤醒车辆或增加停车耗电吗？

不会。组件只读取数据库，不调用任何车辆控制接口。

### 安装后需要修改 My T 设置吗？

通常不需要。安装器复用现有 TeslaMate API 域名和认证，My T 会自动检测能力接口。

### 卸载会影响 TeslaMate 吗？

不会。Companion 是独立 Compose 项目；卸载脚本只移除自身容器和安装器管理的
代理路由，TeslaMate 主服务及数据库保持不变。
