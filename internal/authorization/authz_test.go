package authorization

import (
	"testing"
	"time"
)

func TestInviteLifecycle(t *testing.T) {
	now := time.Now()
	inv, code, err := NewInvite("w1", RolePlayer, "owner", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if code == "" || inv.CodeHash == code {
		t.Fatal("plaintext code must never be stored")
	}
	role, err := inv.Redeem(code, now.Add(time.Minute))
	if err != nil || role != RolePlayer {
		t.Fatalf("redeem: role=%v err=%v", role, err)
	}
	if _, err := inv.Redeem(code, now.Add(2*time.Minute)); err == nil {
		t.Fatal("single-use invite redeemed twice")
	}
}

func TestInviteExpiryAndOwnerBan(t *testing.T) {
	now := time.Now()
	inv, code, _ := NewInvite("w1", RoleHost, "owner", time.Minute, now)
	if _, err := inv.Redeem(code, now.Add(2*time.Minute)); err == nil {
		t.Fatal("expired invite redeemed")
	}
	if _, _, err := NewInvite("w1", RoleOwner, "owner", time.Hour, now); err == nil {
		t.Fatal("invites must never grant owner")
	}
}

func TestRolePermissions(t *testing.T) {
	if !Can(RoleOwner, PermManageWorld) || !Can(RolePlayer, PermJoinGame) {
		t.Fatal("basic role grants wrong")
	}
	if Can(RolePlayer, PermManagePeers) || Can(RoleWorker, PermHostWorld) || Can(RoleNone, PermJoinGame) {
		t.Fatal("privilege leak in role table")
	}
}
