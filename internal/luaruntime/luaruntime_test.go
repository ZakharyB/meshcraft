package luaruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeAPI struct {
	status   WorldInfo
	backupID string
	tasks    map[string]string // taskID -> extID
	seq      int
}

func (f *fakeAPI) WorldStatus(string) (WorldInfo, error) { return f.status, nil }
func (f *fakeAPI) WorldMetrics(string) (map[string]float64, error) {
	return map[string]float64{"uptime_s": 42, "cpu_percent": -1, "mem_mb": -1}, nil
}
func (f *fakeAPI) ListPeers() ([]PeerInfo, error) {
	return []PeerInfo{{ID: "peerA", Name: "A", CanHost: true, FreeRAMMB: 4096}}, nil
}
func (f *fakeAPI) SubmitTask(extID string, def TaskDef) (string, error) {
	if def.Type == "" {
		return "", fmt.Errorf("task type required")
	}
	f.seq++
	id := fmt.Sprintf("task-%d", f.seq)
	if f.tasks == nil {
		f.tasks = map[string]string{}
	}
	f.tasks[id] = extID
	return id, nil
}
func (f *fakeAPI) CancelTask(extID, taskID string) error {
	if f.tasks[taskID] != extID {
		return fmt.Errorf("not your task")
	}
	delete(f.tasks, taskID)
	return nil
}
func (f *fakeAPI) RequestBackup(string) (string, error) { return f.backupID, nil }

func writeExt(t *testing.T, root, dir, manifest, main string) string {
	t.Helper()
	d := filepath.Join(root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "main.lua"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func manifest(id string, perms ...string) string {
	qs := make([]string, len(perms))
	for i, p := range perms {
		qs[i] = fmt.Sprintf("%q", p)
	}
	return fmt.Sprintf(`{"id":%q,"name":"T","version":"1.0.0","api_version":1,"entry":"main.lua","permissions":[%s]}`,
		id, strings.Join(qs, ","))
}

func newMgr(t *testing.T, api HostAPI) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	m := NewManager(root, api, nil)
	m.Start()
	return m, root
}

func TestValidExtensionAndEvents(t *testing.T) {
	api := &fakeAPI{status: WorldInfo{ID: "w1", Running: true, Endpoint: "127.0.0.1:25565"}, backupID: "snap-1"}
	m, root := newMgr(t, api)
	writeExt(t, root, "good", manifest("dev.test.good", "world:read", "events:subscribe"), `
st = world.get_status()
status_id = st.id
count = 0
events.on("world_started", function(ev) count = count + 1 end)
`)
	if _, err := m.Load(filepath.Join(root, "good")); err != nil {
		t.Fatal(err)
	}
	if v, ok := m.InspectGlobal("dev.test.good", "status_id"); !ok || v != "w1" {
		t.Fatalf("status_id = %q ok=%v", v, ok)
	}
	m.Emit("world_started", map[string]any{"world_id": "w1"})
	m.Flush()
	if v, _ := m.InspectGlobal("dev.test.good", "count"); v != "1" {
		t.Fatalf("handler count = %q, want 1", v)
	}
}

func TestPermissionDenied(t *testing.T) {
	api := &fakeAPI{backupID: "snap-1"}
	m, root := newMgr(t, api)
	// No storage:backup permission declared.
	writeExt(t, root, "noperm", manifest("dev.test.noperm", "events:subscribe"), `
snap, err = storage.request_backup("w1")
denied = tostring(err)
`)
	if _, err := m.Load(filepath.Join(root, "noperm")); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.InspectGlobal("dev.test.noperm", "denied"); !strings.Contains(v, "permission denied") {
		t.Fatalf("denied = %q", v)
	}
	// os/io must not exist in the sandbox.
	if err := m.RunChunk("dev.test.noperm", `x = os.execute("echo hi")`, time.Second); err == nil {
		t.Fatal("os.execute was reachable — sandbox broken")
	}
}

func TestInfiniteLoopTimesOutAndCoreSurvives(t *testing.T) {
	api := &fakeAPI{}
	m, root := newMgr(t, api)
	writeExt(t, root, "looper", manifest("dev.test.looper"), `ok = true`)
	if _, err := m.Load(filepath.Join(root, "looper")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := m.RunChunk("dev.test.looper", `while true do end`, 300*time.Millisecond)
	if err == nil {
		t.Fatal("infinite loop was not cancelled")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout took far too long")
	}
	// The manager still works afterwards.
	writeExt(t, root, "after", manifest("dev.test.after", "events:subscribe"), `alive = "yes"`)
	if _, err := m.Load(filepath.Join(root, "after")); err != nil {
		t.Fatalf("core damaged by looping script: %v", err)
	}
}

func TestBadExtensionsRejectedNotFatal(t *testing.T) {
	api := &fakeAPI{}
	m, root := newMgr(t, api)
	writeExt(t, root, "syntax", manifest("dev.test.syntax"), `this is not lua !!!`)
	if _, err := m.Load(filepath.Join(root, "syntax")); err == nil {
		t.Fatal("syntax error extension loaded")
	}
	writeExt(t, root, "badperm", `{"id":"dev.test.badperm","name":"T","version":"1","api_version":1,"entry":"main.lua","permissions":["system:root"]}`, `x = 1`)
	if _, err := m.Load(filepath.Join(root, "badperm")); err == nil {
		t.Fatal("unknown permission accepted")
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("manager should hold no extensions, has %v", got)
	}
}

func TestFailingHandlerDisablesExtension(t *testing.T) {
	api := &fakeAPI{}
	m, root := newMgr(t, api)
	writeExt(t, root, "failer", manifest("dev.test.failer", "events:subscribe"), `
events.on("world_started", function(ev) error("boom") end)
`)
	if _, err := m.Load(filepath.Join(root, "failer")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		m.Emit("world_started", map[string]any{"world_id": "w1"})
	}
	m.Flush()
	ext, _ := m.Get("dev.test.failer")
	ext.mu.Lock()
	disabled := ext.Disabled
	ext.mu.Unlock()
	if !disabled {
		t.Fatal("extension not disabled after 3 failing handler runs")
	}
}

func TestExampleExtensionsLoad(t *testing.T) {
	api := &fakeAPI{status: WorldInfo{ID: "w1"}, backupID: "snap-9"}
	root := filepath.Join("..", "..", "lua", "examples")
	m := NewManager(root, api, nil)
	m.Start()
	loaded, errs := m.LoadAll()
	if len(errs) > 0 {
		t.Fatalf("example extensions failed: %v", errs)
	}
	if len(loaded) != 4 {
		t.Fatalf("loaded %v, want 4 examples", loaded)
	}
	// The backup scheduler should react to world_stopped without error.
	m.Emit("world_stopped", map[string]any{"world_id": "w1"})
	m.Emit("worker_available", map[string]any{})
	m.Flush()
	if v, _ := m.InspectGlobal("dev.meshcraft.worker-policy", "host_capable"); v != "1" {
		t.Fatalf("worker-policy host_capable = %q, want 1", v)
	}
}
