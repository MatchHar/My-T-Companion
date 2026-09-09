package friendtogether

import (
	"context"
	"net/http"
)

// PublicKey is the public-only, single-algorithm DPoP JWK contract.
type PublicKey struct {
	KTY string `json:"kty"`
	CRV string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}
type Device struct {
	RecipientID string    `json:"recipient_id"`
	PublicKey   PublicKey `json:"public_key"`
}
type Member struct {
	Alias string `json:"alias"`
	Model string `json:"model"`
}
type PermissionSet struct {
	Location   bool `json:"location"`
	Navigation bool `json:"navigation"`
	Battery    bool `json:"battery"`
	Trajectory bool `json:"trajectory"`
}

func (p PermissionSet) policy() Permissions { return Permissions(p) }

// Invitation is deliberately separate from normal connection export. Its
// one-use secret only creates a pending request, never authorizes telemetry.
type Invitation struct {
	SchemaVersion          int           `json:"schema_version"`
	GuestOrigin            string        `json:"guest_origin"`
	IssuerID               string        `json:"issuer_id"`
	SessionID              string        `json:"session_id"`
	MemberID               string        `json:"member_id"`
	InvitationID           string        `json:"invitation_id"`
	ExpiresAtMS            int64         `json:"expires_at_ms"`
	SharingDurationSeconds int64         `json:"sharing_duration_seconds"`
	Permissions            PermissionSet `json:"permissions"`
	Member                 Member        `json:"member"`
	OwnerDevice            Device        `json:"owner_device"`
	RedemptionSecret       string        `json:"redemption_secret"`
}
type CreateInvitationRequest struct {
	SchemaVersion     int           `json:"schema_version"`
	CarID             int64         `json:"car_id"`
	DurationSeconds   int64         `json:"duration_seconds"`
	Permissions       PermissionSet `json:"permissions"`
	Member            Member        `json:"member"`
	OwnerDevice       Device        `json:"owner_device"`
	SessionID         string        `json:"session_id,omitempty"`
	ExpectedRecipient *Device       `json:"expected_recipient,omitempty"`
}
type RedeemRequest struct {
	SchemaVersion    int         `json:"schema_version"`
	InvitationID     string      `json:"invitation_id"`
	RedemptionSecret string      `json:"redemption_secret"`
	RecipientID      string      `json:"recipient_id"`
	RecipientAlias   string      `json:"recipient_alias"`
	ReturnInvitation *Invitation `json:"return_invitation,omitempty"`
}
type GrantView struct {
	SchemaVersion int `json:"schema_version"`
	Binding
	Revision           int64         `json:"revision"`
	ConsentStartedAtMS int64         `json:"consent_started_at_ms"`
	ExpiresAtMS        int64         `json:"expires_at_ms"`
	Permissions        PermissionSet `json:"permissions"`
	Member             Member        `json:"member"`
	Status             string        `json:"status"`
}
type JoinView struct {
	RequestID        string      `json:"request_id"`
	RecipientID      string      `json:"recipient_id"`
	RecipientAlias   string      `json:"recipient_alias"`
	ConfirmationCode string      `json:"confirmation_code"`
	PublicKey        PublicKey   `json:"public_key"`
	CreatedAtMS      int64       `json:"created_at_ms"`
	ExpiresAtMS      int64       `json:"expires_at_ms"`
	Status           string      `json:"status"`
	ReturnInvitation *Invitation `json:"return_invitation,omitempty"`
}
type JoinStatus struct {
	Status           string     `json:"status"`
	ConfirmationCode string     `json:"confirmation_code"`
	ExpiresAtMS      int64      `json:"expires_at_ms"`
	Grant            *GrantView `json:"grant,omitempty"`
}

// SourceReader must use only existing, observed TeslaMate records. It is never
// given a guest URL or guest-selected car ID, and must not wake/call Tesla.
type SourceReader interface {
	OwnsCar(context.Context, int64) (bool, error)
	ReadSource(context.Context, SourceKey, int64, PermissionSet) (SourceSnapshot, error)
}

// HTTPConfig intentionally has no outbound HTTP client or remote peer callback.
// The owner authenticator must only be used on the owner handler.
type HTTPConfig struct {
	Enabled           bool
	GuestOrigin       string
	StatePath         string
	AuthenticateOwner func(*http.Request) bool
	Source            SourceReader
	Clock             func() int64
	// AllowLoopbackHTTP is only for in-process synthetic tests. Runtime config
	// does not expose an environment switch for this exception.
	AllowLoopbackHTTP bool
}
