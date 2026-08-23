# 資料生命週期

[English](DATA_LIFECYCLE.md) · [简体中文](DATA_LIFECYCLE.zh-Hans.md) · 繁體中文

My T Companion 將長期證據與可替換的執行狀態分開，而且絕不會修改或刪除
TeslaMate PostgreSQL 歷史資料。

| 資料 | 用途 | 預設生命週期 |
|---|---|---|
| 停車插槍、充電、安全及空調變化 | 重建長時間停車記錄 | 長期；最新 50,000 筆 |
| 軟體更新通知 ID | 防止重複提醒 | 180 天；最新 1,000 筆 |
| 充電即時動態傳送 ID | 防止重複遠端更新 | 14 天；最新 2,000 筆 |
| 導航即時動態傳送 ID | 防止重複遠端更新 | 7 天；最新 2,000 筆 |
| 進行中充電快照 | 恢復短暫中斷的傳送 | 48 小時 |
| 進行中導航快照、起點與路徑點 | 僅目前行程 | 12 小時 |
| 推播配對 | 私人的裝置至中繼設定 | 直到取消配對或替換 |
| 待重試推播 | 失敗傳送所需的最小簽章事件 | 依事件類型 10 分鐘至 24 小時；總計最新 256 筆 |

導航監控程式每 15 分鐘檢查一次進行中的工作階段。超過 12 小時的工作階段會透過正常結束事件路徑關閉，
確保鎖定畫面的即時動態會結束，而不是留下永久快照。

`PARKING_EVENT_RETENTION_DAYS=0` 代表預設長期政策。管理員可改為 30–3650 天。
`PARKING_EVENT_MAX_EVENTS` 預設為 50,000，可設定為 1,000–500,000；容量清理一定先刪除最舊的有效觀察。

## 備份與移轉

```sh
sudo /opt/my-t-companion/backup.sh
sudo /opt/my-t-companion/restore.sh /var/backups/my-t-companion/BACKUP.tar.gz
sudo /opt/my-t-companion/storage-status.sh
```

VPS 管理員負責靜態加密。備份檔案權限為 `0600`、附有校驗碼，並只保留最新 12 份。
預設備份包含長期停車證據與軟體通知去重資料，不包含暫時充電/導航狀態及推播配對密鑰；
只有可信任的移轉才使用 `backup.sh --include-pairing`。

待重試推播只儲存在使用者 VPS 的 Companion 資料卷及同一個 `0600` 配對儲存區。它不是營運稽核日誌，
會在成功、暫停、取消配對或逾期後刪除；除非可信任移轉明確包含配對資料，否則不會進入備份。

這不是 TeslaMate 備份。TeslaMate PostgreSQL 必須依 TeslaMate 官方流程獨立備份與還原。
