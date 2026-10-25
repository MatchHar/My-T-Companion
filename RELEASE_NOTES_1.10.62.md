# Companion 1.10.62

[English](RELEASE_NOTES_1.10.62.md) · [简体中文](RELEASE_NOTES_1.10.62.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.62.zh-Hant.md)

Compatibility fix for TeslaMate 4.4.0. TeslaMate now publishes an empty
`battery_level`, `state` or `charging_state` when the value is unknown,
which reset the low-battery baseline and could skip the next real alert (for
example 50% → empty → 19%). Companion now treats an empty value as unknown and
keeps the last known value, so the later drop still alerts once. An empty
`shift_state` keeps its parked meaning. No API, setting or relay change.
