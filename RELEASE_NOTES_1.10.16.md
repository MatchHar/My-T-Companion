# My T Companion 1.10.16

## Lock-secure reliability

- Establishes a per-vehicle baseline from retained TeslaMate MQTT values before
  emitting lock-secure alerts. Restarting Companion no longer treats an old
  already-locked state as a new lock event.
- Reports active MQTT readiness separately from saved notification settings.
- Advertises silent notifications, a user-imported alert sound, all six
  App-bundled sounds, and the system default.
- Sends only the lock-secure event to the relay. My T 4.13 and later choose the
  notification sound locally on each iPhone; filenames, audio, and selections
  do not leave the device.

This release remains backward compatible with older My T versions.

## Upgrade

```sh
sudo MY_T_VERSION=1.10.16 /opt/my-t-companion/update.sh
```
