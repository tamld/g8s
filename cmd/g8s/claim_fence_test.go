package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/controlplane"
)

func TestVerifyClaimFenceMissingReceipt(t *testing.T) {
	db, store := setupTestDB(t)
	defer store.Close()
	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: "verify-fence", MaxAttempts: 1, Role: "scout", Model: "test", Permission: "read_only",
		AddDirs: []string{t.TempDir()},
		Payload: json.RawMessage(`{"prompt":"test","receipt_id":"missing","claim_binding":{"task_id":"mesh-task"},"permission":"workspace_write"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--json"}, &out, &errs, db)
	if code != 1 || !strings.Contains(errs.String(), "E_CLAIM_FENCE_STALE") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &errs)
	}
}

func TestReceiptExportRecoversClaimVerdict(t *testing.T) {
	db, store := setupTestDB(t)
	defer store.Close()
	t.Setenv("G8S_DB", db)
	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{IdempotencyKey: "export-fence", MaxAttempts: 1, Role: "scout", Model: "test", Permission: "read_only", AddDirs: []string{t.TempDir()}, Payload: json.RawMessage(`{"prompt":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimTask(ctx, "worker", 60)
	if err != nil {
		t.Fatal(err)
	}
	if !store.StartTask(claimed.TaskID, "worker", *claimed.LeaseToken) {
		t.Fatal("start failed")
	}
	v := &controlplane.ClaimFenceVerdict{TaskID: task.TaskID, ClaimTaskID: "claim-task", ReceiptID: "receipt", Verdict: "rejected", Code: "E_CLAIM_FENCE_OUT_OF_SCOPE"}
	result, err := json.Marshal(map[string]any{"ok": false, "status": "contract_violation", "claim_fence": v})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.FinishAttempt(claimed.TaskID, "worker", *claimed.LeaseToken, controlplane.FinishAttemptParams{Result: result, Success: false})
	if err != nil {
		t.Fatal(err)
	}
	runReceiptExport([]string{"--task", task.TaskID, "--run-dir", t.TempDir(), "--evidence-dir", t.TempDir(), "--json"})
	loaded, err := store.GetTask(ctx, task.TaskID)
	if err != nil || loaded.ReceiptExport == nil || loaded.ReceiptExport.ClaimFence == nil || *loaded.ReceiptExport.ClaimFence != *v {
		t.Fatalf("export lost verdict: %+v %v", loaded, err)
	}
}
