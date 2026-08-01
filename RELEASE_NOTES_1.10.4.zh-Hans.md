# My T Companion 1.10.4

- 导航推送会话落库（目的地、里程、用时），供 App 通过 `GET /api/v1/cars/{id}/navigation/push-history` 读取。
- 新增能力：`navigation_push_history`。
- 导航结束事件携带真实行程时间：`trip_started_at`、`trip_ended_at`、`duration_minutes` 及行驶里程，用于锁屏「已到达」结束帧。
- 推送中继仅信任 `https://push.my-tesla.app/v1/events`。
- 包含 1.10.3 历史 API 与 1.10.2 域名/解绑加固。
