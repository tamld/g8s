package orchestrator

// Regression tests for #986 part-2 batch 1 (blind audit registry-a.json):
//
//   A-7 (medium): Pool.Acquire held p.mu across git subprocesses that ran
//        without a context or timeout — a wedged `git worktree add` blocked
//        every other Acquire caller. The git subprocesses now run OUTSIDE
//        the pool mutex, bounded by gitCommandTimeout, and concurrent
//        Acquire calls for the same taskID are deduplicated single-flight.
//
// Red evidence (against unpatched main, PATH-shim probes, since deleted):
//   - second Acquire blocked 3s+ behind a wedged `git worktree add`
//   - gitAddWorktree blocked 3s+ with no timeout bound
//   - 4 concurrent same-taskID Acquires ran the failing git helper 4 times

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAcquireHungGitDoesNotBlockOtherAcquires: while task-a's
// git worktree add is wedged, a second Acquire for a different task must
// still complete — the pool mutex must not be held across git subprocesses.
func TestAcquireHungGitDoesNotBlockOtherAcquires(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	origAdd := gitAddWorktreeFn
	t.Cleanup(func() { gitAddWorktreeFn = origAdd })

	var releaseOnce sync.Once
	release := make(chan struct{})
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)

	var aEntered atomic.Bool
	gitAddWorktreeFn = func(ctx context.Context, repo, path, branch, base string) error {
		if strings.HasSuffix(branch, "/task-a") {
			aEntered.Store(true)
			<-release // wedged git: returns only when the test releases it
			return errors.New("injected hung git released")
		}
		return gitAddWorktree(ctx, repo, path, branch, base)
	}

	aDone := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(context.Background(), "task-a")
		aDone <- err
	}()

	// Deterministically wait until task-a is wedged inside its git subprocess.
	deadline := time.Now().Add(5 * time.Second)
	for !aEntered.Load() {
		if time.Now().After(deadline) {
			t.Fatal("task-a never entered its git worktree add")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A second caller for a different task must not block behind the wedged git.
	bDone := make(chan struct{})
	var bWT Worktree
	var bErr error
	go func() {
		defer close(bDone)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		bWT, bErr = pool.Acquire(ctx, "task-b")
	}()
	select {
	case <-bDone:
	case <-time.After(3 * time.Second):
		t.Fatal("second Acquire blocked behind a wedged git worktree add: Pool.mu is held across git subprocesses (A-7)")
	}
	if bErr != nil {
		t.Fatalf("second Acquire failed: %v", bErr)
	}
	if bWT.Path == "" {
		t.Fatal("second Acquire returned an empty worktree path")
	}

	// Un-wedge task-a; it must surface the injected failure.
	closeRelease()
	select {
	case err := <-aDone:
		if err == nil {
			t.Fatal("task-a Acquire unexpectedly succeeded after injected git hang")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("task-a Acquire never returned after release")
	}
}

// TestGitAddWorktreeBoundedByTimeout: gitAddWorktree must fail bounded via
// gitCommandTimeout when the git subprocess hangs, not block forever.
func TestGitAddWorktreeBoundedByTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH shim fixture")
	}
	repo := setupGitRepo(t)

	// Shim `git worktree add` behind a hanging script; everything else
	// passes through to the real git binary.
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git not on PATH: %v", err)
	}
	shimDir := t.TempDir()
	// exec replaces the shim so the deadline kill lands on the hung process
	// itself (a plain `sleep 30` child would inherit the stdout pipe and
	// stall CombinedOutput past the kill).
	shim := "#!/bin/sh\nif [ \"$1\" = \"worktree\" ] && [ \"$2\" = \"add\" ]; then\n  exec sleep 30\nfi\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	origTimeout := gitCommandTimeout
	t.Cleanup(func() { gitCommandTimeout = origTimeout })
	gitCommandTimeout = 300 * time.Millisecond

	errCh := make(chan error, 1)
	go func() {
		errCh <- gitAddWorktree(context.Background(), repo, filepath.Join(t.TempDir(), "wt-hang"), "agy/hang-probe", "HEAD")
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("gitAddWorktree err = %v, want context.DeadlineExceeded (bounded by gitCommandTimeout)", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gitAddWorktree did not return within 3s: git subprocess is not bounded by gitCommandTimeout (A-7)")
	}
}

// TestAcquireConcurrentSameTaskIDSingleFlight: concurrent Acquire calls for
// the SAME taskID must all return the same worktree with exactly one git
// subprocess invocation — the single-flight dedup that replaced the
// mutex-held-across-git serialization (A-7).
func TestAcquireConcurrentSameTaskIDSingleFlight(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	var addCalls atomic.Int32
	origAdd := gitAddWorktreeFn
	t.Cleanup(func() { gitAddWorktreeFn = origAdd })
	gitAddWorktreeFn = func(ctx context.Context, repo, path, branch, base string) error {
		addCalls.Add(1)
		time.Sleep(80 * time.Millisecond) // widen the concurrency window
		return gitAddWorktree(ctx, repo, path, branch, base)
	}

	const callers = 4
	wts := make([]Worktree, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wts[i], errs[i] = pool.Acquire(context.Background(), "task-shared")
		}(i)
	}
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("Acquire caller %d: %v", i, errs[i])
		}
	}
	for i := 1; i < callers; i++ {
		if wts[i].Path != wts[0].Path {
			t.Errorf("Acquire caller %d path = %s, want shared path %s", i, wts[i].Path, wts[0].Path)
		}
	}
	if got := addCalls.Load(); got != 1 {
		t.Errorf("git worktree add invoked %d times, want exactly 1", got)
	}
}

// TestAcquireConcurrentSameTaskIDSingleFlightSharedFailure: when the shared
// git run FAILS, every concurrent same-taskID caller must share that single
// flight's result — exactly one git invocation, one error surfaced to all.
// (On unpatched main the mutex serialized callers into retrying the failing
// git helper N times — the red probe measured 4 invocations.)
func TestAcquireConcurrentSameTaskIDSingleFlightSharedFailure(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	var addCalls atomic.Int32
	origAdd := gitAddWorktreeFn
	t.Cleanup(func() { gitAddWorktreeFn = origAdd })
	injected := errors.New("injected git worktree add failure")
	gitAddWorktreeFn = func(ctx context.Context, repo, path, branch, base string) error {
		addCalls.Add(1)
		time.Sleep(150 * time.Millisecond) // widen the concurrency window
		return injected
	}

	const callers = 4
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = pool.Acquire(context.Background(), "task-shared")
		}(i)
	}
	wg.Wait()

	for i := range errs {
		if !errors.Is(errs[i], injected) {
			t.Fatalf("Acquire caller %d err = %v, want the shared injected failure", i, errs[i])
		}
	}
	if got := addCalls.Load(); got != 1 {
		t.Errorf("failing git worktree add invoked %d times, want exactly 1 (single-flight shares the failure)", got)
	}

	// The failed flight must be cleaned up: a subsequent Acquire retries.
	restored := 0
	gitAddWorktreeFn = func(ctx context.Context, repo, path, branch, base string) error {
		restored++
		return gitAddWorktree(ctx, repo, path, branch, base)
	}
	wt, err := pool.Acquire(context.Background(), "task-shared")
	if err != nil {
		t.Fatalf("retry Acquire after shared failure: %v", err)
	}
	defer func() { _ = pool.Release(context.Background(), wt, false) }()
	if restored != 1 {
		t.Errorf("retry after failed flight invoked git %d times, want exactly 1", restored)
	}
}
