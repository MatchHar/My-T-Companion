# My T Companion 1.10.4

- 導航推送工作階段落庫（目的地、里程、用時），供 App 透過 `GET /api/v1/cars/{id}/navigation/push-history` 讀取。
- 新增能力：`navigation_push_history`。
- 導航結束事件攜帶真實行程時間：`trip_started_at`、`trip_ended_at`、`duration_minutes` 及行駛里程，用於鎖定畫面「已到達」結束幀。
- 推送中繼僅信任 `https://push.my-tesla.app/v1/events`。
- 包含 1.10.3 歷史 API 與 1.10.2 網域/解綁加固。
