# My T Companion 1.10.45

- Add optional Friend Together for mutually approved, time-limited sharing
  between independent TeslaMate owners. The feature stays **off** unless
  `FRIEND_TOGETHER_ENABLED=true` and a dedicated HTTPS guest origin are set.
- Keep parking history, navigation, pairing, APNs, and existing capabilities
  unchanged on a normal HostBox or CLI upgrade from 1.10.44.
- Advertise `friend_together_v1` only after the service is enabled and ready.
  Owner controls remain behind existing API authentication; guest `/friend/v1/*`
  must use a separate public origin and must not expose port 8083.
- Preserve the existing API, pairings, per-vehicle notification preferences,
  navigation history, polling rate, and vehicle wake behavior.

No database, Tesla token, or stored-data migration is required. Enabling the
new feature later still needs a dedicated guest HTTPS route; catalog promotion
alone does not create DNS or proxy configuration.
