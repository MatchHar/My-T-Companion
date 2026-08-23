# 支援

[English](SUPPORT.md) · [简体中文](SUPPORT.zh-Hans.md) · 繁體中文

提交問題前：

1. 確認 TeslaMate 本身運作正常，且仍在記錄目標車輛。
2. 確認 My T 的一般 TeslaMateAPI 連線可以使用。
3. 在主機執行 `curl --fail http://127.0.0.1:8083/api/healthz`。
4. 確認統一 API 位址會將 `/api/v1/capabilities` 路由至 Companion，且需要驗證。
5. 使用選用的軟體通知時，檢查經過驗證的 `/api/v1/notifications/software-update/status` 回應。
6. 記錄 My T、Companion、TeslaMate、Docker 與反向代理版本。

請提供已去識別化的錯誤文字與重現步驟。不得公開 `.env`、Token、Cookie、資料庫密碼、
中繼密鑰、安裝 ID、裝置 Token、公網伺服器位址、VIN、精確位置、資料庫匯出或原始正式環境日誌。

可重現的功能錯誤及文件問題請使用一般 GitHub Issue；安全性問題請使用 GitHub 私密漏洞回報。
確認問題不是 Companion 引起後，TeslaMate 與 TeslaMateAPI 問題應分別提交至對應的上游專案。

本元件只會傳回 TeslaMate 實際記錄的資料。缺少的歷史休眠/喚醒或電池觀察無法事後重建。
