package orchestrator

import (
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"testing"
)

func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestFanOutEmpty(t *testing.T) {
	got, err := FanOut(context.Background(), nil, FanOutOptions{})
	if err != nil {
		t.Fatalf("empty plan should be no-op, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil receipts, got %v", got)
	}
}

func TestFanOutMissingRegistry(t *testing.T) {
	_, err := FanOut(context.Background(), []TaskSpec{{TaskID: "t1"}}, FanOutOptions{Pool: &Pool{}})
	if err == nil || !containsString(err.Error(), "Registry and Pool required") {
		t.Errorf("expected missing-registry error, got %v", err)
	}
}

func TestFanOutAllFailToSpawn(t *testing.T) {
	reg := NewRegistry()
	reg.Register("nope", func() Worker { return fakeWorker{name: "nope", available: false} })
	_, err := FanOut(context.Background(), []TaskSpec{{TaskID: "t1"}}, FanOutOptions{Registry: reg, Pool: &Pool{}})
	if err == nil {
		t.Fatal("expected all-fail error")
	}
	if !containsString(err.Error(), "pick worker") && !containsString(err.Error(), "all") {
		t.Errorf("expected fan-out failure, got %v", err)
	}
}

type alwaysSpawnOK struct {
	fakeWorker
	spawns atomic.Int32
}

func (a *alwaysSpawnOK) Spawn(ctx context.Context, t Task) (Handle, error) {
	a.spawns.Add(1)
	return &okHandle{receipt: Receipt{OK: true, TaskID: t.ID, WorkerName: a.name, CommitSHA: "x"}}, nil
}

type okHandle struct {
	receipt Receipt
}

func (o *okHandle) PID() int                                  { return 1 }
func (o *okHandle) Wait(ctx context.Context) (Receipt, error) { return o.receipt, nil }
func (o *okHandle) Cancel(ctx context.Context) error          { return nil }
func (o *okHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}

func TestFanOutSuccess(t *testing.T) {
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	mustRunGit(t, dir, "checkout", "-q", "-b", "main")

	worker := &alwaysSpawnOK{fakeWorker: fakeWorker{name: "ok", available: true}}
	reg := NewRegistry()
	reg.Register("ok", func() Worker { return worker })

	pool, err := NewPool(PoolOptions{Repo: dir})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	results, err := FanOut(context.Background(), []TaskSpec{
		{TaskID: "a", Task: Task{ID: "a", Role: "collector"}},
		{TaskID: "b", Task: Task{ID: "b", Role: "collector"}},
	}, FanOutOptions{Registry: reg, Pool: pool})
	if err != nil {
		t.Fatalf("fanout: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if worker.spawns.Load() < 2 {
		t.Errorf("expected at least 2 spawns, got %d", worker.spawns.Load())
	}
}

type alwaysFail struct {
	fakeWorker
}

func (a *alwaysFail) Spawn(ctx context.Context, t Task) (Handle, error) {
	return nil, errors.New("spawn boom")
}

func TestFanOutSpawnError(t *testing.T) {
	worker := &alwaysFail{fakeWorker: fakeWorker{name: "bad", available: true}}
	reg := NewRegistry()
	reg.Register("bad", func() Worker { return worker })

	_, err := FanOut(context.Background(), []TaskSpec{
		{TaskID: "x", Task: Task{ID: "x"}},
	}, FanOutOptions{Registry: reg, Pool: &Pool{}})
	if err == nil {
		t.Fatal("expected all-fail when spawn errors")
	}
}

func TestAnySpawned(t *testing.T) {
	if anySpawned([]error{nil, errors.New("x")}) != true {
		t.Error("expected true when one is nil")
	}
	if anySpawned([]error{errors.New("x"), errors.New("y")}) != false {
		t.Error("expected false when all non-nil")
	}
	if anySpawned(nil) != false {
		t.Error("expected false for empty")
	}
}

func TestSplitNonEmpty(t *testing.T) {
	got := splitNonEmpty("a\nb\n\nc\n", '\n')
	if len(got) != 3 {
		t.Errorf("expected 3 entries, got %v", got)
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("wrong order/content: %v", got)
	}
}

func TestSplitNonEmptyEmpty(t *testing.T) {
	if got := splitNonEmpty("", '\n'); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
	if got := splitNonEmpty("   ", '\n'); len(got) != 0 {
		t.Errorf("expected only-whitespace to be empty, got %v", got)
	}
}

func TestDiffScopeEmpty(t *testing.T) {
	if got := diffScope(nil, []string{"a"}); got != nil {
		t.Errorf("expected nil for empty modified, got %v", got)
	}
}

func TestDiffScopeAllAllowed(t *testing.T) {
	if got := diffScope([]string{"a.go", "b.go"}, []string{"a.go", "b.go"}); got != nil {
		t.Errorf("expected no violations, got %v", got)
	}
}

func TestDiffScopeViolations(t *testing.T) {
	got := diffScope([]string{"a.go", "evil.go", "b.go"}, []string{"a.go", "b.go"})
	if len(got) != 1 || got[0] != "evil.go" {
		t.Errorf("expected single violation 'evil.go', got %v", got)
	}
}

func TestGitDiffNameOnlyNonRepo(t *testing.T) {
	// Run against a non-git directory; expect empty results, no panic.
	files, sha := gitDiffNameOnly(t.TempDir(), "deadbeef")
	if files != nil || sha != "" {
		t.Errorf("expected empty results for non-repo, got files=%v sha=%q", files, sha)
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	r.Register("only", func() Worker { return fakeWorker{name: "only", available: true} })
	if _, ok := r.Get("only"); !ok {
		t.Error("Get should find registered factory")
	}
	if _, ok := r.Get("missing"); ok {
		t.Error("Get of unknown should return false")
	}
}

func TestDefaultRegistryNotNil(t *testing.T) {
	if DefaultRegistry() == nil {
		t.Error("DefaultRegistry should never be nil")
	}
}

func TestNewPoolDefaults(t *testing.T) {
	// Initialize a git repo so NewPool passes its validity check.
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")

	p, err := NewPool(PoolOptions{Repo: dir})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if p == nil {
		t.Fatal("NewPool returned nil")
	}
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
