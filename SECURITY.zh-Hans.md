# 安全政策

[English](SECURITY.md) · 简体中文 · [繁體中文](SECURITY.zh-Hant.md)

## 支持版本

安全修复提供给[最新正式稳定版](https://github.com/MatchHar/My-T-Companion/releases/latest)。
源码版本只在 [`VERSION`](VERSION) 定义一次，因此本政策不会随每次发布而过期。

## 部署要求

- 服务只能绑定到 `127.0.0.1`。
- 所有车主数据／控制端点（包括 `/api/v1/friend-together/*`）必须位于 HTTPS
  及现有 TeslaMate API 的同一验证边界后方。
- 唯一例外是可选、默认关闭的朋友同行候选功能：只在**独立 HTTPS 分享域名**
  公开 `/friend/v1/*`，保留配置的 Host。访客使用设备绑定 DPoP 与车主明确批准的
  限时授权，不使用 TeslaMate 车主凭据或车主验证探测。取得 nonce／兑换邀请不会
  授予车辆数据。不能把访客路由放在车主 Basic／Cloudflare Access 后，也不能放宽
  车主网站的保护；其他路由、数据库／MQTT 或完整服务器 API 不能公开在分享域名。
  详见[候选功能部署边界](docs/friend-together.md)。
- 复用验证前，确认未验证的 `/api/ping` 请求会被拒绝。
- PostgreSQL 只留在私有 Docker 网络。
- 不得移除 `PGOPTIONS=-c default_transaction_read_only=on`。
- 保留随附 Compose 文件中的容器加固选项。
- 独立备份 TeslaMate 并实际验证恢复；本扩展的备份不能替代 TeslaMate 数据库备份。

## 报告安全漏洞

不要在公开 Issue 中包含凭据、服务器地址、车辆位置、VIN 或数据库导出。
涉及安全的问题请使用本仓库的[私密漏洞报告](https://github.com/MatchHar/My-T-Companion/security/advisories/new)。
普通支持问题只有在删除全部生产密钥和私有车辆数据后，才可使用公开 Issue 模板。

请提供 Companion 版本、TeslaMate 版本、反向代理类型及已脱敏的复现步骤。
不要附加 `.env` 或原始生产日志。
