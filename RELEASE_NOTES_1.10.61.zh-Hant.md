# Companion 1.10.61

[English](RELEASE_NOTES_1.10.61.md) · [简体中文](RELEASE_NOTES_1.10.61.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.61.zh-Hant.md)

TeslaMate 4.4.0 相容修正。TeslaMate 現在會在車輛熄火後發布空的 `shift_state`，
可能導致「已上鎖且安全」通知不再觸發。Companion 現在只在車輛最後回報的檔位是 `P` 時，
才把空檔位視為已駐車；若最後是 `D`、`R`、`N` 或沒有檔位紀錄，仍不視為駐車。
介面、設定與中繼皆無變更。
