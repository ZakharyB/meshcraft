package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/ZakharyB/meshcraft/internal/resources"
)

func peer(id string, ram int64, canHost bool) resources.Snapshot {
	return resources.Snapshot{PeerID: id, NumCPU: 4, FreeRAMMB: ram, CanHost: canHost}
}

func TestSubmitValidation(t *testing.T) {
	s := New()
	if _, err := s.Submit(Task{Type: "run_anything", WorldID: "w1"}); err == nil {
		t.Fatal("arbitrary task type accepted — must never happen")
	}
	if _, err := s.Submit(Task{Type: TaskBackupVerify, WorldID: "w1", MaxMemMB: HardMaxMemMB + 1}); err == nil {
		t.Fatal("over-cap budget accepted")
	}
}

func TestAssignValidatesPolicySuggestions(t *testing.T) {
	s := New()
	s.RegisterPeer(peer("small", 512, true))
	s.RegisterPeer(peer("big", 8192, true))
	// Malicious/confused policy: suggests a peer that doesn't exist,
	// then one too small for the budget. Scheduler must skip both.
	s.SetPolicy(func(t Task, c []resources.Snapshot) []string { return []string{"ghost", "small", "big"} })
	id, err := s.Submit(Task{Type: TaskBackupVerify, WorldID: "w1", MaxMemMB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Assign(id)
	if err != nil || got != "big" {
		t.Fatalf("assign = %q, %v; want big", got, err)
	}
}

func TestRetryThenFailAndCancel(t *testing.T) {
	s := New()
	s.RegisterPeer(peer("p1", 4096, true))
	id, _ := s.Submit(Task{Type: TaskHealthProbe, WorldID: "w1", MaxRetries: 1})
	if _, err := s.Assign(id); err != nil {
		t.Fatal(err)
	}
	_ = s.Transition(id, StateRunning, "")
	if err := s.Transition(id, StateFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	if task, _ := s.Get(id); task.State != StateQueued {
		t.Fatalf("after 1st failure with 1 retry left: state=%s", task.State)
	}
	if _, err := s.Assign(id); err != nil {
		t.Fatal(err)
	}
	_ = s.Transition(id, StateRunning, "")
	_ = s.Transition(id, StateFailed, "boom again")
	if task, _ := s.Get(id); task.State != StateFailed {
		t.Fatalf("after retries exhausted: state=%s", task.State)
	}

	id2, _ := s.Submit(Task{Type: TaskHealthProbe, WorldID: "w1"})
	if err := s.Cancel(id2); err != nil {
		t.Fatal(err)
	}
	if task, _ := s.Get(id2); task.State != StateCancelled {
		t.Fatalf("cancel: state=%s", task.State)
	}
}

func TestRunContextTimeout(t *testing.T) {
	s := New()
	id, _ := s.Submit(Task{Type: TaskHealthProbe, WorldID: "w1", Timeout: 50 * time.Millisecond})
	ctx, cancel, err := s.RunContext(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("task context did not time out")
	}
}
