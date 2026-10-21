package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello mesh")
	sig := id.Sign(msg)
	if !Verify(id.PublicKeyHex(), msg, sig) {
		t.Fatal("valid signature rejected")
	}
	if Verify(id.PublicKeyHex(), []byte("tampered"), sig) {
		t.Fatal("signature verified against a different message")
	}
	other, _ := Generate()
	if Verify(other.PublicKeyHex(), msg, sig) {
		t.Fatal("signature verified under the wrong key")
	}
}

func TestSaveLoadAndPermissions(t *testing.T) {
	dir := t.TempDir()
	id, created, err := LoadOrGenerate(dir)
	if err != nil || !created {
		t.Fatalf("first load: created=%v err=%v", created, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("identity.key perms = %o, want 600", fi.Mode().Perm())
	}
	again, created2, err := LoadOrGenerate(dir)
	if err != nil || created2 {
		t.Fatalf("second load: created=%v err=%v", created2, err)
	}
	if again.PeerID != id.PeerID {
		t.Fatal("peer id changed across loads")
	}
}
