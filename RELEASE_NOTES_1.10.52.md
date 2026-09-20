# Companion 1.10.52

- A stable authenticated owner API under `/api/companion/v1/`. Ordinary new resources no longer require a new external routing rule after the one-time connection migration.
- HostBox 3.01 and updated My T use this boundary. Existing owner URLs are temporary aliases to the same handlers; guest sharing and the stock TeslaMate API remain separate.
- Command-line recommended installation and updates use the signed HostBox recommendation and verified release archive. Updates reject unknown installations, ordinary downgrades, conflicting same-version receipts and simultaneous operations.
- Preserves vehicle history, credentials and notification pairing. Tire history reads TeslaMate's recorded pressure and outside temperature, not synthetic weather.

For remotely managed Cloudflare tunnels, use HostBox 3.01 connection repair once to add the fixed owner prefix. Companion cannot modify Cloudflare account settings using a tunnel connector token. Retain existing access protection. Custom routing conflicts stop without overwriting the conflicting configuration.
