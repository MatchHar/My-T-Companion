# Release checklist

This checklist is for the repository owner. A release is complete only when its
source, immutable assets, provenance, documentation, and downstream catalog
entry agree.

## Before creating a tag

- [ ] Confirm the compatible My T build is publicly available, or clearly
      label the Companion feature as unavailable until that build ships.
- [ ] Update `VERSION`, release notes, compatibility documentation, changelog,
      and HostBox catalog metadata in one reviewed change.
- [ ] Test install, update, failed-update rollback, and uninstall on a
      disposable TeslaMate-compatible host when deployment behavior changed.
- [ ] Require all CI checks, including Go tests/vetting/race, shell syntax,
      updater and release tests, container build, network-boundary enforcement,
      push-secret enforcement, dependency scanning, gitleaks, and CodeQL.
- [ ] Confirm every data route uses the existing API authentication and port
      8083 remains bound only to `127.0.0.1`.
- [ ] Review the change and relevant Git history for secrets, private
      addresses, VINs, coordinates, logs, database exports, and personal data.
- [ ] Confirm repository immutable releases are enabled.
- [ ] Create the `vX.Y.Z` tag only from a commit already contained in `main`;
      the tag must match `VERSION` and be signed or GitHub-verified.

## Release workflow

- [ ] Let the tag workflow create a draft release before uploading anything.
- [ ] Build the reproducible archive from the tagged commit and verify its
      SHA-256 manifest locally.
- [ ] Upload only that version's archive, checksum, and localized release
      notes; verify every GitHub asset digest before publishing the draft.
- [ ] Generate GitHub build-provenance attestations for the archive and
      checksum.
- [ ] Confirm the published release reports immutable and cannot be edited or
      receive replacement assets.
- [ ] Verify the public installer against the published checksum and run
      `gh attestation verify` against the release workflow identity.

## After publication

- [ ] Open every README language and verify images, internal links, App Store
      links, and the public/private project boundary from a signed-out browser.
- [ ] Publish compatibility, upgrade, rollback, known limitations, and the
      exact verified checksum in release notes.
- [ ] Update and validate the separately signed HostBox catalog only after the
      immutable GitHub release is healthy; never place its signing private key
      in this repository or GitHub Actions.
- [ ] Keep production VPS deployments on their current version until the
      published release passes a separate deployment check.
- [ ] Confirm the latest-release badge, release page, repository description,
      security alerts, and required branch checks remain correct.
