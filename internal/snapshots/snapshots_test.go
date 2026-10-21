package snapshots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeWorld(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "world", "region"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "world", "level.dat"), []byte("level-data-v1"), 0o644)
	os.WriteFile(filepath.Join(dir, "world", "region", "r.0.0.mca"), []byte("chunk-bytes"), 0o644)
	os.WriteFile(filepath.Join(dir, "server.properties"), []byte("server-port=25565\n"), 0o644)
	return dir
}

func TestCreateVerifyRestore(t *testing.T) {
	src := makeWorld(t)
	store := NewStore(t.TempDir())
	m, err := store.Create("w1", "1.21", "peerA", 7, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 3 || m.FencingToken != 7 {
		t.Fatalf("manifest wrong: %+v", m)
	}
	if err := store.Verify("w1", m.ID); err != nil {
		t.Fatalf("fresh snapshot does not verify: %v", err)
	}
	// Mutate the source, then restore and expect the original bytes.
	os.WriteFile(filepath.Join(src, "world", "level.dat"), []byte("CORRUPTED-PLAY"), 0o644)
	if err := store.Restore("w1", m.ID, src, "1.21"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(src, "world", "level.dat"))
	if string(b) != "level-data-v1" {
		t.Fatalf("restore produced %q", b)
	}
}

func TestCorruptionDetected(t *testing.T) {
	src := makeWorld(t)
	root := t.TempDir()
	store := NewStore(root)
	m, err := store.Create("w1", "1.21", "peerA", 1, src)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "w1", m.ID, "data.tar.gz")
	b, _ := os.ReadFile(archive)
	// Flip bytes in the middle of the archive.
	for i := len(b) / 2; i < len(b)/2+8 && i < len(b); i++ {
		b[i] ^= 0xFF
	}
	os.WriteFile(archive, b, 0o644)
	if err := store.Verify("w1", m.ID); err == nil {
		t.Fatal("corrupted snapshot passed verification")
	}
	if err := store.Restore("w1", m.ID, t.TempDir(), "1.21"); err == nil {
		t.Fatal("corrupted snapshot was restored")
	}
}

func TestVersionMismatchAndRotation(t *testing.T) {
	src := makeWorld(t)
	store := NewStore(t.TempDir())
	var lastID string
	for i := 0; i < 3; i++ {
		m, err := store.Create("w1", "1.21", "peerA", uint64(i+1), src)
		if err != nil {
			t.Fatal(err)
		}
		lastID = m.ID
	}
	if err := store.Restore("w1", lastID, t.TempDir(), "1.20"); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("version mismatch not enforced: %v", err)
	}
	deleted, err := store.Rotate("w1", 2)
	if err != nil || deleted != 1 {
		t.Fatalf("rotation: deleted=%d err=%v", deleted, err)
	}
	list, _ := store.List("w1")
	if len(list) != 2 {
		t.Fatalf("after rotation: %d snapshots", len(list))
	}
}
