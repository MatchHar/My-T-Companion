# My T Companion 1.10.31

## Privacy-minimal vehicle statistics

- Companion now reports one stable anonymous alias for each selected TeslaMate
  car so the protected My T administration page can show vehicle counts and
  first/last-seen dates.
- Multiple paired phones using the same TeslaMate car are counted as one
  vehicle. Cars on different TeslaMate installations remain separate.
- Reports contain no vehicle name, VIN, raw car ID, server identity, Apple ID,
  device identity, location, or driving data.
- Inventory is refreshed after pairing, at startup, and once per day.

No database migration or push re-pairing is required.
