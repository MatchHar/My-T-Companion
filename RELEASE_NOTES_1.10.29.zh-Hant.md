# My T Companion 1.10.29

## 修正 TeslaMate 版本顯示

- Companion 現在會從 TeslaMate 內部設定頁面即時讀取目前版本。TeslaMate
  升級後，安裝時儲存的舊 `TESLAMATE_VERSION` 不會再覆蓋即時結果。
- 能力介面新增 `teslamate_version_source` 與
  `teslamate_version_checked_at`，My T 可據此區分即時資料與安裝資訊的備援值。
- 安裝與更新會優先從正在運作的 TeslaMate 容器取得備援版本，最後才讀取舊設定。

## 發布與交付強化

- 安裝、Docker 建置及發布完整性檢查都已包含新的版本偵測檔案。
- 受信任的部署工具可使用獨立簽署的 HostBox 目錄摘要再次驗證發布壓縮檔。
- 持續超過 12 小時的遺留導航工作階段現在會透過正常結束事件關閉。
- 版本標籤包含弱點檢查、可重複產生的壓縮檔、校驗值及建置來源證明。

不需要遷移資料庫，也不需要重新配對推播。
