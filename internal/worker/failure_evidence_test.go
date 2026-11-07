package worker

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pin construction sites as well as the dynamic provider/serialization guards.
// New explicit failure literals must carry a reason at their source.
func TestFalseResultConstructionSitesCarryReason(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			resultType, isResult := lit.Type.(*ast.Ident)
			failed := isResult && resultType.Name == "workerResult"
			reason := false
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key := ""
				switch k := kv.Key.(type) {
				case *ast.Ident:
					key = k.Name
				case *ast.BasicLit:
					key = strings.Trim(k.Value, `"`)
				}
				if key == "Reason" || key == "reason" {
					reason = true
				}
				if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "false" && (key == "OK" || key == "ok") {
					failed = true
				}
				if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "true" && key == "OK" {
					failed = false
				}
			}
			if failed {
				checked++
				if !reason {
					t.Errorf("%s: ok=false construction has no reason", fs.Position(lit.Pos()))
				}
			}
			return true
		})
	}
	if checked < 10 {
		t.Fatalf("construction guard checked only %d failures", checked)
	}
}

func TestFalseWorkerResultAlwaysCarriesReason(t *testing.T) {
	for _, tc := range []struct {
		name, raw, stdout string
		code              int
	}{
		{"cursor verdict", `{"ok":false,"status":"OK"}`, "", 0},
		{"blank reason", `{"ok":false,"status":"failed","reason":"  "}`, "", 0},
		{"fenced verdict", "", "```json\n{\"ok\":false,\"status\":\"OK\"}\n```", 0},
		{"AGY empty error", "", `{"result":{"status":"ERROR"}}`, 0},
		{"process exit", "", "", 7},
		{"stream error", "", `{"event":"step_update","step_update":{"step_type":"error_message"}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wr := readWorkerResult([]byte(tc.raw), tc.stdout, tc.code)
			if wr.OK || strings.TrimSpace(wr.Reason) == "" {
				t.Fatalf("failure lacks evidence: %+v", wr)
			}
		})
	}
	var stored workerResult
	if err := json.Unmarshal(mustResultJSON(workerResult{Status: "blocked"}, "", ""), &stored); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stored.Reason) == "" {
		t.Fatal("serialization backstop omitted reason")
	}
	for _, status := range []string{"cancelled", "timeout", "invalid_result"} {
		if reason, ok := outcomeEnvelope(false, status, "", "")["reason"].(string); !ok || reason == "" {
			t.Fatalf("%s outcome missing reason", status)
		}
	}
	if err := json.Unmarshal(mustJSON(make(chan int)), &stored); err != nil || stored.Reason == "" {
		t.Fatalf("marshal failure missing reason: %+v, %v", stored, err)
	}
}

func failureTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestFailedAttemptQuarantineAndNextAttemptCleanup(t *testing.T) {
	for _, mode := range []string{"verdict", "nonzero exit", "spawn error"} {
		t.Run(mode, func(t *testing.T) {
			repo := initGitRepoForDiffFirewall(t)
			dir := filepath.Join(t.TempDir(), "wt with spaces")
			failureTestGit(t, repo, "worktree", "add", "-b", "failure-test", "--", dir, "HEAD")
			payload := filepath.Join(dir, "implementation.go")
			if err := os.WriteFile(payload, []byte("unharvested implementation"), 0o600); err != nil {
				t.Fatal(err)
			}
			iso := &fakeWorktreeIsolator{dir: dir}
			env := newIsolationEnv(t, iso)
			env.runner.factory = func(opts SpawnOptions) Child {
				code, result := 0, `{"ok":false,"status":"OK"}`
				if mode == "nonzero exit" {
					code, result = 9, ""
				}
				child := newFakeChild(code)
				child.finishLater(opts.ResultPath, result, time.Millisecond)
				return child
			}
			if mode == "spawn error" {
				env.runner.err = errors.New("spawn unavailable")
			}
			task := submitTask(t, env, "quarantine-"+mode, 2, map[string]any{"permission": "workspace_write"})
			final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "failure-worker", LeaseSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			if !iso.keepFlag || iso.released != 1 {
				t.Fatalf("dirty failure discarded: %+v", iso)
			}
			var wr workerResult
			if err := json.Unmarshal(final.Result, &wr); err != nil || wr.OK || wr.Reason == "" {
				t.Fatalf("stored failure lacks evidence: %s, %v", final.Result, err)
			}
			if b, err := os.ReadFile(payload); err != nil || string(b) != "unharvested implementation" {
				t.Fatalf("lost implementation: %s, %v", b, err)
			}
			marker := filepath.Join(env.runDir, task.TaskID, failedWorktreeMarker)
			if b, err := os.ReadFile(marker); err != nil || string(b) != dir {
				t.Fatalf("missing quarantine marker: %s, %v", b, err)
			}
			if err := cleanupFailedWorktree(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("another task cleaned this task's quarantine: %v", err)
			}
			// A fresh supervisor proves retention is disk-backed, not a closure.
			if err := env.store.RequeueResult(context.Background(), task.TaskID, "supervisor", "second attempt"); err != nil {
				t.Fatal(err)
			}
			iso.dir = filepath.Join(t.TempDir(), "next-worktree")
			env.sup = NewSupervisor(env.store, env.runDir, WithRunner(env.runner), WithPollInterval(time.Millisecond), WithWorktreeIsolator(iso))
			if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "next-worker", LeaseSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			for _, removed := range []string{dir, marker} {
				if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("old quarantine remains at %s: %v", removed, err)
				}
			}
			failureTestGit(t, repo, "worktree", "add", "-b", "failure-test", "--", filepath.Join(t.TempDir(), "branch-reuse"), "HEAD")
		})
	}
}

func TestFailedWorktreeInspectionErrorNeverCallsRelease(t *testing.T) {
	dir, taskDir := t.TempDir(), t.TempDir()
	payload := filepath.Join(dir, "implementation.go")
	if err := os.WriteFile(payload, []byte("evidence in a damaged worktree"), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseFailedWorktree(taskDir, dir, func(bool) {
		t.Fatal("unverifiable worktree must not reach destructive release callback")
	})
	if _, err := os.Stat(payload); err != nil {
		t.Fatalf("lost evidence after failed inspection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(taskDir, failedWorktreeMarker)); err != nil {
		t.Fatalf("missing quarantine marker: %v", err)
	}
}

func TestFailedWorktreeCleanupRefusesOrdinaryCheckout(t *testing.T) {
	dir, taskDir := initGitRepoForDiffFirewall(t), t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, failedWorktreeMarker), []byte(dir), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupFailedWorktree(taskDir); err == nil || !strings.Contains(err.Error(), "E_FAILED_WORKTREE_UNSAFE") {
		t.Fatalf("ordinary checkout cleanup was not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "allowed.go")); err != nil {
		t.Fatalf("ordinary checkout was damaged: %v", err)
	}
}
