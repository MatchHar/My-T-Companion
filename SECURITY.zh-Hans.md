# 安全政策

[English](SECURITY.md) · 简体中文 · [繁體中文](SECURITY.zh-Hant.md)

## 支持版本

安全修复提供给[最新正式稳定版](https://github.com/MatchHar/My-T-Companion/releases/latest)。
源码版本只在 [`VERSION`](VERSION) 定义一次，因此本政策不会随每次发布而过期。

## 部署要求

- 服务只能绑定到 `127.0.0.1`。
- 所有数据端点必须位于 HTTPS 及现有 TeslaMate API 的同一验证边界后方。
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
