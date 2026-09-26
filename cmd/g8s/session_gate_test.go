package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/lockfile"
)

// Red Test Proof for ADR-0023's sibling slice — S1 PR-2 (#393), per
// plans/260926-s1-session-state/plan.md: flock-gated conditional isolation.
// RED = acquireSessionGate does not exist yet; GREEN = implemented below.

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "seed")
	return dir
}

func headAt(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return string(out)
}

// C-equivalent of the plan's Red Test Proof: first session runs in-place
// holding the lock; a contended second session is auto-promoted into an
// isolated pool worktree; the shared checkout's refs are untouched; finishing
// the holder frees the lock (kernel-release property) for the next session.
func TestConcurrentSessionPromotion(t *testing.T) {
	dir := initGitRepo(t)
	store, err := controlplane.NewControlPlane(filepath.Join(dir, "g8s.db"), nil)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	headBefore := headAt(t, dir)

	g1, err := acquireSessionGate(ctx, store, dir)
	if err != nil {
		t.Fatalf("first gate: %v", err)
	}
	if g1.Mode != controlplane.SessionModeInPlace {
		t.Fatalf("first session mode = %q, want in_place", g1.Mode)
	}

	g2, err := acquireSessionGate(ctx, store, dir)
	if err != nil {
		t.Fatalf("second gate: %v", err)
	}
	// The promoted session's worktree must not leak past the test.
	t.Cleanup(func() { _ = g2.Finish(ctx, false) })
	if g2.Mode != controlplane.SessionModeWorktree {
		t.Fatalf("second session mode = %q, want worktree (auto-promoted)", g2.Mode)
	}
	if g2.WorktreePath == "" || filepath.Base(g2.WorktreePath) == filepath.Base(dir) {
		t.Fatalf("worktree path = %q, want isolated dir", g2.WorktreePath)
	}
	if headAt(t, dir) != headBefore {
		t.Fatal("shared checkout HEAD moved — D6 regression")
	}

	// Registry truth: holder active, promoted session active with its path.
	s1, err := store.GetSession(ctx, g1.ID)
	if err != nil || s1.Status != controlplane.SessionStatusActive {
		t.Fatalf("g1 registry = %+v, %v; want active", s1, err)
	}
	s2, err := store.GetSession(ctx, g2.ID)
	if err != nil || s2.WorktreePath != g2.WorktreePath {
		t.Fatalf("g2 registry worktree_path mismatch: %+v, %v", s2, err)
	}

	// Heartbeat keeps the holder alive in the registry.
	if err := g1.Heartbeat(ctx); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// Finishing the holder frees the lock — the next session runs in-place.
	if err := g1.Finish(ctx, false); err != nil {
		t.Fatalf("finish g1: %v", err)
	}
	if s, err := store.GetSession(ctx, g1.ID); err != nil || s.Status != controlplane.SessionStatusDead {
		t.Fatalf("g1 after finish = %+v, %v; want dead", s, err)
	}
	g3, err := acquireSessionGate(ctx, store, dir)
	if err != nil {
		t.Fatalf("third gate: %v", err)
	}
	if g3.Mode != controlplane.SessionModeInPlace {
		t.Fatalf("third session mode = %q, want in_place after release", g3.Mode)
	}
	if err := g3.Finish(ctx, false); err != nil {
		t.Fatalf("finish g3: %v", err)
	}
}

// Kernel-release property: a SIGKILLed lock holder must not wedge the gate.
func TestSessionLockFreedOnProcessDeath(t *testing.T) {
	if os.Getenv("GO_HELPER_LOCK_HOLDER") == "1" {
		// Retry briefly: on a loaded CI machine the parent's own poll holds
		// the lock for microseconds and a single-shot take can lose the race.
		var l *lockfile.Lock
		var err error
		helperDeadline := time.Now().Add(5 * time.Second)
		for {
			l, err = lockfile.TryLock(os.Getenv("GO_HELPER_LOCK_PATH"))
			if err == nil {
				break
			}
			if time.Now().After(helperDeadline) {
				os.Exit(3)
			}
			time.Sleep(20 * time.Millisecond)
		}
		// The child exits via os.Exit, which skips defers anyway — the
		// kernel releases the lock on death either way.
		defer func() {
			if rerr := l.Release(); rerr != nil {
				t.Logf("helper release: %v", rerr)
			}
		}()
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	dir := initGitRepo(t)
	lockPath := filepath.Join(dir, ".g8s", "session.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestSessionLockFreedOnProcessDeath", "-test.v")
	cmd.Env = append(os.Environ(), "GO_HELPER_LOCK_HOLDER=1", "GO_HELPER_LOCK_PATH="+lockPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	// Wait for the helper to hold the lock: only contention counts as
	// "helper acquired it" — any other error is a real failure.
	deadline := time.Now().Add(10 * time.Second)
	for {
		l, err := lockfile.TryLock(lockPath)
		if errors.Is(err, lockfile.ErrContended) {
			break // helper holds it
		}
		if err != nil {
			t.Fatalf("poll TryLock: %v", err)
		}
		_ = l.Release()
		if time.Now().After(deadline) {
			t.Fatal("helper never acquired the lock")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := cmd.Process.Kill(); err != nil { // SIGKILL
		t.Fatalf("kill helper: %v", err)
	}
	_ = cmd.Wait()

	// The kernel must have released the flock on process death.
	l, err := lockfile.TryLock(lockPath)
	if err != nil {
		t.Fatalf("lock still held after SIGKILL: %v", err)
	}
	_ = l.Release()
}
