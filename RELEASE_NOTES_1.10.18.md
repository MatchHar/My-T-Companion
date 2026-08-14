# My T Companion 1.10.18

This release fixes duplicate navigation lifecycle notifications.

- Keeps the current navigation session when Tesla changes only the destination
  label, such as replacing a street address with `Home`, without materially
  changing the remaining route.
- Sends an explicit navigation end reason so the relay can distinguish a true
  arrival from a redirect or cancellation.
- Preserves genuine mid-drive destination changes and remains backward
  compatible with previous My T versions.

Update an existing installation:

```bash
sudo MY_T_VERSION=1.10.18 /opt/my-t-companion/update.sh
```
