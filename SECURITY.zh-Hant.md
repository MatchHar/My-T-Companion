# 安全性政策

[English](SECURITY.md) · [简体中文](SECURITY.zh-Hans.md) · 繁體中文

## 支援版本

安全性修正提供給[最新正式穩定版](https://github.com/MatchHar/My-T-Companion/releases/latest)。
原始碼版本只在 [`VERSION`](VERSION) 定義一次，因此本政策不會隨每次發布而過期。

## 部署要求

- 服務只能繫結至 `127.0.0.1`。
- 所有資料端點必須位於 HTTPS 及既有 TeslaMate API 的同一驗證邊界後方。
- 重複使用驗證前，確認未驗證的 `/api/ping` 要求會被拒絕。
- PostgreSQL 只能保留在私人 Docker 網路。
- 不得移除 `PGOPTIONS=-c default_transaction_read_only=on`。
- 保留隨附 Compose 檔案中的容器強化選項。
- 獨立備份 TeslaMate 並實際驗證還原；本擴充功能的備份不能取代 TeslaMate 資料庫備份。

## 回報安全漏洞

請勿在公開 Issue 中包含憑證、伺服器位址、車輛位置、VIN 或資料庫匯出。
涉及安全性的問題請使用本存放庫的[私密漏洞回報](https://github.com/MatchHar/My-T-Companion/security/advisories/new)。
一般支援問題只有在移除所有正式環境密鑰與私人車輛資料後，才可使用公開 Issue 範本。

請提供 Companion 版本、TeslaMate 版本、反向代理類型及已去識別化的重現步驟。
請勿附加 `.env` 或原始正式環境日誌。
