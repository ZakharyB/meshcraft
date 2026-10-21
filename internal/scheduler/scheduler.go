// Package scheduler implements the Go task scheduler (spec §5.2).
//
// Lua policies may RANK candidate peers, but every decision is
// validated here: permissions, resource budgets, quotas and task-type
// allowlists are enforced in Go and cannot be bypassed by a policy.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ZakharyB/meshcraft/internal/resources"
)

// TaskType identifies a kind of background work. Only registered types
// can run — there is deliberately no "run arbitrary command" type.
type TaskType string

const (
	TaskSnapshotReplicate TaskType = "snapshot_replicate"
	TaskBackupVerify      TaskType = "backup_verify"
	TaskFileIntegrity     TaskType = "file_integrity"
	TaskHealthProbe       TaskType = "health_probe"
)

// State is a task's lifecycle state.
type State string

const (
	StateQueued    State = "queued"
	StateAssigned  State = "assigned"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Task definition and auditable runtime record.
type Task struct {
	ID          string            `json:"id"`
	Type        TaskType          `json:"type"`
	WorldID     string            `json:"world_id"`
	SubmittedBy string            `json:"submitted_by"` // peer id or extension id
	Payload     map[string]string `json:"payload"`
	// Budgets (hard caps enforced by Go, never by the policy)
	MaxCPUPct  int           `json:"max_cpu_pct"`
	MaxMemMB   int64         `json:"max_mem_mb"`
	Timeout    time.Duration `json:"timeout_ns"`
	MaxRetries int           `json:"max_retries"`

	State      State     `json:"state"`
	AssignedTo string    `json:"assigned_to,omitempty"`
	Attempts   int       `json:"attempts"`
	Progress   int       `json:"progress"` // 0-100
	Result     string    `json:"result,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Policy ranks candidate peers for a task. Return peer IDs best-first.
// Policies (Go built-in or Lua) only advise; the scheduler validates.
type Policy func(t Task, candidates []resources.Snapshot) []string

// BuiltinPolicy prefers the most capable available peer.
func BuiltinPolicy(t Task, candidates []resources.Snapshot) []string {
	best := ""
	var bestScore int64 = -2
	for _, c := range candidates {
		if sc := resources.Score(c); sc > bestScore {
			bestScore, best = sc, c.PeerID
		}
	}
	if best == "" {
		return nil
	}
	return []string{best}
}

// Scheduler tracks peers and tasks. Safe for concurrent use.
type Scheduler struct {
	mu      sync.Mutex
	tasks   map[string]*Task
	peers   map[string]resources.Snapshot
	policy  Policy
	allowed map[TaskType]bool
	audit   []string
	seq     int
	now     func() time.Time
}

// New creates a scheduler with the built-in policy.
func New() *Scheduler {
	return &Scheduler{
		tasks: map[string]*Task{}, peers: map[string]resources.Snapshot{},
		policy: BuiltinPolicy,
		allowed: map[TaskType]bool{
			TaskSnapshotReplicate: true, TaskBackupVerify: true,
			TaskFileIntegrity: true, TaskHealthProbe: true,
		},
		now: time.Now,
	}
}

// SetPolicy installs an advisory policy (e.g. from Lua). Validation
// stays in Go regardless of what the policy returns.
func (s *Scheduler) SetPolicy(p Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != nil {
		s.policy = p
	}
}

// RegisterPeer updates the scheduler's view of a peer's capacity.
func (s *Scheduler) RegisterPeer(snap resources.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[snap.PeerID] = snap
}

// RemovePeer drops a peer (disconnect).
func (s *Scheduler) RemovePeer(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, peerID)
}

var (
	ErrUnknownType  = errors.New("unknown or disallowed task type")
	ErrBudgetTooBig = errors.New("task budget exceeds hard caps")
)

// Hard caps no submitter (human or Lua) may exceed.
const (
	HardMaxMemMB = 4096
	HardMaxCPU   = 100
)

// Submit validates and queues a task.
func (s *Scheduler) Submit(t Task) (string, error) {
	if !s.allowed[t.Type] {
		return "", fmt.Errorf("%w: %q", ErrUnknownType, t.Type)
	}
	if t.MaxMemMB > HardMaxMemMB || t.MaxCPUPct > HardMaxCPU || t.MaxCPUPct < 0 || t.MaxMemMB < 0 {
		return "", ErrBudgetTooBig
	}
	if t.Timeout <= 0 {
		t.Timeout = 5 * time.Minute
	}
	if t.Timeout > time.Hour {
		return "", fmt.Errorf("%w: timeout over 1h", ErrBudgetTooBig)
	}
	if t.MaxRetries < 0 || t.MaxRetries > 5 {
		return "", fmt.Errorf("%w: retries must be 0..5", ErrBudgetTooBig)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	t.ID = fmt.Sprintf("task-%d", s.seq)
	t.State = StateQueued
	t.CreatedAt, t.UpdatedAt = s.now(), s.now()
	cp := t
	s.tasks[t.ID] = &cp
	s.audit = append(s.audit, fmt.Sprintf("%s submit %s type=%s by=%s", t.CreatedAt.Format(time.RFC3339), t.ID, t.Type, t.SubmittedBy))
	return t.ID, nil
}

// Assign picks a peer for a queued task using the policy, validating
// that the chosen peer exists, can host work, and fits the budget.
func (s *Scheduler) Assign(taskID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[taskID]
	if !ok {
		return "", fmt.Errorf("no such task %s", taskID)
	}
	if t.State != StateQueued {
		return "", fmt.Errorf("task %s is %s, not queued", taskID, t.State)
	}
	cands := make([]resources.Snapshot, 0, len(s.peers))
	for _, p := range s.peers {
		cands = append(cands, p)
	}
	for _, peerID := range s.policy(*t, cands) {
		snap, ok := s.peers[peerID]
		if !ok {
			continue // policy suggested an unknown peer; ignore, never trust
		}
		if t.MaxMemMB > 0 && snap.FreeRAMMB >= 0 && snap.FreeRAMMB < t.MaxMemMB {
			continue // does not fit the budget
		}
		t.AssignedTo = peerID
		t.State = StateAssigned
		t.UpdatedAt = s.now()
		s.audit = append(s.audit, fmt.Sprintf("%s assign %s -> %s", t.UpdatedAt.Format(time.RFC3339), t.ID, peerID))
		return peerID, nil
	}
	return "", errors.New("no suitable peer available")
}

// Transition moves a task through its lifecycle with retry handling.
func (s *Scheduler) Transition(taskID string, to State, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("no such task %s", taskID)
	}
	switch to {
	case StateRunning:
		if t.State != StateAssigned {
			return fmt.Errorf("task %s cannot run from %s", taskID, t.State)
		}
		t.Attempts++
	case StateFailed:
		t.LastError = detail
		if t.Attempts <= t.MaxRetries {
			t.State = StateQueued // retry
			t.AssignedTo = ""
			t.UpdatedAt = s.now()
			return nil
		}
	case StateCompleted:
		t.Result = detail
		t.Progress = 100
	case StateCancelled:
		// terminal from any non-terminal state
	default:
		return fmt.Errorf("unsupported transition to %s", to)
	}
	t.State = to
	t.UpdatedAt = s.now()
	s.audit = append(s.audit, fmt.Sprintf("%s %s -> %s %s", t.UpdatedAt.Format(time.RFC3339), taskID, to, detail))
	return nil
}

// ReportProgress records worker progress (0-100, clamped).
func (s *Scheduler) ReportProgress(taskID string, pct int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[taskID]; ok {
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		t.Progress = pct
		t.UpdatedAt = s.now()
	}
}

// Cancel marks a task cancelled (workers observe via context checks).
func (s *Scheduler) Cancel(taskID string) error {
	return s.Transition(taskID, StateCancelled, "cancelled by request")
}

// Get returns a copy of a task.
func (s *Scheduler) Get(taskID string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[taskID]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// List returns copies of all tasks.
func (s *Scheduler) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	return out
}

// Audit returns the scheduler's audit trail.
func (s *Scheduler) Audit() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.audit...)
}

// RunContext derives a timeout context for executing a task.
func (s *Scheduler) RunContext(ctx context.Context, taskID string) (context.Context, context.CancelFunc, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, nil, fmt.Errorf("no such task %s", taskID)
	}
	c, cancel := context.WithTimeout(ctx, t.Timeout)
	return c, cancel, nil
}
