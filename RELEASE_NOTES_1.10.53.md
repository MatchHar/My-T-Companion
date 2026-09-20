# Companion 1.10.53

- Distinguish current personnel-detection context from retained MQTT snapshots, reconnects and sleeping vehicles. My T can display unconfirmed or last-known information through the existing authenticated status API.
- Recheck fresh lock transitions, known closed doors/trunks, live presence evidence and connection health after a short settling period. Retained startup messages cannot trigger a new lock reminder.
- Preserve per-phone/per-vehicle preferences, pairing and history. No new tunnel rule is needed for this upgrade from 1.10.52.

Presence detection is advisory, not proof that every seat, child or pet is absent. If current evidence is insufficient, a reminder is deliberately suppressed; please always check the cabin yourself.
