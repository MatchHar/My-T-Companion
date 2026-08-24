# 兼容性与发布验证

[English](COMPATIBILITY.md) · 简体中文 · [繁體中文](COMPATIBILITY.zh-Hant.md)

1.5.0 加入可选的 TeslaMate MQTT 软件更新观察与签名中继投递。1.7.1
加入目的地导航实时活动投递，并保留充电实时活动及已修补的构建依赖，API
和部署方式没有改变。未配对推送时，停车与导航功能仍可使用。

1.10.30 与 TeslaMate 4.2.0、上一稳定版 4.1.1 以及 TeslaMateAPI 1.25.0
兼容。实时活动定向补发继续使用现有订阅设备登记，不需要数据库迁移或重新配对。

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
- 历史状态响应暂未分页；客户端应请求有边界的停车时段。
