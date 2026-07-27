# Public release checklist

This checklist is for the repository owner. Documentation review alone is not
enough to mark the project ready.

## Before changing repository visibility

- [ ] Confirm the compatible My T build is publicly available or clearly label
      the companion as pre-release.
- [ ] Test install, update, failed-update rollback, and uninstall on a disposable
      TeslaMate-compatible host.
- [ ] Verify Go tests/vetting, shell syntax, updater tests, and container build.
- [ ] Confirm every data route requires the existing API authentication and
      port 8083 remains loopback-only.
- [ ] Review all files and Git history for secrets, private addresses, VINs,
      coordinates, logs, database exports, and personal author email addresses.
- [ ] Create a clean signed or GitHub-verified release tag matching `VERSION`.
- [ ] Build the release archive from that tag and publish its SHA-256 file.
- [ ] Verify the public one-command installer against the published assets.
- [ ] Enable GitHub private vulnerability reporting.
- [ ] Configure branch protection, required review/status checks, Dependabot
      alerts, and secret scanning where available.
- [ ] Confirm GitHub Actions billing/spending limits permit CI and releases.

## Immediately after publication

- [ ] Open every README language and verify images and internal links.
- [ ] Test the App Store, TeslaMate, TeslaMateAPI, and My T documentation links
      from a signed-out browser.
- [ ] Confirm no draft release, obsolete branch, or historical tag exposes
      superseded content.
- [ ] Publish release notes with compatibility, upgrade, rollback, known
      limitations, and the exact verified checksum.
- [ ] Keep the production VPS on its existing version until the published
      release passes a separate deployment check.
