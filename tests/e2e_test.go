// End-to-end test: coordinator + two agents + gateway, all real
// processes' worth of behaviour in-process over real HTTP/TCP.
// The Minecraft server is the fake-Paper harness process.
package tests

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZakharyB/meshcraft/internal/agent"
	"github.com/ZakharyB/meshcraft/internal/config"
	"github.com/ZakharyB/meshcraft/internal/coordinator"
	"github.com/ZakharyB/meshcraft/internal/gateway"
	"github.com/ZakharyB/meshcraft/internal/hosting"
	"github.com/ZakharyB/meshcraft/internal/identity"
	"github.com/ZakharyB/meshcraft/internal/observability"
	"github.com/ZakharyB/meshcraft/internal/transport"
)

func fakeJava(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "tools", "test-harness", "fakejava.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "fakejava.sh")
	if err := os.WriteFile(dst, src, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestTwoAgentsCoordinatorAndWorldLifecycle(t *testing.T) {
	ctx := context.Background()
	// --- coordinator ---
	coord := coordinator.NewServer()
	coordTS := httptest.NewServer(coord.Handler())
	defer coordTS.Close()

	// --- agent A with a world ---
	worldDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(worldDir, "paper.jar"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	mcPort := freePort(t)
	dataA := t.TempDir()
	idA, _, err := identity.LoadOrGenerate(dataA)
	if err != nil {
		t.Fatal(err)
	}
	cfgA := &config.AgentConfig{
		NodeName: "agent-a", DataDir: dataA, APIAddr: fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		CoordinatorURL: coordTS.URL,
		Worlds: []config.WorldConfig{{
			ID: "survival", Name: "Survival", Edition: "java", Dir: worldDir,
			JavaPath: fakeJava(t), JarPath: "paper.jar", MCVersion: "1.21",
			Port: mcPort, MaxRAMMB: 512, MaxPlayers: 5, OnlineMode: true,
			BackupKeep: 3, OwnerPeerID: idA.PeerID,
		}},
	}
	if err := cfgA.Validate(); err != nil {
		t.Fatal(err)
	}
	agentA, err := agent.New(cfgA, idA, observability.New("test-a"))
	if err != nil {
		t.Fatal(err)
	}

	// --- agent B: enrolls and discovers A ---
	dataB := t.TempDir()
	idB, _, _ := identity.LoadOrGenerate(dataB)
	trB := &transport.HTTPTransport{CoordinatorURL: coordTS.URL, Identity: idB, Name: "agent-b", Address: "http://127.0.0.1:1", CanHost: false}
	trA := &transport.HTTPTransport{CoordinatorURL: coordTS.URL, Identity: idA, Name: "agent-a", Address: "http://" + cfgA.APIAddr, CanHost: true, FreeRAMMB: 4096}
	if err := trA.Announce(ctx); err != nil {
		t.Fatalf("A announce: %v", err)
	}
	if err := trB.Announce(ctx); err != nil {
		t.Fatalf("B announce: %v", err)
	}
	peers, err := trB.Peers(ctx)
	if err != nil || len(peers) != 2 {
		t.Fatalf("B sees %v peers, err %v", peers, err)
	}
	if d, err := trB.Ping(ctx, transport.PeerEndpoint{PeerID: idA.PeerID, Address: coordTS.URL}); err != nil || d < 0 {
		t.Fatalf("ping via coordinator healthz stand-in: %v %v", d, err)
	}

	// --- A runs its local API; status endpoint works with the token ---
	srvA := httptest.NewServer(agentA.Handler())
	defer srvA.Close()
	req, _ := http.NewRequest("GET", srvA.URL+"/status", nil)
	req.Header.Set("X-MeshCraft-Token", agentA.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("agent status: %v %v", resp, err)
	}
	resp.Body.Close()
	// ... and refuses requests without it.
	resp2, _ := http.Get(srvA.URL + "/status")
	if resp2.StatusCode != 401 {
		t.Fatalf("unauthenticated status returned %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	// --- world lifecycle: start -> backup -> stop -> restore ---
	if err := agentA.StartWorld(ctx, "survival"); err != nil {
		t.Fatalf("start world: %v", err)
	}
	man, err := agentA.BackupWorld(ctx, "survival")
	if err != nil {
		t.Fatalf("backup while running (quiesced): %v", err)
	}
	if err := agentA.Snaps.Verify("survival", man.ID); err != nil {
		t.Fatalf("backup verify: %v", err)
	}
	if err := agentA.StopWorld(ctx, "survival"); err != nil {
		t.Fatalf("stop world: %v", err)
	}
	// Damage the world, restore, bytes must match the snapshot.
	level := filepath.Join(worldDir, "world", "level.dat")
	orig, _ := os.ReadFile(level)
	os.WriteFile(level, []byte("griefed"), 0o644)
	if err := agentA.RestoreWorld("survival", man.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, _ := os.ReadFile(level)
	if string(restored) != string(orig) {
		t.Fatalf("restored level.dat = %q, want %q", restored, orig)
	}

	// --- gateway: A's coordinator lease authorizes the route ---
	lease, err := coord.Auth.Acquire("survival", idA.PeerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	auth := hosting.NewAuthority(nil)
	gw := gateway.New(gateway.Config{ListenAddr: "127.0.0.1:0", PerIPPerMinute: 100}, auth)
	gw.ObserveLease(lease)
	target := fmt.Sprintf("127.0.0.1:%d", mcPort)
	if err := gw.RegisterRoute("survival", target, idA.PeerID, lease.Token); err != nil {
		t.Fatalf("gateway route: %v", err)
	}
	// Restart the world so the gateway target is live, then push bytes.
	if err := agentA.StartWorld(ctx, "survival"); err != nil {
		t.Fatalf("restart world: %v", err)
	}
	defer agentA.StopWorld(context.Background(), "survival")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go gw.Serve(ln)
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The fake server treats unknown lines as log noise; what matters
	// is the TCP path gateway->server exists and the process stays up.
	if _, err := conn.Write([]byte("meshcraft-e2e-probe\n")); err != nil {
		t.Fatalf("write through gateway: %v", err)
	}
	st, err := agentA.WorldStatus("survival")
	if err != nil || !st.Running {
		t.Fatalf("world not running after gateway probe: %+v %v", st, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(conn, 0))
}
