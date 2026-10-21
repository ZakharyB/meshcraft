// Package luaruntime hosts Lua extensions (spec §9) on gopher-lua.
//
// Safety model (ADR-0005): states are created without os/io/package/
// debug; scripts reach the platform only through the capability API
// in api.go, and every capability checks the extension's manifest
// permissions in Go. Calls run under a context deadline so infinite
// loops are cancelled. A handler that fails 3 times in a row disables
// its extension; no Lua failure can take down the Go core, and the
// core runs identically with zero extensions installed.
package luaruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// APIVersion is the current Go<->Lua API version.
const APIVersion = 1

// Known permissions an extension may declare.
var KnownPermissions = map[string]bool{
	"world:read": true, "peers:read": true, "tasks:submit": true,
	"storage:backup": true, "events:subscribe": true,
}

// KnownEvents is the closed list of subscribable events.
var KnownEvents = map[string]bool{
	"peer_connected": true, "peer_disconnected": true,
	"worker_available": true, "worker_unavailable": true,
	"task_started": true, "task_completed": true, "task_failed": true,
	"backup_requested": true, "backup_completed": true,
	"host_health_changed": true, "world_started": true, "world_stopped": true,
}

// Manifest is an extension's manifest.json.
type Manifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	APIVersion  int               `json:"api_version"`
	Entry       string            `json:"entry"`
	Permissions []string          `json:"permissions"`
	Config      map[string]string `json:"config"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)

// Validate checks a manifest against platform rules. Permissions cannot
// be added after install without editing the manifest on disk — the
// manager re-reads manifests on reload, and the running extension only
// ever holds the permissions validated at load time.
func (m Manifest) Validate() error {
	if !idRe.MatchString(m.ID) {
		return fmt.Errorf("extension id %q is invalid", m.ID)
	}
	if m.Name == "" || m.Version == "" {
		return errors.New("extension name and version are required")
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("extension %s targets API v%d; this agent speaks v%d", m.ID, m.APIVersion, APIVersion)
	}
	if m.Entry == "" || filepath.IsAbs(m.Entry) || filepath.Clean(m.Entry) != m.Entry || m.Entry == ".." {
		return fmt.Errorf("extension %s: entry must be a clean relative path", m.ID)
	}
	for _, p := range m.Permissions {
		if !KnownPermissions[p] {
			return fmt.Errorf("extension %s declares unknown permission %q", m.ID, p)
		}
	}
	return nil
}

func (m Manifest) has(perm string) bool {
	for _, p := range m.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// WorldInfo / PeerInfo are the data shapes the API exposes to Lua.
type WorldInfo struct {
	ID       string
	Running  bool
	Ready    bool
	Endpoint string
}

type PeerInfo struct {
	ID        string
	Name      string
	CanHost   bool
	FreeRAMMB int64
}

// TaskDef is a validated task request coming from Lua.
type TaskDef struct {
	Type     string
	WorldID  string
	MaxMemMB int64
	TimeoutS int
	Retries  int
	Payload  map[string]string
}

// HostAPI is implemented by the agent. Lua never touches internals
// directly; these Go methods re-validate everything.
type HostAPI interface {
	WorldStatus(worldID string) (WorldInfo, error)
	WorldMetrics(worldID string) (map[string]float64, error)
	ListPeers() ([]PeerInfo, error)
	SubmitTask(extID string, def TaskDef) (string, error)
	CancelTask(extID, taskID string) error
	RequestBackup(worldID string) (string, error)
}

// Extension is one loaded extension.
type Extension struct {
	Manifest Manifest
	Dir      string

	mu       sync.Mutex
	state    *lua.LState
	handlers map[string][]*lua.LFunction
	failures int
	Disabled bool
	owned    map[string]bool // task ids submitted by this extension
}

// Manager discovers, loads and supervises extensions in a directory.
type Manager struct {
	Dir            string
	API            HostAPI
	Log            *slog.Logger
	HandlerTimeout time.Duration

	mu    sync.Mutex
	exts  map[string]*Extension
	queue chan eventItem
	wg    sync.WaitGroup
}

type eventItem struct {
	name    string
	payload map[string]any
}

// NewManager creates a manager. HandlerTimeout defaults to 2s.
func NewManager(dir string, api HostAPI, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		Dir: dir, API: api, Log: log, HandlerTimeout: 2 * time.Second,
		exts: map[string]*Extension{}, queue: make(chan eventItem, 128),
	}
}

// Start launches the event dispatcher goroutine.
func (m *Manager) Start() {
	go func() {
		for item := range m.queue {
			m.dispatch(item)
			m.wg.Done()
		}
	}()
}

// LoadAll discovers and loads every valid extension in Dir. Invalid
// extensions are logged and skipped — never fatal to the core.
func (m *Manager) LoadAll() (loaded []string, errs []error) {
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no extensions installed is a valid state
		}
		return nil, []error{err}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, err := m.Load(filepath.Join(m.Dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			m.Log.Error("extension failed to load", "dir", e.Name(), "err", err)
			continue
		}
		loaded = append(loaded, id)
	}
	return loaded, errs
}

// Load validates and activates one extension directory.
func (m *Manager) Load(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", fmt.Errorf("%s: read manifest: %w", dir, err)
	}
	var man Manifest
	if err := json.Unmarshal(b, &man); err != nil {
		return "", fmt.Errorf("%s: parse manifest: %w", dir, err)
	}
	if err := man.Validate(); err != nil {
		return "", err
	}
	entry := filepath.Join(dir, man.Entry)
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("extension %s: entry %q missing", man.ID, man.Entry)
	}
	ext := &Extension{Manifest: man, Dir: dir, handlers: map[string][]*lua.LFunction{}, owned: map[string]bool{}}
	if err := m.activate(ext); err != nil {
		return "", err
	}
	m.mu.Lock()
	if old, dup := m.exts[man.ID]; dup {
		old.close()
	}
	m.exts[man.ID] = ext
	m.mu.Unlock()
	m.Log.Info("extension loaded", "id", man.ID, "version", man.Version, "permissions", man.Permissions)
	return man.ID, nil
}

// activate builds the sandboxed state and runs the entry script.
func (m *Manager) activate(ext *Extension) error {
	L := lua.NewState()
	// Strip everything dangerous (ADR-0005). What remains: base
	// (minus loaders), table, string, math, coroutine.
	for _, name := range []string{"os", "io", "package", "debug", "require", "dofile", "loadfile", "load", "loadstring"} {
		L.SetGlobal(name, lua.LNil)
	}
	ext.state = L
	m.installAPI(ext)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	L.SetContext(ctx)
	if err := L.DoFile(filepath.Join(ext.Dir, ext.Manifest.Entry)); err != nil {
		L.Close()
		return fmt.Errorf("extension %s entry failed: %w", ext.Manifest.ID, err)
	}
	return nil
}

func (e *Extension) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != nil {
		e.state.Close()
		e.state = nil
	}
}

// Get returns a loaded extension by id.
func (m *Manager) Get(id string) (*Extension, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.exts[id]
	return e, ok
}

// List returns the loaded extension ids.
func (m *Manager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.exts))
	for id := range m.exts {
		out = append(out, id)
	}
	return out
}

// Disable stops an extension from receiving events or API service.
func (m *Manager) Disable(id string, reason string) {
	m.mu.Lock()
	ext, ok := m.exts[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	ext.mu.Lock()
	ext.Disabled = true
	ext.mu.Unlock()
	m.Log.Warn("extension disabled", "id", id, "reason", reason)
}

// Reload re-reads an extension from disk (picking up manifest changes
// only through this explicit action).
func (m *Manager) Reload(id string) error {
	m.mu.Lock()
	ext, ok := m.exts[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no such extension %s", id)
	}
	ext.close()
	_, err := m.Load(ext.Dir)
	return err
}

// Close shuts down all extensions.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.exts {
		e.close()
	}
}

// Emit queues an event for asynchronous dispatch (bounded queue;
// a full queue drops the event with a warning instead of blocking
// the core — spec §9.2).
func (m *Manager) Emit(name string, payload map[string]any) {
	if !KnownEvents[name] {
		m.Log.Warn("emit of unknown event ignored", "event", name)
		return
	}
	m.wg.Add(1)
	select {
	case m.queue <- eventItem{name, payload}:
	default:
		m.wg.Done()
		m.Log.Warn("event queue full; dropping", "event", name)
	}
}

// Flush blocks until all queued events have been dispatched.
// Used by tests and graceful shutdown.
func (m *Manager) Flush() { m.wg.Wait() }

func (m *Manager) dispatch(item eventItem) {
	m.mu.Lock()
	exts := make([]*Extension, 0, len(m.exts))
	for _, e := range m.exts {
		exts = append(exts, e)
	}
	m.mu.Unlock()
	for _, ext := range exts {
		ext.mu.Lock()
		if ext.Disabled || ext.state == nil {
			ext.mu.Unlock()
			continue
		}
		fns := ext.handlers[item.name]
		if len(fns) == 0 {
			ext.mu.Unlock()
			continue
		}
		L := ext.state
		ctx, cancel := context.WithTimeout(context.Background(), m.HandlerTimeout)
		L.SetContext(ctx)
		payload := toLuaTable(L, item.payload)
		failed := false
		for _, fn := range fns {
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, payload); err != nil {
				m.Log.Error("event handler failed", "ext", ext.Manifest.ID, "event", item.name, "err", err)
				failed = true
			}
		}
		cancel()
		if failed {
			ext.failures++
			if ext.failures >= 3 {
				ext.Disabled = true
				m.Log.Warn("extension disabled after repeated handler failures", "id", ext.Manifest.ID)
			}
		} else {
			ext.failures = 0
		}
		ext.mu.Unlock()
	}
}

// RunWithTimeout executes fn (a Lua chunk) inside an extension's state
// under a deadline. Exposed for the lifecycle `main` test path and for
// agent diagnostics; production code paths use handlers + API calls.
func (m *Manager) RunChunk(id, chunk string, timeout time.Duration) error {
	ext, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("no such extension %s", id)
	}
	ext.mu.Lock()
	defer ext.mu.Unlock()
	if ext.Disabled || ext.state == nil {
		return fmt.Errorf("extension %s is disabled", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ext.state.SetContext(ctx)
	return ext.state.DoString(chunk)
}

func toLuaTable(L *lua.LState, data map[string]any) *lua.LTable {
	t := L.NewTable()
	for k, v := range data {
		t.RawSetString(k, toLuaValue(L, v))
	}
	return t
}

func toLuaValue(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case string:
		return lua.LString(x)
	case bool:
		return lua.LBool(x)
	case int:
		return lua.LNumber(x)
	case int64:
		return lua.LNumber(x)
	case float64:
		return lua.LNumber(x)
	case map[string]any:
		return toLuaTable(L, x)
	case nil:
		return lua.LNil
	default:
		return lua.LString(fmt.Sprint(x))
	}
}

// InspectGlobal returns the string form of a global in an extension's
// state (diagnostics + tests). It never exposes the state itself.
func (m *Manager) InspectGlobal(id, name string) (string, bool) {
	ext, ok := m.Get(id)
	if !ok {
		return "", false
	}
	ext.mu.Lock()
	defer ext.mu.Unlock()
	if ext.state == nil {
		return "", false
	}
	v := ext.state.GetGlobal(name)
	if v == lua.LNil {
		return "", false
	}
	return lua.LVAsString(v), true
}
