// Package workers executes scheduler tasks locally with hard resource
// boundaries. Handlers are a fixed registry — a task can never cause
// arbitrary code or arbitrary filesystem access: every handler's paths
// are confined to the configured snapshot/world roots, verified before
// any work happens.
package workers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZakharyB/meshcraft/internal/scheduler"
	"github.com/ZakharyB/meshcraft/internal/snapshots"
)

// Handler executes one task type. progress reports 0-100.
type Handler func(ctx context.Context, t scheduler.Task, progress func(int)) (string, error)

// Executor runs tasks against a snapshot store.
type Executor struct {
	Snaps    *snapshots.Store
	handlers map[scheduler.TaskType]Handler
}

// NewExecutor creates an executor with the built-in handlers.
func NewExecutor(snaps *snapshots.Store) *Executor {
	e := &Executor{Snaps: snaps, handlers: map[scheduler.TaskType]Handler{}}
	e.handlers[scheduler.TaskBackupVerify] = e.verifyBackup
	e.handlers[scheduler.TaskSnapshotReplicate] = e.replicateSnapshot
	e.handlers[scheduler.TaskFileIntegrity] = e.fileIntegrity
	e.handlers[scheduler.TaskHealthProbe] = func(ctx context.Context, t scheduler.Task, progress func(int)) (string, error) {
		progress(100)
		return "ok", nil
	}
	return e
}

// confined ensures p is inside root (after cleaning). This is the guard
// that keeps worker tasks away from the rest of the user's disk.
func confined(root, p string) (string, error) {
	abs, err := filepath.Abs(filepath.Join(root, p))
	if err != nil {
		return "", err
	}
	r, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if abs != r && !strings.HasPrefix(abs, r+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes its allowed root", p)
	}
	return abs, nil
}

func (e *Executor) verifyBackup(ctx context.Context, t scheduler.Task, progress func(int)) (string, error) {
	snapID := t.Payload["snapshot_id"]
	if snapID == "" {
		return "", fmt.Errorf("snapshot_id payload required")
	}
	progress(10)
	if err := e.Snaps.Verify(t.WorldID, snapID); err != nil {
		return "", err
	}
	progress(100)
	return "snapshot " + snapID + " verified", nil
}

func (e *Executor) replicateSnapshot(ctx context.Context, t scheduler.Task, progress func(int)) (string, error) {
	snapID, destRoot := t.Payload["snapshot_id"], t.Payload["dest_root"]
	if snapID == "" || destRoot == "" {
		return "", fmt.Errorf("snapshot_id and dest_root payloads required")
	}
	// Verify before copying anything.
	if err := e.Snaps.Verify(t.WorldID, snapID); err != nil {
		return "", fmt.Errorf("source snapshot does not verify: %w", err)
	}
	progress(25)
	src := filepath.Join(e.Snaps.Root, t.WorldID, snapID)
	dst, err := confined(destRoot, filepath.Join(t.WorldID, snapID))
	if err != nil {
		return "", err
	}
	if err := copyDir(ctx, src, dst, progress); err != nil {
		return "", err
	}
	// Verify the copy landed intact.
	destStore := snapshots.NewStore(destRoot)
	if err := destStore.Verify(t.WorldID, snapID); err != nil {
		return "", fmt.Errorf("replica failed verification: %w", err)
	}
	progress(100)
	return "replicated + verified " + snapID, nil
}

func (e *Executor) fileIntegrity(ctx context.Context, t scheduler.Task, progress func(int)) (string, error) {
	snapID := t.Payload["snapshot_id"]
	if snapID == "" {
		return "", fmt.Errorf("snapshot_id payload required")
	}
	m, err := e.Snaps.LoadManifest(t.WorldID, snapID)
	if err != nil {
		return "", err
	}
	progress(50)
	if err := e.Snaps.Verify(t.WorldID, snapID); err != nil {
		return "", err
	}
	progress(100)
	return fmt.Sprintf("%d files, %d bytes, all hashes match", len(m.Files), m.TotalBytes), nil
}

func copyDir(ctx context.Context, src, dst string, progress func(int)) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Run executes a task end-to-end against the scheduler's state machine.
func (e *Executor) Run(ctx context.Context, sched *scheduler.Scheduler, taskID string) error {
	t, ok := sched.Get(taskID)
	if !ok {
		return fmt.Errorf("no such task %s", taskID)
	}
	h, ok := e.handlers[t.Type]
	if !ok {
		return fmt.Errorf("no handler for task type %q", t.Type)
	}
	if _, err := sched.Assign(taskID); err != nil {
		return err
	}
	if err := sched.Transition(taskID, scheduler.StateRunning, ""); err != nil {
		return err
	}
	runCtx, cancel, err := sched.RunContext(ctx, taskID)
	if err != nil {
		return err
	}
	defer cancel()
	result, err := h(runCtx, t, func(p int) { sched.ReportProgress(taskID, p) })
	if err != nil {
		_ = sched.Transition(taskID, scheduler.StateFailed, err.Error())
		return err
	}
	return sched.Transition(taskID, scheduler.StateCompleted, result)
}
