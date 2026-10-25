# Companion 1.10.62

[English](RELEASE_NOTES_1.10.62.md) · [简体中文](RELEASE_NOTES_1.10.62.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.62.zh-Hant.md)

TeslaMate 4.4.0 兼容修正。TeslaMate 现在会在数值未知时发布空的 `battery_level`、
`state` 或 `charging_state`，这会重置低电量基准，可能漏掉下一次真正的提醒（例如
50% → 空 → 19%）。Companion 现在把空值视为未知并保留最后已知值，之后的下降仍会提醒一次。
空的 `shift_state` 仍视为驻车。接口、设置与中继均无变化。
