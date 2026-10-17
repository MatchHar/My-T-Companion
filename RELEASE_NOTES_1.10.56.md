# Companion 1.10.56

[English](RELEASE_NOTES_1.10.56.md) · [简体中文](RELEASE_NOTES_1.10.56.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.56.zh-Hant.md)

Adds optional, owner-authenticated charging-settings synchronization for newer
My T clients. Only an explicit charging-popup open or Overview pull-to-refresh
may request it. The server independently requires an enabled car with current
MQTT baselines, suspended logging, a plugged cable and a future scheduled charge.

The fixed TeslaMate logging-resume operation has a five-minute per-car cooldown,
single-flight and six-second timeout; redirects and arbitrary commands/URLs are
rejected. It does not enable TeslaMateAPI commands or send wake/charging controls.
A full data read can delay sleep. Acceptance is not proof of new settings: the
ordinary status stream must first obtain the updated values.

Existing passive polling, charging observations, pairing, preferences and history
are preserved. The disposable acceptance workflow covers exact56 installation,
upgrade, deliberate failure recovery, repeat and backup-assisted rollback to
immutable55. Real vehicle settings freshness and APNs remain separate checks.

Install/update using the existing authenticated instructions. Publishing alone
does not update a running server; My T's matching client update is separate.
