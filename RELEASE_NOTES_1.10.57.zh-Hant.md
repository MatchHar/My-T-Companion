# Companion 1.10.57

[English](RELEASE_NOTES_1.10.57.md) · [简体中文](RELEASE_NOTES_1.10.57.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.57.zh-Hant.md)

朋友同行在建立授權時新增可選的主人端裝置識別。舊紀錄維持為空。清單中的
GrantView 帶 `owner_device_id`。`GET /grants?owner_device_id=` 只回傳該安裝的
授權。空的 approve 請求體仍有效，並在有邀請主人裝置時複製該識別；顯式識別
與邀請不一致則拒絕。

本機日記缺失時，只能據此清理本機遺留授權。空識別或其他裝置的授權不會被該
過濾選中。充電設定同步、停車、導航、配對和通知不變。

沿用現有認證安裝／更新流程。發布不會自行更新執行中的伺服器，配套 My T 更新
也需單獨安裝。本次目錄釘選不是生產部署。
