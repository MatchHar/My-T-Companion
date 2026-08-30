# My T Companion 1.10.35

## Low-battery notifications and reliable multi-iPhone delivery

- Add an optional notification when a selected TeslaMate vehicle becomes
  parked, not charging, and strictly below 20 percent. It uses TeslaMate MQTT
  data only and never wakes or polls the vehicle.
- Let each iPhone acknowledge the alert or snooze it for four hours. A snooze
  can produce one reminder only if the vehicle is still eligible; charging
  cancels that reminder, a drop below 10 percent sends one stronger alert, and
  the episode rearms only after the battery reaches 25 percent.
- Keep the first complete retained MQTT snapshot silent so an already-low
  vehicle does not generate a false startup alert.
- Give every paired iPhone its own deterministic relay event identity and honor
  relay retry timing, preventing one device from suppressing another device's
  charging, navigation, destination, or low-battery notification.

Low-battery notifications are off by default and require a compatible My T
version. No Tesla login, database migration, push re-pairing, or vehicle wake
is required.
