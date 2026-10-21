package membership

import (
	"path/filepath"
	"testing"

	"github.com/ZakharyB/meshcraft/internal/authorization"
)

func TestMembershipLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "membership.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("w1", Member{PeerID: "owner1", Role: authorization.RoleOwner}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("w1", Member{PeerID: "peer2", Role: authorization.RolePlayer}); err != nil {
		t.Fatal(err)
	}
	if !s.Can("w1", "peer2", authorization.PermJoinGame) {
		t.Fatal("player should be able to join")
	}
	if s.Can("w1", "peer2", authorization.PermManageWorld) {
		t.Fatal("player must not manage the world")
	}
	if err := s.Add("w1", Member{PeerID: "owner1", Role: authorization.RolePlayer}); err == nil {
		t.Fatal("owner demotion via Add must be blocked")
	}
	if err := s.Revoke("w1", "peer2"); err != nil {
		t.Fatal(err)
	}
	if s.RoleOf("w1", "peer2") != authorization.RoleNone || s.Can("w1", "peer2", authorization.PermJoinGame) {
		t.Fatal("revoked peer kept access")
	}
	// persistence
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.RoleOf("w1", "owner1") != authorization.RoleOwner {
		t.Fatal("membership did not persist")
	}
}
