# Companion 1.10.58

[English](RELEASE_NOTES_1.10.58.md) · [简体中文](RELEASE_NOTES_1.10.58.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.58.zh-Hant.md)

编译和验证改用 Go 1.26.10。Go 1.26.9 的 crypto/x509 受 GO-2026-6809 影响，
1.26.10 包含该修复。Docker 构建镜像为 golang:1.26.10-alpine。模块依赖保持
不变，包括 golang.org/x/net 0.60.0。

朋友同行的主人端设备标识、充电设置同步、停车、导航、配对和通知不变。

本次提交不是 GitHub release，也没有 tag。安装和更新仍沿用现有认证流程。
仅发布不会更新正在运行的服务器。生产环境仍是此前已部署的 Companion 1.10.56。
