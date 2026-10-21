package workers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZakharyB/meshcraft/internal/resources"
	"github.com/ZakharyB/meshcraft/internal/scheduler"
	"github.com/ZakharyB/meshcraft/internal/snapshots"
)

func TestVerifyAndReplicateEndToEnd(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "world"), 0o755)
	os.WriteFile(filepath.Join(src, "world", "level.dat"), []byte("data"), 0o644)
	store := snapshots.NewStore(t.TempDir())
	m, err := store.Create("w1", "1.21", "peerA", 1, src)
	if err != nil {
		t.Fatal(err)
	}
	sched := scheduler.New()
	sched.RegisterPeer(resources.Snapshot{PeerID: "self", NumCPU: 2, FreeRAMMB: 4096, CanHost: true})
	// The executor assigns to the best peer; force policy to "self".
	sched.SetPolicy(func(t scheduler.Task, c []resources.Snapshot) []string { return []string{"self"} })
	ex := NewExecutor(store)

	id, err := sched.Submit(scheduler.Task{Type: scheduler.TaskBackupVerify, WorldID: "w1", Payload: map[string]string{"snapshot_id": m.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ex.Run(context.Background(), sched, id); err != nil {
		t.Fatal(err)
	}
	if task, _ := sched.Get(id); task.State != scheduler.StateCompleted {
		t.Fatalf("verify task state=%s", task.State)
	}

	destRoot := t.TempDir()
	id2, _ := sched.Submit(scheduler.Task{Type: scheduler.TaskSnapshotReplicate, WorldID: "w1",
		Payload: map[string]string{"snapshot_id": m.ID, "dest_root": destRoot}})
	if err := ex.Run(context.Background(), sched, id2); err != nil {
		t.Fatal(err)
	}
	if err := snapshots.NewStore(destRoot).Verify("w1", m.ID); err != nil {
		t.Fatalf("replica does not verify: %v", err)
	}
}

func TestConfinedRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := confined(root, filepath.Join("..", "..", "etc", "passwd")); err == nil {
		t.Fatal("path escape was allowed")
	}
	if _, err := confined(root, filepath.Join("w1", "snap1")); err != nil {
		t.Fatalf("legit path rejected: %v", err)
	}
}
