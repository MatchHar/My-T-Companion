# Companion 1.10.57

[English](RELEASE_NOTES_1.10.57.md) · [简体中文](RELEASE_NOTES_1.10.57.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.57.zh-Hant.md)

Adds an optional originating owner-device id on Friend Together grant creation.
Old persisted grants remain null. Grant list responses include `owner_device_id`
on GrantView. `GET /grants?owner_device_id=` returns only grants tagged with
that install. Empty approve bodies stay valid and copy the invitation owner
device when present. A mismatched explicit owner-device id is rejected.

This lets a phone whose Friend Together journal is missing revoke only its own
leftover grants. Grants with a null or other owner-device id are never selected
by that filter. Existing charging-settings sync, parking, navigation, pairing
and notifications are unchanged.

Install/update using the existing authenticated instructions. Publishing alone
does not update a running server; My T's matching client update is separate.
This local catalog pin is not a production deploy.
