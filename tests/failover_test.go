// Failover test: host A crashes without releasing its coordinator
// lease. Peer B's takeover must FAIL while the lease is valid
// (fail-closed), then SUCCEED after expiry — restoring the replicated
// snapshot and serving the world under a higher fencing token.
package tests

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZakharyB/meshcraft/internal/agent"
	"github.com/ZakharyB/meshcraft/internal/authorization"
	"github.com/ZakharyB/meshcraft/internal/config"
	"github.com/ZakharyB/meshcraft/internal/coordinator"
	"github.com/ZakharyB/meshcraft/internal/identity"
	"github.com/ZakharyB/meshcraft/internal/membership"
	"github.com/ZakharyB/meshcraft/internal/observability"
	"github.com/ZakharyB/meshcraft/internal/resources"
	"github.com/ZakharyB/meshcraft/internal/scheduler"
	"github.com/ZakharyB/meshcraft/internal/transport"
	"github.com/ZakharyB/meshcraft/internal/workers"
)

func mkAgent(t *testing.T, name, coordURL, worldDir string, port int) (*agent.Agent, *identity.Identity, string) {
	t.Helper()
	dataDir := t.TempDir()
	id, _, err := identity.LoadOrGenerate(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.AgentConfig{
		NodeName: name, DataDir: dataDir, APIAddr: fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		CoordinatorURL: coordURL,
		Worlds: []config.WorldConfig{{
			ID: "survival", Name: "Survival", Edition: "java", Dir: worldDir,
			JavaPath: fakeJava(t), JarPath: "paper.jar", MCVersion: "1.21",
			Port: port, MaxRAMMB: 512, MaxPlayers: 5, OnlineMode: false, BackupKeep: 3,
		}},
	}
	a, err := agent.New(cfg, id, observability.New("failover-"+name))
	if err != nil {
		t.Fatal(err)
	}
	return a, id, dataDir
}

func TestFailoverTakeover(t *testing.T) {
	ctx := context.Background()
	coord := coordinator.NewServer()
	coordTS := httptest.NewServer(coord.Handler())
	defer coordTS.Close()

	worldDirA := t.TempDir()
	os.WriteFile(filepath.Join(worldDirA, "paper.jar"), []byte("fake"), 0o644)
	worldDirB := t.TempDir()
	os.WriteFile(filepath.Join(worldDirB, "paper.jar"), []byte("fake"), 0o644)

	agentA, idA, _ := mkAgent(t, "host-a", coordTS.URL, worldDirA, freePort(t))
	agentB, idB, _ := mkAgent(t, "host-b", coordTS.URL, worldDirB, freePort(t))
	agentA.Cfg.Worlds[0].OwnerPeerID = idA.PeerID
	agentB.Cfg.Worlds[0].OwnerPeerID = idA.PeerID // B is not the owner

	// Owner A grants B the host role in B's membership view.
	if err := agentB.Members.Add("survival", membership.Member{PeerID: idA.PeerID, Role: authorization.RoleOwner}); err != nil {
		t.Fatal(err)
	}
	if err := agentB.Members.Add("survival", membership.Member{PeerID: idB.PeerID, Role: authorization.RoleHost}); err != nil {
		t.Fatal(err)
	}

	// A hosts and takes a snapshot of the running world.
	if err := agentA.StartWorld(ctx, "survival"); err != nil {
		t.Fatalf("A start: %v", err)
	}
	man, err := agentA.BackupWorld(ctx, "survival")
	if err != nil {
		t.Fatalf("A backup: %v", err)
	}

	// A also holds the coordinator lease (short TTL for the test).
	trA := &transport.HTTPTransport{CoordinatorURL: coordTS.URL, Identity: idA, Name: "host-a"}
	leaseA, err := trA.AcquireLease(ctx, "survival", 2*time.Second)
	if err != nil {
		t.Fatalf("A coordinator lease: %v", err)
	}

	// Replicate A's snapshot into B's store via the worker framework.
	exec := workers.NewExecutor(agentA.Snaps)
	sched := scheduler.New()
	sched.RegisterPeer(resources.Snapshot{PeerID: idB.PeerID, NumCPU: 2, FreeRAMMB: 4096, CanHost: true})
	sched.SetPolicy(func(task scheduler.Task, c []resources.Snapshot) []string { return []string{idB.PeerID} })
	taskID, err := sched.Submit(scheduler.Task{
		Type: scheduler.TaskSnapshotReplicate, WorldID: "survival", SubmittedBy: idA.PeerID,
		Payload: map[string]string{"snapshot_id": man.ID, "dest_root": agentB.Snaps.Root},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Run(ctx, sched, taskID); err != nil {
		t.Fatalf("replicate to B: %v", err)
	}
	if err := agentB.Snaps.Verify("survival", man.ID); err != nil {
		t.Fatalf("B's replica does not verify: %v", err)
	}

	// A crashes: process killed, lease NOT released.
	if err := agentA.SimulateCrash("survival"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // let the fake process die

	// B's takeover must FAIL while A's lease is still valid.
	if _, err := agentB.TakeoverWorld(ctx, "survival"); err == nil {
		t.Fatal("takeover succeeded under a valid lease — split-brain risk")
	} else {
		t.Logf("takeover correctly refused under valid lease: %v", err)
	}

	// After the lease expires, takeover succeeds.
	time.Sleep(2 * time.Second)
	leaseB, err := agentB.TakeoverWorld(ctx, "survival")
	if err != nil {
		t.Fatalf("takeover after expiry: %v", err)
	}
	if leaseB.Token <= leaseA.Token {
		t.Fatalf("fencing token did not increase: A=%d B=%d", leaseA.Token, leaseB.Token)
	}
	st, err := agentB.WorldStatus("survival")
	if err != nil || !st.Running {
		t.Fatalf("B is not hosting after takeover: %+v %v", st, err)
	}
	// The restored world carries A's world data.
	b, err := os.ReadFile(filepath.Join(worldDirB, "world", "level.dat"))
	if err != nil || string(b) != "fake-level-data\n" {
		t.Fatalf("B's restored level.dat = %q, %v", b, err)
	}
	// The coordinator now rejects A's stale token and honours B's.
	if coord.Auth.Verify("survival", idA.PeerID, leaseA.Token) {
		t.Fatal("stale host A token still verifies at the coordinator")
	}
	if !coord.Auth.Verify("survival", idB.PeerID, leaseB.Token) {
		t.Fatal("new host B token does not verify at the coordinator")
	}
	if err := agentB.StopWorld(ctx, "survival"); err != nil {
		t.Fatalf("B stop: %v", err)
	}
}
