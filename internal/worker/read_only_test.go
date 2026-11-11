package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/testenv"
)

func TestMain(m *testing.M) {
	if err := testenv.ScrubAmbientDelegationEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestReadOnlyEnforcementDecision(t *testing.T) {
	registry := &config.File{Providers: config.BuiltinPlatformProviders()}
	registry.Providers = append(registry.Providers,
		config.ProviderEntry{Name: "custom", Class: "platform_dispatch", Enforcement: config.EnforcementInProcess},
		config.ProviderEntry{Name: "custom-external", Class: "platform_dispatch"},
		config.ProviderEntry{Name: "api", Class: "api_call"})
	for _, tt := range []struct {
		name           string
		refuseReadOnly bool
	}{
		{"agy", false},
		{"claude", true},
		{"codex", false},
		{"opencode", false},
		{"commandcode", false},
		{"qwen", false},
		{"cursor", false},
		{"custom", false},
		{"custom-external", true},
		{"api", false},
	} {
		p, ok := registry.FindProvider(tt.name)
		if !ok {
			t.Fatalf("provider %s missing", tt.name)
		}
		for _, permission := range []string{"read_only", "workspace_write"} {
			t.Run(p.Name+"/"+permission, func(t *testing.T) {
				err := checkReadOnlyEnforcement(*p, permission)
				refuse := permission == "read_only" && tt.refuseReadOnly
				if (err != nil) != refuse {
					t.Fatalf("refuse = %v, want %v: %v", err != nil, refuse, err)
				}
				if refuse {
					want := "E_READ_ONLY_UNENFORCEABLE: read_only cannot be mechanically enforced for " + p.Name + "; dispatch with -permission workspace_write (isolated worktree) or use a provider with in-process enforcement"
					if err.Error() != want {
						t.Fatalf("error = %q, want %q", err, want)
					}
				}
			})
		}
	}
}

func TestReadOnlyClaimRefusedBeforeSpawnAndArtifacts(t *testing.T) {
	for _, permission := range []string{"read_only", ""} {
		t.Run("permission="+permission, func(t *testing.T) {
			iso := &fakeWorktreeIsolator{dir: filepath.Join(t.TempDir(), "worktree")}
			env := newWorkerEnv(t, nil, WithWorktreeIsolator(iso))
			submitTask(t, env, "refuse-read-only", 1, map[string]any{
				"provider": "claude", "permission": permission, "tool_policy": "none",
			})
			task, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-refused", LeaseSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			if task == nil || task.LastError == nil || !strings.Contains(*task.LastError, "E_READ_ONLY_UNENFORCEABLE") {
				t.Fatalf("expected refusal, got %+v", task)
			}
			if task.LeaseToken != nil || task.LeaseOwner != nil {
				t.Fatal("refusal leaked lease")
			}
			var result struct {
				Code   string `json:"code"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(task.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.Code != "E_READ_ONLY_UNENFORCEABLE" || result.Status != "claim_refused_read_only" {
				t.Fatalf("unexpected refusal result: %+v", result)
			}
			if len(env.runner.spawned) != 0 || iso.acquired != 0 {
				t.Fatal("refusal spawned subprocess or acquired worktree")
			}
			if _, err := os.Stat(env.runDir); !os.IsNotExist(err) {
				t.Fatalf("refusal created run artifacts: %v", err)
			}
		})
	}
}

func TestReadOnlyProviderResolution(t *testing.T) {
	s := NewSupervisor(nil, "", WithProviderRegistry([]config.ProviderEntry{
		{Name: "custom", Class: "platform_dispatch", Models: []config.ModelEntry{{ID: "m"}}, Args: []string{"custom"}},
		{Name: "api", Class: "api_call", Models: []config.ModelEntry{{ID: "api-model"}}, Args: []string{"g8s", "llm-call"}},
	}))
	for _, tt := range []struct {
		provider, model, want string
		refuse                bool
	}{
		{"", "m", "custom", true},
		{"", "api-model", "api", false},
		{"", "unknown", "agy", false},
		{"codex", "m", "codex", false},
		{"agy", "m", "agy", false},
	} {
		t.Run(tt.provider+"/"+tt.model, func(t *testing.T) {
			p := s.resolveTaskProvider(taskRequest{Provider: tt.provider, Model: tt.model})
			if p.Name != tt.want || (checkReadOnlyEnforcement(p, "read_only") != nil) != tt.refuse {
				t.Fatalf("unexpected provider decision: %+v", p)
			}
		})
	}
}

func TestReadOnlyAllowedProviderExecution(t *testing.T) {
	for _, tt := range []struct{ provider, permission string }{
		{"agy", "read_only"},
		{"codex", "workspace_write"},
		{"custom", "read_only"},
		{"api", "read_only"},
	} {
		t.Run(tt.provider+"/"+tt.permission, func(t *testing.T) {
			iso := &fakeWorktreeIsolator{dir: filepath.Join(t.TempDir(), "worktree")}
			env := newWorkerEnv(t, nil, WithWorktreeIsolator(iso), WithProviderRegistry([]config.ProviderEntry{
				{Name: "custom", Class: "platform_dispatch", Enforcement: config.EnforcementInProcess},
				{Name: "api", Class: "api_call"},
			}))
			submitTask(t, env, "allowed", 1, map[string]any{"provider": tt.provider, "permission": tt.permission})
			task, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-allowed", LeaseSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			if task.LastError != nil && strings.Contains(*task.LastError, "E_READ_ONLY_UNENFORCEABLE") {
				t.Fatalf("allowed provider refused: %s", *task.LastError)
			}
			if len(env.runner.spawned) != 1 {
				t.Fatalf("spawns = %d, want 1", len(env.runner.spawned))
			}
			if tt.permission == "workspace_write" && (iso.acquired != 1 || env.runner.spawned[0].Dir != iso.dir) {
				t.Fatal("workspace_write did not preserve worktree isolation")
			}
		})
	}
}
