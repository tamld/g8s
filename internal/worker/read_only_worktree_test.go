package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newExternalCLIReadOnlyEnv(t *testing.T) (*workerEnv, *fakeWorktreeIsolator) {
	t.Helper()
	wt := initGitRepoForDiffFirewall(t)
	iso := &fakeWorktreeIsolator{dir: wt}
	env := newWorkerEnv(t, nil)
	env.sup = NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithPollInterval(2*time.Millisecond),
		WithWorktreeIsolator(iso),
		WithProviderCommandResolver(NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"codex", "cursor", "opencode", "commandcode", "qwen"},
		})),
	)
	return env, iso
}

func TestNeedsEphemeralReadOnlyWorktree(t *testing.T) {
	if !needsEphemeralReadOnlyWorktree(taskRequest{Permission: "read_only", Provider: "codex"}) {
		t.Fatal("expected codex read_only to need ephemeral worktree")
	}
	if needsEphemeralReadOnlyWorktree(taskRequest{Permission: "read_only", Provider: "agy"}) {
		t.Fatal("agy read_only must not need ephemeral worktree")
	}
	if needsEphemeralReadOnlyWorktree(taskRequest{Permission: "workspace_write", Provider: "codex"}) {
		t.Fatal("workspace_write uses same isolator but not this predicate")
	}
}

func TestExternalCLIReadOnlyCleanRunIsolatedAndReleased(t *testing.T) {
	env, iso := newExternalCLIReadOnlyEnv(t)
	done := make(chan struct{})
	close(done)
	env.runner.factory = func(opts SpawnOptions) Child {
		if opts.Dir != iso.dir {
			t.Fatalf("spawn dir = %q, want worktree %q", opts.Dir, iso.dir)
		}
		return &instantChild{done: done}
	}
	submitTask(t, env, "ro-cli-clean", 1, map[string]any{
		"provider":    "codex",
		"permission":  "read_only",
		"result_mode": "stdout",
	})
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro-clean", LeaseSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if iso.acquired != 1 {
		t.Fatalf("expected one worktree acquisition, got %d", iso.acquired)
	}
	if iso.released != 1 || iso.keepFlag {
		t.Fatalf("read_only worktree must release with keep=false, got released=%d keep=%v", iso.released, iso.keepFlag)
	}
}

func TestExternalCLIReadOnlyMutationFailsDiffFirewall(t *testing.T) {
	env, iso := newExternalCLIReadOnlyEnv(t)
	done := make(chan struct{})
	close(done)
	env.runner.factory = func(opts SpawnOptions) Child {
		if err := os.WriteFile(filepath.Join(opts.Dir, "rogue.txt"), []byte("nope\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return &instantChild{done: done}
	}
	task := submitTask(t, env, "ro-cli-dirty", 1, map[string]any{
		"provider":    "cursor",
		"permission":  "read_only",
		"result_mode": "stdout",
	})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro-dirty", LeaseSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if final == nil {
		t.Fatal("expected final task")
	}
	var res workerResult
	if err := json.Unmarshal(final.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected failure on worktree mutation, got %+v", res)
	}
	if res.Status != "contract_violation" || !strings.Contains(res.Reason, "E_DIFF_FIREWALL_READ_ONLY_MUTATION") {
		t.Fatalf("expected diff firewall violation, got %+v", res)
	}
	if iso.released != 1 || iso.keepFlag {
		t.Fatalf("dirty read_only worktree must still be released with keep=false, got released=%d keep=%v", iso.released, iso.keepFlag)
	}
	if _, err := os.Stat(filepath.Join(iso.dir, "rogue.txt")); err != nil {
		t.Fatalf("mutation should remain inside isolated worktree only: %v", err)
	}
	_ = task
}

// TestExternalCLIReadOnlyIsolationUnavailable (#987): a read_only
// platform_dispatch attempt claimed by a worker whose cwd is not a git
// repository (nil isolator — e.g. a launchd-spawned background worker with
// cwd=/) must REFUSE THE CLAIM before consuming the task, instead of the
// legacy consume-then-fail (FinishAttempt ok=false) that terminally killed
// freshly submitted tasks within seconds. The task stays QUEUED and
// claimable by an isolation-capable worker; RunOnce surfaces the typed
// E_WORKER_CWD_NOT_GIT_REPO error.
func TestExternalCLIReadOnlyIsolationUnavailable(t *testing.T) {
	prevDefer := claimRefusalDeferSeconds
	claimRefusalDeferSeconds = 3600
	t.Cleanup(func() { claimRefusalDeferSeconds = prevDefer })

	env := newWorkerEnv(t, nil)
	env.sup = NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithPollInterval(2*time.Millisecond),
		WithProviderCommandResolver(NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"codex"},
		})),
	)
	task := submitTask(t, env, "ro-cli-no-iso", 1, map[string]any{
		"provider":   "codex",
		"permission": "read_only",
	})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro-no-iso", LeaseSeconds: 60})
	if err == nil {
		t.Fatal("expected typed claim-refusal error from RunOnce, got nil")
	}
	if !errors.Is(err, ErrWorkerCwdNotGitRepo) {
		t.Fatalf("expected ErrWorkerCwdNotGitRepo, got %v", err)
	}
	if !strings.Contains(err.Error(), "E_WORKER_CWD_NOT_GIT_REPO") {
		t.Fatalf("refusal error must carry typed code E_WORKER_CWD_NOT_GIT_REPO, got %v", err)
	}
	if !strings.Contains(err.Error(), "is not a git repository") {
		t.Fatalf("refusal error must name the cwd defect, got %v", err)
	}
	if final != nil {
		t.Fatalf("refused claim must not return a consumed task, got %+v", final)
	}
	if len(env.runner.spawned) != 0 {
		t.Fatal("must not spawn child when read_only external CLI lacks isolator")
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}
	if got.State != "QUEUED" {
		t.Fatalf("task must stay QUEUED (claimable) after refusal, got %q", got.State)
	}
	if got.LeaseToken != nil {
		t.Fatalf("claim must be released: lease_token=%v", *got.LeaseToken)
	}
	if got.LeaseOwner != nil {
		t.Fatalf("claim must be released: lease_owner=%v", *got.LeaseOwner)
	}
	if got.Result != nil {
		t.Fatalf("refused task must not carry a consumed result, got %s", string(got.Result))
	}
	if got.Attempts != 0 {
		t.Fatalf("refusal must restore the attempt budget, got attempts=%d", got.Attempts)
	}

	var req map[string]any
	if err := json.Unmarshal(got.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	nb, ok := req["not_before"].(float64)
	if !ok {
		t.Fatalf("expected deferred not_before in request_json, got %v", req["not_before"])
	}
	if nb <= float64(time.Now().UnixNano())/1e9 {
		t.Fatalf("not_before %v must defer re-claim visibility into the future", nb)
	}

	foundRefusal := false
	events, evErr := env.store.Events(context.Background(), task.TaskID)
	if evErr != nil {
		t.Fatalf("events: %v", evErr)
	}
	for _, e := range events {
		if e.EventType != "task_claim_refused" {
			continue
		}
		foundRefusal = true
		var details map[string]any
		if json.Unmarshal(e.Details, &details) != nil {
			t.Fatalf("refusal event details must decode, got %s", string(e.Details))
		}
		if details["code"] != "E_WORKER_CWD_NOT_GIT_REPO" {
			t.Fatalf("refusal event must carry typed code, got %v", details)
		}
	}
	if !foundRefusal {
		t.Fatalf("expected task_claim_refused event, got %+v", events)
	}
}

// TestReadOnlyClaimRefusalRestoresClaimability (#987): after a refusal the
// SAME task is claimable and executable by an isolation-capable worker on the
// shared queue — the deterministic single-executor handoff. With the legacy
// consume-then-fail the refusal burned the single attempt and the second
// worker had nothing left to claim.
func TestReadOnlyClaimRefusalRestoresClaimability(t *testing.T) {
	prevDefer := claimRefusalDeferSeconds
	claimRefusalDeferSeconds = 0
	t.Cleanup(func() { claimRefusalDeferSeconds = prevDefer })

	env := newWorkerEnv(t, nil)
	// First supervisor has NO isolator (worker cwd not a git repository).
	env.sup = NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithPollInterval(2*time.Millisecond),
		WithProviderCommandResolver(NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"cursor"},
		})),
	)
	task := submitTask(t, env, "ro-refusal-reclaim", 1, map[string]any{
		"provider":    "cursor",
		"permission":  "read_only",
		"result_mode": "stdout",
	})
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-incapable", LeaseSeconds: 60}); !errors.Is(err, ErrWorkerCwdNotGitRepo) {
		t.Fatalf("expected typed refusal from incapable worker, got %v", err)
	}

	// Second supervisor on the SAME store runs inside a git checkout: claims
	// the still-queued task and executes it to completion.
	wt := initGitRepoForDiffFirewall(t)
	done := make(chan struct{})
	close(done)
	capable := NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithPollInterval(2*time.Millisecond),
		WithWorktreeIsolator(&fakeWorktreeIsolator{dir: wt}),
		WithProviderCommandResolver(NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"cursor"},
		})),
	)
	env.runner.factory = func(opts SpawnOptions) Child {
		if opts.Dir != wt {
			t.Fatalf("capable worker spawn dir = %q, want isolated worktree %q", opts.Dir, wt)
		}
		return &instantChild{done: done}
	}
	final, err := capable.RunOnce(context.Background(), RunOptions{WorkerID: "w-capable", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("capable worker RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("capable worker must claim the refused task")
	}
	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}
	if got.State != "WORKER_COMPLETED" {
		t.Fatalf("capable worker must consume the task to completion, got %q", got.State)
	}
	if got.Attempts != 1 {
		t.Fatalf("task must be consumed exactly once, got attempts=%d", got.Attempts)
	}
}

// --- redcell D-01 F1.1/F6.1: bounded claim/refuse churn per drain cycle ---

// rotatingRefusalDrainEnv builds an isolation-incapable drain (no worktree
// isolator) against a small rotating pool of ALWAYS-CLAIMABLE read_only
// tasks: claimRefusalDeferSeconds=0 neutralizes the per-task $.not_before
// deferral, which is exactly the hostile-rotation shape from redcell D-01
// F1.1 — fresh task IDs defeat the per-task re-claim deferral, so only a
// loop-level refusal park can bound the claim/refuse churn (F6.1).
func rotatingRefusalDrainEnv(t *testing.T) *workerEnv {
	t.Helper()
	prevDefer := claimRefusalDeferSeconds
	claimRefusalDeferSeconds = 0
	t.Cleanup(func() { claimRefusalDeferSeconds = prevDefer })

	env := newWorkerEnv(t, nil)
	env.sup = NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithPollInterval(2*time.Millisecond),
		WithProviderCommandResolver(NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"cursor"},
		})),
	)
	for i := 0; i < 4; i++ {
		submitTask(t, env, fmt.Sprintf("ro-rotate-%d", i), 1, map[string]any{
			"provider":    "cursor",
			"permission":  "read_only",
			"result_mode": "stdout",
		})
	}
	return env
}

// runRefusalChurnWindow drives the drain for a bounded window with a
// ctx-cancellable RunLoop and returns the number of claim refusals observed
// via OnError. With 4 rotating tasks and <=2 drain workers, the queue can
// never go empty (max 2 concurrent leases vs 4 claimable tasks), so the
// refusal count is a deterministic measure of loop-level backoff.
func runRefusalChurnWindow(t *testing.T, env *workerEnv, concurrency int) int {
	t.Helper()
	prevPark := claimRefusalParkWindow
	claimRefusalParkWindow = 400 * time.Millisecond
	t.Cleanup(func() { claimRefusalParkWindow = prevPark })

	var mu sync.Mutex
	refusals := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- env.sup.RunLoop(ctx, LoopOptions{
			WorkerID:     "w-rotate",
			LeaseSeconds: 60,
			Concurrency:  concurrency,
			OnError: func(err error) {
				if !errors.Is(err, ErrWorkerCwdNotGitRepo) {
					return
				}
				mu.Lock()
				refusals++
				mu.Unlock()
			},
		})
	}()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return refusals
}

// TestRunLoopSerialParksAfterConsecutiveClaimRefusals (redcell D-01 F1.1):
// an incapable serial drain against a rotating always-claimable task set
// must park after maxConsecutiveClaimRefusals consecutive refusals instead
// of hot-looping claim->refuse at DB speed. Pre-fix the drain churned
// unboundedly (the $.not_before deferral is per-task, so rotating task IDs
// never defer).
func TestRunLoopSerialParksAfterConsecutiveClaimRefusals(t *testing.T) {
	env := rotatingRefusalDrainEnv(t)
	refusals := runRefusalChurnWindow(t, env, 1)
	t.Logf("serial drain: %d claim refusals in the 1s window (park window %v, cap %d)", refusals, claimRefusalParkWindow, maxConsecutiveClaimRefusals)

	// Parked drain: bursts of maxConsecutiveClaimRefusals refusals separated
	// by claimRefusalParkWindow — well under 15 in the 1s window.
	if max := maxConsecutiveClaimRefusals * 5; refusals > max {
		t.Fatalf("redcell F1.1 unbounded refusal loop: %d claim/refuse cycles in the 1s window against a rotating always-claimable task set (drain must park after %d consecutive refusals, bound %d)", refusals, maxConsecutiveClaimRefusals, max)
	}
}

// TestRunLoopConcurrentParksAfterConsecutiveClaimRefusals (redcell D-01
// F1.1): the concurrent drain applies the same per-worker refusal cap —
// each drain worker parks independently after its own consecutive refusals.
// Pre-fix the claim-skip branch was unreachable (a refusal returns nil task,
// which the park condition swallowed): one refusal per worker parked the
// whole loop while tasks stayed claimable — exactly 2 refusals, then exit.
func TestRunLoopConcurrentParksAfterConsecutiveClaimRefusals(t *testing.T) {
	env := rotatingRefusalDrainEnv(t)
	refusals := runRefusalChurnWindow(t, env, 2)
	t.Logf("concurrent drain (2 workers): %d claim refusals in the 1s window (park window %v, cap %d)", refusals, claimRefusalParkWindow, maxConsecutiveClaimRefusals)

	// The drain must KEEP draining (pre-fix it exited after the first
	// refusal per worker: 2 refusals total)...
	if min := maxConsecutiveClaimRefusals * 2; refusals < min {
		t.Fatalf("redcell F1.1 concurrent drain liveness: only %d refusals in the 1s window against a rotating always-claimable task set — the drain parked/exited instead of continuing claim-skip cycles (want >= %d)", refusals, min)
	}
	// ...while staying bounded: 2 workers, each capped at bursts of
	// maxConsecutiveClaimRefusals refusals per park window.
	if max := maxConsecutiveClaimRefusals * 10; refusals > max {
		t.Fatalf("redcell F1.1 unbounded refusal loop (concurrent): %d claim/refuse cycles in the 1s window against a rotating always-claimable task set (per-worker park bound %d)", refusals, max)
	}
}
