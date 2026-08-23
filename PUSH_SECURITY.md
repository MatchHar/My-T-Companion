# Push security boundary

My T Companion is a public, self-hosted component. It must never contain or require Apple Push Notification service (APNs) provider credentials.

## Architecture

```text
TeslaMate / MQTT
      |
      v
My T Companion (user controlled)
      |  signed event only
      |  per-installation HMAC credential
      v
https://push.my-tesla.app
      |
      |  developer-operated APNs provider credentials
      v
Apple APNs
      |
      v
My T on iPhone
```

The Companion is responsible for observing TeslaMate/MQTT state, deciding when a notification or Live Activity event should be emitted, signing that event with the paired installation secret, retrying temporary delivery failures, and sending the event only to the pinned official relay.

The developer-operated relay is responsible for validating installation signatures, rate limiting, delivery audit/metrics, APNs token/provider authentication, and talking to Apple APNs.

## Secrets allowed in this repository/runtime

The following values are allowed because they belong to the user's own self-hosted installation:

- TeslaMate/PostgreSQL credentials used by that user's Companion.
- MQTT credentials used by that user's Companion.
- The per-installation `relay_secret` issued during secure pairing.
- The opaque installation ID associated with that pairing.

`relay_secret` is an installation-scoped HMAC credential. It is not an APNs provider key and must not grant access to another installation.

## Secrets forbidden in this repository/runtime

Never commit, package, document with real values, or require any developer-wide APNs provider credential in Companion, including:

- APNs `.p8` private keys.
- `BEGIN PRIVATE KEY` / `BEGIN EC PRIVATE KEY` material.
- APNs provider JWT signing keys.
- Developer-wide APNs bearer/provider tokens.
- Apple Developer Team private signing material.
- Any relay administrator/master secret that can impersonate arbitrary installations.

Provider credentials belong only in the private Push Relay project and in Cloudflare encrypted secrets/bindings. They must not be placed in source control, including a private repository.

## Network boundary

Production push delivery is pinned to:

```text
https://push.my-tesla.app/v1/events
```

Companion must not follow redirects for relay delivery and must reject subscriber records that reference any other relay URL. This prevents a compromised or misconfigured pairing from exfiltrating signed push payloads to another host.

## Data minimization

Push events should contain only the fields required to render the notification or Live Activity. Do not send Tesla account credentials, TeslaMate credentials, VIN, raw location history, trip trajectories, or database contents to the developer relay.

## Repository guard

`scripts/check-push-secret-boundary.sh` is intended to run in CI and fail if common APNs provider-secret patterns appear in tracked source. This is a guardrail, not a substitute for Cloudflare Secrets, key rotation, branch protection, and normal secret-scanning controls.
