package main

// #394 PR-B: `g8s worker --concurrency N` — bounded concurrent drain behind
// the worktree-isolation hard gate. The worker layer (PR-A/A2) owns the loop
// mechanics; this file owns CLI policy: gating, session provenance, output,
// and failure surfacing.
//
// Crash-recovery contract (#401 reaper): every concurrent attempt registers
// its OWN session row whose ID equals the pool branch name under the `sess/`
// prefix, with WorktreePath pointing at its private worktree. A SIGKILL mid-
// drain therefore leaves zombie rows that the existing orphan-session reaper
// reaps exactly like any other session: worktree removed, `sess/<row-id>`
// branch deleted, registry row deleted. The drain-level flock is intentionally
// NOT taken (unlike orchestrate's session gate): every attempt is already
// worktree-isolated and touches no shared checkout, so concurrent drains may
// legitimately run beside an in-place orchestrate session.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/worker"
)

// poolIsolation adapts the orchestrator worktree pool to the worker loop's
// LoopIsolation contract (#394): every concurrent attempt runs in its own
// checkout. Each Acquire registers a session row aligned with the reaper's
// `sess/<id>` sweep (see the crash-recovery contract above) and heartbeats
// it for the attempt's lifetime; Release marks the row dead before the
// worktree and branch go away.
type poolIsolation struct {
	pool  *orchestrator.Pool
	store *controlplane.Store

	mu    sync.Mutex
	rows  []string // live attempt row IDs, heartbeated by the drain loop
	warn  func(string)
	outMu *sync.Mutex // serializes stderr warnings (worker goroutines)
}

func (p *poolIsolation) Acquire(ctx context.Context, workerID string) (string, func(), error) {
	key := "worker-" + strings.TrimPrefix(newSessionID(), "sess-")
	if workerID != "" {
		key += "-" + workerID
	}
	wt, err := p.pool.Acquire(ctx, key)
	if err != nil {
		return "", nil, err
	}
	if rerr := p.store.RegisterSession(ctx, controlplane.Session{
		ID:           key,
		Mode:         controlplane.SessionModeWorktree,
		WorktreePath: wt.Path,
	}); rerr != nil {
		_ = p.pool.Release(context.Background(), wt, false)
		return "", nil, fmt.Errorf("register attempt session %s: %w", key, rerr)
	}
	p.mu.Lock()
	p.rows = append(p.rows, key)
	p.mu.Unlock()
	return wt.Path, func() {
		p.forget(key)
		if ferr := p.store.MarkSessionDead(context.Background(), key); ferr != nil {
			p.warn("worker: mark attempt session dead: " + ferr.Error())
		}
		if rerr := p.pool.Release(context.Background(), wt, false); rerr != nil {
			p.warn("worker: release worktree: " + rerr.Error())
		}
	}, nil
}

// HeartbeatAttempts refreshes every live attempt row so the #401 zombie
// sweep cannot reap a worktree whose attempt is still running (>10m tasks).
func (p *poolIsolation) HeartbeatAttempts(ctx context.Context) {
	p.mu.Lock()
	rows := append([]string(nil), p.rows...)
	p.mu.Unlock()
	for _, id := range rows {
		if err := p.store.HeartbeatSession(ctx, id); err != nil && !errors.Is(err, controlplane.ErrSessionNotActive) {
			p.warn("worker: attempt heartbeat: " + err.Error())
		}
	}
}

func (p *poolIsolation) forget(id string) {
	p.mu.Lock()
	for i, r := range p.rows {
		if r == id {
			p.rows = append(p.rows[:i], p.rows[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
}

// runWorkerConcurrentDrain drains the queue through up to N simultaneous
// attempts (#394 PR-B). Concurrency is hard-gated on worktree isolation:
// outside a git checkout it is a usage error, never a warning. RunOnce
// errors surface through OnError and exit 1 — never a silent "drained" —
// matching the serial path's contract. os.Exit paths finish sessions
// explicitly because deferred calls do not run on exit.
func runWorkerConcurrentDrain(ctx context.Context, sup *worker.Supervisor, store *controlplane.Store, actor string, lease, concurrency int, jsonMode, jsonl bool, traceID string) {
	cwd, err := os.Getwd()
	if err != nil {
		exitRuntime("worker", "", traceID, cli.CodeIO, err, "", jsonl)
	}
	pool, perr := orchestrator.NewPool(orchestrator.PoolOptions{Repo: cwd, Prefix: "sess"})
	if perr != nil {
		if strings.Contains(perr.Error(), "is not a git working tree") {
			exitUsage("worker", "concurrency", traceID,
				"concurrency>1 requires worktree isolation",
				fmt.Sprintf("run inside a git checkout so each concurrent attempt gets its own worktree (%v)", perr), jsonl)
		}
		// Environmental failure (git missing, corrupt repo, unwritable
		// root): the gate wording would mislead — report as a runtime error.
		exitRuntime("worker", "concurrency", traceID, cli.CodeRuntime, perr, "", jsonl)
	}
	sessionID := newSessionID()
	if rerr := store.RegisterSession(ctx, controlplane.Session{
		ID:   sessionID,
		Mode: controlplane.SessionModeWorktree,
	}); rerr != nil {
		exitRuntime("worker", "session-gate", traceID, cli.CodeRuntime, rerr, "", jsonl)
	}
	gate := &sessionGate{ID: sessionID, Mode: controlplane.SessionModeWorktree, store: store}
	finishGate := func() {
		if ferr := gate.Finish(context.Background(), false); ferr != nil {
			fmt.Fprintln(os.Stderr, "worker: session gate finish:", ferr)
		}
	}
	defer finishGate()

	var outMu sync.Mutex
	iso := &poolIsolation{pool: pool, store: store, outMu: &outMu}
	iso.warn = func(msg string) {
		outMu.Lock()
		defer outMu.Unlock()
		fmt.Fprintln(os.Stderr, msg)
	}

	hbCtx, hbStop := context.WithCancel(context.Background())
	defer hbStop()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := gate.Heartbeat(hbCtx); err != nil {
					return // row gone: reaper owns cleanup; stop heartbeating
				}
				iso.HeartbeatAttempts(hbCtx)
			}
		}
	}()

	printTask := func(t *controlplane.Task) {
		outMu.Lock()
		defer outMu.Unlock()
		if jsonMode || jsonl {
			env := cli.NewEnvelope("worker_task", "worker", "", map[string]any{"task_id": t.TaskID, "state": t.State})
			env.TraceID = traceID
			_ = cli.WriteResponse(os.Stdout, env, jsonl)
		} else {
			fmt.Printf("%s %s\n", t.TaskID, t.State)
		}
	}
	printDrained := func() {
		outMu.Lock()
		defer outMu.Unlock()
		if jsonMode || jsonl {
			env := cli.NewEnvelope("worker_status", "worker", "", map[string]any{"status": "drained"})
			env.TraceID = traceID
			_ = cli.WriteResponse(os.Stdout, env, jsonl)
		} else {
			fmt.Println("queue drained")
		}
	}

	var sawError error
	code := sup.RunLoop(ctx, worker.LoopOptions{
		WorkerID:     actor,
		LeaseSeconds: lease,
		Concurrency:  concurrency,
		Isolation:    iso,
		OnTask:       printTask,
		OnError: func(err error) {
			outMu.Lock()
			if sawError == nil {
				sawError = err
			}
			outMu.Unlock()
		},
	})
	// Defers do not run on os.Exit — finish the gate explicitly on every
	// non-zero path (worktrees and attempt rows are already released/finished
	// by the worker goroutines' defers inside RunLoop).
	if code != 0 {
		finishGate()
		hbStop()
		os.Exit(code) // 143 on cancellation
	}
	if sawError != nil {
		finishGate()
		hbStop()
		exitRuntime("worker", "", traceID, cli.CodeRuntime, sawError, "", jsonl)
	}
	printDrained()
}
