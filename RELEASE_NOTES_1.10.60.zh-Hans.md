# Companion 1.10.60

[English](RELEASE_NOTES_1.10.60.md) · [简体中文](RELEASE_NOTES_1.10.60.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.60.zh-Hant.md)

新增只读车主接口 `GET /api/companion/v1/cars/<id>/road-coverage`（能力 `road_coverage_v1`），
供 My T 足迹地图使用。返回抽稀后的行程轨迹（约 60 m 间隔，最多 80,000 点）与 200 m 访问格，
可选 `from`/`to` 时间范围。仅限车主令牌；不写入、不分享、不发送到中继。预留 `mode` 字段供日后按驾驶模式着色。
