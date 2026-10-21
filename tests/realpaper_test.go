// Real-Paper end-to-end test. Skipped unless both env vars are set:
//
//	MESHCRAFT_JAVA=/path/to/java
//	MESHCRAFT_PAPER_JAR=/path/to/paper.jar
//
// This is the "testable in Java Minecraft" gate: a REAL Paper server
// booted by the MeshCraft adapter, spoken to with the REAL Minecraft
// protocol (internal/mcproto — the same handshake/status/ping a game
// client performs), directly and through the public gateway, plus a
// login probe and a quiesced backup of the running server.
package tests

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZakharyB/meshcraft/internal/agent"
	"github.com/ZakharyB/meshcraft/internal/config"
	"github.com/ZakharyB/meshcraft/internal/gateway"
	"github.com/ZakharyB/meshcraft/internal/hosting"
	"github.com/ZakharyB/meshcraft/internal/identity"
	"github.com/ZakharyB/meshcraft/internal/mcproto"
	"github.com/ZakharyB/meshcraft/internal/minecraft"
	"github.com/ZakharyB/meshcraft/internal/observability"
)

func TestRealPaperEndToEnd(t *testing.T) {
	java := os.Getenv("MESHCRAFT_JAVA")
	jar := os.Getenv("MESHCRAFT_PAPER_JAR")
	if java == "" || jar == "" {
		t.Skip("set MESHCRAFT_JAVA and MESHCRAFT_PAPER_JAR to run the real-Paper test")
	}
	ctx := context.Background()
	worldDir := t.TempDir()
	jarBytes, err := os.ReadFile(jar)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, "paper.jar"), jarBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// Paperclip downloads the Mojang server jar on first run. Java's
	// HTTP stack cannot use every proxy, so pre-seed its cache with
	// the genuine jar (SHA-1 verified against Mojang's manifest)
	// fetched by the Go HTTP client, which honours proxy env vars.
	if seed := os.Getenv("MESHCRAFT_MOJANG_JAR"); seed != "" {
		b, err := os.ReadFile(seed)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(worldDir, "cache"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worldDir, "cache", "mojang_1.21.1.jar"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Pre-seed a light world: flat type + tiny view distance keep first
	// boot fast on small machines. The adapter preserves these keys.
	pre := "level-type=minecraft:flat\nview-distance=3\nsimulation-distance=3\n"
	if err := os.WriteFile(filepath.Join(worldDir, "server.properties"), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	wcfg := config.WorldConfig{
		ID: "survival", Name: "Survival", Edition: "java", Dir: worldDir,
		JavaPath: java, JarPath: "paper.jar", MCVersion: "1.21.1",
		Port: port, MaxRAMMB: 768, MaxPlayers: 5, OnlineMode: false, BackupKeep: 3,
	}

	// --- 1. Adapter boots REAL Paper; protocol checks direct ---
	ad := minecraft.NewPaperAdapter(wcfg)
	if err := ad.Configure(); err != nil {
		t.Fatal(err)
	}
	if err := ad.Start(ctx); err != nil {
		t.Fatalf("start real paper: %v", err)
	}
	if err := ad.WaitReady(ctx, 300*time.Second); err != nil {
		t.Fatalf("real paper never became ready: %v\nlogs:\n%v", err, ad.Logs(30))
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	st, rtt, err := mcproto.PingStatus(addr, 10*time.Second)
	if err != nil {
		t.Fatalf("protocol status ping failed: %v", err)
	}
	t.Logf("status ping: version=%s protocol=%d players=%d/%d rtt=%s motd=%s",
		st.VersionName, st.VersionProtocol, st.PlayersOnline, st.PlayersMax, rtt, st.Description)
	if st.VersionProtocol != mcproto.ProtocolVersion {
		t.Fatalf("protocol = %d, want %d", st.VersionProtocol, mcproto.ProtocolVersion)
	}
	if st.PlayersMax != 5 {
		t.Fatalf("max players = %d, want 5 (adapter config not applied)", st.PlayersMax)
	}
	lr, err := mcproto.LoginProbe(addr, "MeshCraftProbe", 10*time.Second)
	if err != nil {
		t.Fatalf("login probe: %v", err)
	}
	t.Logf("login probe: kind=%s reason=%s", lr.Kind, lr.Reason)
	if lr.Kind != "encryption_request" && lr.Kind != "login_success" && lr.Kind != "set_compression" {
		t.Fatalf("unexpected login response: %+v", lr)
	}
	if err := ad.Stop(ctx, 60*time.Second); err != nil {
		t.Fatalf("stop real paper: %v", err)
	}

	// --- 2. Agent hosts it; gateway carries the protocol; backup live ---
	dataDir := t.TempDir()
	id, _, err := identity.LoadOrGenerate(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.AgentConfig{
		NodeName: "realpaper-agent", DataDir: dataDir,
		APIAddr: fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		Worlds:  []config.WorldConfig{wcfg},
	}
	cfg.Worlds[0].OwnerPeerID = id.PeerID
	a, err := agent.New(cfg, id, observability.New("realpaper-test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.StartWorld(ctx, "survival"); err != nil {
		t.Fatalf("agent start world: %v", err)
	}
	defer a.StopWorld(context.Background(), "survival")

	lease, ok := a.CurrentLease("survival")
	if !ok {
		t.Fatal("agent holds no lease after start")
	}
	gwAuth := hosting.NewAuthority(nil)
	gw := gateway.New(gateway.Config{ListenAddr: "127.0.0.1:0", PerIPPerMinute: 100}, gwAuth)
	if !gw.ObserveLease(lease) {
		t.Fatal("gateway rejected agent lease")
	}
	if err := gw.RegisterRoute("survival", addr, id.PeerID, lease.Token); err != nil {
		t.Fatalf("gateway route: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go gw.Serve(ln)
	st2, rtt2, err := mcproto.PingStatus(ln.Addr().String(), 10*time.Second)
	if err != nil {
		t.Fatalf("protocol ping THROUGH GATEWAY failed: %v", err)
	}
	t.Logf("gateway ping: version=%s players=%d/%d rtt=%s", st2.VersionName, st2.PlayersOnline, st2.PlayersMax, rtt2)

	// Quiesced backup of the RUNNING real server, then verify.
	man, err := a.BackupWorld(ctx, "survival")
	if err != nil {
		t.Fatalf("backup of running real paper: %v", err)
	}
	if err := a.Snaps.Verify("survival", man.ID); err != nil {
		t.Fatalf("real backup does not verify: %v", err)
	}
	foundLevel := false
	for _, f := range man.Files {
		if f.Path == "world/level.dat" {
			foundLevel = true
		}
	}
	if !foundLevel {
		t.Fatalf("backup is missing world/level.dat; files=%v", man.Files)
	}
	t.Logf("real backup ok: %d files, %d bytes", len(man.Files), man.TotalBytes)

	if err := a.StopWorld(ctx, "survival"); err != nil {
		t.Fatalf("agent stop world: %v", err)
	}
}
