package worker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/worker"
)

func submitTestTask(t *testing.T, store controlplane.TaskQueueStore, key string, scopeDir string) *controlplane.Task {
	t.Helper()
	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: key,
		Priority:       1,
		MaxAttempts:    1,
		Role:           "test-runner",
		Model:          "test-model",
		AddDirs:        []string{scopeDir},
		Permission:     "read_only",
		Payload:        json.RawMessage(`{"prompt":"test export"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	return task
}

func finishTaskAttempt(t *testing.T, store controlplane.ControlPlaneRepository, taskID string) {
	t.Helper()
	ctx := context.Background()
	claimed, err := store.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if !store.StartTask(claimed.TaskID, "worker-1", *claimed.LeaseToken) {
		t.Fatal("StartTask failed")
	}
	_, err = store.FinishAttempt(claimed.TaskID, "worker-1", *claimed.LeaseToken, controlplane.FinishAttemptParams{
		Result:  json.RawMessage(`{"ok":true,"verdict":"PASS"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
}

func TestWorkerReceiptExport_Success(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	evidenceDir := filepath.Join(tempDir, "evidence")
	runBaseDir := filepath.Join(tempDir, "runs")
	sup := worker.NewSupervisor(store, runBaseDir, worker.WithEvidenceDir(evidenceDir))

	task := submitTestTask(t, store, "export-success", tempDir)
	finishTaskAttempt(t, store, task.TaskID)

	runDir := filepath.Join(runBaseDir, task.TaskID, "attempt-1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	status := sup.ExportReceiptDetailed(ctx, task.TaskID, runDir)

	if status.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Local.Status = %s, want %s (err: %s)", status.Local.Status, controlplane.ExportStatusSucceeded, status.Local.Error)
	}
	if status.Local.Digest == "" {
		t.Error("expected non-empty Local.Digest")
	}
	if status.Central.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Central.Status = %s, want %s (err: %s)", status.Central.Status, controlplane.ExportStatusSucceeded, status.Central.Error)
	}
	if status.Central.Digest != status.Local.Digest {
		t.Errorf("Central.Digest (%s) != Local.Digest (%s)", status.Central.Digest, status.Local.Digest)
	}
	if !status.Success {
		t.Error("expected status.Success == true")
	}
	if !status.FullyEvidenced {
		t.Error("expected status.FullyEvidenced == true")
	}

	// Verify task in controlplane reflects export status
	reloaded, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if reloaded.ReceiptExport == nil {
		t.Fatal("expected non-nil ReceiptExport in reloaded task")
	}
	if reloaded.ReceiptExport.Local.Digest != status.Local.Digest {
		t.Errorf("reloaded digest = %s, want %s", reloaded.ReceiptExport.Local.Digest, status.Local.Digest)
	}
}

func TestWorkerReceiptExport_DisabledCentral(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	runBaseDir := filepath.Join(tempDir, "runs")
	// Explicitly disable evidence dir
	sup := worker.NewSupervisor(store, runBaseDir, worker.WithEvidenceDir(""))

	task := submitTestTask(t, store, "export-disabled", tempDir)
	finishTaskAttempt(t, store, task.TaskID)

	runDir := filepath.Join(runBaseDir, task.TaskID, "attempt-1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	status := sup.ExportReceiptDetailed(ctx, task.TaskID, runDir)

	if status.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Local.Status = %s, want %s", status.Local.Status, controlplane.ExportStatusSucceeded)
	}
	if status.Central.Status != controlplane.ExportStatusDisabled {
		t.Errorf("Central.Status = %s, want %s", status.Central.Status, controlplane.ExportStatusDisabled)
	}
	if !status.Success {
		t.Error("expected status.Success == true when central is disabled")
	}
	if !status.FullyEvidenced {
		t.Error("expected status.FullyEvidenced == true when central is disabled and local succeeded")
	}
}

func TestWorkerReceiptExport_LocalOnlyFailure(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	evidenceDir := filepath.Join(tempDir, "evidence")
	runBaseDir := filepath.Join(tempDir, "runs")
	sup := worker.NewSupervisor(store, runBaseDir, worker.WithEvidenceDir(evidenceDir))

	task := submitTestTask(t, store, "export-local-fail", tempDir)
	finishTaskAttempt(t, store, task.TaskID)

	// Induce failure: place a regular file where the run directory is expected
	blockingFile := filepath.Join(tempDir, "blocking_regular_file")
	if err := os.WriteFile(blockingFile, []byte("block"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}

	status := sup.ExportReceiptDetailed(ctx, task.TaskID, blockingFile)

	if status.Local.Status != controlplane.ExportStatusFailed {
		t.Errorf("Local.Status = %s, want %s", status.Local.Status, controlplane.ExportStatusFailed)
	}
	if status.Local.Category != "write_failed" {
		t.Errorf("Local.Category = %s, want write_failed", status.Local.Category)
	}
	// Verify no absolute host paths in error string
	if strings.Contains(status.Local.Error, "/Users/") || strings.Contains(status.Local.Error, "/home/") || strings.Contains(status.Local.Error, tempDir) {
		t.Errorf("Local.Error leaked raw absolute path: %s", status.Local.Error)
	}

	// Central should have succeeded independently
	if status.Central.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Central.Status = %s, want %s (err: %s)", status.Central.Status, controlplane.ExportStatusSucceeded, status.Central.Error)
	}
	if status.Success {
		t.Error("expected status.Success == false when local failed")
	}

	// Execution state must be preserved
	reloaded, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if reloaded.State != controlplane.StateWorkerCompleted {
		t.Errorf("State modified: %s, want %s", reloaded.State, controlplane.StateWorkerCompleted)
	}
}

func TestWorkerReceiptExport_CentralOnlyFailure(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	evidenceDir := filepath.Join(tempDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("mkdir evidenceDir: %v", err)
	}
	runBaseDir := filepath.Join(tempDir, "runs")
	sup := worker.NewSupervisor(store, runBaseDir, worker.WithEvidenceDir(evidenceDir))

	task := submitTestTask(t, store, "export-central-fail", tempDir)
	finishTaskAttempt(t, store, task.TaskID)

	runDir := filepath.Join(runBaseDir, task.TaskID, "attempt-1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	// Induce failure: place a regular file at the expected task evidence directory path
	blockedEvidenceDir := filepath.Join(evidenceDir, task.TaskID)
	if err := os.WriteFile(blockedEvidenceDir, []byte("regular file blocking mkdir"), 0o600); err != nil {
		t.Fatalf("write blockedEvidenceDir: %v", err)
	}

	status := sup.ExportReceiptDetailed(ctx, task.TaskID, runDir)

	// Local must succeed
	if status.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Local.Status = %s, want %s", status.Local.Status, controlplane.ExportStatusSucceeded)
	}

	// Central must fail independently
	if status.Central.Status != controlplane.ExportStatusFailed {
		t.Errorf("Central.Status = %s, want %s", status.Central.Status, controlplane.ExportStatusFailed)
	}
	if status.Central.Category != "mkdir_failed" {
		t.Errorf("Central.Category = %s, want mkdir_failed", status.Central.Category)
	}
	// Verify no absolute host paths in error string
	if strings.Contains(status.Central.Error, "/Users/") || strings.Contains(status.Central.Error, "/home/") || strings.Contains(status.Central.Error, tempDir) {
		t.Errorf("Central.Error leaked raw absolute path: %s", status.Central.Error)
	}

	if status.Success {
		t.Error("expected status.Success == false when central failed")
	}
	if status.FullyEvidenced {
		t.Error("expected status.FullyEvidenced == false when central failed")
	}

	// Execution state must remain WORKER_COMPLETED
	reloaded, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if reloaded.State != controlplane.StateWorkerCompleted {
		t.Errorf("State = %s, want WORKER_COMPLETED", reloaded.State)
	}
}

func TestWorkerReceiptExport_BothFailed(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	evidenceDir := filepath.Join(tempDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("mkdir evidenceDir: %v", err)
	}
	runBaseDir := filepath.Join(tempDir, "runs")
	sup := worker.NewSupervisor(store, runBaseDir, worker.WithEvidenceDir(evidenceDir))

	task := submitTestTask(t, store, "export-both-fail", tempDir)
	finishTaskAttempt(t, store, task.TaskID)

	// Induce both failures with regular files
	badRunDir := filepath.Join(tempDir, "bad_run_dir")
	if err := os.WriteFile(badRunDir, []byte("block run"), 0o600); err != nil {
		t.Fatalf("write badRunDir: %v", err)
	}
	badEvidenceDir := filepath.Join(evidenceDir, task.TaskID)
	if err := os.WriteFile(badEvidenceDir, []byte("block evidence"), 0o600); err != nil {
		t.Fatalf("write badEvidenceDir: %v", err)
	}

	status := sup.ExportReceiptDetailed(ctx, task.TaskID, badRunDir)

	if status.Local.Status != controlplane.ExportStatusFailed {
		t.Errorf("Local.Status = %s, want %s", status.Local.Status, controlplane.ExportStatusFailed)
	}
	if status.Central.Status != controlplane.ExportStatusFailed {
		t.Errorf("Central.Status = %s, want %s", status.Central.Status, controlplane.ExportStatusFailed)
	}
	if status.Success {
		t.Error("expected status.Success == false when both failed")
	}
}
