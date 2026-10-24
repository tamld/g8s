package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestGitRepo(t *testing.T, dir string, tag string, dirty bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test Operator")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+filepath.Base(dir)+"\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")
	if tag != "" {
		run("tag", tag)
	}
	if dirty {
		if err := os.WriteFile(filepath.Join(dir, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("write dirty file: %v", err)
		}
	}
}

func TestFleetStatus_CleanAndDirtyRepos(t *testing.T) {
	binPath := buildG8sBinary(t)
	wsDir := t.TempDir()

	repoClean := filepath.Join(wsDir, "repo-alpha")
	repoDirty := filepath.Join(wsDir, "repo-beta")
	initTestGitRepo(t, repoClean, "v1.0.0", false)
	initTestGitRepo(t, repoDirty, "v0.1.0", true)

	// 1. Run fleet status --json (without --require-clean)
	cmd := exec.Command(binPath, "fleet", "status", "--dir", wsDir, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fleet status failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Kind string            `json:"kind"`
		Data FleetStatusReport `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal fleet status: %v\nRaw: %s", err, string(out))
	}
	if env.Kind != "fleet_status" {
		t.Errorf("kind = %q, want fleet_status", env.Kind)
	}
	if env.Data.TotalRepos != 2 || env.Data.CleanRepos != 1 || env.Data.DirtyRepos != 1 || env.Data.AllClean {
		t.Errorf("unexpected summary: %+v", env.Data)
	}

	// 2. Run fleet status --require-clean on dirty workspace -> must exit non-zero
	cmdReq := exec.Command(binPath, "fleet", "status", "--dir", wsDir, "--require-clean", "--json")
	if reqOut, reqErr := cmdReq.CombinedOutput(); reqErr == nil {
		t.Fatalf("expected non-zero exit with --require-clean on dirty workspace, got 0: %s", string(reqOut))
	}
}

func TestSubmit_SubtaskContainmentAndAdaptiveTimeout(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "containment.db")
	subDirA := filepath.Join(tempDir, "modA")
	subDirB := filepath.Join(tempDir, "modB")
	_ = os.MkdirAll(subDirA, 0o755)
	_ = os.MkdirAll(subDirB, 0o755)

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + filepath.Join(tempDir, "absent.json"),
	}

	// 1. Submit F1 task (scoped to subDirA, effort high -> adaptive timeout 240s)
	envF1, codeF1, rawF1 := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "task-f1",
		"--prompt", "F1 module A worker",
		"--effort", "high",
		"--scope-root", tempDir,
		"--add-dir", subDirA,
	)
	if codeF1 != 0 {
		t.Fatalf("submit F1 failed: code=%d raw=%s", codeF1, rawF1)
	}
	var f1Data struct {
		TaskID  string `json:"task_id"`
		Request struct {
			Timeout string `json:"timeout"`
		} `json:"request"`
	}
	bF1, _ := json.Marshal(envF1.Data)
	_ = json.Unmarshal(bF1, &f1Data)
	if f1Data.Request.Timeout != "240s" {
		t.Errorf("adaptive timeout for high effort = %q, want 240s", f1Data.Request.Timeout)
	}

	// 2. Submit valid F2 subtask under F1 (staying within subDirA, effort low -> adaptive timeout 45s)
	envF2, codeF2, rawF2 := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "task-f2",
		"--parent-task-id", f1Data.TaskID,
		"--prompt", "F2 bounded helper inside modA",
		"--effort", "low",
		"--scope-root", tempDir,
		"--add-dir", subDirA,
	)
	if codeF2 != 0 {
		t.Fatalf("submit F2 failed: code=%d raw=%s", codeF2, rawF2)
	}
	var f2Data struct {
		TaskID  string `json:"task_id"`
		Request struct {
			Timeout string `json:"timeout"`
		} `json:"request"`
	}
	bF2, _ := json.Marshal(envF2.Data)
	_ = json.Unmarshal(bF2, &f2Data)
	if f2Data.Request.Timeout != "45s" {
		t.Errorf("adaptive timeout for low effort = %q, want 45s", f2Data.Request.Timeout)
	}

	// 3. Reject F2 -> F3 recursive subtask (ADR-0032 depth <= 2)
	_, codeF3, rawF3 := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "task-f3",
		"--parent-task-id", f2Data.TaskID,
		"--prompt", "Forbidden F3 subtask",
		"--scope-root", tempDir,
		"--add-dir", subDirA,
	)
	if codeF3 == 0 {
		t.Fatalf("expected F2 -> F3 submission to be rejected, but exited 0: %s", rawF3)
	}
	if !strings.Contains(rawF3, "containment depth exceeded") {
		t.Errorf("expected 'containment depth exceeded' error, got: %s", rawF3)
	}

	// 4. Reject F1 -> F2 scope escape (child requesting subDirB when F1 only owns subDirA)
	_, codeEsc, rawEsc := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "task-f2-escape",
		"--parent-task-id", f1Data.TaskID,
		"--prompt", "F2 trying to escape F1 add-dir",
		"--scope-root", tempDir,
		"--add-dir", subDirB,
	)
	if codeEsc == 0 {
		t.Fatalf("expected subtask scope escape to be rejected, but exited 0: %s", rawEsc)
	}
	if !strings.Contains(rawEsc, "subtask scope escape") {
		t.Errorf("expected 'subtask scope escape' error, got: %s", rawEsc)
	}
}
