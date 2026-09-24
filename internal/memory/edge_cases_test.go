package memory

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/vault"
)

func TestAdapter_EdgeCases_NilGuards(t *testing.T) {
	ctx := context.Background()
	// Bare adapter without sub-services
	adapter := &LocalSQLiteMemoryAdapter{
		clock: time.Now,
	}

	// 1. StoreKnowledge nil guards
	if err := adapter.StoreKnowledge(ctx, nil); err == nil {
		t.Errorf("expected error when vault is nil")
	}

	// 2. SearchKnowledge guards
	if _, err := adapter.SearchKnowledge(ctx, "query", 10); err == nil {
		t.Errorf("expected error when vault is nil")
	}

	// 3. ValidateCapability guards
	if _, err := adapter.ValidateCapability(ctx, "rc-1", "task-1"); err == nil {
		t.Errorf("expected error when receiptManager is nil")
	}
	if _, err := adapter.ValidateCapability(ctx, "", "task-1"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty receiptID")
	}

	// 4. WorkingContext guards
	if err := adapter.StoreWorkingContext(ctx, "", nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty taskID")
	}
	if _, err := adapter.LoadWorkingContext(ctx, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty taskID in LoadWorkingContext")
	}
	if err := adapter.PurgeWorkingContext(ctx, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty taskID in PurgeWorkingContext")
	}

	// 5. Episode guards
	if err := adapter.RecordEpisode(ctx, "", nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty taskID in RecordEpisode")
	}
	if _, err := adapter.GetLineageTree(ctx, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty taskID in GetLineageTree")
	}

	// 6. Vector guards
	if err := adapter.StoreVector(ctx, "", nil, nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty vector in StoreVector")
	}
	if _, err := adapter.SearchVector(ctx, nil, 10, 0.0); !errors.Is(err, ErrEmptyVector) {
		t.Errorf("expected ErrEmptyVector for nil queryVector")
	}
}

func TestAdapter_Fallback_And_Defaults(t *testing.T) {
	ctx := context.Background()

	// In-memory adapter with empty DBPath
	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath: "", // Should default to :memory:
	})
	if err != nil {
		t.Fatalf("failed to create memory adapter: %v", err)
	}
	defer adapter.Close()

	// Test SearchKnowledge with empty query -> returns nil, nil
	dummyVault, _ := vault.NewVault(filepath.Join(t.TempDir(), "v.db"), time.Now)
	adapter.vault = dummyVault
	defer dummyVault.Close()

	results, err := adapter.SearchKnowledge(ctx, "", 0)
	if err != nil || results != nil {
		t.Errorf("expected nil, nil for empty query, got (%v, %v)", results, err)
	}

	// Test StoreKnowledge with nil record
	if err := adapter.StoreKnowledge(ctx, nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for nil record, got %v", err)
	}

	// Test SearchKnowledge with default limit <= 0
	err = adapter.StoreKnowledge(ctx, &vault.DistillationRecord{
		ID:    "FALLBACK-1",
		Title: "Fallback Record",
		Causality: vault.CausalityAnchor{
			Problem:  "Fallback problem",
			TradeOff: "Fallback trade off",
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package: "internal/memory",
			File:    "adapter.go",
		},
	})
	if err != nil {
		t.Fatalf("StoreKnowledge failed: %v", err)
	}
	res, err := adapter.SearchKnowledge(ctx, "Fallback", -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) == 0 {
		t.Errorf("expected at least 1 result with default limit")
	}

	// Test GetLineageTree fallback when controlplane is nil
	// Case A: Task not found in working contexts
	dag, err := adapter.GetLineageTree(ctx, "nonexistent-task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dag.TotalNodes != 0 {
		t.Errorf("expected 0 total nodes for nonexistent task in fallback")
	}

	// Case B: Task exists in working contexts
	_ = adapter.StoreWorkingContext(ctx, "fallback-wm-task", &WorkingContext{
		TaskID: "fallback-wm-task",
		Role:   "scout",
		Prompt: "Scan files",
		Status: StatusActive,
	})
	dag, err = adapter.GetLineageTree(ctx, "fallback-wm-task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dag.TotalNodes != 1 || dag.Nodes[0].Role != "scout" {
		t.Errorf("expected 1 node with role scout, got %v", dag)
	}
}

func TestAdapter_GetLineageTree_ExitCodes(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	wName := "worker"
	// Create tasks with SUCCEEDED and FAILED states
	pTask, err := adapter.controlPlane.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "p-succ-idemp",
		Payload:        []byte(`{"prompt":"Success prompt"}`),
		WorkerName:     &wName,
		Model:          "test-model",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	cTask, err := adapter.controlPlane.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "c-fail-idemp",
		Payload:        []byte(`{"prompt":"Fail prompt"}`),
		ParentTaskID:   &pTask.TaskID,
		WorkerName:     &wName,
		Model:          "test-model",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Claim, Start and Complete parent task to SUCCEEDED
	claimedP, err := adapter.controlPlane.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("claim parent task failed: %v", err)
	}
	if !adapter.controlPlane.StartTask(claimedP.TaskID, *claimedP.LeaseOwner, *claimedP.LeaseToken) {
		t.Fatalf("StartTask parent failed")
	}
	_, err = adapter.controlPlane.FinishAttempt(claimedP.TaskID, *claimedP.LeaseOwner, *claimedP.LeaseToken, controlplane.FinishAttemptParams{
		Result:  []byte(`{}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt parent failed: %v", err)
	}

	// Claim, Start and Fail child task to FAILED
	claimedC, err := adapter.controlPlane.ClaimTask(ctx, "worker-2", 60)
	if err != nil {
		t.Fatalf("claim child task failed: %v", err)
	}
	if !adapter.controlPlane.StartTask(claimedC.TaskID, *claimedC.LeaseOwner, *claimedC.LeaseToken) {
		t.Fatalf("StartTask child failed")
	}
	_, err = adapter.controlPlane.FinishAttempt(claimedC.TaskID, *claimedC.LeaseOwner, *claimedC.LeaseToken, controlplane.FinishAttemptParams{
		Result:  []byte(`{}`),
		Success: false,
	})
	if err != nil {
		t.Fatalf("FinishAttempt child failed: %v", err)
	}
	_ = adapter.controlPlane.RejectResult(ctx, claimedC.TaskID, "supervisor", "test error", "")

	dag, err := adapter.GetLineageTree(ctx, cTask.TaskID)
	if err != nil {
		t.Fatalf("GetLineageTree failed: %v", err)
	}

	for _, n := range dag.Nodes {
		t.Logf("Node: %s Status: %s ExitCode: %v", n.TaskID, n.Status, n.ExitCode)
		if n.TaskID == pTask.TaskID {
			if n.ExitCode == nil || *n.ExitCode != 0 {
				t.Errorf("expected exit code 0 for succeeded task %s, got status %s exitCode %v", n.TaskID, n.Status, n.ExitCode)
			}
		}
		if n.TaskID == cTask.TaskID {
			if n.ExitCode == nil || *n.ExitCode != 1 {
				t.Errorf("expected exit code 1 for failed task %s, got status %s exitCode %v", n.TaskID, n.Status, n.ExitCode)
			}
		}
	}
}

func TestAdapter_PurgeWorkingContext_AlreadyDigest(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	taskID := "task-pre-digest"
	wCtx := &WorkingContext{
		TaskID:         taskID,
		Prompt:         "some prompt",
		RedactedDigest: "sha256:pre-existing-digest-123",
		Status:         StatusActive,
	}

	if err := adapter.StoreWorkingContext(ctx, taskID, wCtx); err != nil {
		t.Fatalf("StoreWorkingContext failed: %v", err)
	}

	if err := adapter.PurgeWorkingContext(ctx, taskID); err != nil {
		t.Fatalf("PurgeWorkingContext failed: %v", err)
	}

	loaded, err := adapter.LoadWorkingContext(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadWorkingContext failed: %v", err)
	}
	if loaded.RedactedDigest != "sha256:pre-existing-digest-123" {
		t.Errorf("expected pre-existing digest to be preserved, got %q", loaded.RedactedDigest)
	}
}

func TestAdapter_NewAdapter_InvalidPath(t *testing.T) {
	// Attempt to create database in non-existent directory
	_, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath: "/nonexistent/invalid/dir/db.sqlite",
	})
	if err == nil {
		t.Errorf("expected error creating adapter in invalid directory, got nil")
	}
}

func TestHybridRetrieve_CanceledContext(t *testing.T) {
	adapter, _ := setupTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := adapter.HybridRetrieve(ctx, HybridQuery{
		Query: "search",
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestAdapter_GetLineageTree_WithChildren(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	wName := "parent-worker"
	pTask, err := adapter.controlPlane.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "p-tree-parent",
		Payload:        []byte(`{"prompt":"parent task"}`),
		WorkerName:     &wName,
		Model:          "test-model",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit parent failed: %v", err)
	}

	cTask, err := adapter.controlPlane.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "p-tree-child",
		Payload:        []byte(`{"prompt":"child task"}`),
		ParentTaskID:   &pTask.TaskID,
		WorkerName:     &wName,
		Model:          "test-model",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit child failed: %v", err)
	}

	// Query from root parent perspective to populate children nodes
	dag, err := adapter.GetLineageTree(ctx, pTask.TaskID)
	if err != nil {
		t.Fatalf("GetLineageTree on parent failed: %v", err)
	}
	if len(dag.Nodes) < 2 {
		t.Errorf("expected at least 2 nodes, got %d", len(dag.Nodes))
	}

	// Verify target node has child attached
	var target *LineageNode
	for _, n := range dag.Nodes {
		if n.TaskID == pTask.TaskID {
			target = n
			break
		}
	}
	if target == nil || len(target.Children) == 0 {
		t.Errorf("expected parent node to have children attached, got %v", target)
	}
	if target.Children[0].TaskID != cTask.TaskID {
		t.Errorf("expected child task %s, got %s", cTask.TaskID, target.Children[0].TaskID)
	}
}

func TestAdapter_SearchVector_DimensionMismatch(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	// Store vector with 2 dims
	_ = adapter.StoreVector(ctx, "vec-2d", []float32{1.0, 0.0}, nil)
	// Store vector with 3 dims
	_ = adapter.StoreVector(ctx, "vec-3d", []float32{1.0, 0.0, 0.0}, map[string]any{"note": "3 dimensions"})

	// Query with 2 dims: should match vec-2d and skip vec-3d gracefully
	matches, err := adapter.SearchVector(ctx, []float32{1.0, 0.0}, 5, 0.0)
	if err != nil {
		t.Fatalf("SearchVector failed: %v", err)
	}
	if len(matches) != 1 || matches[0].RecordID != "vec-2d" {
		t.Errorf("expected 1 match for vec-2d, got %v", matches)
	}
}

func TestWorkingMemory_Upsert_Update(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	taskID := "task-upsert-1"
	_ = adapter.StoreWorkingContext(ctx, taskID, &WorkingContext{
		TaskID: taskID,
		Prompt: "first prompt",
		Role:   "scout",
	})

	// Upsert with new prompt
	err := adapter.StoreWorkingContext(ctx, taskID, &WorkingContext{
		TaskID: taskID,
		Prompt: "updated prompt",
		Role:   "scout",
	})
	if err != nil {
		t.Fatalf("upsert StoreWorkingContext failed: %v", err)
	}

	loaded, err := adapter.LoadWorkingContext(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadWorkingContext failed: %v", err)
	}
	if loaded.Prompt != "updated prompt" {
		t.Errorf("got prompt %q, want updated prompt", loaded.Prompt)
	}
}

func TestWorkingContext_RawPrompt(t *testing.T) {
	// 1. Nil receiver
	var nilCtx *WorkingContext
	if _, err := nilCtx.RawPrompt(); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for nil receiver, got %v", err)
	}

	// 2. Active status
	activeCtx := &WorkingContext{
		Prompt: "active prompt",
		Status: StatusActive,
	}
	prompt, err := activeCtx.RawPrompt()
	if err != nil || prompt != "active prompt" {
		t.Errorf("expected 'active prompt', got (%q, %v)", prompt, err)
	}

	// 3. Purged status
	purgedCtx := &WorkingContext{
		Prompt: "",
		Status: StatusPurged,
	}
	_, err = purgedCtx.RawPrompt()
	if !errors.Is(err, ErrContextPurged) {
		t.Errorf("expected ErrContextPurged, got %v", err)
	}
}

func TestVectorMath_Adversarial_NaN_And_Underflow(t *testing.T) {
	// 1. Cosine similarity with NaN
	nanVec := []float32{float32(math.NaN()), 1.0}
	normalVec := []float32{1.0, 1.0}
	sim, err := CosineSimilarity(nanVec, normalVec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sim != 0.0 {
		t.Errorf("expected 0.0 for NaN vector, got %v", sim)
	}

	// 2. Underflow in NormalizeVector
	tinyVec := []float32{1e-39, 1e-39}
	normed, err := NormalizeVector(tinyVec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, v := range normed {
		if math.IsInf(float64(v), 0) || math.IsNaN(float64(v)) {
			t.Errorf("NormalizeVector produced non-finite value: %v", v)
		}
	}
}

func TestAdapter_NewAdapter_PreExistingFilePermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "preexisting.db")

	// Create file with 0644
	if err := os.WriteFile(dbPath, []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write preexisting file: %v", err)
	}

	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath: dbPath,
	})
	if err != nil {
		t.Fatalf("failed to open adapter on preexisting file: %v", err)
	}
	defer adapter.Close()

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected 0600 on preexisting file, got %04o", perm)
	}
}

func TestSearchVector_DimensionFilterAndDeferredJSON(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	// Store vector with 2 dimensions
	_ = adapter.StoreVector(ctx, "rec-2d", []float32{1.0, 0.0}, map[string]any{"label": "2d"})

	// Store vector with 3 dimensions
	_ = adapter.StoreVector(ctx, "rec-3d", []float32{1.0, 0.0, 0.0}, map[string]any{"label": "3d"})

	// Query with 3 dimensions
	matches, err := adapter.SearchVector(ctx, []float32{1.0, 0.0, 0.0}, 10, 0.5)
	if err != nil {
		t.Fatalf("SearchVector failed: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 match for 3D query, got %d", len(matches))
	}
	if matches[0].RecordID != "rec-3d" {
		t.Errorf("expected 'rec-3d', got %q", matches[0].RecordID)
	}
	if matches[0].Metadata["label"] != "3d" {
		t.Errorf("expected metadata label '3d', got %v", matches[0].Metadata)
	}
}
