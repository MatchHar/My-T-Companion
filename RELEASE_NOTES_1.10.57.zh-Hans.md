# Companion 1.10.57

[English](RELEASE_NOTES_1.10.57.md) · [简体中文](RELEASE_NOTES_1.10.57.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.57.zh-Hant.md)

朋友同行在创建授权时增加可选的主人端设备标识。旧记录保持为空。列表中的
GrantView 带 `owner_device_id`。`GET /grants?owner_device_id=` 只返回该安装的
授权。空的 approve 请求体仍有效，并在有邀请主人设备时复制该标识；显式标识
与邀请不一致则拒绝。

本机日记缺失时，只能据此清理本机遗留授权。空标识或其他设备的授权不会被该
过滤选中。充电设置同步、停车、导航、配对和通知不变。

沿用现有认证安装／更新流程。发布不会自行更新运行中的服务器，配套 My T 更新
也需单独安装。本次目录钉选不是生产部署。
