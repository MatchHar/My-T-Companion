# My T Companion 1.10.43

- Keep ordered TeslaMate parking-event handling responsive by moving full JSON
  snapshot writes out of the MQTT callback.
- Coalesce nearby changes into one durable snapshot and flush the latest state
  when the service stops normally.
- Preserve rapid four-door open/close transitions in arrival order, including
  the existing bounded per-door receipt diagnostics.
- Apply the same persistence path to windows, lock, plug, charging, climate,
  preconditioning, battery heating and charge-port actions.
- Avoid redundant writes for unchanged ordinary telemetry. Existing API,
  pairings, notification preferences and parking history remain compatible.

This release improves retention after TeslaMate publishes an observation. It
does not reconstruct an action that TeslaMate never emitted, and it does not
increase vehicle polling or wake a vehicle.
