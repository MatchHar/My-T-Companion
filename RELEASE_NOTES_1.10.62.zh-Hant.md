# Companion 1.10.62

[English](RELEASE_NOTES_1.10.62.md) · [简体中文](RELEASE_NOTES_1.10.62.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.62.zh-Hant.md)

TeslaMate 4.4.0 相容修正。TeslaMate 現在會在數值未知時發布空的 `battery_level`、
`state` 或 `charging_state`，這會重置低電量基準，可能漏掉下一次真正的提醒（例如
50% → 空 → 19%）。Companion 現在把空值視為未知並保留最後已知值，之後的下降仍會提醒一次。
空的 `shift_state` 仍視為駐車。介面、設定與中繼皆無變更。
