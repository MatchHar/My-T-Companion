# Companion 1.10.60

[English](RELEASE_NOTES_1.10.60.md) · [简体中文](RELEASE_NOTES_1.10.60.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.60.zh-Hant.md)

新增唯讀車主端點 `GET /api/companion/v1/cars/<id>/road-coverage`（能力 `road_coverage_v1`），
供 My T 足跡地圖使用。回傳抽稀後的行程軌跡（約 60 m 間隔，最多 80,000 點）與 200 m 造訪格，
可選 `from`/`to` 時間範圍。僅限車主權杖；不寫入、不分享、不送往中繼。保留 `mode` 欄位供日後依駕駛模式著色。
