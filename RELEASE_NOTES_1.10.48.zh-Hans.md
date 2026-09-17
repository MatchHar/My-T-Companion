# My T Companion 1.10.48

- 导航开始时若还没有 `start_name`，先发 Live Activity，短时等待起点补全后再发行程开始通知。
- 起点补全后（或约 8 秒超时）再发仅通知的 `navigation_started`，便于推送显示「从…前往…」。
- 行程结束或改道时取消等待中的开始通知。
- 无数据库迁移；Friend Together 默认仍为关闭。
