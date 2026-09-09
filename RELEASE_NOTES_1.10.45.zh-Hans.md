# My T Companion 1.10.45

- 加入可选的朋友同行：独立 TeslaMate 车主之间经双方同意的限时分享。默认关闭，
  只有设置 `FRIEND_TOGETHER_ENABLED=true` 并配置独立 HTTPS 访客域名后才会启用。
- 从 1.10.44 用 HostBox 或命令行常规升级时，停车、导航、配对、APNs 与原有
  能力保持不变。
- 仅在功能启用且就绪后才公布 `friend_together_v1`。车主接口仍走原有 API 认证；
  访客 `/friend/v1/*` 必须使用独立公开域名，不能公开 8083。
- 保留现有 API、配对、分车通知偏好、导航历史、轮询频率与车辆唤醒行为。

不需要数据库、Tesla 令牌或已有数据迁移。之后启用该功能仍需单独配置访客 HTTPS
路由；只更新 HostBox 目录不会自动创建 DNS 或代理。
