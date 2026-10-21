package minecraft

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZakharyB/meshcraft/internal/config"
)

// fakeJava copies the test-harness script into a temp dir as an
// executable "java" stand-in. No Java, no Paper download, real process.
func fakeJava(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "tools", "test-harness", "fakejava.sh"))
	if err != nil {
		t.Fatalf("read fakejava: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "fakejava.sh")
	if err := os.WriteFile(dst, src, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestPaperLifecycleWithFakeRuntime(t *testing.T) {
	dir := t.TempDir()
	// The adapter requires a jar file to exist.
	if err := os.WriteFile(filepath.Join(dir, "paper.jar"), []byte("not-a-real-jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	ad := NewPaperAdapter(config.WorldConfig{
		ID: "w1", Name: "Test", Edition: "java", Dir: dir,
		JavaPath: fakeJava(t), JarPath: "paper.jar", MCVersion: "1.21",
		Port: 25599, MaxRAMMB: 512, MaxPlayers: 5, OnlineMode: true,
	})
	ctx := context.Background()
	if err := ad.InstallCheck(ctx); err != nil {
		t.Fatalf("install check: %v", err)
	}
	if err := ad.Configure(); err != nil {
		t.Fatal(err)
	}
	props, err := ad.Properties()
	if err != nil || props["server-port"] != "25599" || props["max-players"] != "5" {
		t.Fatalf("properties wrong: %v %v", props, err)
	}
	if err := ad.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ad.WaitReady(ctx, 10*time.Second); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	st := ad.Status()
	if !st.Running || !st.Ready || st.Endpoint != "127.0.0.1:25599" {
		t.Fatalf("status wrong: %+v", st)
	}
	// The fake server must have created world data in the world dir.
	if _, err := os.Stat(filepath.Join(dir, "world", "level.dat")); err != nil {
		t.Fatalf("world data missing: %v", err)
	}
	if err := ad.Stop(ctx, 10*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if ad.Status().Running {
		t.Fatal("server still running after stop")
	}
	if _, err := os.Stat(filepath.Join(dir, "world", "session-end.txt")); err != nil {
		t.Fatalf("graceful stop did not reach the server (no final save marker): %v", err)
	}
	logs := ad.Logs(50)
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Done (") {
		t.Fatalf("logs missing readiness line:\n%s", joined)
	}
	// Stop is idempotent.
	if err := ad.Stop(ctx, time.Second); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

func TestInstallCheckFailsHonestly(t *testing.T) {
	dir := t.TempDir() // no jar here
	ad := NewPaperAdapter(config.WorldConfig{
		ID: "w1", Edition: "java", Dir: dir, JavaPath: fakeJava(t),
		JarPath: "paper.jar", Port: 25565, MaxRAMMB: 512,
	})
	err := ad.InstallCheck(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected ErrNotInstalled, got %v", err)
	}
}
