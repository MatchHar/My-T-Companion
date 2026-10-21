# Companion 1.10.58

[English](RELEASE_NOTES_1.10.58.md) · [简体中文](RELEASE_NOTES_1.10.58.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.58.zh-Hant.md)

編譯和驗證改用 Go 1.26.10。Go 1.26.9 的 crypto/x509 受 GO-2026-6809 影響，
1.26.10 包含該修復。Docker 建置映像為 golang:1.26.10-alpine。模組依賴保持
不變，包括 golang.org/x/net 0.60.0。

朋友同行的主人端裝置識別、充電設定同步、停車、導航、配對和通知不變。

本次提交不是 GitHub release，也沒有 tag。安裝和更新仍沿用現有認證流程。
僅發布不會更新正在執行的伺服器。生產環境仍是此前已部署的 Companion 1.10.56。
