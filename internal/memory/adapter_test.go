package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/vault"
)

func setupTestAdapter(t *testing.T) (*LocalSQLiteMemoryAdapter, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	cpDB := filepath.Join(dir, "controlplane.db")
	cp, err := controlplane.NewControlPlane(cpDB, time.Now)
	if err != nil {
		t.Fatalf("failed to init controlplane: %v", err)
	}
	t.Cleanup(func() { _ = cp.Close() })

	vDB := filepath.Join(dir, "vault.db")
	v, err := vault.NewVault(vDB, time.Now)
	if err != nil {
		t.Fatalf("failed to init vault: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })

	rcDB := filepath.Join(dir, "receipts.db")
	rc, err := receipt.NewReceiptManager(rcDB, time.Now)
	if err != nil {
		t.Fatalf("failed to init receipt manager: %v", err)
	}
	t.Cleanup(func() { _ = rc.Close() })

	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath:         dbPath,
		ControlPlane:   cp,
		Vault:          v,
		ReceiptManager: rc,
		Clock:          time.Now,
	})
	if err != nil {
		t.Fatalf("failed to create memory adapter: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	return adapter, dbPath
}

func TestWorkingMemory_CRUD_And_Redaction(t *testing.T) {
	adapter, dbPath := setupTestAdapter(t)
	ctx := context.Background()

	// Verify POSIX 0600 file permissions
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("failed to stat db file: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("expected 0600 file permissions, got %04o", perm)
	}

	taskID := "task-wm-001"
	rawPrompt := "Implement Pure-Go Decoupled Memory Layer with high security constraints"
	wCtx := &WorkingContext{
		TaskID:       taskID,
		Prompt:       rawPrompt,
		Role:         "refactor",
		AllowedPaths: []string{"internal/memory/*", "spec/openspec/21*"},
		Status:       StatusActive,
	}

	// 1. Store
	if err := adapter.StoreWorkingContext(ctx, taskID, wCtx); err != nil {
		t.Fatalf("StoreWorkingContext failed: %v", err)
	}

	// 2. Load
	loaded, err := adapter.LoadWorkingContext(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadWorkingContext failed: %v", err)
	}
	if loaded.Prompt != rawPrompt {
		t.Errorf("got prompt %q, want %q", loaded.Prompt, rawPrompt)
	}
	if loaded.Status != StatusActive {
		t.Errorf("got status %v, want %v", loaded.Status, StatusActive)
	}
	if len(loaded.AllowedPaths) != 2 {
		t.Errorf("got %d allowed paths, want 2", len(loaded.AllowedPaths))
	}

	// 3. Purge & Verify Zero-Leak Redaction
	expectedDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(rawPrompt)))
	if err := adapter.PurgeWorkingContext(ctx, taskID); err != nil {
		t.Fatalf("PurgeWorkingContext failed: %v", err)
	}

	purged, err := adapter.LoadWorkingContext(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadWorkingContext after purge failed: %v", err)
	}
	if purged.Prompt != "" {
		t.Errorf("expected empty prompt after purge, got %q", purged.Prompt)
	}
	if purged.Status != StatusPurged {
		t.Errorf("expected status %v, got %v", StatusPurged, purged.Status)
	}
	if purged.RedactedDigest != expectedDigest {
		t.Errorf("got digest %q, want %q", purged.RedactedDigest, expectedDigest)
	}
}

func TestEpisodicMemory_Events_And_Lineage(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	// Create ancestor & child task in controlplane
	workerArchitect := "architect"
	parentReq := controlplane.SubmitTaskRequest{
		IdempotencyKey: "parent-idemp-1",
		Payload:        []byte(`{"prompt":"Architecture task"}`),
		WorkerName:     &workerArchitect,
		Model:          "test-model",
		AddDirs:        []string{"."},
	}
	parentTask, err := adapter.controlPlane.SubmitTask(ctx, parentReq)
	if err != nil {
		t.Fatalf("submit parent task failed: %v", err)
	}

	workerCoder := "coder"
	childReq := controlplane.SubmitTaskRequest{
		IdempotencyKey: "child-idemp-1",
		Payload:        []byte(`{"prompt":"Implementation task"}`),
		ParentTaskID:   &parentTask.TaskID,
		WorkerName:     &workerCoder,
		Model:          "test-model",
		AddDirs:        []string{"."},
	}
	childTask, err := adapter.controlPlane.SubmitTask(ctx, childReq)
	if err != nil {
		t.Fatalf("submit child task failed: %v", err)
	}

	// Record an episode
	event := &EpisodicEvent{
		TaskID:    childTask.TaskID,
		EventType: "TASK_STARTED",
		Payload:   map[string]any{"approach": "direct-patch"},
	}
	if err := adapter.RecordEpisode(ctx, childTask.TaskID, event); err != nil {
		t.Fatalf("RecordEpisode failed: %v", err)
	}

	// Get Lineage Tree
	dag, err := adapter.GetLineageTree(ctx, childTask.TaskID)
	if err != nil {
		t.Fatalf("GetLineageTree failed: %v", err)
	}
	if dag.RootTaskID != parentTask.TaskID {
		t.Errorf("got root task %q, want %q", dag.RootTaskID, parentTask.TaskID)
	}
	if dag.TotalNodes < 2 {
		t.Errorf("expected at least 2 nodes, got %d", dag.TotalNodes)
	}
}

func TestSemanticMemory_Vault_Delegation(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	record := &vault.DistillationRecord{
		ID:    "REC-MEM-001",
		Title: "Decoupled Memory Performance Invariant",
		SpatialCoordinates: vault.SpatialAnchor{
			Package: "internal/memory",
			File:    "adapter.go",
			Symbol:  "NewLocalSQLiteMemoryAdapter",
		},
		Causality: vault.CausalityAnchor{
			Problem:   "SQLite lock contention under heavy concurrency",
			TradeOff:  "Memory cache vs immediate WAL consistency",
			RootCause: "Missing busy_timeout pragma",
		},
	}

	if err := adapter.StoreKnowledge(ctx, record); err != nil {
		t.Fatalf("StoreKnowledge failed: %v", err)
	}

	results, err := adapter.SearchKnowledge(ctx, "sqlite lock contention", 5)
	if err != nil {
		t.Fatalf("SearchKnowledge failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results, got 0")
	}
	if results[0].Record.ID != "REC-MEM-001" {
		t.Errorf("got record ID %q, want REC-MEM-001", results[0].Record.ID)
	}
}

func TestCapabilityMemory_Receipt_Delegation(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	env := &receipt.CanonicalEnvelope{
		SchemaURI:      "g8s://envelope/write-receipt/v1",
		FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths"},
		RequiredFields: []string{"receipt_id"},
	}
	rs := &receipt.RuleGraphSnapshot{RulesetVersion: "v1.0.0", PipelineDigest: "deadbeef", ADRRef: "docs/decisions/0019-unified-memory-facade.md"}
	rc, err := adapter.receiptManager.IssueReceipt("brain", []string{"internal/memory/*"}, time.Hour,
		receipt.WithCanonicalEnvelope(env),
		receipt.WithRuleGraph(rs),
		receipt.WithProvenanceContext("g8s/v0.10.0", "b503108", "trace-test"),
	)
	if err != nil {
		t.Fatalf("IssueReceipt failed: %v", err)
	}

	// First consumption: must succeed
	consumed, err := adapter.ValidateCapability(ctx, rc.ReceiptID, "worker-1")
	if err != nil {
		t.Fatalf("ValidateCapability failed on first use: %v", err)
	}
	if consumed.ReceiptID != rc.ReceiptID {
		t.Errorf("got receipt ID %q, want %q", consumed.ReceiptID, rc.ReceiptID)
	}

	// Second consumption: must fail (Single-Use Invariant)
	_, err = adapter.ValidateCapability(ctx, rc.ReceiptID, "worker-2")
	if err == nil {
		t.Fatalf("expected error on second receipt consumption, got nil")
	}
}

func TestVectorMemory_StoreAndSearch(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	// Index 3 vectors
	vec1 := []float32{1.0, 0.0, 0.0}
	vec2 := []float32{0.0, 1.0, 0.0}
	vec3 := []float32{0.7071, 0.7071, 0.0}

	if err := adapter.StoreVector(ctx, "doc-x", vec1, map[string]any{"label": "x-axis"}); err != nil {
		t.Fatalf("StoreVector doc-x failed: %v", err)
	}
	if err := adapter.StoreVector(ctx, "doc-y", vec2, map[string]any{"label": "y-axis"}); err != nil {
		t.Fatalf("StoreVector doc-y failed: %v", err)
	}
	if err := adapter.StoreVector(ctx, "doc-xy", vec3, map[string]any{"label": "diagonal"}); err != nil {
		t.Fatalf("StoreVector doc-xy failed: %v", err)
	}

	// Query with [1.0, 0.0, 0.0]
	queryVec := []float32{1.0, 0.0, 0.0}
	matches, err := adapter.SearchVector(ctx, queryVec, 10, 0.5)
	if err != nil {
		t.Fatalf("SearchVector failed: %v", err)
	}

	if len(matches) != 2 {
		t.Fatalf("expected 2 matches above 0.5 score, got %d", len(matches))
	}

	if matches[0].RecordID != "doc-x" {
		t.Errorf("top match should be doc-x, got %q", matches[0].RecordID)
	}
	if matches[1].RecordID != "doc-xy" {
		t.Errorf("second match should be doc-xy, got %q", matches[1].RecordID)
	}
}

func TestHybridRetrieve(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	// 1. Setup Semantic Knowledge
	_ = adapter.StoreKnowledge(ctx, &vault.DistillationRecord{
		ID:    "HYBRID-001",
		Title: "Hybrid Retrieval Spec",
		SpatialCoordinates: vault.SpatialAnchor{
			Package: "internal/memory",
			File:    "hybrid.go",
			Symbol:  "HybridRetrieve",
		},
		Causality: vault.CausalityAnchor{
			Problem:   "Information fragmentation across multimodal sources",
			TradeOff:  "Latency vs recall",
			RootCause: "Decoupled storage layers",
		},
	})

	// 2. Setup Episodic Task Lineage
	workerRoot := "root-planner"
	pTask, err := adapter.controlPlane.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "hybrid-root-idemp",
		Payload:        []byte(`{"prompt":"Hybrid root prompt"}`),
		WorkerName:     &workerRoot,
		Model:          "test-model",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit hybrid root task failed: %v", err)
	}

	workerSub := "sub-worker"
	cTask, err := adapter.controlPlane.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "hybrid-child-idemp",
		Payload:        []byte(`{"prompt":"Hybrid child prompt"}`),
		ParentTaskID:   &pTask.TaskID,
		WorkerName:     &workerSub,
		Model:          "test-model",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit hybrid child task failed: %v", err)
	}

	// 3. Setup Vector
	_ = adapter.StoreVector(ctx, "vec-hybrid-001", []float32{0.5, 0.5, 0.5}, map[string]any{"source": "hybrid"})

	// Execute Hybrid Retrieve
	res, err := adapter.HybridRetrieve(ctx, HybridQuery{
		Query:    "multimodal information",
		TaskID:   cTask.TaskID,
		Vector:   []float32{0.5, 0.5, 0.5},
		Limit:    5,
		MinScore: 0.1,
	})
	if err != nil {
		t.Fatalf("HybridRetrieve failed: %v", err)
	}

	if len(res.SemanticLessons) == 0 {
		t.Errorf("expected semantic lessons in hybrid result")
	}
	if res.LineageTree == nil || res.LineageTree.RootTaskID != pTask.TaskID {
		t.Errorf("expected lineage tree with root %s, got %v", pTask.TaskID, res.LineageTree)
	}
	if len(res.VectorMatches) == 0 {
		t.Errorf("expected vector matches in hybrid result")
	}
}

func TestConcurrency_ReadWriteRace(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	const workers = 30
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				taskID := fmt.Sprintf("task-race-%d-%d", workerID, i)

				// Write
				err := adapter.StoreWorkingContext(ctx, taskID, &WorkingContext{
					TaskID:       taskID,
					Prompt:       fmt.Sprintf("Concurrent task prompt %d", i),
					Role:         "worker",
					AllowedPaths: []string{"*"},
					Status:       StatusActive,
				})
				if err != nil {
					t.Errorf("StoreWorkingContext worker %d iteration %d failed: %v", workerID, i, err)
					return
				}

				// Read
				_, err = adapter.LoadWorkingContext(ctx, taskID)
				if err != nil {
					t.Errorf("LoadWorkingContext worker %d iteration %d failed: %v", workerID, i, err)
					return
				}

				// Vector write & search
				vecID := fmt.Sprintf("vec-%d-%d", workerID, i)
				_ = adapter.StoreVector(ctx, vecID, []float32{float32(workerID), float32(i)}, nil)
				_, _ = adapter.SearchVector(ctx, []float32{1.0, 1.0}, 2, 0.0)

				// Purge
				_ = adapter.PurgeWorkingContext(ctx, taskID)
			}
		}()
	}

	wg.Wait()
}

func BenchmarkHybridRetrieve(b *testing.B) {
	dir := b.TempDir()
	dbPath := filepath.Join(dir, "memory_bench.db")
	cp, _ := controlplane.NewControlPlane(filepath.Join(dir, "cp.db"), time.Now)
	v, _ := vault.NewVault(filepath.Join(dir, "v.db"), time.Now)
	rc, _ := receipt.NewReceiptManager(filepath.Join(dir, "rc.db"), time.Now)

	adapter, _ := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath:         dbPath,
		ControlPlane:   cp,
		Vault:          v,
		ReceiptManager: rc,
		Clock:          time.Now,
	})
	defer adapter.Close()

	ctx := context.Background()
	_ = adapter.StoreKnowledge(ctx, &vault.DistillationRecord{
		ID:    "BENCH-1",
		Title: "Bench Record",
		SpatialCoordinates: vault.SpatialAnchor{
			Package: "internal/memory",
			File:    "adapter.go",
		},
		Causality: vault.CausalityAnchor{
			Problem: "Bench Problem",
		},
	})
	_ = adapter.StoreVector(ctx, "bench-vec", []float32{1.0, 2.0, 3.0}, nil)

	q := HybridQuery{
		Query:    "Bench",
		Vector:   []float32{1.0, 2.0, 3.0},
		Limit:    5,
		MinScore: 0.5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = adapter.HybridRetrieve(ctx, q)
	}
}
