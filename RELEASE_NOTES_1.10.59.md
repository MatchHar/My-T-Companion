# Companion 1.10.59

[English](RELEASE_NOTES_1.10.59.md) · [简体中文](RELEASE_NOTES_1.10.59.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.59.zh-Hant.md)

Arrival notifications now include trip energy (kWh) and efficiency (Wh/km)
when they can be computed. No coordinates are added.

New optional ETA share links, off unless `ETA_SHARE_ENABLED=true`. Links use
random tokens (only hashes are stored), last at most 4 hours, end when
navigation ends, can be revoked, and are rate-limited. The page shows ETA;
approximate position (~110 m) appears only when the owner turns it on for that
link. Shares are kept in memory, so a restart ends them.
