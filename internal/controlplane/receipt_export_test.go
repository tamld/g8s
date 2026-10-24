package controlplane_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func TestReceiptExportPersistenceAndReopen(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "test-export-persist-01",
		Priority:       1,
		MaxAttempts:    3,
		Role:           "test-runner",
		Model:          "test-model",
		AddDirs:        []string{tempDir},
		Permission:     "read_only",
		Payload:        json.RawMessage(`{"prompt":"evidence export test"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// Initial task should have nil ReceiptExport
	if task.ReceiptExport != nil {
		t.Fatalf("expected nil ReceiptExport on fresh task, got %+v", task.ReceiptExport)
	}

	// Record receipt export status
	now := float64(time.Now().UnixNano()) / 1e9
	exportStatus := controlplane.ReceiptExportStatus{
		Local: controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusSucceeded,
			Time:     now,
			Digest:   "a1b2c3d4e5f67890abcdef1234567890abcdef1234567890abcdef1234567890",
			Path:     "receipt.json",
			Category: "local_write",
		},
		Central: controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusFailed,
			Time:     now,
			Category: "mkdir_failed",
			Error:    "mkdir evidence_dir: not a directory",
			Path:     filepath.Join(task.TaskID, "receipt.json"),
		},
		UpdatedAt:      now,
		Success:        false,
		FullyEvidenced: false,
	}

	if err := store.RecordReceiptExport(ctx, task.TaskID, exportStatus); err != nil {
		t.Fatalf("RecordReceiptExport: %v", err)
	}

	// Verify GetTask reflects the persisted export status
	reloaded, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if reloaded.ReceiptExport == nil {
		t.Fatal("expected non-nil ReceiptExport on reloaded task")
	}
	if reloaded.ReceiptExport.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Local.Status = %s, want %s", reloaded.ReceiptExport.Local.Status, controlplane.ExportStatusSucceeded)
	}
	if reloaded.ReceiptExport.Local.Digest != exportStatus.Local.Digest {
		t.Errorf("Local.Digest = %s, want %s", reloaded.ReceiptExport.Local.Digest, exportStatus.Local.Digest)
	}
	if reloaded.ReceiptExport.Central.Status != controlplane.ExportStatusFailed {
		t.Errorf("Central.Status = %s, want %s", reloaded.ReceiptExport.Central.Status, controlplane.ExportStatusFailed)
	}
	if reloaded.ReceiptExport.Central.Category != "mkdir_failed" {
		t.Errorf("Central.Category = %s, want mkdir_failed", reloaded.ReceiptExport.Central.Category)
	}
	if reloaded.ReceiptExport.Central.Error != "mkdir evidence_dir: not a directory" {
		t.Errorf("Central.Error = %s, want %s", reloaded.ReceiptExport.Central.Error, "mkdir evidence_dir: not a directory")
	}

	// Verify ListTasks also returns the receipt export status
	tasks, err := store.ListTasks(ctx, controlplane.TaskFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	var found *controlplane.Task
	for _, tk := range tasks {
		if tk.TaskID == task.TaskID {
			found = tk
			break
		}
	}
	if found == nil || found.ReceiptExport == nil {
		t.Fatal("expected task with ReceiptExport in ListTasks")
	}
	if found.ReceiptExport.Local.Digest != exportStatus.Local.Digest {
		t.Errorf("ListTasks task Local.Digest = %s, want %s", found.ReceiptExport.Local.Digest, exportStatus.Local.Digest)
	}

	// Test persistence across store reopen
	if err := store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	store2, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("reopen NewControlPlane: %v", err)
	}
	defer store2.Close()

	reopenedTask, err := store2.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask after reopen: %v", err)
	}
	if reopenedTask.ReceiptExport == nil {
		t.Fatal("expected non-nil ReceiptExport after reopen")
	}
	if reopenedTask.ReceiptExport.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("reopened Local.Status = %s, want %s", reopenedTask.ReceiptExport.Local.Status, controlplane.ExportStatusSucceeded)
	}
	if reopenedTask.ReceiptExport.Central.Error != "mkdir evidence_dir: not a directory" {
		t.Errorf("reopened Central.Error = %s, want sanitized error", reopenedTask.ReceiptExport.Central.Error)
	}
}

func TestReceiptExportInvariantsExecutionOutcomePreserved(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "test-invariants-01",
		Priority:       1,
		MaxAttempts:    1,
		Role:           "test-runner",
		Model:          "test-model",
		AddDirs:        []string{tempDir},
		Payload:        json.RawMessage(`{"prompt":"invariant test"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// Complete task
	claimed, err := store.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if !store.StartTask(claimed.TaskID, "worker-1", *claimed.LeaseToken) {
		t.Fatal("StartTask failed")
	}
	resJSON := json.RawMessage(`{"ok":true,"output":"all done"}`)
	_, err = store.FinishAttempt(claimed.TaskID, "worker-1", *claimed.LeaseToken, controlplane.FinishAttemptParams{
		Result:  resJSON,
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	completedTask, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	// Record execution snapshot values
	origState := completedTask.State
	origResult := string(completedTask.Result)
	origResultHash := completedTask.ResultHash
	origReceiptHash := completedTask.ReceiptHash
	origCompletedAt := completedTask.CompletedAt

	// Record an export failure
	now := float64(time.Now().UnixNano()) / 1e9
	err = store.RecordReceiptExport(ctx, task.TaskID, controlplane.ReceiptExportStatus{
		Local: controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusFailed,
			Time:     now,
			Category: "write_failed",
			Error:    "write receipt.json: disk full",
		},
		Central: controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusDisabled,
			Time:     now,
			Category: "disabled",
		},
		UpdatedAt: now,
		Success:   false,
	})
	if err != nil {
		t.Fatalf("RecordReceiptExport: %v", err)
	}

	// Re-fetch and verify execution invariants
	afterExport, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask after export: %v", err)
	}

	if afterExport.State != origState {
		t.Errorf("State modified: got %s, want %s", afterExport.State, origState)
	}
	if string(afterExport.Result) != origResult {
		t.Errorf("Result modified: got %s, want %s", string(afterExport.Result), origResult)
	}
	if (origResultHash == nil && afterExport.ResultHash != nil) ||
		(origResultHash != nil && (afterExport.ResultHash == nil || *origResultHash != *afterExport.ResultHash)) {
		t.Errorf("ResultHash modified: got %v, want %v", afterExport.ResultHash, origResultHash)
	}
	if (origReceiptHash == nil && afterExport.ReceiptHash != nil) ||
		(origReceiptHash != nil && (afterExport.ReceiptHash == nil || *origReceiptHash != *afterExport.ReceiptHash)) {
		t.Errorf("ReceiptHash modified: got %v, want %v", afterExport.ReceiptHash, origReceiptHash)
	}
	if (origCompletedAt == nil && afterExport.CompletedAt != nil) ||
		(origCompletedAt != nil && (afterExport.CompletedAt == nil || *origCompletedAt != *afterExport.CompletedAt)) {
		t.Errorf("CompletedAt modified: got %v, want %v", afterExport.CompletedAt, origCompletedAt)
	}
}

func TestCanonicalReceiptSnapshotStability(t *testing.T) {
	now := 1700000000.0
	completed := 1700000010.0
	resHash := "result-hash-abc"
	rcHash := "receipt-hash-xyz"

	task := &controlplane.Task{
		TaskID:         "task-snapshot-stable",
		IdempotencyKey: "key-123",
		SchemaVersion:  controlplane.TaskSchemaVersion,
		State:          controlplane.StateWorkerCompleted,
		Priority:       1,
		Request:        json.RawMessage(`{"prompt":"stable snapshot"}`),
		RequestHash:    "req-hash-123",
		Result:         json.RawMessage(`{"ok":true}`),
		ResultHash:     &resHash,
		ReceiptHash:    &rcHash,
		Attempts:       1,
		MaxAttempts:    1,
		CreatedAt:      now,
		UpdatedAt:      completed + 100, // UpdatedAt has churned due to subsequent operations
		CompletedAt:    &completed,
		ReceiptExport: &controlplane.ReceiptExportStatus{
			Local: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
				Time:   completed + 5,
			},
			UpdatedAt: completed + 5,
		},
	}

	snap1, err := controlplane.CanonicalReceiptSnapshot(task)
	if err != nil {
		t.Fatalf("CanonicalReceiptSnapshot 1: %v", err)
	}
	hash1 := sha256.Sum256(snap1)
	digest1 := hex.EncodeToString(hash1[:])

	// Mutate task receipt export and updated_at (simulating later retry or store update)
	task.UpdatedAt = completed + 500
	task.ReceiptExport.Central = controlplane.ExportDestinationStatus{
		Status: controlplane.ExportStatusSucceeded,
		Time:   completed + 500,
	}
	task.ReceiptExport.UpdatedAt = completed + 500

	snap2, err := controlplane.CanonicalReceiptSnapshot(task)
	if err != nil {
		t.Fatalf("CanonicalReceiptSnapshot 2: %v", err)
	}
	hash2 := sha256.Sum256(snap2)
	digest2 := hex.EncodeToString(hash2[:])

	// Digests must be byte-identical!
	if digest1 != digest2 {
		t.Fatalf("digest mismatch across snapshots: %s != %s", digest1, digest2)
	}
	if string(snap1) != string(snap2) {
		t.Fatalf("snapshot bytes mismatch across churn:\n snap1: %s\n snap2: %s", string(snap1), string(snap2))
	}

	// Verify mutable receipt_export is excluded from exported snapshot bytes
	var unmarshaled map[string]any
	if err := json.Unmarshal(snap1, &unmarshaled); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if _, ok := unmarshaled["receipt_export"]; ok {
		t.Errorf("canonical snapshot must not contain mutable receipt_export")
	}
}

func TestCheckExportAcceptancePolicy(t *testing.T) {
	// 1. Nil receipt export with required local policy
	taskNil := &controlplane.Task{TaskID: "t-nil"}
	if err := taskNil.CheckExportAcceptance(controlplane.ExportPolicy{RequireLocal: true}); err == nil {
		t.Error("expected error when receipt export is nil and RequireLocal=true")
	}

	// 2. Local failed with RequireLocal=true
	taskFailedLocal := &controlplane.Task{
		TaskID: "t-fail-local",
		ReceiptExport: &controlplane.ReceiptExportStatus{
			Local: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusFailed,
				Error:  "write failed: permission denied",
			},
		},
	}
	if err := taskFailedLocal.CheckExportAcceptance(controlplane.ExportPolicy{RequireLocal: true}); err == nil {
		t.Error("expected error when Local is failed and RequireLocal=true")
	}

	// 3. Central failed with RequireCentral=true
	taskFailedCentral := &controlplane.Task{
		TaskID: "t-fail-central",
		ReceiptExport: &controlplane.ReceiptExportStatus{
			Local: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
			},
			Central: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusFailed,
				Error:  "mkdir failed: not a directory",
			},
		},
	}
	if err := taskFailedCentral.CheckExportAcceptance(controlplane.ExportPolicy{RequireLocal: true, RequireCentral: true}); err == nil {
		t.Error("expected error when Central is failed and RequireCentral=true")
	}

	// 4. Central disabled, but optional (RequireCentral=false)
	taskOptionalCentral := &controlplane.Task{
		TaskID: "t-opt-central",
		ReceiptExport: &controlplane.ReceiptExportStatus{
			Local: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
			},
			Central: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusDisabled,
			},
		},
	}
	if err := taskOptionalCentral.CheckExportAcceptance(controlplane.ExportPolicy{RequireLocal: true, RequireCentral: false}); err != nil {
		t.Errorf("unexpected error when Central is disabled but optional: %v", err)
	}

	// 5. Both succeeded
	taskBothSucceeded := &controlplane.Task{
		TaskID: "t-both-ok",
		ReceiptExport: &controlplane.ReceiptExportStatus{
			Local: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
			},
			Central: controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
			},
		},
	}
	if err := taskBothSucceeded.CheckExportAcceptance(controlplane.ExportPolicy{RequireLocal: true, RequireCentral: true}); err != nil {
		t.Errorf("unexpected error when both succeeded: %v", err)
	}
}

func TestRecordReceiptExportUnknownTask(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	err = store.RecordReceiptExport(ctx, "non-existent-task-id", controlplane.ReceiptExportStatus{})
	if err == nil {
		t.Fatal("expected error on RecordReceiptExport for non-existent task")
	}
}
