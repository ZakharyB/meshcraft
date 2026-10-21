// Package minecraft defines the edition-agnostic runtime adapter
// interface (spec §6.2) and the Paper implementation for Java Edition.
package minecraft

import (
	"context"
	"errors"
	"time"
)

// Status is the adapter's view of a server process.
type Status struct {
	WorldID   string    `json:"world_id"`
	Running   bool      `json:"running"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Ready     bool      `json:"ready"`
	Endpoint  string    `json:"endpoint"` // host:port players connect to
	ExitCode  *int      `json:"exit_code,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

// RuntimeAdapter is implemented per edition (Paper now, Bedrock later).
type RuntimeAdapter interface {
	// InstallCheck verifies the runtime exists and is usable.
	InstallCheck(ctx context.Context) error
	// Configure writes edition-specific config (server.properties) into the world dir.
	Configure() error
	// Start launches the server process and returns once spawned (not necessarily ready).
	Start(ctx context.Context) error
	// WaitReady blocks until the server reports readiness or timeout.
	WaitReady(ctx context.Context, timeout time.Duration) error
	// Stop gracefully shuts the server down, escalating to kill after grace.
	Stop(ctx context.Context, grace time.Duration) error
	// Status returns the current process state.
	Status() Status
	// Console sends one console command to the running server.
	Console(cmd string) error
	// Logs returns the most recent n log lines captured from the process.
	Logs(n int) []string
	// Edition reports "java" / "bedrock".
	Edition() string
}

// ErrServerRunning is returned when an operation needs a stopped server.
var ErrServerRunning = errors.New("server is running; stop it first (or use the coordinated save-off backup path)")

// ErrNotInstalled is returned when the Java runtime or server jar is missing.
var ErrNotInstalled = errors.New("runtime not installed")
