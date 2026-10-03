package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/verifier"
)

func setupTestDB(t *testing.T) (string, *controlplane.Store) {
	t.Helper()
	t.Setenv("AGY_MCP_ALLOW_WORKSPACE_WRITE", "1")
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to create controlplane: %v", err)
	}

	return dbPath, store
}

// openReceiptsDB opens the sibling receipts.db — the canonical receipt ledger
// the receipt CLI and controlplane write to (real state-dir layout) — and
// ensures the write_receipts table exists. Verify must resolve receipt paths
// from THIS database, never from the controlplane g8s.db (the receipts
// split-brain, issue #516 round 1 finding).
func openReceiptsDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	receiptsPath := filepath.Join(filepath.Dir(dbPath), "receipts.db")
	rawDB, err := sql.Open("sqlite", pathutil.SQLiteURI(receiptsPath, "_pragma=foreign_keys(ON)"))
	if err != nil {
		t.Fatalf("failed to open receipts db: %v", err)
	}
	t.Cleanup(func() { rawDB.Close() })

	_, err = rawDB.Exec(`
		CREATE TABLE IF NOT EXISTS write_receipts (
			receipt_id TEXT PRIMARY KEY,
			issuer TEXT,
			allowed_paths_json TEXT,
			expires_at REAL,
			consumed INTEGER DEFAULT 0,
			created_at REAL
		);
	`)
	if err != nil {
		t.Fatalf("failed to create write_receipts table: %v", err)
	}
	return rawDB
}

// TestVerifyCLI_Usage verifies that missing --task flag prints usage error envelope and returns code 2.
func TestVerifyCLI_Usage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}

	var env cli.Envelope
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse error envelope from stderr %q: %v", stderr.String(), err)
	}
	if env.Kind != "error" || env.Error == nil || env.Error.Code != cli.CodeUsage {
		t.Errorf("expected error/usage envelope, got %+v", env)
	}
}

// TestVerifyCLI_TaskNotFound verifies that querying a non-existent task returns code 1 and not-found error.
func TestVerifyCLI_TaskNotFound(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", "nonexistent-task", "--json"}, &stdout, &stderr, dbPath)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	var env cli.Envelope
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse error envelope from stderr %q: %v", stderr.String(), err)
	}
	if env.Kind != "error" || env.Error == nil || env.Error.Code != cli.CodeNotFound {
		t.Errorf("expected error/not_found envelope, got %+v", env)
	}
}

// TestVerifyCLI_SelfGradeGuard verifies that caller equal to target is refused with typed error.
func TestVerifyCLI_SelfGradeGuard(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "k-self-grade",
		Model:          "test-model",
		Role:           "scout",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"test self-grade"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--as-task", task.TaskID, "--json"}, &stdout, &stderr, dbPath)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	var env cli.Envelope
	if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse error envelope from stderr %q: %v", stderr.String(), err)
	}
	if env.Kind != "error" {
		t.Errorf("expected error envelope, got %+v", env)
	}
	if !strings.Contains(stderr.String(), "no verifier grades its own class") {
		t.Errorf("expected 'no verifier grades its own class' in stderr, got %s", stderr.String())
	}
}

// TestVerifyCLI_ReadOnlyTaskUnregistered verifies that a task without receipt produces an unregistered verdict and exit 0.
func TestVerifyCLI_ReadOnlyTaskUnregistered(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "k-ro-task",
		Model:          "test-model",
		Role:           "scout",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"research docs"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--json"}, &stdout, &stderr, dbPath)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", code, stderr.String())
	}

	var env cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse envelope from stdout %q: %v", stdout.String(), err)
	}
	if env.Kind != "verdict" {
		t.Errorf("envelope kind = %q, want verdict", env.Kind)
	}

	payloadBytes, _ := json.Marshal(env.Data)
	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.Class != "unregistered" {
		t.Errorf("verdict.Class = %q, want unregistered", v.Class)
	}
	if v.Registered != false {
		t.Errorf("verdict.Registered = %v, want false", v.Registered)
	}
	if v.Outcome != verifier.OutcomeUnregistered {
		t.Errorf("verdict.Outcome = %q, want unregistered", v.Outcome)
	}
}

// TestVerifyCLI_DocsTaskResolution verifies that a task with receipt for docs prose resolves to docs class.
func TestVerifyCLI_DocsTaskResolution(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()

	// Insert receipt into the sibling receipts.db (canonical receipt ledger)
	rawDB := openReceiptsDB(t, dbPath)

	pathsJSON := `["README.md", "docs/architecture.md", "plans/261002-factory/plan.md"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-docs-1', 'brain', ?, 9999999999, 100)
	`, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-docs-task",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{"."},
		SkipPermissions: true,
		Payload:         json.RawMessage(`{"prompt":"update docs","receipt_id":"rcpt-docs-1"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("ok"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--json"}, &stdout, &stderr, dbPath, mockRunner)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", code, stderr.String())
	}

	var env cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	payloadBytes, _ := json.Marshal(env.Data)
	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.Class != "docs" {
		t.Errorf("verdict.Class = %q, want docs", v.Class)
	}
	if v.Registered != true {
		t.Errorf("verdict.Registered = %v, want true", v.Registered)
	}
	if v.Status != verifier.StatusAdvisory {
		t.Errorf("verdict.Status = %q, want advisory", v.Status)
	}
}

// TestVerifyCLI_TestTaskResolution verifies that a task with brief + test file resolves to test class.
func TestVerifyCLI_TestTaskResolution(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()

	rawDB := openReceiptsDB(t, dbPath)

	pathsJSON := `["plans/261002-factory/brief-H1-verifier-registry.md", "cmd/g8s/verify_test.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-test-1', 'brain', ?, 9999999999, 100)
	`, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-test-task",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{"."},
		SkipPermissions: true,
		Payload:         json.RawMessage(`{"prompt":"write tests","receipt_id":"rcpt-test-1"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("ok"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--json"}, &stdout, &stderr, dbPath, mockRunner)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", code, stderr.String())
	}

	var env cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	payloadBytes, _ := json.Marshal(env.Data)
	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.Class != "test" {
		t.Errorf("verdict.Class = %q, want test", v.Class)
	}
	if v.Registered != true {
		t.Errorf("verdict.Registered = %v, want true", v.Registered)
	}
}

// TestVerifyCLI_MixedCodePathUnregistered verifies that a task touching unclassified code paths yields unregistered verdict.
func TestVerifyCLI_MixedCodePathUnregistered(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()

	rawDB := openReceiptsDB(t, dbPath)

	pathsJSON := `["internal/verifier/verifier.go", "internal/verifier/verifier_test.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-mixed-1', 'brain', ?, 9999999999, 100)
	`, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-mixed-task",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{"."},
		SkipPermissions: true,
		Payload:         json.RawMessage(`{"prompt":"write mixed code","receipt_id":"rcpt-mixed-1"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	var stdout, stderr bytes.Buffer
	// Test positional argument syntax: g8s verify <task-id>
	code := runVerifyWithIO([]string{task.TaskID, "--json"}, &stdout, &stderr, dbPath)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", code, stderr.String())
	}

	var env cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	payloadBytes, _ := json.Marshal(env.Data)
	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.Class != "unregistered" {
		t.Errorf("verdict.Class = %q, want unregistered", v.Class)
	}
	if v.Registered != false {
		t.Errorf("verdict.Registered = %v, want false", v.Registered)
	}
	if v.Outcome != verifier.OutcomeUnregistered {
		t.Errorf("verdict.Outcome = %q, want unregistered", v.Outcome)
	}
}
