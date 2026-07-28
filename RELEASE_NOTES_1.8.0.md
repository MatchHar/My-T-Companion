# My T Companion 1.8.0

This release completes the product and technical rename from the pre-release
parking-only name to **My T Companion**.

## What changed

- Renamed the public repository to `MatchHar/My-T-Companion`.
- Renamed release archives to `my-t-companion-<version>.tar.gz`.
- Renamed the default install directory to `/opt/my-t-companion`.
- Renamed the Compose project and service to `my-t-companion` / `companion`.
- Renamed the local image to `myt/companion`.
- Renamed the capability service identifier to `my-t-companion`.
- Expanded English, Simplified Chinese, and Traditional Chinese documentation
  to explain parking history, live-drive trajectories, charging/navigation Live
  Activities, and vehicle software notifications.

## Upgrade note

This component had not yet been publicly adopted under the previous technical
name. Version 1.8.0 therefore uses the clean new identifiers without maintaining
the old installation path as a public interface.

Back up the existing test installation, uninstall its old Compose project, and
then use the verified 1.8.0 installer. TeslaMate data is not modified.

