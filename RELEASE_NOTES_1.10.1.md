# My T Companion 1.10.1

This reliability release improves destination-navigation Live Activities.

- Navigation can start from genuine driving state and destination data before
  optional distance or ETA values become available.
- Active sessions survive a Companion restart long enough to reconcile with
  fresh TeslaMate MQTT state. An unconfirmed restored session emits a real end
  event instead of being silently discarded and leaving the Lock Screen stuck.
- Navigation end events use an independent delivery worker and are no longer
  delayed behind retries for ordinary updates.
- New regression tests cover early start and terminal-event priority.

No coordinates, routes, credentials, VIN, or driving history are added to push
payloads. The release remains backward compatible with normal TeslaMate use.

Update an installer-managed deployment with:

```bash
sudo /opt/my-t-companion/update.sh
```
