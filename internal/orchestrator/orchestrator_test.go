package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistryPickReturnsFirstAvailable(t *testing.T) {
	r := NewRegistry()
	called := 0
	r.Register("a", func() Worker {
		called++
		return fakeWorker{name: "a", available: true}
	})
	r.Register("b", func() Worker {
		called++
		return fakeWorker{name: "b", available: true}
	})

	w, err := r.Pick(context.Background())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if w.Name() != "a" {
		t.Fatalf("expected first registered (a), got %q", w.Name())
	}
}

func TestRegistryPickSkipsUnavailable(t *testing.T) {
	r := NewRegistry()
	r.Register("a", func() Worker { return fakeWorker{name: "a", available: false} })
	r.Register("b", func() Worker { return fakeWorker{name: "b", available: true} })

	w, err := r.Pick(context.Background())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if w.Name() != "b" {
		t.Fatalf("expected b (a unavailable), got %q", w.Name())
	}
}

func TestRegistryErrorsOnDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("agy", func() Worker { return fakeWorker{name: "agy"} }); err != nil {
		t.Fatalf("unexpected error on first registration: %v", err)
	}
	if err := r.Register("agy", func() Worker { return fakeWorker{name: "agy"} }); err == nil {
		t.Fatalf("expected error on duplicate registration, got nil")
	}
}

func TestRegistryNamesSorted(t *testing.T) {
	r := NewRegistry()
	r.Register("z", func() Worker { return fakeWorker{name: "z"} })
	r.Register("a", func() Worker { return fakeWorker{name: "a"} })
	r.Register("m", func() Worker { return fakeWorker{name: "m"} })

	got := strings.Join(r.Names(), ",")
	if got != "a,m,z" {
		t.Fatalf("names not sorted: %q", got)
	}
}

func TestPoolAcquireRelease(t *testing.T) {
	repo := setupGitRepo(t)
	root := filepath.Join(t.TempDir(), "wtpool")

	pool, err := NewPool(PoolOptions{Repo: repo, Root: root, Prefix: "test"})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wt1, err := pool.Acquire(ctx, "task-1")
	if err != nil {
		t.Fatalf("acquire task-1: %v", err)
	}
	if !strings.HasPrefix(wt1.ID, "wt-") {
		t.Fatalf("wt ID format: %q", wt1.ID)
	}
	if wt1.BaseSHA == "" {
		t.Fatalf("BaseSHA empty")
	}
	if _, err := os.Stat(wt1.Path); err != nil {
		t.Fatalf("worktree path missing: %v", err)
	}

	wt2, err := pool.Acquire(ctx, "task-2")
	if err != nil {
		t.Fatalf("acquire task-2: %v", err)
	}
	if wt1.ID == wt2.ID {
		t.Fatalf("expected distinct worktrees, got %q twice", wt1.ID)
	}

	if len(pool.Active()) != 2 {
		t.Fatalf("expected 2 active, got %d", len(pool.Active()))
	}

	if err := pool.Release(ctx, wt1, false); err != nil {
		t.Fatalf("release wt1: %v", err)
	}
	if _, err := os.Stat(wt1.Path); err == nil {
		t.Fatalf("worktree path still exists after release")
	}
	if len(pool.Active()) != 1 {
		t.Fatalf("expected 1 active after release, got %d", len(pool.Active()))
	}
}

func TestPoolRequiresGitRepo(t *testing.T) {
	notRepo := t.TempDir()
	_, err := NewPool(PoolOptions{Repo: notRepo})
	if err == nil {
		t.Fatalf("expected error for non-git dir")
	}
}

func TestPoolConcurrentAcquire(t *testing.T) {
	repo := setupGitRepo(t)
	root := filepath.Join(t.TempDir(), "wtpool")

	// Windows CI runners can be slow, making 200ms insufficient for 8 goroutines
	// Use a WaitGroup to ensure all acquires have completed instead of time.Sleep
	pool, err := NewPool(PoolOptions{Repo: repo, Root: root, Prefix: "race"})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	ctx := context.Background()

	const n = 8
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			wt, err := pool.Acquire(ctx, "task-"+string(rune('a'+i)))
			if err != nil {
				t.Errorf("acquire %d: %v", i, err)
				return
			}
			ids[i] = wt.ID
		}()
	}
	wg.Wait()
	if len(pool.Active()) != n {
		t.Fatalf("expected %d active, got %d", n, len(pool.Active()))
	}
}

// fakeWorker is a no-op Worker used by registry tests.
type fakeWorker struct {
	name      string
	available bool
}

func (w fakeWorker) Name() string { return w.name }
func (w fakeWorker) Available(_ context.Context) error {
	if !w.available {
		return errFakeUnavailable
	}
	return nil
}
func (w fakeWorker) Spawn(_ context.Context, _ Task) (Handle, error) { return nil, nil }

var errFakeUnavailable = stringErr("unavailable")

type stringErr string

func (e stringErr) Error() string { return string(e) }

// setupGitRepo creates a temp git repo with one commit so worktree add has
// something to anchor on.
func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-q"},
		{"git", "config", "user.email", "test@test"},
		{"git", "config", "user.name", "test"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "commit", "--allow-empty", "-m", "init", "-q"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", c, err, out)
		}
	}
	return dir
}
