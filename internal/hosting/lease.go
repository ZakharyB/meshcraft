// Package hosting implements host authority leases with fencing tokens.
//
// Invariant (spec §11): only one peer may hold valid write authority
// for a world. Tokens increase monotonically per world; a stale token
// is rejected everywhere. When validity cannot be confirmed, callers
// must fail closed.
package hosting

import (
	"errors"
	"sync"
	"time"
)

// Lease is a grant of execution authority.
type Lease struct {
	WorldID      string    `json:"world_id"`
	HolderPeerID string    `json:"holder_peer_id"`
	Token        uint64    `json:"fencing_token"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// ValidAt reports whether the lease is unexpired at t.
func (l Lease) ValidAt(t time.Time) bool {
	return t.Before(l.ExpiresAt) && l.Token > 0
}

// Authority tracks the highest fencing token seen per world and the
// current lease. It is used by the coordinator (issuer) and mirrored
// by gateways/hosts (verifier).
type Authority struct {
	mu        sync.Mutex
	lastToken map[string]uint64
	current   map[string]Lease
	now       func() time.Time
}

// NewAuthority creates an Authority; now may be nil (wall clock).
func NewAuthority(now func() time.Time) *Authority {
	if now == nil {
		now = time.Now
	}
	return &Authority{lastToken: map[string]uint64{}, current: map[string]Lease{}, now: now}
}

var ErrLeaseHeld = errors.New("a valid lease is already held by another peer")

// Acquire grants a lease. If a valid lease exists for a different
// holder, it fails with ErrLeaseHeld. Re-acquiring as the same holder
// renews with a NEW token (old token becomes stale immediately).
func (a *Authority) Acquire(worldID, peerID string, ttl time.Duration) (Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if cur, ok := a.current[worldID]; ok && cur.ValidAt(now) && cur.HolderPeerID != peerID {
		return Lease{}, ErrLeaseHeld
	}
	a.lastToken[worldID]++
	l := Lease{
		WorldID: worldID, HolderPeerID: peerID, Token: a.lastToken[worldID],
		IssuedAt: now, ExpiresAt: now.Add(ttl),
	}
	a.current[worldID] = l
	return l, nil
}

// Release gives up a lease; only the holder with the current token can.
func (a *Authority) Release(worldID, peerID string, token uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.current[worldID]
	if !ok || cur.HolderPeerID != peerID || cur.Token != token {
		return false
	}
	delete(a.current, worldID)
	return true
}

// Verify reports whether (peerID, token) is the current authority for
// worldID at the current time. This is the fail-closed check: anything
// other than an exact, unexpired match is not authority.
func (a *Authority) Verify(worldID, peerID string, token uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.current[worldID]
	if !ok {
		return false
	}
	return cur.HolderPeerID == peerID && cur.Token == token && cur.ValidAt(a.now())
}

// Observe records a token seen from another authority source (e.g. a
// gateway learning routes). A lease whose token is not greater than
// the highest observed is stale and never installed.
func (a *Authority) Observe(l Lease) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if l.Token <= a.lastToken[l.WorldID] {
		return false
	}
	a.lastToken[l.WorldID] = l.Token
	a.current[l.WorldID] = l
	return true
}

// Current returns the current lease for a world, if any.
func (a *Authority) Current(worldID string) (Lease, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, ok := a.current[worldID]
	return l, ok
}
