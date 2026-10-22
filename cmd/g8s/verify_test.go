package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestVerifyCLI_CrossRepoTargetResolution verifies that a task targeting a sibling repo directory
// resolves its verifier classes configuration and script checks from the sibling repo root.
func TestVerifyCLI_CrossRepoTargetResolution(t *testing.T) {
	siblingDir := t.TempDir()

	// Create sibling repo's .g8s/verifier-classes.yml
	g8sDir := filepath.Join(siblingDir, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatalf("mkdir .g8s: %v", err)
	}
	classesYAML := `classes:
  - name: sibling-class
    status: hard
    founding_catch: "#100 sibling check"
    priority: 10
    paths:
      - "src/**"
    checks:
      - type: script
        run: tools/check_ok.sh
`
	if err := os.WriteFile(filepath.Join(g8sDir, "verifier-classes.yml"), []byte(classesYAML), 0o644); err != nil {
		t.Fatalf("write verifier-classes.yml: %v", err)
	}

	// Create passing check script tools/check_ok.sh
	toolsDir := filepath.Join(siblingDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	scriptContent := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "check_ok.sh"), []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write check_ok.sh: %v", err)
	}

	dbPath, store := setupTestDB(t)
	defer store.Close()

	rawDB := openReceiptsDB(t, dbPath)
	pathsJSON := `["src/feature.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-cross-1', 'brain', ?, 9999999999, 100)
	`, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-cross-task",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{siblingDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"cross repo task","receipt_id":"rcpt-cross-1","add_dirs":[%q]}`, siblingDir)),
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
		t.Fatalf("failed to parse envelope: %v", err)
	}

	payloadBytes, _ := json.Marshal(env.Data)
	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.Class != "sibling-class" {
		t.Errorf("verdict.Class = %q, want sibling-class", v.Class)
	}
	if v.Registered != true {
		t.Errorf("verdict.Registered = %v, want true", v.Registered)
	}
	if v.Outcome != verifier.OutcomePass {
		t.Errorf("verdict.Outcome = %q, want pass", v.Outcome)
	}
	if len(v.Checks) != 1 || !v.Checks[0].OK {
		t.Errorf("verdict.Checks = %+v, want 1 passing check", v.Checks)
	}

	// Verify scope_root field in payload also resolves sibling repo correctly
	pathsJSON2 := `["src/sub/feature2.go"]`
	_, err = rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-cross-2', 'brain', ?, 9999999999, 100)
	`, pathsJSON2)
	if err != nil {
		t.Fatalf("insert receipt 2: %v", err)
	}

	task2, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-cross-task-scoperoot",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{"."},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"cross repo task scope_root","receipt_id":"rcpt-cross-2","scope_root":%q}`, siblingDir)),
	})
	if err != nil {
		t.Fatalf("submit task 2: %v", err)
	}

	var stdout2, stderr2 bytes.Buffer
	code2 := runVerifyWithIO([]string{"--task", task2.TaskID, "--json"}, &stdout2, &stderr2, dbPath)
	if code2 != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", code2, stderr2.String())
	}

	var env2 cli.Envelope
	if err := json.Unmarshal(stdout2.Bytes(), &env2); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	payloadBytes2, _ := json.Marshal(env2.Data)
	var v2 verifier.Verdict
	if err := json.Unmarshal(payloadBytes2, &v2); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v2.Class != "sibling-class" {
		t.Errorf("verdict2.Class = %q, want sibling-class", v2.Class)
	}
	if v2.Registered != true {
		t.Errorf("verdict2.Registered = %v, want true", v2.Registered)
	}
	if v2.Outcome != verifier.OutcomePass {
		t.Errorf("verdict2.Outcome = %q, want pass", v2.Outcome)
	}
}

// TestVerifyCLI_WorktreeTargetResolution tests L3 fix: task.Result with deliverable.mode="worktree"
// routes verifier to execute inside the F1 worktree directory.
func TestVerifyCLI_WorktreeTargetResolution(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)

	wtDir := t.TempDir()
	regDir := filepath.Join(wtDir, ".g8s")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatalf("mkdir .g8s: %v", err)
	}
	classYAML := `
classes:
  - name: wt-class
    status: advisory
    founding_catch: "#510"
    priority: 10
    paths:
      - "pkg/foo/**"
    checks:
      - type: go-test
        packages: ["pkg/foo"]
`
	if err := os.WriteFile(filepath.Join(regDir, "verifier-classes.yml"), []byte(classYAML), 0o644); err != nil {
		t.Fatalf("write verifier-classes.yml: %v", err)
	}

	pathsJSON := `["pkg/foo/bar.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-wt-1', 'brain', ?, 9999999999, 100)
	`, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-wt-task",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{"/unmodified/main/checkout"},
		SkipPermissions: true,
		Payload:         json.RawMessage(`{"prompt":"wt task","receipt_id":"rcpt-wt-1"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	// Update task result with deliverable pointer
	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	resultJSON := fmt.Sprintf(`{"deliverable":{"mode":"worktree","dir":%q},"dod_matrix":[{"id":"D1","status":"MET","evidence":"unit tests passed"}]}`, wtDir)
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
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

	if v.WorktreeDir != wtDir {
		t.Errorf("verdict.WorktreeDir = %q, want %q", v.WorktreeDir, wtDir)
	}
	if v.Class != "wt-class" {
		t.Errorf("verdict.Class = %q, want wt-class", v.Class)
	}
	if !v.Registered {
		t.Errorf("verdict.Registered = %v, want true", v.Registered)
	}
	if v.Outcome != verifier.OutcomePass {
		t.Errorf("verdict.Outcome = %q, want pass", v.Outcome)
	}
	if len(v.DoDMatrix) != 1 || v.DoDMatrix[0].ID != "D1" || v.DoDMatrix[0].Status != "MET" {
		t.Errorf("verdict.DoDMatrix = %+v", v.DoDMatrix)
	}
}

// TestVerifyCLI_BriefFlag tests --brief flag integration with DoD completeness checking.
func TestVerifyCLI_BriefFlag(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)

	ctx := context.Background()
	briefID := "brief-test-cli"
	if err := store.CreateBrief(ctx, controlplane.BriefRow{
		ID:        briefID,
		Title:     "Test Brief",
		PayloadMD: "Goal",
		DodMD:     "- [ ] D1: Core logic\n- [ ] D2: Additional tests",
		IssuedBy:  "operator",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	pathsJSON := `["pkg/foo/bar.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-brief-1', 'brain', ?, 9999999999, 100)
	`, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-brief-task",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{"."},
		SkipPermissions: true,
		Payload:         json.RawMessage(`{"prompt":"brief task","receipt_id":"rcpt-brief-1"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	// Update task with only D1 reported (D2 missing!)
	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	resultJSON := `{"dod_matrix":[{"id":"D1","status":"MET","evidence":"logic implemented"}]}`
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("ok"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--brief", briefID, "--json"}, &stdout, &stderr, dbPath, mockRunner)
	// Missing D2 forces OutcomeFail. If status is advisory, exit code is 0, but verdict.Outcome is fail
	_ = code

	var env cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	payloadBytes, _ := json.Marshal(env.Data)
	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.Outcome != verifier.OutcomeFail {
		t.Errorf("verdict.Outcome = %q, want fail due to missing D2", v.Outcome)
	}
	if len(v.DoDMatrix) != 2 {
		t.Fatalf("expected 2 items in DoDMatrix, got %d: %+v", len(v.DoDMatrix), v.DoDMatrix)
	}
	if v.DoDMatrix[1].ID != "D2" || v.DoDMatrix[1].Status != "UNMET" {
		t.Errorf("D2 should be UNMET, got %+v", v.DoDMatrix[1])
	}
}

// TestVerifyCLI_StandaloneFeatureBlindPacket verifies that g8s verify runs standalone with --feature and --blind-packet.
func TestVerifyCLI_StandaloneFeatureBlindPacket(t *testing.T) {
	repoRoot := findRepoRoot(t)

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("ok"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{
		"--feature", "FEAT-FEATUREMAP-01",
		"--blind-packet",
		"--repo", repoRoot,
		"--json",
	}, &stdout, &stderr, "", mockRunner)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", code, stderr.String())
	}

	var env cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse envelope: %v. stdout: %s", err, stdout.String())
	}

	if env.Kind != "verdict" || env.Command != "verify" {
		t.Fatalf("unexpected envelope kind/command: kind=%q cmd=%q", env.Kind, env.Command)
	}
	if env.Subcommand != "feature-FEAT-FEATUREMAP-01" {
		t.Fatalf("expected Subcommand (taskID) 'feature-FEAT-FEATUREMAP-01', got %q", env.Subcommand)
	}

	payloadBytes, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("failed to marshal env.Data: %v", err)
	}

	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.FeatureID != "FEAT-FEATUREMAP-01" {
		t.Errorf("verdict.FeatureID = %q, want FEAT-FEATUREMAP-01", v.FeatureID)
	}
	if v.BlindReviewPacket == nil {
		t.Fatalf("expected non-nil blind_review_packet in verdict")
	}
	if v.BlindReviewPacket.FeatureID != "FEAT-FEATUREMAP-01" {
		t.Errorf("BlindReviewPacket.FeatureID = %q, want FEAT-FEATUREMAP-01", v.BlindReviewPacket.FeatureID)
	}
	if v.BlindReviewPacket.PacketID == "" {
		t.Errorf("expected non-empty PacketID in BlindReviewPacket")
	}
	if !strings.Contains(v.BlindReviewPacket.SpecContract, "FEAT-FEATUREMAP-01") && !strings.Contains(v.BlindReviewPacket.SpecContract, "Declarative Feature Map") {
		t.Errorf("unexpected SpecContract: %q", v.BlindReviewPacket.SpecContract)
	}
	if len(v.Checks) == 0 {
		t.Errorf("expected transitive feature checks to be executed, got 0 checks")
	}
}

// TestVerifyCLI_StandaloneFeatureHoldoutAndCanary verifies holdout script execution and canary mutant check.
func TestVerifyCLI_StandaloneFeatureHoldoutAndCanary(t *testing.T) {
	repoRoot := findRepoRoot(t)
	tempHoldoutDir := t.TempDir()

	scriptPath := filepath.Join(tempHoldoutDir, "holdout_verify.sh")
	scriptContent := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("failed to write holdout script: %v", err)
	}

	holdoutCallCount := 0
	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		for _, arg := range args {
			if strings.Contains(arg, "holdout_verify.sh") {
				holdoutCallCount++
				if holdoutCallCount > 1 {
					// Simulated probe runner mutant catch
					return nil, []byte("canary caught mutant"), 1, nil
				}
				return []byte("holdout passed"), nil, 0, nil
			}
		}
		return []byte("ok"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{
		"--feature", "FEAT-FEATUREMAP-01",
		"--holdout-dir", tempHoldoutDir,
		"--canary-mutant",
		"--repo", repoRoot,
		"--json",
	}, &stdout, &stderr, "", mockRunner)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", code, stderr.String())
	}

	if holdoutCallCount < 2 {
		t.Errorf("expected holdout script to be called at least 2 times (holdout check + canary probe), got %d", holdoutCallCount)
	}

	var env cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("failed to parse envelope: %v. stdout: %s", err, stdout.String())
	}

	payloadBytes, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("failed to marshal env.Data: %v", err)
	}

	var v verifier.Verdict
	if err := json.Unmarshal(payloadBytes, &v); err != nil {
		t.Fatalf("unmarshal verdict: %v", err)
	}

	if v.HoldoutChecksOK == nil || !*v.HoldoutChecksOK {
		t.Errorf("expected HoldoutChecksOK to be true, got %v", v.HoldoutChecksOK)
	}
	if v.CanaryResult == nil {
		t.Fatalf("expected non-nil CanaryResult in verdict")
	}
	if !v.CanaryResult.Executed {
		t.Errorf("expected CanaryResult.Executed to be true")
	}
	if !v.CanaryResult.CanaryCaught {
		t.Errorf("expected CanaryResult.CanaryCaught to be true")
	}
	if v.VetoReason != "" {
		t.Errorf("expected empty VetoReason, got %q", v.VetoReason)
	}
}
