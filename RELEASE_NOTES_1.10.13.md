# My T Companion 1.10.13

## What changed

- **Optional lock-secure push:** when the vehicle is locked and no one is
  present, Companion can send a signed push via the existing APNs relay
  (`vehicle_lock_secure`). Off by default; enable only after push pairing.
- New prefs API: `GET/PUT /api/v1/notifications/lock-secure` (same auth as
  other Companion routes; already covered by `/api/v1/notifications/*` gateway
  routes).
- Capabilities advertise `lock_secure_push`. Older My T builds that do not
  know this flag simply ignore it.
- Optional custom APNs sound names (whitelist of App-bundled `.caf` + `default`).

## Backward compatibility (important)

- **Does not break previous My T versions.** Existing parking, navigation,
  charging Live Activity, software-update push, and all prior APIs are
  unchanged.
- `app_compatibility.minimum_version` remains **3.10**;
  `recommended_version` remains **3.30** (no forced App upgrade).
- New endpoints and the `lock_secure_push` capability are **additive**.
  Old App builds continue to use the same base URL and Token.
- Lock-secure stays disabled until a compatible My T build enables it after
  server confirmation.

## Upgrade

```sh
sudo /opt/my-t-companion/update.sh
# or
sudo MY_T_VERSION=1.10.13 /opt/my-t-companion/update.sh
```

TeslaMate data and pairing state are preserved. No database migration.

## Rollback

```sh
sudo MY_T_VERSION=1.10.12 /opt/my-t-companion/update.sh
```

Or reinstall from the automatic backup directory created under
`/opt/my-t-companion.before-1.10.13-*`.
