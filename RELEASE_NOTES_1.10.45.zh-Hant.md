# My T Companion 1.10.45

- 加入選用的朋友同行：獨立 TeslaMate 車主之間經雙方同意的限時分享。預設關閉，
  只有設定 `FRIEND_TOGETHER_ENABLED=true` 並配置獨立 HTTPS 訪客網域後才會啟用。
- 從 1.10.44 用 HostBox 或命令列常規升級時，停車、導航、配對、APNs 與原有
  能力維持不變。
- 僅在功能啟用且就緒後才公布 `friend_together_v1`。車主接口仍走原有 API 驗證；
  訪客 `/friend/v1/*` 必須使用獨立公開網域，不能公開 8083。
- 保留現有 API、配對、分車通知偏好、導航歷史、輪詢頻率與車輛喚醒行為。

不需要資料庫、Tesla 權杖或既有資料遷移。之後啟用該功能仍需另外設定訪客 HTTPS
路由；只更新 HostBox 目錄不會自動建立 DNS 或代理。
