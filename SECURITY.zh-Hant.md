# 安全性政策

[English](SECURITY.md) · [简体中文](SECURITY.zh-Hans.md) · 繁體中文

## 支援版本

安全性修正提供給[最新正式穩定版](https://github.com/MatchHar/My-T-Companion/releases/latest)。
原始碼版本只在 [`VERSION`](VERSION) 定義一次，因此本政策不會隨每次發布而過期。

## 部署要求

- 服務只能繫結至 `127.0.0.1`。
- 所有車主資料／控制端點（包括 `/api/v1/friend-together/*`）必須位於 HTTPS
  及既有 TeslaMate API 的同一驗證邊界後方。
- 唯一例外是選用、預設關閉的朋友同行候選功能：只在**獨立 HTTPS 分享網域**
  公開 `/friend/v1/*`，保留設定的 Host。訪客使用裝置綁定 DPoP 與車主明確核准的
  限時授權，不使用 TeslaMate 車主憑證或車主驗證探測。取得 nonce／兌換邀請不會
  授予車輛資料。不能把訪客路由放在車主 Basic／Cloudflare Access 後，也不能放寬
  車主網站的保護；其他路由、資料庫／MQTT 或完整伺服器 API 不能公開在分享網域。
  詳見[候選功能部署邊界](docs/friend-together.md)。
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
