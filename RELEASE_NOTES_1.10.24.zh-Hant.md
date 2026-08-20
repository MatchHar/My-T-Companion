# My T Companion 1.10.24

推送改為每支 iPhone 一個 `installation_id`。切換 TeslaMate 伺服器時，只暫停這支手機在舊 VPS 上的推送，不會清掉其他人。再切回來仍是同一列，不是新手機。

每支手機可分別開關軟體更新、鎖車、充電鎖屏、目的地行程即時動態，並可勾選車輛。

舊版 `POST /pair` 仍是加入/更新。多人時 `DELETE /pair` 必須帶本機 installation 標頭。

向後兼容。`recommended_version` 仍為 3.30。
