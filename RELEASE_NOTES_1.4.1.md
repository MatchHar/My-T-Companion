# My T VPS Companion 1.4.1

This is the first public release of the optional read-only companion used by
My T Parking Monitor and enhanced live navigation.

## Highlights

- Installs and updates from immutable GitHub Release assets.
- Verifies the release archive with the accompanying SHA-256 manifest.
- Preserves a recovery backup before an update.
- Verifies both the local service and the authenticated unified My T route.
- Supports system Caddy automatically when its protected API route can be
  identified, with documented manual routes for other proxy layouts.

## Data and privacy

The companion does not create a vehicle database, copy TeslaMate history, call
Tesla APIs, wake a vehicle, or issue vehicle commands. It queries TeslaMate's
`states`, `drives`, and `positions` data in PostgreSQL read-only transaction
mode and returns derived JSON only when requested.

## Compatibility

- TeslaMate must already be deployed and working.
- Docker Engine and Docker Compose v2 are required.
- My T must reach TeslaMateAPI and all three companion routes through the same
  protected base URL.
- VPS, VPN, Tailscale, and private-LAN deployments are supported through a
  unified reverse proxy.
- Port `8083` remains loopback-only.

## Upgrade

After installing an earlier release through the managed installer:

```sh
sudo MY_T_VERSION=1.4.1 /opt/my-t-parking-monitor/update.sh
```

The published GitHub Release must contain:

- `my-t-parking-monitor-1.4.1.tar.gz`
- `my-t-parking-monitor-1.4.1.tar.gz.sha256`

The exact checksum shown by GitHub must match the downloaded manifest before
installation.

## Rollback and removal

The updater keeps a recovery backup of the prior installation. For a managed
deployment, removal is available through:

```sh
sudo /opt/my-t-parking-monitor/uninstall.sh
```

This removes the standalone container and installer-managed proxy routes while
leaving TeslaMate and its PostgreSQL history intact.

## Known limitations

- Historical completeness is limited to observations retained by TeslaMate.
- Battery and rated-range boundary values are returned only when a real sample
  exists within 30 minutes of the transition.
- Custom reverse proxies require an administrator to add the three supplied
  routes and verify authentication.
- The public My T 3.10 build does not expose Parking Monitor integration.
  Installing this release early does not add those app screens.
