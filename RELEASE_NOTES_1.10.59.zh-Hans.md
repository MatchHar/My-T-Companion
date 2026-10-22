# Companion 1.10.59

[English](RELEASE_NOTES_1.10.59.md) · [简体中文](RELEASE_NOTES_1.10.59.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.59.zh-Hant.md)

到达通知在可计算时附上本次行程能耗（kWh）与能效（Wh/km），不增加任何坐标。

新增可选的 ETA 分享链接，仅在 `ETA_SHARE_ENABLED=true` 时启用。链接使用随机令牌（只保存哈希），
最长 4 小时，导航结束即失效，可随时撤销，并有速率限制。页面显示预计到达时间；仅当车主为该链接
开启时才显示约略位置（约 110 m）。分享只保存在内存中，重启即全部失效。
