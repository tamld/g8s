package main

// #394 PR-B: `g8s worker --concurrency N` — bounded concurrent drain behind
// the worktree-isolation hard gate. The worker layer (PR-A) owns the loop
// mechanics; this file owns CLI policy: gating, session provenance, output.

import (
	"context"
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
// checkout; release removes the worktree and its branch.
type poolIsolation struct {
	pool *orchestrator.Pool
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
	return wt.Path, func() {
		if rerr := p.pool.Release(context.Background(), wt, false); rerr != nil {
			fmt.Fprintln(os.Stderr, "worker: release worktree:", rerr)
		}
	}, nil
}

// runWorkerConcurrentDrain drains the queue through up to N simultaneous
// attempts (#394 PR-B). Concurrency is hard-gated on worktree isolation:
// outside a git checkout it is a usage error, never a warning. The session
// registers in the sessions registry (#393) so heartbeat, provenance, and
// cleanup reap see it; os.Exit paths finish the gate explicitly because
// deferred calls do not run on exit.
func runWorkerConcurrentDrain(ctx context.Context, sup *worker.Supervisor, store *controlplane.Store, actor string, lease, concurrency int, jsonMode, jsonl bool, traceID string) {
	cwd, err := os.Getwd()
	if err != nil {
		exitRuntime("worker", "", traceID, cli.CodeIO, err, "", jsonl)
	}
	pool, perr := orchestrator.NewPool(orchestrator.PoolOptions{Repo: cwd, Prefix: "worker"})
	if perr != nil {
		exitUsage("worker", "concurrency", traceID,
			"concurrency>1 requires worktree isolation",
			fmt.Sprintf("run inside a git checkout so each concurrent attempt gets its own worktree (%v)", perr), jsonl)
	}
	sessionID := newSessionID()
	if rerr := store.RegisterSession(ctx, controlplane.Session{
		ID:   sessionID,
		Mode: controlplane.SessionModeWorktree,
	}); rerr != nil {
		exitRuntime("worker", "session-gate", traceID, cli.CodeRuntime, rerr, "", jsonl)
	}
	gate := &sessionGate{ID: sessionID, Mode: controlplane.SessionModeWorktree, store: store}
	defer func() {
		if ferr := gate.Finish(context.Background(), false); ferr != nil {
			fmt.Fprintln(os.Stderr, "worker: session gate finish:", ferr)
		}
	}()
	hbCtx, hbStop := context.WithCancel(context.Background())
	defer hbStop()
	go sessionHeartbeatLoop(hbCtx, gate, 5*time.Second)

	var outMu sync.Mutex
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
	code := sup.RunLoop(ctx, worker.LoopOptions{
		WorkerID:     actor,
		LeaseSeconds: lease,
		Concurrency:  concurrency,
		Isolation:    &poolIsolation{pool: pool},
		OnTask:       printTask,
	})
	if code != 0 {
		if ferr := gate.Finish(context.Background(), false); ferr != nil {
			fmt.Fprintln(os.Stderr, "worker: session gate finish:", ferr)
		}
		hbStop()
		os.Exit(code) // 143 on cancellation, 1 on mid-loop isolation failure
	}
	printDrained()
}
