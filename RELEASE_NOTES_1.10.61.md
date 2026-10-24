# Companion 1.10.61

[English](RELEASE_NOTES_1.10.61.md) · [简体中文](RELEASE_NOTES_1.10.61.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.61.zh-Hant.md)

Compatibility fix for TeslaMate 4.4.0. TeslaMate now publishes an empty
`shift_state` after the car switches off, which could stop the "locked and
secure" notification from firing. Companion now treats an empty gear as parked
only when the last gear the car reported was `P`. After `D`, `R` or `N`, or
with no gear history, it is still not treated as parked. No API, setting or
relay change.
