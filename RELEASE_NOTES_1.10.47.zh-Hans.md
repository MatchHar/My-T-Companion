# My T Companion 1.10.47

- 修复 HostBox 启用朋友同行后，My T 建立邀请失败。GET `/status` 已显示就绪，
  POST `/invitations` 因独立只读连接池使用 `postgres:?host=` 连不上 TeslaMate
  数据库而返回 503 `source_unavailable`。
- 改用与停车／通知相同的 keyword DSN，并在提供服务前 ping。
- 朋友同行仍默认关闭。无需数据库迁移。
