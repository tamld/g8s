// Package orchestrator implements the Brain→Worker fan-out layer that sits
// on top of internal/dispatch (agy CLI wrapper) and internal/controlplane
// (SQLite WAL task store). It owns the Worker interface, the git worktree
// pool, and the FSM that drives each worker from spawn through receipt.
//
// AGY is the default worker. Other backends (codex, claude-cli, gemini-cli)
// plug in via the Worker interface — see registry.go.
package orchestrator

import (
	"context"
	"errors"
	"time"
)

// Worker spawns one bounded subprocess per task and returns a Handle the
// orchestrator can monitor, cancel, and harvest for a receipt.
type Worker interface {
	// Name identifies the backend ("agy", "codex", ...). Used in receipts.
	Name() string
	// Available returns nil if the backend binary is resolvable on the host.
	Available(ctx context.Context) error
	// Spawn starts one subprocess and returns a Handle. Must not block on
	// the subprocess — Wait does that.
	Spawn(ctx context.Context, t Task) (Handle, error)
}

// Handle is a live worker subprocess. Implementations must be safe to call
// Wait/Cancel concurrently from multiple goroutines.
type Handle interface {
	PID() int
	// Wait blocks until the subprocess exits or ctx fires. The returned
	// Receipt is the worker-emitted JSON envelope (or a synthesized one on
	// hard timeout).
	Wait(ctx context.Context) (Receipt, error)
	// Cancel asks the subprocess to stop. Implementations should escalate
	// to SIGKILL/TaskKill after a short grace period.
	Cancel(ctx context.Context) error
	// StdoutStream is an optional live NDJSON/byte stream. nil if unsupported.
	StdoutStream() interface {
		Read(p []byte) (int, error)
		Close() error
	}
}

// Task is the orchestrator-side projection of a controlplane task.
type Task struct {
	ID           string
	ParentID     string
	Worktree     Worktree
	Prompt       string
	Model        string
	Role         string
	Permission   string
	Timeout      time.Duration
	AllowedFiles []string
	ReceiptID    string
	Iter         int
}

// Worktree is the per-worker isolated git checkout.
type Worktree struct {
	ID      string
	Path    string
	Branch  string
	BaseSHA string
}

// Receipt is the worker-emitted result plus orchestrator-side metadata.
// Persisted into the controlplane evidence lake by the orchestrator after
// validation.
type Receipt struct {
	OK              bool
	WorkerName      string
	TaskID          string
	WorktreeID      string
	Branch          string
	CommitSHA       string
	ReturnCode      int
	HarnessCode     int
	DurationSeconds float64
	Stdout          string
	Stderr          string
	Violations      []string
	FilesModified   []string
	ScopeViolations []string
	StartedAt       time.Time
	FinishedAt      time.Time
}

// ErrWorkerUnavailable is returned by Worker.Spawn when Available was nil
// at registration time but the binary disappeared since.
var ErrWorkerUnavailable = errors.New("worker binary unavailable")

// ErrWorkerTimeout is returned by Handle.Wait when the orchestrator's ctx
// fires before the subprocess exits naturally.
var ErrWorkerTimeout = errors.New("worker wait timeout")

// ErrWorkerCancelled is returned by Handle.Wait when Cancel was called
// successfully.
var ErrWorkerCancelled = errors.New("worker cancelled by orchestrator")
