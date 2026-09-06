package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/orchestrator"
)

func TestFailingStubWorkerAlwaysReturnsFailure(t *testing.T) {
	w := newFailingStubWorker()
	if name := w.Name(); name != "failing-stub" {
		t.Errorf("Name() = %q, want failing-stub", name)
	}
	if err := w.Available(context.Background()); err != nil {
		t.Fatalf("Available() error: %v", err)
	}
	task := orchestrator.Task{ID: "sup-test-failing-stub"}
	h, err := w.Spawn(context.Background(), task)
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	if pid := h.PID(); pid != -1 {
		t.Errorf("PID() = %d, want -1", pid)
	}
	r, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error: %v", err)
	}
	if r.OK {
		t.Error("Receipt.OK = true, want false")
	}
	if r.LastError == "" {
		t.Error("Receipt.LastError is empty, want deterministic reason")
	}
	if r.ReturnCode != 1 || r.HarnessCode != 1 {
		t.Errorf("Receipt codes = (%d, %d), want (1, 1)", r.ReturnCode, r.HarnessCode)
	}
}

func TestOrchestrateSelfTestEscalatesAfterNineAttempts(t *testing.T) {
	origCtor := orchestratorWorkerCtor
	origOverride := selfTestWorkerOverride
	defer func() {
		orchestratorWorkerCtor = origCtor
		selfTestWorkerOverride = origOverride
	}()
	orchestratorWorkerCtor = func() orchestrator.Worker { return &trackingStubWorker{} }
	selfTestWorkerOverride = nil

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "self-test.db")
	t.Setenv("G8S_DB", dbPath)

	binPath := buildG8sBinary(t)

	type result struct {
		out  string
		err  error
		code int
	}
	resCh := make(chan result, 1)
	go func() {
		out, err := runCaptured(t, binPath, "orchestrate",
			"--self-test",
			"--model", "gemini-3.8-flash-high",
			"--role", "collector",
			"--permission", "read_only",
			"--max-attempts", "3",
			"--max-approaches", "3",
			"--no-poll",
			"--silence-threshold", "100ms",
			"--timeout", "30s",
			"--json",
		)
		resCh <- result{out: out, err: err, code: 0}
	}()

	var r result
	select {
	case r = <-resCh:
	case <-time.After(45 * time.Second):
		t.Fatal("orchestrate --self-test exceeded 45s timeout")
	}

	if !strings.Contains(r.out, `"approaches_tried": 3`) {
		t.Errorf("missing approaches_tried=3 in output:\n%s", r.out)
	}
	if !strings.Contains(r.out, `"total_attempts": 9`) {
		t.Errorf("missing total_attempts=9 in output:\n%s", r.out)
	}
	if !strings.Contains(r.out, `"escalated": true`) {
		t.Errorf("missing escalated=true in output:\n%s", r.out)
	}
	if !strings.Contains(r.out, `"outcome": "ESCALATED"`) {
		t.Errorf("missing outcome=ESCALATED in output:\n%s", r.out)
	}
	if !strings.Contains(r.out, `approach_budget_exhausted`) {
		t.Errorf("missing trigger=approach_budget_exhausted in output:\n%s", r.out)
	}
}

func runCaptured(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "G8S_DB="+filepath.Join(t.TempDir(), "captured.db"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}
