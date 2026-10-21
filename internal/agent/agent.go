// Package agent is the headless peer agent: it wires identity, config,
// membership, the Paper adapter, snapshots, the scheduler/workers and
// the Lua runtime behind a token-protected local HTTP API.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ZakharyB/meshcraft/internal/config"
	"github.com/ZakharyB/meshcraft/internal/hosting"
	"github.com/ZakharyB/meshcraft/internal/identity"
	"github.com/ZakharyB/meshcraft/internal/luaruntime"
	"github.com/ZakharyB/meshcraft/internal/membership"
	"github.com/ZakharyB/meshcraft/internal/minecraft"
	"github.com/ZakharyB/meshcraft/internal/resources"
	"github.com/ZakharyB/meshcraft/internal/scheduler"
	"github.com/ZakharyB/meshcraft/internal/snapshots"
	"github.com/ZakharyB/meshcraft/internal/transport"
	"github.com/ZakharyB/meshcraft/internal/workers"
)

// Agent is the daemon. Construct with New, run with Serve.
type Agent struct {
	Cfg     *config.AgentConfig
	ID      *identity.Identity
	Log     *slog.Logger
	Members *membership.Store
	Snaps   *snapshots.Store
	Sched   *scheduler.Scheduler
	Exec    *workers.Executor
	Lua     *luaruntime.Manager
	Auth    *hosting.Authority // local lease authority (offline/single-node mode)

	mu       sync.Mutex
	adapters map[string]*minecraft.PaperAdapter
	leases   map[string]hosting.Lease
	peers    []transport.PeerEndpoint
	token    string
}

// New builds an agent from config + identity, opening stores under DataDir.
func New(cfg *config.AgentConfig, id *identity.Identity, log *slog.Logger) (*Agent, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	members, err := membership.Open(filepath.Join(cfg.DataDir, "membership.json"))
	if err != nil {
		return nil, err
	}
	a := &Agent{
		Cfg: cfg, ID: id, Log: log, Members: members,
		Snaps:    snapshots.NewStore(filepath.Join(cfg.DataDir, "backups")),
		Sched:    scheduler.New(),
		Auth:     hosting.NewAuthority(nil),
		adapters: map[string]*minecraft.PaperAdapter{},
		leases:   map[string]hosting.Lease{},
	}
	a.Exec = workers.NewExecutor(a.Snaps)
	extDir := cfg.ExtensionsDir
	if extDir == "" {
		extDir = filepath.Join(cfg.DataDir, "extensions")
	}
	a.Lua = luaruntime.NewManager(extDir, a, log)
	a.token, err = loadOrCreateToken(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	// The local peer is always a scheduler candidate.
	a.Sched.RegisterPeer(resources.Probe(id.PeerID, cfg.DataDir))
	return a, nil
}

func loadOrCreateToken(dir string) (string, error) {
	p := filepath.Join(dir, "api.token")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 16 {
		return string(b), nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// Token returns the local API token (for the CLI running on this device).
func (a *Agent) Token() string { return a.token }

func (a *Agent) adapter(worldID string) (*minecraft.PaperAdapter, *config.WorldConfig, error) {
	w, ok := a.Cfg.World(worldID)
	if !ok {
		return nil, nil, fmt.Errorf("unknown world %q", worldID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ad, ok := a.adapters[worldID]; ok {
		return ad, w, nil
	}
	ad := minecraft.NewPaperAdapter(*w)
	a.adapters[worldID] = ad
	return ad, w, nil
}

// AcquireLocalLease takes host authority in local (offline) mode.
// When a coordinator is configured, authority should come from it;
// this local authority is the single-node/offline path and uses the
// same fencing machinery.
func (a *Agent) AcquireLocalLease(worldID string) (hosting.Lease, error) {
	l, err := a.Auth.Acquire(worldID, a.ID.PeerID, 10*time.Minute)
	if err != nil {
		return hosting.Lease{}, err
	}
	a.mu.Lock()
	a.leases[worldID] = l
	a.mu.Unlock()
	return l, nil
}

// StartWorld configures and starts a world's server and waits for readiness.
func (a *Agent) StartWorld(ctx context.Context, worldID string) error {
	ad, _, err := a.adapter(worldID)
	if err != nil {
		return err
	}
	if !a.hasValidLease(worldID) {
		if _, err := a.AcquireLocalLease(worldID); err != nil {
			return fmt.Errorf("host authority: %w", err)
		}
	}
	if err := ad.Configure(); err != nil {
		return err
	}
	if err := ad.Start(ctx); err != nil {
		return err
	}
	if err := ad.WaitReady(ctx, 120*time.Second); err != nil {
		return err
	}
	a.Log.Info("world started", "world", worldID, "endpoint", ad.Status().Endpoint)
	a.Lua.Emit("world_started", map[string]any{"world_id": worldID})
	return nil
}

// StopWorld gracefully stops a world's server.
func (a *Agent) StopWorld(ctx context.Context, worldID string) error {
	ad, _, err := a.adapter(worldID)
	if err != nil {
		return err
	}
	err = ad.Stop(ctx, 30*time.Second)
	a.mu.Lock()
	if l, ok := a.leases[worldID]; ok {
		a.Auth.Release(worldID, a.ID.PeerID, l.Token)
		delete(a.leases, worldID)
	}
	a.mu.Unlock()
	if err == nil {
		a.Log.Info("world stopped", "world", worldID)
		a.Lua.Emit("world_stopped", map[string]any{"world_id": worldID})
	}
	return err
}

func (a *Agent) hasValidLease(worldID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, ok := a.leases[worldID]
	return ok && l.ValidAt(time.Now())
}

// SimulateCrash kills the world process abruptly and deliberately
// does NOT release the lease — the exact state a real crash leaves
// behind. Used by failover tests and operator drills.
func (a *Agent) SimulateCrash(worldID string) error {
	ad, _, err := a.adapter(worldID)
	if err != nil {
		return err
	}
	ad.Kill()
	return nil
}

// TakeoverWorld performs a failover (spec §11): this peer becomes the
// host for a world whose previous host is gone. Steps, in order:
//  1. Authorization — this peer must hold the host role (or own the world).
//  2. Acquire the lease from the coordinator. If a valid lease still
//     exists elsewhere, this FAILS — fail closed, never fork the world.
//  3. Restore the newest locally-held, verified snapshot.
//  4. Start the server under the new fencing token.
//
// The old host's token is stale from this moment; gateways and the
// coordinator reject it.
func (a *Agent) TakeoverWorld(ctx context.Context, worldID string) (hosting.Lease, error) {
	w, err := a.resolveWorld(worldID)
	if err != nil {
		return hosting.Lease{}, err
	}
	if w.OwnerPeerID != a.ID.PeerID && !a.Members.Can(worldID, a.ID.PeerID, "world:host") {
		return hosting.Lease{}, fmt.Errorf("peer %s is not authorized to host world %s", a.ID.PeerID, worldID)
	}
	var lease hosting.Lease
	if a.Cfg.CoordinatorURL != "" {
		tr := &transport.HTTPTransport{CoordinatorURL: a.Cfg.CoordinatorURL, Identity: a.ID, Name: a.Cfg.NodeName}
		lease, err = tr.AcquireLease(ctx, worldID, 10*time.Minute)
		if err != nil {
			return hosting.Lease{}, fmt.Errorf("takeover refused: %w", err)
		}
		a.Auth.Observe(lease)
	} else {
		lease, err = a.Auth.Acquire(worldID, a.ID.PeerID, 10*time.Minute)
		if err != nil {
			return hosting.Lease{}, fmt.Errorf("takeover refused: %w", err)
		}
	}
	a.mu.Lock()
	a.leases[worldID] = lease
	a.mu.Unlock()

	snaps, err := a.Snaps.List(worldID)
	if err != nil || len(snaps) == 0 {
		return hosting.Lease{}, fmt.Errorf("takeover aborted: no snapshot available for world %s", worldID)
	}
	latest := snaps[0]
	if err := a.RestoreWorld(worldID, latest.ID); err != nil {
		return hosting.Lease{}, fmt.Errorf("takeover restore: %w", err)
	}
	if err := a.StartWorld(ctx, worldID); err != nil {
		return hosting.Lease{}, fmt.Errorf("takeover start: %w", err)
	}
	a.Log.Info("takeover complete", "world", worldID, "snapshot", latest.ID, "fencing_token", lease.Token)
	a.Lua.Emit("host_health_changed", map[string]any{"world_id": worldID, "new_host": a.ID.PeerID})
	return lease, nil
}

// BackupWorld takes a consistent snapshot (ADR-0006): a running server
// is quiesced via save-off/save-all flush first; a stopped server is
// copied directly. Rotates to the world's retention count.
func (a *Agent) BackupWorld(ctx context.Context, worldID string) (snapshots.Manifest, error) {
	ad, w, err := a.adapter(worldID)
	if err != nil {
		return snapshots.Manifest{}, err
	}
	a.Lua.Emit("backup_requested", map[string]any{"world_id": worldID})
	running := ad.Status().Running
	if running {
		if err := ad.Quiesce(ctx); err != nil {
			return snapshots.Manifest{}, fmt.Errorf("quiesce server for backup: %w", err)
		}
		defer func() { _ = ad.Resume() }()
	}
	a.mu.Lock()
	token := a.leases[worldID].Token
	a.mu.Unlock()
	// Exclude Paper runtime artifacts that are re-downloadable /
	// re-extractable (paperclip cache, libraries, extracted versions)
	// and the live session lock — a backup is world state + config +
	// plugins, not the runtime's download cache (~230MB on Paper).
	m, err := a.Snaps.Create(worldID, w.MCVersion, a.ID.PeerID, token, w.Dir,
		"session.lock", "cache", "versions", "libraries")
	if err != nil {
		return snapshots.Manifest{}, err
	}
	if _, err := a.Snaps.Rotate(worldID, w.BackupKeep); err != nil {
		a.Log.Warn("backup rotation failed", "err", err)
	}
	a.Log.Info("backup created", "world", worldID, "snapshot", m.ID, "bytes", m.TotalBytes)
	a.Lua.Emit("backup_completed", map[string]any{"world_id": worldID, "snapshot_id": m.ID})
	return m, nil
}

// RestoreWorld replaces a stopped world's directory from a verified snapshot.
func (a *Agent) RestoreWorld(worldID, snapID string) error {
	ad, w, err := a.adapter(worldID)
	if err != nil {
		return err
	}
	if ad.Status().Running {
		return minecraft.ErrServerRunning
	}
	return a.Snaps.Restore(worldID, snapID, w.Dir, w.MCVersion)
}

// --- Lua HostAPI implementation ---

func (a *Agent) resolveWorld(worldID string) (*config.WorldConfig, error) {
	if worldID == "" {
		if len(a.Cfg.Worlds) == 0 {
			return nil, errors.New("no worlds configured")
		}
		return &a.Cfg.Worlds[0], nil
	}
	w, ok := a.Cfg.World(worldID)
	if !ok {
		return nil, fmt.Errorf("unknown world %q", worldID)
	}
	return w, nil
}

// WorldStatus implements luaruntime.HostAPI.
func (a *Agent) WorldStatus(worldID string) (luaruntime.WorldInfo, error) {
	w, err := a.resolveWorld(worldID)
	if err != nil {
		return luaruntime.WorldInfo{}, err
	}
	ad, _, err := a.adapter(w.ID)
	if err != nil {
		return luaruntime.WorldInfo{}, err
	}
	st := ad.Status()
	return luaruntime.WorldInfo{ID: w.ID, Running: st.Running, Ready: st.Ready, Endpoint: st.Endpoint}, nil
}

// WorldMetrics implements luaruntime.HostAPI. Unmeasurable values are -1.
func (a *Agent) WorldMetrics(worldID string) (map[string]float64, error) {
	w, err := a.resolveWorld(worldID)
	if err != nil {
		return nil, err
	}
	ad, _, err := a.adapter(w.ID)
	if err != nil {
		return nil, err
	}
	st := ad.Status()
	uptime := float64(-1)
	if st.Running {
		uptime = time.Since(st.StartedAt).Seconds()
	}
	return map[string]float64{"uptime_s": uptime, "cpu_percent": -1, "mem_mb": -1}, nil
}

// ListPeers implements luaruntime.HostAPI from the coordinator cache
// (or just this peer when no coordinator is configured).
func (a *Agent) ListPeers() ([]luaruntime.PeerInfo, error) {
	a.mu.Lock()
	peers := append([]transport.PeerEndpoint(nil), a.peers...)
	a.mu.Unlock()
	out := []luaruntime.PeerInfo{{ID: a.ID.PeerID, Name: a.Cfg.NodeName + " (this device)", CanHost: true, FreeRAMMB: resources.Probe(a.ID.PeerID, a.Cfg.DataDir).FreeRAMMB}}
	for _, p := range peers {
		if p.PeerID == a.ID.PeerID {
			continue
		}
		out = append(out, luaruntime.PeerInfo{ID: p.PeerID, Name: p.Name, CanHost: true, FreeRAMMB: -1})
	}
	return out, nil
}

// SubmitTask implements luaruntime.HostAPI: Go re-validates the task
// (world exists, type allowed, budgets capped by the scheduler) and
// runs it here if this peer is the assignee.
func (a *Agent) SubmitTask(extID string, def luaruntime.TaskDef) (string, error) {
	if _, err := a.resolveWorld(def.WorldID); err != nil {
		return "", err
	}
	t := scheduler.Task{
		Type: scheduler.TaskType(def.Type), WorldID: def.WorldID,
		SubmittedBy: "ext:" + extID, MaxMemMB: def.MaxMemMB,
		MaxRetries: def.Retries, Payload: def.Payload,
	}
	if def.TimeoutS > 0 {
		t.Timeout = time.Duration(def.TimeoutS) * time.Second
	}
	id, err := a.Sched.Submit(t)
	if err != nil {
		return "", err
	}
	assignee, err := a.Sched.Assign(id)
	if err == nil && assignee == a.ID.PeerID {
		go func() {
			if err := a.Exec.Run(context.Background(), a.Sched, id); err != nil {
				a.Log.Warn("extension task failed", "task", id, "err", err)
				a.Lua.Emit("task_failed", map[string]any{"task_id": id})
				return
			}
			a.Lua.Emit("task_completed", map[string]any{"task_id": id})
		}()
		a.Lua.Emit("task_started", map[string]any{"task_id": id})
	}
	return id, nil
}

// CancelTask implements luaruntime.HostAPI with ownership enforcement:
// an extension can only cancel tasks it submitted.
func (a *Agent) CancelTask(extID, taskID string) error {
	t, ok := a.Sched.Get(taskID)
	if !ok {
		return fmt.Errorf("no such task %s", taskID)
	}
	if t.SubmittedBy != "ext:"+extID {
		return fmt.Errorf("task %s belongs to %s, not extension %s", taskID, t.SubmittedBy, extID)
	}
	return a.Sched.Cancel(taskID)
}

// RequestBackup implements luaruntime.HostAPI.
func (a *Agent) RequestBackup(worldID string) (string, error) {
	m, err := a.BackupWorld(context.Background(), worldID)
	if err != nil {
		return "", err
	}
	return m.ID, nil
}

// --- background coordination ---

// StartBackground launches the Lua dispatcher, loads extensions, and
// (when configured) heartbeats to the coordinator every 20s.
func (a *Agent) StartBackground(ctx context.Context) {
	a.Lua.Start()
	if loaded, errs := a.Lua.LoadAll(); len(loaded) > 0 || len(errs) > 0 {
		a.Log.Info("extensions loaded", "loaded", loaded, "errors", len(errs))
	}
	if a.Cfg.CoordinatorURL == "" {
		return
	}
	tr := &transport.HTTPTransport{
		CoordinatorURL: a.Cfg.CoordinatorURL, Identity: a.ID,
		Name: a.Cfg.NodeName, Address: "http://" + a.Cfg.APIAddr,
		CanHost: true, FreeRAMMB: resources.Probe(a.ID.PeerID, a.Cfg.DataDir).FreeRAMMB,
	}
	go func() {
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		announce := func() {
			if err := tr.Announce(ctx); err != nil {
				a.Log.Warn("coordinator announce failed", "err", err)
				return
			}
			if peers, err := tr.Peers(ctx); err == nil {
				a.mu.Lock()
				a.peers = peers
				a.mu.Unlock()
				for _, p := range peers {
					a.Sched.RegisterPeer(resources.Snapshot{PeerID: p.PeerID, CanHost: true, FreeRAMMB: -1, NumCPU: 1})
				}
			}
		}
		announce()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				announce()
			}
		}
	}()
}

// --- local HTTP API ---

func (a *Agent) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-MeshCraft-Token") != a.token {
			writeJSON(w, 401, map[string]string{"error": "missing or invalid local API token"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type idRequest struct {
	ID       string `json:"id"`
	Snapshot string `json:"snapshot"`
}

// Handler returns the agent's local API (loopback + token by default).
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "peer_id": a.ID.PeerID, "node": a.Cfg.NodeName})
	})
	mux.HandleFunc("GET /status", a.auth(func(w http.ResponseWriter, r *http.Request) {
		worlds := map[string]any{}
		for _, wc := range a.Cfg.Worlds {
			if ad, _, err := a.adapter(wc.ID); err == nil {
				worlds[wc.ID] = ad.Status()
			}
		}
		writeJSON(w, 200, map[string]any{
			"peer_id": a.ID.PeerID, "node": a.Cfg.NodeName,
			"resources": resources.Probe(a.ID.PeerID, a.Cfg.DataDir),
			"worlds":    worlds, "extensions": a.Lua.List(),
			"tasks": a.Sched.List(),
		})
	}))
	worldAction := func(fn func(ctx context.Context, id string) error) http.HandlerFunc {
		return a.auth(func(w http.ResponseWriter, r *http.Request) {
			var req idRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
				writeJSON(w, 400, map[string]string{"error": "json body with \"id\" required"})
				return
			}
			if err := fn(r.Context(), req.ID); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
		})
	}
	mux.HandleFunc("POST /world/start", worldAction(func(ctx context.Context, id string) error { return a.StartWorld(ctx, id) }))
	mux.HandleFunc("POST /world/stop", worldAction(func(ctx context.Context, id string) error { return a.StopWorld(ctx, id) }))
	mux.HandleFunc("POST /world/backup", a.auth(func(w http.ResponseWriter, r *http.Request) {
		var req idRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
			writeJSON(w, 400, map[string]string{"error": "json body with \"id\" required"})
			return
		}
		m, err := a.BackupWorld(r.Context(), req.ID)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, m)
	}))
	mux.HandleFunc("POST /world/restore", a.auth(func(w http.ResponseWriter, r *http.Request) {
		var req idRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Snapshot == "" {
			writeJSON(w, 400, map[string]string{"error": "json body with \"id\" and \"snapshot\" required"})
			return
		}
		if err := a.RestoreWorld(req.ID, req.Snapshot); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /backups", a.auth(func(w http.ResponseWriter, r *http.Request) {
		list, err := a.Snaps.List(r.URL.Query().Get("id"))
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"snapshots": list})
	}))
	mux.HandleFunc("GET /world/logs", a.auth(func(w http.ResponseWriter, r *http.Request) {
		ad, _, err := a.adapter(r.URL.Query().Get("id"))
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"lines": ad.Logs(100)})
	}))
	return mux
}

// Serve runs the local API until ctx is cancelled.
func (a *Agent) Serve(ctx context.Context) error {
	srv := &http.Server{Addr: a.Cfg.APIAddr, Handler: a.Handler()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		a.Lua.Flush()
		a.Lua.Close()
	}()
	a.Log.Info("agent listening", "addr", a.Cfg.APIAddr, "peer_id", a.ID.PeerID)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// CurrentLease returns this agent's current local lease for a world.
func (a *Agent) CurrentLease(worldID string) (hosting.Lease, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, ok := a.leases[worldID]
	return l, ok
}
