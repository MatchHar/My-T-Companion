# Companion 1.10.58

[English](RELEASE_NOTES_1.10.58.md) · [简体中文](RELEASE_NOTES_1.10.58.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.58.zh-Hant.md)

Build and validate with Go 1.26.10. Go 1.26.9's crypto/x509 is affected by
GO-2026-6809; 1.26.10 contains the fix. The Docker build image is
golang:1.26.10-alpine. Module requirements stay on the existing set, including
golang.org/x/net 0.60.0.

Friend Together owner-device grants, charging-settings sync, parking,
navigation, pairing, and notifications are unchanged.

This commit is not a GitHub release and has no tag. Install and update still
use the existing authenticated instructions. Publishing alone does not update a
running server. Production remains on the previously deployed Companion 1.10.56.
