package friendtogether

import (
	"encoding/hex"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Store must not be copied. Its zero value works with the system clock.
// Expired/revoked grants remain tombstones within their lifetime, so an old ID
// cannot be reassigned. Explicit housekeeping may remove absolutely expired
// tombstones; network grants always use fresh random IDs. Restart loses this
// standalone store's grants and fails closed.
type Store struct {
	mu     sync.Mutex
	clock  func() int64
	grants map[string]*grant
}

type grant struct {
	metadata               GrantMetadata
	source                 SourceKey
	alias                  *DestinationAlias
	revoked                bool
	expired                bool
	sequence               int64
	lastServerMS           int64
	lastMotionMS           *int64
	lastNavigationMS       *int64
	lastBatteryMS          *int64
	lastTrajectoryMS       *int64
	lastNavigationRevision int64
	navigationWithdrawn    bool
	lastDestination        *destinationIdentity
}

type destinationIdentity struct {
	alias     *string
	latitude  *float64
	longitude *float64
}

func NewStore() *Store { return &Store{} }

// PruneTerminalBefore only releases old, absolutely expired tombstones. Active
// grants are never evicted to make room. A network service uses fresh random
// IDs; explicit callers still cannot reuse an ID during its grant lifetime.
func (s *Store) PruneTerminalBefore(cutoffMS int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !validTime(cutoffMS) || cutoffMS > now {
		return
	}
	for id, g := range s.grants {
		if g.metadata.ExpiresAtMS <= cutoffMS && now >= g.metadata.ExpiresAtMS {
			delete(s.grants, id)
		}
	}
}

func (s *Store) now() int64 {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now().UnixMilli()
}

func (s *Store) Create(owner OwnerPrincipal, spec GrantSpec) (GrantMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	binding, valid := normalizeBinding(spec.Binding)
	source, sourceValid := normalizeSource(spec.Source)
	if !validOwner(owner, binding, source) {
		return GrantMetadata{}, ErrNotAuthorized
	}
	if !valid || !sourceValid || !validTime(now) || !spec.Permissions.Location ||
		!validTime(spec.ExpiresAtMS) || spec.ExpiresAtMS <= now || spec.ExpiresAtMS-now > MaxGrantLifetimeMS {
		return GrantMetadata{}, ErrInvalid
	}
	if spec.DestinationAlias != nil && (!spec.Permissions.Navigation || !validAlias(*spec.DestinationAlias)) {
		return GrantMetadata{}, ErrInvalid
	}
	if _, exists := s.grants[binding.GrantID]; exists {
		return GrantMetadata{}, ErrNotAuthorized
	}
	if len(s.grants) >= MaxGrants {
		return GrantMetadata{}, ErrCapacity
	}
	if s.grants == nil {
		s.grants = make(map[string]*grant)
	}
	m := GrantMetadata{Binding: binding, Revision: 1, ConsentStartedAtMS: now,
		ExpiresAtMS: spec.ExpiresAtMS, Permissions: spec.Permissions}
	s.grants[binding.GrantID] = &grant{metadata: m, source: source,
		alias: copyPointer(spec.DestinationAlias), lastServerMS: now}
	return m, nil
}

// Revoke is an owner-authority operation. The future caller must authenticate
// that authority before calling; knowledge of a Binding is not authentication.
// Success linearizes under the same lock as Project. Already returned values
// cannot be recalled. No later Project call can succeed after this returns.
func (s *Store) Revoke(owner OwnerPrincipal, binding Binding, source SourceKey, revision int64) (GrantMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ownerMatchesRequest(owner, binding, source) {
		return GrantMetadata{}, ErrNotAuthorized
	}
	g, err := s.ownerGrant(binding, source, revision)
	if err != nil {
		return GrantMetadata{}, err
	}
	if !validOwner(owner, g.metadata.Binding, g.source) {
		return GrantMetadata{}, ErrNotAuthorized
	}
	if g.revoked {
		return g.metadata, nil
	}
	g.revoked = true
	// Revocation must remain available even when a counter cannot advance.
	if g.metadata.Revision < math.MaxInt64 {
		g.metadata.Revision++
	}
	return g.metadata, nil
}

// Restrict can only remove optional permissions or shorten expiry. Expanding
// consent/renewing requires a fresh grant ID and a new consent start. Retaining
// the same grant cannot disclose newly selected historical categories.
func (s *Store) Restrict(owner OwnerPrincipal, binding Binding, source SourceKey, revision int64, permissions Permissions, expiresAtMS int64) (GrantMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ownerMatchesRequest(owner, binding, source) {
		return GrantMetadata{}, ErrNotAuthorized
	}
	g, err := s.ownerGrant(binding, source, revision)
	if err != nil {
		return GrantMetadata{}, err
	}
	if !validOwner(owner, g.metadata.Binding, g.source) {
		return GrantMetadata{}, ErrNotAuthorized
	}
	now := s.now()
	if err := g.active(now); err != nil {
		return GrantMetadata{}, err
	}
	p := g.metadata.Permissions
	if !permissions.Location || permissions.Navigation && !p.Navigation ||
		permissions.Battery && !p.Battery || permissions.Trajectory && !p.Trajectory ||
		!validTime(expiresAtMS) || expiresAtMS <= now || expiresAtMS > g.metadata.ExpiresAtMS {
		return GrantMetadata{}, ErrInvalid
	}
	if g.metadata.Revision == math.MaxInt64 {
		return GrantMetadata{}, ErrExhausted
	}
	g.metadata.Revision++
	g.metadata.Permissions = permissions
	g.metadata.ExpiresAtMS = expiresAtMS
	g.lastServerMS = now
	if !permissions.Navigation {
		g.alias = nil
	}
	return g.metadata, nil
}

// Project authorizes both the exact recipient scope and the owner-selected
// source/car before looking at telemetry. The caller must bind Binding to an
// authenticated device, not accept it as an unauthenticated request body.
func (s *Store) Project(recipient RecipientPrincipal, binding Binding, revision int64, source SourceSnapshot) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recipientID, recipientOK := normalizeID(recipient.RecipientID)
	requestedRecipientID, bindingOK := normalizeID(binding.RecipientID)
	if !recipientOK || !bindingOK || recipientID != requestedRecipientID {
		return Snapshot{}, ErrNotAuthorized
	}
	g, err := s.ownerGrant(binding, source.Source, revision)
	if err != nil {
		return Snapshot{}, err
	}
	now := s.now()
	if err := g.active(now); err != nil {
		return Snapshot{}, err
	}
	if g.sequence == math.MaxInt64 {
		return Snapshot{}, ErrExhausted
	}
	out, err := g.project(source, now)
	if err != nil {
		return Snapshot{}, err
	}
	if err := g.checkOrder(out); err != nil {
		return Snapshot{}, err
	}
	// Only a completely valid projection advances counters/order state.
	g.sequence++
	out.Sequence = g.sequence
	g.lastServerMS = now
	if out.Motion.ObservedAtMS != nil {
		g.lastMotionMS = copyPointer(out.Motion.ObservedAtMS)
	}
	if out.Battery != nil {
		g.lastBatteryMS = copyPointer(&out.Battery.ObservedAtMS)
	}
	if out.Navigation != nil {
		g.lastNavigationMS = copyPointer(&out.Navigation.ObservedAtMS)
		g.lastNavigationRevision = out.Navigation.Revision
		g.navigationWithdrawn = false
		g.lastDestination = &destinationIdentity{alias: copyPointer(out.Navigation.Destination), latitude: copyPointer(out.Navigation.Latitude), longitude: copyPointer(out.Navigation.Longitude)}
	} else if g.lastNavigationRevision > 0 {
		g.navigationWithdrawn = true
	}
	if out.Trajectory != nil && len(*out.Trajectory) > 0 {
		points := *out.Trajectory
		g.lastTrajectoryMS = copyPointer(&points[len(points)-1].ObservedAtMS)
	}
	return out, nil
}

func (s *Store) ownerGrant(binding Binding, source SourceKey, revision int64) (*grant, error) {
	b, ok := normalizeBinding(binding)
	key, sourceOK := normalizeSource(source)
	if !ok || !sourceOK {
		return nil, ErrNotAuthorized
	}
	g := s.grants[b.GrantID]
	if g == nil || g.metadata.Binding != b || g.source != key {
		return nil, ErrNotAuthorized
	}
	if revision < 1 || g.metadata.Revision != revision {
		return nil, ErrRevision
	}
	return g, nil
}

func (g *grant) active(now int64) error {
	if g.revoked || g.expired {
		return ErrInactive
	}
	if !validTime(now) || now < g.metadata.ConsentStartedAtMS || now < g.lastServerMS {
		return ErrOrder
	}
	if now >= g.metadata.ExpiresAtMS {
		g.expired = true
		return ErrInactive
	}
	return nil
}

func (g *grant) checkOrder(out Snapshot) error {
	if regressed(out.Motion.ObservedAtMS, g.lastMotionMS) {
		return ErrOrder
	}
	if out.Battery != nil && regressed(&out.Battery.ObservedAtMS, g.lastBatteryMS) {
		return ErrOrder
	}
	if out.Navigation != nil {
		n := out.Navigation
		if regressed(&n.ObservedAtMS, g.lastNavigationMS) || n.Revision < g.lastNavigationRevision ||
			g.navigationWithdrawn && n.Revision <= g.lastNavigationRevision {
			return ErrOrder
		}
		if n.Revision == g.lastNavigationRevision && g.lastDestination != nil {
			d := g.lastDestination
			if !equalPointer(n.Destination, d.alias) || !equalPointer(n.Latitude, d.latitude) || !equalPointer(n.Longitude, d.longitude) {
				return ErrOrder
			}
		}
	}
	if out.Trajectory != nil && len(*out.Trajectory) > 0 {
		points := *out.Trajectory
		if regressed(&points[len(points)-1].ObservedAtMS, g.lastTrajectoryMS) {
			return ErrOrder
		}
	}
	return nil
}

func regressed(current, previous *int64) bool {
	return current != nil && previous != nil && *current < *previous
}

func validOwner(owner OwnerPrincipal, binding Binding, source SourceKey) bool {
	issuer, ok := normalizeID(owner.IssuerID)
	ownedSource, sourceOK := normalizeSource(owner.Source)
	return ok && sourceOK && issuer == binding.IssuerID && ownedSource == source
}

func ownerMatchesRequest(owner OwnerPrincipal, binding Binding, source SourceKey) bool {
	b, ok := normalizeBinding(binding)
	s, sourceOK := normalizeSource(source)
	return ok && sourceOK && validOwner(owner, b, s)
}

func equalPointer[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func validTime(t int64) bool { return t >= 0 && t <= MaxTimestampMS }
func copyPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}

func normalizeBinding(b Binding) (Binding, bool) {
	ids := []*string{&b.GrantID, &b.IssuerID, &b.SessionID, &b.MemberID, &b.RecipientID}
	for _, id := range ids {
		v, ok := normalizeID(*id)
		if !ok {
			return Binding{}, false
		}
		*id = v
	}
	return b, true
}

func normalizeSource(s SourceKey) (SourceKey, bool) {
	id, ok := normalizeID(s.SourceID)
	if !ok || s.CarID < 1 {
		return SourceKey{}, false
	}
	s.SourceID = id
	return s, true
}

func normalizeID(id string) (string, bool) {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return "", false
	}
	hexText := id[:8] + id[9:13] + id[14:18] + id[19:23] + id[24:]
	decoded, err := hex.DecodeString(hexText)
	if err != nil {
		return "", false
	}
	nonzero := false
	for _, b := range decoded {
		nonzero = nonzero || b != 0
	}
	return strings.ToLower(id), nonzero
}

func validAlias(a DestinationAlias) bool {
	if a.NavigationRevision < 1 || len(a.Value) > 256 || !utf8.ValidString(a.Value) || strings.TrimSpace(a.Value) == "" {
		return false
	}
	for _, r := range a.Value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
