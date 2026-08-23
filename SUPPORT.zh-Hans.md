# 支持

[English](SUPPORT.md) · 简体中文 · [繁體中文](SUPPORT.zh-Hant.md)

提交问题前：

1. 确认 TeslaMate 本身健康并仍在记录目标车辆。
2. 确认 My T 的普通 TeslaMateAPI 连接可以使用。
3. 在主机运行 `curl --fail http://127.0.0.1:8083/api/healthz`。
4. 确认统一 API 地址会把 `/api/v1/capabilities` 路由到 Companion，且需要验证。
5. 使用可选的软件通知时，检查经过验证的 `/api/v1/notifications/software-update/status` 响应。
6. 记录 My T、Companion、TeslaMate、Docker 与反向代理版本。

请提供已脱敏的错误文字和复现步骤。不得公开 `.env`、Token、Cookie、数据库密码、
中继密钥、安装 ID、设备 Token、公网服务器地址、VIN、精确位置、数据库导出或原始生产日志。

可复现的功能错误及文档问题使用普通 GitHub Issue；安全问题使用 GitHub 私密漏洞报告。
确认问题不是 Companion 引起后，TeslaMate 和 TeslaMateAPI 问题应分别提交给对应上游项目。

本组件只返回 TeslaMate 实际记录的数据。缺失的历史休眠/唤醒或电池观察无法事后重建。
