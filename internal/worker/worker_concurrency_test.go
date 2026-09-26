package worker

// #394 PR-A: bounded concurrent drain loop + telemetry lifecycle races.
// RED-first: these tests fail to compile / fail behaviorally before the
// LoopOptions.Concurrency implementation lands.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// --- #394: concurrent drain ---

func TestRunLoopConcurrentDrainsAtN4(t *testing.T) {
	env := newWorkerEnv(t, nil)
	const total = 8
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		task := submitTask(t, env, fmt.Sprintf("conc-%d", i), 1, nil)
		ids = append(ids, task.TaskID)
	}
	code := env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w-conc", LeaseSeconds: 60, Concurrency: 4})
	if code != 0 {
		t.Fatalf("expected drain exit 0, got %d", code)
	}
	env.runner.mu.Lock()
	spawned := len(env.runner.spawned)
	env.runner.mu.Unlock()
	if spawned != total {
		t.Fatalf("expected %d spawned attempts, got %d", total, spawned)
	}
	ctx := context.Background()
	for _, id := range ids {
		task, err := env.store.GetTask(ctx, id)
		if err != nil || task == nil {
			t.Fatalf("get task %s: %v", id, err)
		}
		if task.State != controlplane.StateWorkerCompleted {
			t.Fatalf("task %s = %s, want WORKER_COMPLETED", id, task.State)
		}
	}
}

func TestRunLoopConcurrentBoundedInFlight(t *testing.T) {
	env := newWorkerEnv(t, nil)
	const total, n = 6, 2
	for i := 0; i < total; i++ {
		submitTask(t, env, fmt.Sprintf("bounded-%d", i), 1, nil)
	}
	type gatedSpawn struct {
		child *fakeChild
		opts  SpawnOptions
	}
	var (
		mu       sync.Mutex
		pending  []gatedSpawn
		snapshot []int
	)
	env.runner.factory = func(opts SpawnOptions) Child {
		c := newFakeChild(0) // hangs until released below
		mu.Lock()
		pending = append(pending, gatedSpawn{child: c, opts: opts})
		mu.Unlock()
		return c
	}
	// Release hanging children in waves; each wave waits for a stall so the
	// snapshot records the true simultaneous-in-flight high-water mark.
	var releaser sync.WaitGroup
	releaser.Add(1)
	go func() {
		defer releaser.Done()
		for done := 0; done < total; {
			for {
				mu.Lock()
				inflight := len(pending) - done
				spawned := len(pending)
				mu.Unlock()
				if inflight >= n || spawned == total {
					break
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(20 * time.Millisecond) // stall window
			mu.Lock()
			snapshot = append(snapshot, len(pending)-done)
			batch := append([]gatedSpawn(nil), pending[done:]...)
			mu.Unlock()
			for _, g := range batch {
				_ = os.WriteFile(g.opts.ResultPath, []byte(`{"ok":true,"status":"succeeded"}`), 0o600)
				g.child.closeOnce.Do(func() { close(g.child.done) })
			}
			done += len(batch)
		}
	}()
	code := env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w-bounded", LeaseSeconds: 60, Concurrency: n})
	releaser.Wait()
	if code != 0 {
		t.Fatalf("expected drain exit 0, got %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pending) != total {
		t.Fatalf("expected %d spawns, got %d", total, len(pending))
	}
	for i, s := range snapshot {
		if s > n {
			t.Fatalf("snapshot %d observed %d simultaneous spawns, bound is %d", i, s, n)
		}
	}
}

func TestRunLoopConcurrentCancelReturns143(t *testing.T) {
	env := newWorkerEnv(t, nil)
	for i := 0; i < 4; i++ {
		submitTask(t, env, fmt.Sprintf("cancel-%d", i), 1, nil)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelOnce sync.Once
	env.runner.factory = func(opts SpawnOptions) Child {
		cancelOnce.Do(cancel)
		return newHangChild()
	}
	code := env.sup.RunLoop(ctx, LoopOptions{WorkerID: "w-cancel", LeaseSeconds: 60, Concurrency: 4})
	if code != 143 {
		t.Fatalf("expected cancel exit 143, got %d", code)
	}
}

func TestRunLoopOnceModeIgnoresConcurrency(t *testing.T) {
	env := newWorkerEnv(t, nil)
	code := env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w-once", LeaseSeconds: 60, Once: true, Concurrency: 4})
	if code != 0 {
		t.Fatalf("once-mode with empty queue must exit 0, got %d", code)
	}
	submitTask(t, env, "once-conc", 1, map[string]any{"timeout": "30s"})
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(3)
		child.finishLater(opts.ResultPath, `{"ok":false,"status":"failed"}`, 5*time.Millisecond)
		return child
	}
	code = env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w-once", LeaseSeconds: 60, Once: true, Concurrency: 4})
	if code != 1 {
		t.Fatalf("once-mode failed attempt must exit 1, got %d", code)
	}
}

// --- #394: per-worker isolation hook ---

type fakeIsolation struct {
	mu       sync.Mutex
	seq      int
	failAt   int // 1-based acquire call that must fail; 0 = never
	acquired []string
	released []string
}

func (f *fakeIsolation) Acquire(ctx context.Context, workerID string) (string, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	if f.failAt > 0 && f.seq == f.failAt {
		return "", nil, errors.New("pool exhausted")
	}
	dir := fmt.Sprintf("/wt/worker-%d", f.seq)
	f.acquired = append(f.acquired, dir)
	return dir, func() {
		f.mu.Lock()
		f.released = append(f.released, dir)
		f.mu.Unlock()
	}, nil
}

func TestRunLoopIsolationPerWorkerAcquireRelease(t *testing.T) {
	env := newWorkerEnv(t, nil)
	const total, n = 4, 2
	for i := 0; i < total; i++ {
		submitTask(t, env, fmt.Sprintf("iso-%d", i), 1, nil)
	}
	iso := &fakeIsolation{}
	code := env.sup.RunLoop(context.Background(), LoopOptions{
		WorkerID: "w-iso", LeaseSeconds: 60, Concurrency: n, Isolation: iso,
	})
	if code != 0 {
		t.Fatalf("expected drain exit 0, got %d", code)
	}
	iso.mu.Lock()
	defer iso.mu.Unlock()
	if len(iso.acquired) != n {
		t.Fatalf("expected %d per-worker acquisitions, got %d (%v)", n, len(iso.acquired), iso.acquired)
	}
	if len(iso.released) != n {
		t.Fatalf("expected %d releases after drain, got %d", n, len(iso.released))
	}
	acquired := map[string]bool{}
	for _, d := range iso.acquired {
		acquired[d] = true
	}
	env.runner.mu.Lock()
	defer env.runner.mu.Unlock()
	for _, sp := range env.runner.spawned {
		if !acquired[sp.Dir] {
			t.Fatalf("child ran in un-isolated dir %q (want one of %v)", sp.Dir, iso.acquired)
		}
	}
}

func TestRunLoopIsolationAcquireFailureFailsLoop(t *testing.T) {
	env := newWorkerEnv(t, nil)
	for i := 0; i < 2; i++ {
		submitTask(t, env, fmt.Sprintf("isofail-%d", i), 1, nil)
	}
	iso := &fakeIsolation{failAt: 1}
	code := env.sup.RunLoop(context.Background(), LoopOptions{
		WorkerID: "w-isofail", LeaseSeconds: 60, Concurrency: 2, Isolation: iso,
	})
	if code != 1 {
		t.Fatalf("isolation acquire failure must exit 1, got %d", code)
	}
	iso.mu.Lock()
	defer iso.mu.Unlock()
	for _, d := range iso.released {
		found := false
		for _, a := range iso.acquired {
			if a == d {
				found = true
			}
		}
		if !found {
			t.Fatalf("released dir %q was never acquired", d)
		}
	}
}

// --- #394: telemetry lifecycle (refcounted close, resettable lazy init) ---

func countTelemetryTasks(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open telemetry ledger: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT task_id) FROM telemetry_events`).Scan(&n); err != nil {
		t.Fatalf("query telemetry ledger: %v", err)
	}
	return n
}

// #253 regression: per-run close must not silence later runs in the same
// process. Before the fix, run 2+ events were dropped because the lazy-init
// Once had already fired when closeTelemetry nulled the engine.
func TestTelemetrySurvivesRepeatedSerialRuns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tel-serial.db")
	t.Setenv("G8S_TELEMETRY", "1")
	t.Setenv("G8S_TELEMETRY_DB", dbPath)
	env := newWorkerEnv(t, nil)
	submitTask(t, env, "tel-serial-1", 1, nil)
	submitTask(t, env, "tel-serial-2", 1, nil)
	code := env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w-tel", LeaseSeconds: 60, Concurrency: 1})
	if code != 0 {
		t.Fatalf("expected drain exit 0, got %d", code)
	}
	if got := countTelemetryTasks(t, dbPath); got != 2 {
		t.Fatalf("expected telemetry from 2 distinct tasks, got %d", got)
	}
}

func TestTelemetryConcurrentRunsAllIngest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tel-conc.db")
	t.Setenv("G8S_TELEMETRY", "1")
	t.Setenv("G8S_TELEMETRY_DB", dbPath)
	env := newWorkerEnv(t, nil)
	for i := 0; i < 4; i++ {
		submitTask(t, env, fmt.Sprintf("tel-conc-%d", i), 1, nil)
	}
	code := env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w-telc", LeaseSeconds: 60, Concurrency: 4})
	if code != 0 {
		t.Fatalf("expected drain exit 0, got %d", code)
	}
	if got := countTelemetryTasks(t, dbPath); got != 4 {
		t.Fatalf("expected telemetry from 4 distinct tasks, got %d", got)
	}
}
