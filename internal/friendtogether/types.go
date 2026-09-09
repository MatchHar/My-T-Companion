// Package friendtogether is an unconnected, in-memory consent and projection
// foundation. It does not authenticate callers, expose routes, or read vehicles.
package friendtogether

import "errors"

const (
	SchemaVersion                   = 1
	MaxGrants                       = 64
	MaxTrajectoryPoints             = 500
	MaxSourceTrajectoryPoints       = 5000
	MaxGrantLifetimeMS        int64 = 8 * 60 * 60 * 1000
	MaxTimestampMS            int64 = 253402300799999
)

var (
	ErrInvalid       = errors.New("invalid friend share input")
	ErrNotAuthorized = errors.New("friend share not authorized")
	ErrInactive      = errors.New("friend share inactive")
	ErrRevision      = errors.New("friend share revision mismatch")
	ErrOrder         = errors.New("friend share observation order invalid")
	ErrCapacity      = errors.New("friend share capacity reached")
	ErrExhausted     = errors.New("friend share counter exhausted")
)

// Binding is the exact public scope. IDs are nonzero UUIDs, not credentials.
type Binding struct {
	GrantID     string `json:"grant_id"`
	IssuerID    string `json:"issuer_id"`
	SessionID   string `json:"session_id"`
	MemberID    string `json:"member_id"`
	RecipientID string `json:"recipient_id"`
}

// SourceKey is an owner-selected internal mapping. No guest chooses the car.
// It must be supplied by a trusted source adapter and is never in Snapshot.
type SourceKey struct {
	SourceID string `json:"-"`
	CarID    int64  `json:"-"`
}

// Principals must come from the future trusted authentication/authorization
// adapter, separately from request fields. Constructing one is not login.
type OwnerPrincipal struct {
	IssuerID string
	Source   SourceKey
}

type RecipientPrincipal struct{ RecipientID string }

type Permissions struct {
	Location   bool
	Navigation bool
	Battery    bool
	Trajectory bool
}

// DestinationAlias is explicitly approved by the owner for this navigation
// revision. Source place names, geofences and address labels are never copied.
type DestinationAlias struct {
	NavigationRevision int64
	Value              string
}

// GrantSpec may only enter from an authenticated owner consent operation.
// Consent starts at the store's clock on Create, so it cannot be backdated.
type GrantSpec struct {
	Binding          Binding
	Source           SourceKey
	Permissions      Permissions
	ExpiresAtMS      int64
	DestinationAlias *DestinationAlias
}

type GrantMetadata struct {
	Binding            Binding
	Revision           int64
	ConsentStartedAtMS int64
	ExpiresAtMS        int64
	Permissions        Permissions
}

type State string

const (
	StateDriving  State = "driving"
	StateParked   State = "parked"
	StateCharging State = "charging"
	StateAsleep   State = "asleep"
	StateOffline  State = "offline"
	StateUnknown  State = "unknown"
)

// The following DTOs are the only permitted wire projection. Unknown numeric
// values serialize as null, including real zero values through nonnil pointers.
type Snapshot struct {
	SchemaVersion int `json:"schema_version"`
	Binding
	Revision           int64              `json:"revision"`
	Sequence           int64              `json:"sequence"`
	ServerTimeMS       int64              `json:"server_time_ms"`
	ConsentStartedAtMS int64              `json:"consent_started_at_ms"`
	ExpiresAtMS        int64              `json:"expires_at_ms"`
	State              State              `json:"state"`
	Motion             Motion             `json:"motion"`
	Navigation         *Navigation        `json:"navigation,omitempty"`
	Battery            *Battery           `json:"battery,omitempty"`
	Trajectory         *[]TrajectoryPoint `json:"trajectory,omitempty"`
}

type Motion struct {
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	SpeedKPH       *float64 `json:"speed_kph"`
	HeadingDegrees *float64 `json:"heading_degrees"`
	ObservedAtMS   *int64   `json:"observed_at_ms"`
}

type Navigation struct {
	Revision            int64    `json:"revision"`
	Destination         *string  `json:"destination"`
	Latitude            *float64 `json:"latitude"`
	Longitude           *float64 `json:"longitude"`
	RemainingDistanceKM *float64 `json:"remaining_distance_km"`
	RemainingMinutes    *float64 `json:"remaining_minutes"`
	ObservedAtMS        int64    `json:"observed_at_ms"`
}

type Battery struct {
	Percentage   *float64 `json:"percentage"`
	RatedRangeKM *float64 `json:"rated_range_km"`
	ObservedAtMS int64    `json:"observed_at_ms"`
}

type TrajectoryPoint struct {
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	ObservedAtMS int64   `json:"observed_at_ms"`
}

// SourceSnapshot is a narrow trusted-adapter input, never a raw vehicle row.
// Motion's timestamp must cover state and every included motion value; an
// adapter must omit fields it cannot establish at that source observation.
// Navigation deliberately has no source destination label.
type SourceSnapshot struct {
	Source     SourceKey
	State      State
	Motion     *Motion
	Navigation *NavigationObservation
	Battery    *Battery
	Trajectory []TrajectoryPoint
}

type NavigationObservation struct {
	Revision            int64
	Latitude            *float64
	Longitude           *float64
	RemainingDistanceKM *float64
	RemainingMinutes    *float64
	ObservedAtMS        int64
}
