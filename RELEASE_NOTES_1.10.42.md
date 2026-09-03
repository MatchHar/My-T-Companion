# My T Companion 1.10.42

- Coordinate multi-stop navigation, destination changes and arrival handling with My T 5.32 (599) and Push Relay 1.4.10-cf.
- Keep queued notifications distinct from APNs-accepted delivery; isolate activity tokens by vehicle and journey.
- Classify charging-stop events only after observed charging, and add bounded per-door MQTT receipt diagnostics. Missing historical events are not invented.
- Restore the exact previous source/configuration after a failed upgrade, including when the installed updater is older. Newly added source files no longer prevent rollback compilation. Failed installation files remain recoverable; notification and parking state volumes are preserved.
- Official release archive, checksums and provenance remain the common update source for HostBox and command-line installations. Physical locked-phone delivery still requires real-device verification.

The source guard protects the official updater path, not a full machine rollback.
Failed snapshots are private to root and retained for manual recovery/cleanup.
If a mounted or inaccessible install directory cannot be moved, the installer
reports the recovery path rather than deleting files to force restoration.
