# Companion 1.10.59

[English](RELEASE_NOTES_1.10.59.md) · [简体中文](RELEASE_NOTES_1.10.59.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.59.zh-Hant.md)

抵達通知在可計算時附上本次行程能耗（kWh）與能效（Wh/km），不增加任何座標。

新增可選的 ETA 分享連結，僅在 `ETA_SHARE_ENABLED=true` 時啟用。連結使用隨機代碼（只保存雜湊），
最長 4 小時，導航結束即失效，可隨時撤銷，並有速率限制。頁面顯示預計抵達時間；僅當車主為該連結
開啟時才顯示約略位置（約 110 m）。分享只保存在記憶體中，重啟即全部失效。
