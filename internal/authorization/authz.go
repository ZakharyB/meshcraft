// Package authorization defines world roles, permissions and invitations.
//
// Authority model (spec §4): owner identity, peer identity, admin
// permissions, execution authority (leases, internal/hosting) and
// worker permissions are distinct concepts. An invitation code grants
// membership only — never administration of an existing world.
package authorization

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Role within one world.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleHost   Role = "host"   // may run the authoritative server when delegated
	RoleWorker Role = "worker" // may run background tasks
	RolePlayer Role = "player" // may join the game
	RoleNone   Role = ""
)

// Permission strings checked by Go before any privileged operation.
type Permission string

const (
	PermManageWorld  Permission = "world:manage"
	PermManagePeers  Permission = "peers:manage"
	PermHostWorld    Permission = "world:host"
	PermRunTasks     Permission = "tasks:run"
	PermJoinGame     Permission = "game:join"
	PermEditSettings Permission = "world:settings"
)

var rolePermissions = map[Role][]Permission{
	RoleOwner:  {PermManageWorld, PermManagePeers, PermHostWorld, PermRunTasks, PermJoinGame, PermEditSettings},
	RoleAdmin:  {PermManagePeers, PermHostWorld, PermRunTasks, PermJoinGame, PermEditSettings},
	RoleHost:   {PermHostWorld, PermJoinGame},
	RoleWorker: {PermRunTasks},
	RolePlayer: {PermJoinGame},
}

// Can reports whether role grants perm.
func Can(role Role, perm Permission) bool {
	for _, p := range rolePermissions[role] {
		if p == perm {
			return true
		}
	}
	return false
}

// Invite is a single-use, expiring invitation. Only the SHA-256 hash of
// the code is stored; the plaintext code is shown to the owner once.
type Invite struct {
	CodeHash  string    `json:"code_hash"`
	WorldID   string    `json:"world_id"`
	Role      Role      `json:"role"` // invitations can never grant owner
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	CreatedBy string    `json:"created_by"`
}

var ErrInviteInvalid = errors.New("invite invalid, expired, or already used")

// NewInvite mints an invite and returns the plaintext code (shown once).
func NewInvite(worldID string, role Role, createdBy string, ttl time.Duration, now time.Time) (Invite, string, error) {
	if role == RoleOwner || role == RoleNone {
		return Invite{}, "", fmt.Errorf("invitations cannot grant role %q", role)
	}
	if _, ok := rolePermissions[role]; !ok {
		return Invite{}, "", fmt.Errorf("unknown role %q", role)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Invite{}, "", err
	}
	code := hex.EncodeToString(raw)
	return Invite{
		CodeHash:  HashCode(code),
		WorldID:   worldID,
		Role:      role,
		ExpiresAt: now.Add(ttl),
		CreatedBy: createdBy,
	}, code, nil
}

// HashCode hashes an invite code for storage/comparison.
func HashCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// Redeem validates code against the invite and marks it used.
func (inv *Invite) Redeem(code string, now time.Time) (Role, error) {
	if inv.Used || now.After(inv.ExpiresAt) || HashCode(code) != inv.CodeHash {
		return RoleNone, ErrInviteInvalid
	}
	inv.Used = true
	return inv.Role, nil
}
