package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestExternalCLIReadOnlyIsolationUnavailable(t *testing.T) {
	env := newWorkerEnv(t, nil)
	env.sup = NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithPollInterval(2*time.Millisecond),
		WithProviderCommandResolver(NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"codex"},
		})),
	)
	submitTask(t, env, "ro-cli-no-iso", 1, map[string]any{
		"provider":   "codex",
		"permission": "read_only",
	})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro-no-iso", LeaseSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(final.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "isolation_unavailable" {
		t.Fatalf("expected isolation_unavailable, got %+v", result)
	}
	if len(env.runner.spawned) != 0 {
		t.Fatal("must not spawn child when read_only external CLI lacks isolator")
	}
}
