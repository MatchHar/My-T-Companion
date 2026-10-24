# Companion 1.10.61

[English](RELEASE_NOTES_1.10.61.md) · [简体中文](RELEASE_NOTES_1.10.61.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.61.zh-Hant.md)

TeslaMate 4.4.0 兼容修正。TeslaMate 现在会在车辆熄火后发布空的 `shift_state`，
可能导致“已锁车且安全”通知不再触发。Companion 现在只在车辆最后上报的挡位是 `P` 时，
才把空挡位视为已驻车；若最后是 `D`、`R`、`N` 或没有挡位记录，仍不视为驻车。
接口、设置与中继均无变化。
