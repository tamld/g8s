package vault

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestVault(t *testing.T, clock func() time.Time) *Vault {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vault.db")
	v, err := NewVault(dbPath, clock)
	if err != nil {
		t.Fatalf("NewVault failed: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func sampleRecord(id, title string) DistillationRecord {
	return DistillationRecord{
		ID:        id,
		Title:     title,
		Milestone: "v0.1.1",
		Status:    "APPLIED",
		Causality: CausalityAnchor{
			Problem:   "filepath.EvalSymlinks fails on non-existent leaf targets",
			TradeOff:  "Iterate parent directories instead of following recursion",
			RootCause: "Symlink traversal bypass",
		},
		SpatialCoordinates: SpatialAnchor{
			Package:         "internal/harness",
			File:            "internal/harness/harness.go",
			Symbol:          "resolveExistingSymlinks",
			DeniedFragments: []string{"/.ssh", "/.env"},
		},
		ForensicVerification: ForensicAnchor{
			TestFile:     "internal/harness/harness_test.go",
			TestCase:     "TestValidateRequestRejectsNestedNonExistentChildInSymlink",
			ExitCriteria: "PASS",
			ReceiptHash:  "sha256:abc123456",
		},
	}
}

func TestVaultStoreAndGet(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	rec := sampleRecord("DELTA-01-A", "Symlink Gating")
	stored, err := v.Store(ctx, rec)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if stored.ID != "DELTA-01-A" {
		t.Fatalf("expected ID DELTA-01-A, got %s", stored.ID)
	}

	got, err := v.Get(ctx, "DELTA-01-A")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Title != "Symlink Gating" {
		t.Fatalf("expected Title 'Symlink Gating', got %s", got.Title)
	}
	if got.Causality.Problem != rec.Causality.Problem {
		t.Fatalf("expected Causality.Problem matching original, got %s", got.Causality.Problem)
	}
	if got.SpatialCoordinates.Symbol != "resolveExistingSymlinks" {
		t.Fatalf("expected Symbol resolveExistingSymlinks, got %s", got.SpatialCoordinates.Symbol)
	}
}

func TestVaultValidation(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	cases := []struct {
		name string
		rec  DistillationRecord
	}{
		{"missing id", DistillationRecord{Title: "T", Causality: CausalityAnchor{Problem: "P", TradeOff: "O"}}},
		{"missing title", DistillationRecord{ID: "1", Causality: CausalityAnchor{Problem: "P", TradeOff: "O"}}},
		{"missing problem", DistillationRecord{ID: "1", Title: "T", Causality: CausalityAnchor{TradeOff: "O"}}},
		{"missing trade_off", DistillationRecord{ID: "1", Title: "T", Causality: CausalityAnchor{Problem: "P"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Store(ctx, tc.rec)
			if !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("expected ErrInvalidRecord, got %v", err)
			}
		})
	}
}

func TestVaultFTS5BM25Search(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	// Store 3 records
	r1 := sampleRecord("R1", "Symlink Gating")
	r1.Causality.Problem = "Symlink sandbox bypass vulnerability in harness"
	r1.SpatialCoordinates.Symbol = "resolveExistingSymlinks"
	if _, err := v.Store(ctx, r1); err != nil {
		t.Fatalf("store r1: %v", err)
	}

	r2 := sampleRecord("R2", "SQLite WAL Concurrency")
	r2.Causality.Problem = "Database lock contention during parallel worker dispatch"
	r2.Causality.TradeOff = "Use immediate transaction locking mode"
	r2.SpatialCoordinates.Package = "internal/controlplane"
	if _, err := v.Store(ctx, r2); err != nil {
		t.Fatalf("store r2: %v", err)
	}

	r3 := sampleRecord("R3", "PTY Streaming")
	r3.Causality.Problem = "Buffered pipe stdout causes delayed terminal output"
	if _, err := v.Store(ctx, r3); err != nil {
		t.Fatalf("store r3: %v", err)
	}

	// Query 1: "symlink bypass"
	res1, err := v.Query(ctx, "symlink bypass", 10)
	if err != nil {
		t.Fatalf("query symlink bypass: %v", err)
	}
	if len(res1) == 0 {
		t.Fatalf("expected at least 1 match for 'symlink bypass'")
	}
	if res1[0].Record.ID != "R1" {
		t.Fatalf("expected top match R1, got %s", res1[0].Record.ID)
	}

	// Query 2: "immediate transaction locking"
	res2, err := v.Query(ctx, "immediate transaction", 10)
	if err != nil {
		t.Fatalf("query immediate transaction: %v", err)
	}
	if len(res2) == 0 {
		t.Fatalf("expected at least 1 match for 'immediate transaction'")
	}
	if res2[0].Record.ID != "R2" {
		t.Fatalf("expected top match R2, got %s", res2[0].Record.ID)
	}

	// Query 3: Non-matching term
	res3, err := v.Query(ctx, "quantum teleportation", 10)
	if err != nil {
		t.Fatalf("query non-matching: %v", err)
	}
	if len(res3) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(res3))
	}
}

func TestVaultListAndFilter(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		r := sampleRecord(fmt.Sprintf("REC-%d", i), fmt.Sprintf("Title %d", i))
		if i <= 3 {
			r.Milestone = "v0.1.1"
			r.Status = "APPLIED"
			r.SpatialCoordinates.Package = "internal/harness"
		} else {
			r.Milestone = "v0.2.0"
			r.Status = "PROPOSED"
			r.SpatialCoordinates.Package = "internal/controlplane"
		}
		if _, err := v.Store(ctx, r); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
	}

	// Filter by milestone v0.1.1
	m := "v0.1.1"
	listM, err := v.List(ctx, VaultFilter{Milestone: &m})
	if err != nil {
		t.Fatalf("list milestone: %v", err)
	}
	if len(listM) != 3 {
		t.Fatalf("expected 3 records with milestone v0.1.1, got %d", len(listM))
	}

	// Filter by status PROPOSED
	s := "PROPOSED"
	listS, err := v.List(ctx, VaultFilter{Status: &s})
	if err != nil {
		t.Fatalf("list status: %v", err)
	}
	if len(listS) != 2 {
		t.Fatalf("expected 2 records with status PROPOSED, got %d", len(listS))
	}
}

func TestVaultUpdateAndFTSIndexSync(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	rec := sampleRecord("SYNC-1", "Initial Title")
	rec.Causality.Problem = "Original bug description"
	if _, err := v.Store(ctx, rec); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Update problem statement
	rec.Title = "Updated Title"
	rec.Causality.Problem = "Brand new updated vulnerability report"
	if _, err := v.Store(ctx, rec); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Query old term -> 0 matches
	resOld, err := v.Query(ctx, "Original bug", 10)
	if err != nil {
		t.Fatalf("query old: %v", err)
	}
	if len(resOld) != 0 {
		t.Fatalf("expected 0 matches for old term, got %d", len(resOld))
	}

	// Query new term -> 1 match
	resNew, err := v.Query(ctx, "vulnerability report", 10)
	if err != nil {
		t.Fatalf("query new: %v", err)
	}
	if len(resNew) != 1 || resNew[0].Record.ID != "SYNC-1" {
		t.Fatalf("expected 1 match for new term, got %+v", resNew)
	}
}

func TestVaultDelete(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	rec := sampleRecord("DEL-1", "To Be Deleted")
	if _, err := v.Store(ctx, rec); err != nil {
		t.Fatalf("store: %v", err)
	}

	if err := v.Delete(ctx, "DEL-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := v.Get(ctx, "DEL-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after deletion, got %v", err)
	}

	// Verify FTS index also dropped the entry
	res, err := v.Query(ctx, "Deleted", 10)
	if err != nil {
		t.Fatalf("query deleted: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected 0 matches in FTS index, got %d", len(res))
	}
}

func TestVaultConcurrency(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	var wg sync.WaitGroup
	workers := 10
	iterations := 20

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				recID := fmt.Sprintf("W%d-REC-%d", workerID, i)
				rec := sampleRecord(recID, fmt.Sprintf("Concurrent task %d from worker %d", i, workerID))
				if _, err := v.Store(ctx, rec); err != nil {
					t.Errorf("concurrent store %s: %v", recID, err)
					return
				}
				if _, err := v.Get(ctx, recID); err != nil {
					t.Errorf("concurrent get %s: %v", recID, err)
					return
				}
				if _, err := v.Query(ctx, fmt.Sprintf("worker %d", workerID), 5); err != nil {
					t.Errorf("concurrent query: %v", err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
}

func TestTokenizeCodeSymbols(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			input:    "CalculateBlastRadius",
			expected: []string{"calculate", "blast", "radius"},
		},
		{
			input:    "write_receipt_id",
			expected: []string{"write", "receipt", "id"},
		},
		{
			input:    "XMLParser",
			expected: []string{"xml", "parser"},
		},
		{
			input:    "internal/worker/proc_windows.go:killProcessGroup",
			expected: []string{"internal", "worker", "proc", "windows", "go", "kill", "process", "group"},
		},
	}

	for _, tt := range tests {
		got := TokenizeCodeSymbols(tt.input)
		if len(got) != len(tt.expected) {
			t.Fatalf("TokenizeCodeSymbols(%q) = %v, want %v", tt.input, got, tt.expected)
		}
		for i, tok := range got {
			if tok != tt.expected[i] {
				t.Errorf("token %d: got %s, want %s", i, tok, tt.expected[i])
			}
		}
	}
}

func TestVaultSearchCodeSymbolDecomposition(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()

	rec := sampleRecord("DELTA-07-BLAST", "Blast Radius Analysis")
	rec.SpatialCoordinates.Symbol = "CalculateBlastRadius"
	rec.SpatialCoordinates.File = "internal/analyzer/blast_radius.go"
	_, err := v.Store(ctx, rec)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Query with exact CamelCase symbol
	res, err := v.Query(ctx, "CalculateBlastRadius", 5)
	if err != nil {
		t.Fatalf("Query CalculateBlastRadius: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected match for CalculateBlastRadius, got 0")
	}

	// Query with partial word token 'blast'
	res, err = v.Query(ctx, "blast", 5)
	if err != nil {
		t.Fatalf("Query blast: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected match for sub-token 'blast', got 0")
	}
}
