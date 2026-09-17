# My T Companion 1.10.48

- 導航開始時若尚無 `start_name`，先送 Live Activity，短時間等待起點補齊後再發行程開始通知。
- 起點補齊後（或約 8 秒逾時）再送僅通知的 `navigation_started`，以便推播顯示「從…前往…」。
- 行程結束或改道時取消等待中的開始通知。
- 無資料庫遷移；Friend Together 預設仍為關閉。
