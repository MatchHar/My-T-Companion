# My T Companion 1.10.48

- Defer the destination trip-start banner when `start_name` is not yet known.
- Keep Live Activity updates flowing immediately; send an alert-only start once
  origin enrichment arrives, or after about eight seconds.
- Lets the push relay show 「从…前往…」 / from–to when origin resolves after the
  route becomes active.
- No database migration. Friend Together defaults unchanged.
