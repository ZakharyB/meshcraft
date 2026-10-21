package hosting

import (
	"testing"
	"time"
)

func TestFencingMonotonicAndSingleWriter(t *testing.T) {
	now := time.Now()
	cur := now
	a := NewAuthority(func() time.Time { return cur })

	l1, err := a.Acquire("w1", "peerA", time.Minute)
	if err != nil || l1.Token != 1 {
		t.Fatalf("first lease: %+v err=%v", l1, err)
	}
	if _, err := a.Acquire("w1", "peerB", time.Minute); err != ErrLeaseHeld {
		t.Fatalf("second holder must be refused, got %v", err)
	}
	// Same holder renews: new, higher token; old token instantly stale.
	l2, err := a.Acquire("w1", "peerA", time.Minute)
	if err != nil || l2.Token != 2 {
		t.Fatalf("renewal: %+v err=%v", l2, err)
	}
	if a.Verify("w1", "peerA", l1.Token) {
		t.Fatal("stale token still verifies")
	}
	if !a.Verify("w1", "peerA", l2.Token) {
		t.Fatal("current token does not verify")
	}
	// Expiry frees the world, fail-closed in between.
	cur = now.Add(2 * time.Minute)
	if a.Verify("w1", "peerA", l2.Token) {
		t.Fatal("expired lease still verifies")
	}
	l3, err := a.Acquire("w1", "peerB", time.Minute)
	if err != nil || l3.Token != 3 {
		t.Fatalf("post-expiry takeover: %+v err=%v", l3, err)
	}
}

func TestObserveRejectsStale(t *testing.T) {
	a := NewAuthority(nil)
	fresh := Lease{WorldID: "w1", HolderPeerID: "p1", Token: 5, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	if !a.Observe(fresh) {
		t.Fatal("fresh lease not observed")
	}
	stale := Lease{WorldID: "w1", HolderPeerID: "p2", Token: 3, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	if a.Observe(stale) {
		t.Fatal("stale lease was installed")
	}
	if a.Verify("w1", "p2", 3) {
		t.Fatal("stale holder verifies")
	}
}
