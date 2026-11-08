package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// #845: mid-run claim fence refusal from awaitOutcome must not fall through to success collect.
func TestCollectClaimRefusedFenceMidRun(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "cp.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("open control plane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	runRoot := filepath.Join(tmp, "runs")
	sup := NewSupervisor(store, runRoot, WithRunner(&fakeRunner{}))
	sup.clock = func() time.Time { return time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC) }
	t.Setenv("G8S_TELEMETRY", "")

	payload, _ := json.Marshal(map[string]any{
		"prompt": "p", "timeout": "30s", "provider": "test-prov", "model": "test-model",
	})
	_, err = store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "collect-fence-refusal-1",
		Payload:        payload,
		Model:          "test-model",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "30s",
		AddDirs:        []string{"."},
		MaxAttempts:    3,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	claimed, err := store.ClaimTask(ctx, "w-fence", 60)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	token := derefString(claimed.LeaseToken)
	if !store.StartTask(claimed.TaskID, "w-fence", token) {
		t.Fatal("StartTask returned false")
	}

	var req taskRequest
	if err := json.Unmarshal(claimed.Request, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	attemptDir := filepath.Join(runRoot, claimed.TaskID, fmt.Sprintf("attempt-%d", claimed.Attempts))
	if err := os.MkdirAll(attemptDir, 0o700); err != nil {
		t.Fatalf("mkdir attempt: %v", err)
	}
	resultPath := filepath.Join(attemptDir, "result.json")
	stdoutPath := filepath.Join(attemptDir, "worker.stdout")
	stderrPath := filepath.Join(attemptDir, "worker.stderr")
	// Deliberately omit a success result file — fence refusal must not read default success.
	_ = os.WriteFile(stdoutPath, nil, 0o600)
	_ = os.WriteFile(stderrPath, nil, 0o600)

	child := newFakeChild(1)
	child.closeOnce.Do(func() { close(child.done) })

	fenceDetail := "E_CLAIM_FENCE_STALE: claim fence renewal rejected"
	reason := "claim_refused_fence: " + fenceDetail
	startTime := sup.clock()
	final, cerr := sup.collect(ctx, child, reason, claimed.TaskID, "w-fence", token,
		attemptDir, filepath.Join(attemptDir, "prompt.txt"), resultPath, stdoutPath, stderrPath,
		"", "read_only", "", startTime, "", req)
	if cerr != nil {
		t.Fatalf("collect: %v", cerr)
	}
	// The row state stays WORKER_COMPLETED by contract (#771 supervisor
	// gate) — the fence refusal is carried by the result envelope below,
	// which is what the supervisor verdict reads.
	if final.State != controlplane.StateWorkerCompleted {
		t.Fatalf("state = %q, want WORKER_COMPLETED (supervisor-gate contract)", final.State)
	}
	var envelope map[string]any
	if err := json.Unmarshal(final.Result, &envelope); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if envelope["ok"] != false {
		t.Fatalf("ok = %v, want false", envelope["ok"])
	}
	if envelope["status"] != "claim_refused_fence" {
		t.Fatalf("status = %v, want claim_refused_fence", envelope["status"])
	}
	if envelope["code"] != "E_CLAIM_FENCE_STALE" {
		t.Fatalf("code = %v, want E_CLAIM_FENCE_STALE", envelope["code"])
	}
	reasonStr, _ := envelope["reason"].(string)
	if !strings.Contains(reasonStr, "E_CLAIM_FENCE_STALE") {
		t.Fatalf("reason = %q, want E_CLAIM_FENCE_STALE", reasonStr)
	}
}
