# My T Companion 1.10.41

## Navigation and parking observation reliability

- Preserve the previous destination while staging partial next-stop data.
  Distinguish nearby stops, address aliases, and rerouting; require fresh
  parking evidence before reporting arrival. Traffic stops and disconnections
  are not arrival evidence.
- Distinguish queued, awaiting-token, and APNs-accepted delivery. Reconcile
  durable retries without allowing a stale update to overwrite an ended leg.
- Record charging stops only after actual charging. Plugged or waiting states
  no longer create false charge-stop events; repeated charging remains distinct.
- Add bounded four-door MQTT receipt/persistence diagnostics for investigating
  missing observations. No historical door events are fabricated.

Pairings, per-phone settings, per-vehicle overrides and stored parking history
are preserved. No TeslaMate migration, Tesla token change or extra vehicle
polling is required. Use My T 5.32 (599) and relay 1.4.10-cf for the coordinated
navigation fix. Automated tests do not replace physical locked-phone and
deliberate door-cycle verification.

1.10.40 was never published because its documentation gate failed. Its tag is
left untouched. 1.10.41 includes the reviewed repairs and an earlier archive
check for complete release notes.
