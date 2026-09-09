# My T Companion 1.10.47

- 修復 HostBox 啟用朋友同行後，My T 建立邀請失敗。GET `/status` 已顯示就緒，
  POST `/invitations` 因獨立唯讀連線池使用 `postgres:?host=` 連不上 TeslaMate
  資料庫而回傳 503 `source_unavailable`。
- 改用與停車／通知相同的 keyword DSN，並在提供服務前 ping。
- 朋友同行仍預設關閉。無需資料庫遷移。
