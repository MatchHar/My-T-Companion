# Friend Together local candidate

The package now also contains an optional HTTP/consent candidate. It has not
been released, deployed or physically validated. Production defaults OFF.
`HTTPService` separates owner-only administration from device-bound guest DPoP
access and calls the original Phase A projection policy. Unit/race tests cover
two independent issuers with car1, permission/revocation, invitation approval,
token/key/replay boundaries and fail-closed restart. This does not prove public
HTTPS reachability, live vehicle sample cadence or phone background behavior.

Guest routes never invoke the ordinary broad owner authentication/probe. A
one-use ten-minute invitation permits only a pending request. The owner reviews
the participant's public key/code before consent begins. Mutual sharing needs
two independent approvals. Returned invitation envelopes stay only in memory;
the server never fetches another server's URL. No continuous GPS enters Worker.

Device-bound access follows RFC9449 DPoP with ES256, public-only P-256 JWK,
RFC7638 thumbprint, method/URL/token hash binding, +/-60s proof age and a bounded
single-use server nonce. Tokens/secrets are random256bit; only hashes are stored.
All guest responses are no-store and credential headers are rejected. JSON
duplicate/unknown members, redirects and unconfigured origins are not supported.

The private atomic0600 journal contains consent metadata and token hashes, not
GPS or plaintext invitations/tokens. Restart ends all shares and requires fresh
invitations; do not describe this first implementation as seamless restart.
Permission reductions/owner or recipient stop are durable before acknowledgement.
A failed durable write latches all guest access off and reports failure.

Read `docs/friend-together-local-candidate.md` for deployment boundaries. The
older Phase A description below documents the standalone policy layer only;
its prior absence-of-HTTP statement does not describe the new HTTPService.

## Phase A policy layer (historical implementation boundary)

This package is an **unconnected Phase A foundation**, not a deployed or usable
friend-sharing feature. The existing server does not import it, advertise a
capability, create a route, read a vehicle, or persist a grant. No network client,
credentials, cryptography, invitation redemption, TLS, or authentication is
implemented here. It uses the Go standard library only.

An eventual authenticated owner adapter supplies `OwnerPrincipal` separately
from request fields and proves ownership of the selected `SourceKey`. An
authenticated device adapter supplies `RecipientPrincipal` independently of the
requested `Binding`. These values are assertions from that trusted adapter;
constructing a principal is not authentication, and UUID knowledge is not a
credential. The package compares those principals, the complete public binding,
the expected revision, and the exact internal source plus car mapping. Do not
wire request JSON directly into principal values.

`Create` requires explicit location consent, timestamps consent with the server
clock, and enforces an absolute expiry within eight hours. Navigation, battery,
and trajectory are independently optional. `Restrict` can only remove categories
or shorten expiry and increments revision. Expansion or renewal requires a new
grant ID and a fresh consent start. `Revoke` shares a mutex with `Project`; after
it returns, subsequent projections cannot succeed. Data already returned cannot
be recalled. This is local in-memory behavior, not proof of network revocation.

The store retains at most 64 grant records, including terminal tombstones. It
never evicts/reassigns a grant ID. A restart loses all grants and therefore denies
all previous access. Durable revocation, upgrade/rollback tombstones, clock
rollback recovery, rate limiting, and operational cleanup require separate
integration work; this bounded test store must not become a production store by
adding an HTTP handler.

Only `Snapshot` is the wire DTO, with the agreed v1 snake_case names. Public scope
is `grant_id`, `issuer_id`, `session_id`, `member_id`, `recipient_id`. Source ID
and raw car ID remain internal. Never serialize `GrantSpec`, principals, source
inputs, or normal vehicle records. Snapshots contain no VIN, odometer, geofence,
security history, credentials, or preconsent observation.

Motion is required. Unknown numeric values are explicit JSON nulls; true zero is
preserved. No eligible motion means all motion fields are null and state is
`unknown`. State otherwise allows driving, parked, charging, asleep, offline, or
unknown and shares the motion observation timestamp. An adapter must use a
conservative timestamp covering every included field, omitting values it cannot
establish at that time. Transport receipt never becomes source freshness.

Denied optional categories are omitted before validation. Permitted navigation
or battery is omitted when absent/preconsent. Permitted trajectory is an array,
including an empty array when there are no eligible points. Every exposed point
is at or after consent and at or before server time, with strictly increasing
timestamps. Projection accepts at most 5,000 candidate points and returns only
the newest 500 eligible points in an exactly bounded backing array. It never
reads or copies an earlier trip start. Invalid coordinates, partial pairs,
nonfinite/out-of-range numbers, future times, regressive source times, regressive
navigation revisions, and reversed trajectories fail closed.

Destination is only an owner-approved alias bound to a navigation revision,
at most 256 UTF-8 bytes, nonblank and without control characters. Navigation
source input deliberately has no place-name field. A reroute suppresses the old
alias. Destination coordinates/alias cannot change within one navigation
revision. After navigation withdrawal, an old revision cannot reappear. A grant
without navigation consent cannot carry an alias.

Run `go test -race ./internal/friendtogether` and
`go build ./internal/friendtogether` for the isolated proof. Tests are synthetic;
they do not establish HTTPS reachability, invitation/device authentication,
durability, two-account vehicle behavior, physical GPS, or APNs delivery.
