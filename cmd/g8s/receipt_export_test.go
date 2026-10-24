package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestReceiptExportCLI_SuccessAndIdempotency(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "cli-export-01",
		Priority:       1,
		MaxAttempts:    1,
		Role:           "test-runner",
		Model:          "test-model",
		AddDirs:        []string{tempDir},
		Permission:     "read_only",
		Payload:        json.RawMessage(`{"prompt":"test receipt export cli"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// Claim and finish attempt
	claimed, err := store.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if !store.StartTask(claimed.TaskID, "worker-1", *claimed.LeaseToken) {
		t.Fatal("StartTask failed")
	}
	_, err = store.FinishAttempt(claimed.TaskID, "worker-1", *claimed.LeaseToken, controlplane.FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"ok","output":"completed"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	completedTask, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	origState := completedTask.State
	origResult := string(completedTask.Result)
	origReceiptHash := completedTask.ReceiptHash

	runDir := filepath.Join(tempDir, "runs", task.TaskID, "attempt-1")
	evidenceDir := filepath.Join(tempDir, "evidence")

	// 1. First invocation: exports to both destinations
	runReceiptExport([]string{
		"--task", task.TaskID,
		"--run-dir", runDir,
		"--evidence-dir", evidenceDir,
		"--json",
	})

	// Verify files created
	localReceiptPath := filepath.Join(runDir, "receipt.json")
	localBytes, err := os.ReadFile(localReceiptPath)
	if err != nil {
		t.Fatalf("read local receipt: %v", err)
	}
	centralReceiptPath := filepath.Join(evidenceDir, task.TaskID, "receipt.json")
	centralBytes, err := os.ReadFile(centralReceiptPath)
	if err != nil {
		t.Fatalf("read central receipt: %v", err)
	}

	if string(localBytes) != string(centralBytes) {
		t.Fatal("local and central receipt bytes must be identical")
	}

	h := sha256.Sum256(localBytes)
	initialDigest := hex.EncodeToString(h[:])

	// Verify task in DB has ReceiptExport set
	tAfter1, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if tAfter1.ReceiptExport == nil {
		t.Fatal("expected non-nil ReceiptExport")
	}
	if tAfter1.ReceiptExport.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Local.Status = %s, want succeeded", tAfter1.ReceiptExport.Local.Status)
	}
	if tAfter1.ReceiptExport.Central.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Central.Status = %s, want succeeded", tAfter1.ReceiptExport.Central.Status)
	}
	if tAfter1.ReceiptExport.Local.Digest != initialDigest {
		t.Errorf("Local.Digest = %s, want %s", tAfter1.ReceiptExport.Local.Digest, initialDigest)
	}

	// 2. Second invocation: Idempotency test (verifies and preserves files)
	runReceiptExport([]string{
		"--task", task.TaskID,
		"--run-dir", runDir,
		"--evidence-dir", evidenceDir,
		"--json",
	})

	localBytes2, err := os.ReadFile(localReceiptPath)
	if err != nil {
		t.Fatalf("read local receipt 2: %v", err)
	}
	if string(localBytes2) != string(localBytes) {
		t.Fatal("local receipt bytes must remain identical on retry")
	}

	// Execution invariants must be completely preserved
	tAfter2, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask 2: %v", err)
	}
	if tAfter2.State != origState {
		t.Errorf("State modified: %s != %s", tAfter2.State, origState)
	}
	if string(tAfter2.Result) != origResult {
		t.Errorf("Result modified: %s != %s", string(tAfter2.Result), origResult)
	}
	if (origReceiptHash == nil && tAfter2.ReceiptHash != nil) ||
		(origReceiptHash != nil && (tAfter2.ReceiptHash == nil || *origReceiptHash != *tAfter2.ReceiptHash)) {
		t.Errorf("ReceiptHash modified: %v != %v", tAfter2.ReceiptHash, origReceiptHash)
	}
}

func TestReceiptExportCLI_RepairWorkflow(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "cli-repair-01",
		Priority:       1,
		MaxAttempts:    1,
		Role:           "test-runner",
		Model:          "test-model",
		AddDirs:        []string{tempDir},
		Permission:     "read_only",
		Payload:        json.RawMessage(`{"prompt":"test repair workflow"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	claimed, err := store.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if !store.StartTask(claimed.TaskID, "worker-1", *claimed.LeaseToken) {
		t.Fatal("StartTask failed")
	}
	_, err = store.FinishAttempt(claimed.TaskID, "worker-1", *claimed.LeaseToken, controlplane.FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"ok"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	runDir := filepath.Join(tempDir, "runs", task.TaskID, "attempt-1")
	evidenceDir := filepath.Join(tempDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("mkdir evidenceDir: %v", err)
	}

	// Induce central failure by blocking taskEvidenceDir with a regular file
	blockedEvidenceDir := filepath.Join(evidenceDir, task.TaskID)
	if err := os.WriteFile(blockedEvidenceDir, []byte("regular file blocking directory"), 0o600); err != nil {
		t.Fatalf("write blockedEvidenceDir: %v", err)
	}

	// Run initial export (central fails, local succeeds)
	runReceiptExport([]string{
		"--task", task.TaskID,
		"--run-dir", runDir,
		"--evidence-dir", evidenceDir,
		"--require-central=false",
		"--json",
	})

	tFail, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if tFail.ReceiptExport.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("Local.Status = %s, want succeeded", tFail.ReceiptExport.Local.Status)
	}
	if tFail.ReceiptExport.Central.Status != controlplane.ExportStatusFailed {
		t.Errorf("Central.Status = %s, want failed", tFail.ReceiptExport.Central.Status)
	}
	if strings.Contains(tFail.ReceiptExport.Central.Error, "/Users/") || strings.Contains(tFail.ReceiptExport.Central.Error, tempDir) {
		t.Errorf("Central.Error contains absolute private path: %s", tFail.ReceiptExport.Central.Error)
	}

	// Repair condition: remove the blocking file
	if err := os.Remove(blockedEvidenceDir); err != nil {
		t.Fatalf("remove blocking file: %v", err)
	}

	// Retry receipt export!
	runReceiptExport([]string{
		"--task", task.TaskID,
		"--run-dir", runDir,
		"--evidence-dir", evidenceDir,
		"--json",
	})

	tRepaired, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask after repair: %v", err)
	}
	if tRepaired.ReceiptExport.Local.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("repaired Local.Status = %s, want succeeded", tRepaired.ReceiptExport.Local.Status)
	}
	if tRepaired.ReceiptExport.Central.Status != controlplane.ExportStatusSucceeded {
		t.Errorf("repaired Central.Status = %s, want succeeded", tRepaired.ReceiptExport.Central.Status)
	}
	if tRepaired.ReceiptExport.Central.Digest != tRepaired.ReceiptExport.Local.Digest {
		t.Errorf("repaired Central.Digest (%s) != Local.Digest (%s)", tRepaired.ReceiptExport.Central.Digest, tRepaired.ReceiptExport.Local.Digest)
	}
	if !tRepaired.ReceiptExport.Success {
		t.Error("expected tRepaired.ReceiptExport.Success == true")
	}
	if !tRepaired.ReceiptExport.FullyEvidenced {
		t.Error("expected tRepaired.ReceiptExport.FullyEvidenced == true")
	}
}

func TestCanonicalGetTaskReflectsReceiptExport(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "get-reflect-01",
		Priority:       1,
		MaxAttempts:    1,
		Role:           "test-runner",
		Model:          "test-model",
		AddDirs:        []string{tempDir},
		Payload:        json.RawMessage(`{"prompt":"test canonical get"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// Capture stdout of runGet before export
	var bufBefore bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runGet([]string{task.TaskID, "--json"})

	_ = w.Close()
	os.Stdout = oldStdout
	_, _ = bufBefore.ReadFrom(r)

	var envBefore cli.Envelope
	if err := json.Unmarshal(bufBefore.Bytes(), &envBefore); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}

	// Record an export status
	now := float64(1700000000)
	if err := store.RecordReceiptExport(ctx, task.TaskID, controlplane.ReceiptExportStatus{
		Local: controlplane.ExportDestinationStatus{
			Status: controlplane.ExportStatusSucceeded,
			Time:   now,
			Digest: "digest-12345",
			Path:   "receipt.json",
		},
		Central: controlplane.ExportDestinationStatus{
			Status: controlplane.ExportStatusDisabled,
			Time:   now,
		},
		UpdatedAt: now,
		Success:   true,
	}); err != nil {
		t.Fatalf("RecordReceiptExport: %v", err)
	}

	// Capture stdout of runGet after export
	var bufAfter bytes.Buffer
	r2, w2, _ := os.Pipe()
	os.Stdout = w2

	runGet([]string{task.TaskID, "--json"})

	_ = w2.Close()
	os.Stdout = oldStdout
	_, _ = bufAfter.ReadFrom(r2)

	var envAfter cli.Envelope
	if err := json.Unmarshal(bufAfter.Bytes(), &envAfter); err != nil {
		t.Fatalf("unmarshal get response after: %v", err)
	}

	taskBytes, _ := json.Marshal(envAfter.Data)
	var gotTask controlplane.Task
	if err := json.Unmarshal(taskBytes, &gotTask); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}

	if gotTask.ReceiptExport == nil {
		t.Fatal("expected gotTask.ReceiptExport to be present in g8s get output")
	}
	if gotTask.ReceiptExport.Local.Digest != "digest-12345" {
		t.Errorf("got digest = %s, want digest-12345", gotTask.ReceiptExport.Local.Digest)
	}
}
