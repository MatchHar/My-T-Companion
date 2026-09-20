# My T Companion 1.10.51

## Highlights

- Read existing tire-pressure history with recorded outside temperature, per
  vehicle, through the authenticated Companion API.
- Preserve original record timestamps, nullable readings and explicit units.
  Requests cover up to 31 days with bounded pagination; no coordinates are
  returned and no vehicle wake, new logger or weather service is introduced.
- Detect unsupported source schemas honestly, and add the endpoint to supplied
  installer, Caddy and Nginx routes. Existing features and settings are retained.

## Availability and limitations

The tire-history screen requires a compatible My T build; older App versions do
not gain the screen merely by upgrading Companion. History depends on records
already retained by TeslaMate. Record time is not a sensor measurement time,
and recorded outside temperature is not tire temperature. Missing values and
unsupported schemas are not replaced with invented readings.

## Upgrade and rollback

Use the verified release updater on an installer-managed deployment, or HostBox
after its signed catalog is promoted. Custom reverse proxies must forward the
new authenticated route. No database migration or history reset is required.
Keep the previous verified release and updater backup for rollback; reverting
Companion leaves TeslaMate history intact. See [compatibility and validation](COMPATIBILITY.md)
and the [API contract](docs/tire-pressure-history.md).
