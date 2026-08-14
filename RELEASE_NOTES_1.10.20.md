# My T Companion 1.10.20

This release fixes HostBox upgrades on Tunnel VPS boxes where a previous edge
left TeslaMateAPI on loopback `:18081`.

- Never treats leftover `:18081` as the public API port.
- A failed public-edge capabilities check restores compose and continues;
  Tunnel installs can finish on `:8083`.
- Skips creating a new 8081 edge when Companion on `:8083` already answers
  `/api/v1/capabilities`.
- No API or stored-data migration; backward compatible with previous My T
  versions.

Update an existing installation:

```bash
sudo MY_T_VERSION=1.10.20 /opt/my-t-companion/update.sh
```
