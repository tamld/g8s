package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/verifier"
)

// TestVerifyCLI_DoDMechanicalBinding_Positive verifies that g8s verify CLI executes checks
// and binds DoD criteria to trusted mechanical check records (#648).
func TestVerifyCLI_DoDMechanicalBinding_Positive(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)
	ctx := context.Background()

	wtDir := t.TempDir()
	regDir := filepath.Join(wtDir, ".g8s")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regYAML := `
classes:
  - name: wt-dod-class
    status: hard
    founding_catch: "#208"
    priority: 100
    paths:
      - "internal/verifier/**"
    checks:
      - type: go-test
        packages:
          - "./internal/verifier"
`
	if err := os.WriteFile(filepath.Join(regDir, "verifier-classes.yml"), []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	briefID := "brief-pos-01"
	if err := store.CreateBrief(ctx, controlplane.BriefRow{
		ID:        briefID,
		Title:     "Positive DoD Brief",
		PayloadMD: "Goal",
		DodMD:     "- [ ] D1: Verifier tests pass [hint: gotest:./internal/verifier]",
		IssuedBy:  "operator",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	receiptID := "rcpt-dod-pos-1"
	pathsJSON := `["internal/verifier/verifier.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES (?, 'brain', ?, 9999999999, 100)
	`, receiptID, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-dod-pos-task",
		Model:           "test-model",
		Role:            "test-runner",
		Permission:      "workspace_write",
		AddDirs:         []string{wtDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"test prompt","receipt_id":%q,"allowed_paths":["internal/verifier/verifier.go"]}`, receiptID)),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	resultJSON := fmt.Sprintf(`{"deliverable":{"mode":"worktree","dir":%q},"dod_matrix":[{"id":"D1","status":"MET","evidence":"passed locally"}]}`, wtDir)
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("--- PASS: TestSomething (0.01s)\nPASS\nok  internal/verifier 0.1s\n"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--brief", briefID, "--repo", wtDir, "--json"}, &stdout, &stderr, dbPath, mockRunner)
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

	if v.Outcome != verifier.OutcomePass {
		t.Fatalf("expected OutcomePass, got %q", v.Outcome)
	}
	if len(v.DoDMatrix) != 1 {
		t.Fatalf("expected 1 item in DoDMatrix, got %d: %+v", len(v.DoDMatrix), v.DoDMatrix)
	}
	if v.DoDMatrix[0].ID != "D1" || v.DoDMatrix[0].Status != "MET" {
		t.Errorf("expected D1 MET, got %+v", v.DoDMatrix[0])
	}
	if !strings.Contains(v.DoDMatrix[0].Evidence, "bound to check[go-test:./internal/verifier] ok=true") {
		t.Errorf("evidence missing mechanical check binding: %s", v.DoDMatrix[0].Evidence)
	}
	if !strings.Contains(v.DoDMatrix[0].Evidence, fmt.Sprintf("task=%s", task.TaskID)) {
		t.Errorf("evidence missing task ID: %s", v.DoDMatrix[0].Evidence)
	}
}

// TestVerifyCLI_DoDMechanicalBinding_PositiveTwoDistinctChecks verifies that two criteria
// with distinct exact checks both execute and bind successfully in the CLI (#648).
func TestVerifyCLI_DoDMechanicalBinding_PositiveTwoDistinctChecks(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)
	ctx := context.Background()

	wtDir := t.TempDir()
	regDir := filepath.Join(wtDir, ".g8s")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regYAML := `
classes:
  - name: wt-multi-class
    status: hard
    founding_catch: "#208"
    priority: 100
    paths:
      - "internal/**"
    checks:
      - type: go-test
        packages:
          - "./internal/verifier"
      - type: go-test
        packages:
          - "./internal/pathutil"
`
	if err := os.WriteFile(filepath.Join(regDir, "verifier-classes.yml"), []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	briefID := "brief-pos-multi"
	if err := store.CreateBrief(ctx, controlplane.BriefRow{
		ID:        briefID,
		Title:     "Positive Multi DoD Brief",
		PayloadMD: "Goal",
		DodMD:     "- [ ] D1: Verifier tests pass [hint: gotest:./internal/verifier]\n- [ ] D2: Pathutil tests pass [hint: gotest:./internal/pathutil]",
		IssuedBy:  "operator",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	receiptID := "rcpt-dod-pos-multi"
	pathsJSON := `["internal/verifier/verifier.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES (?, 'brain', ?, 9999999999, 100)
	`, receiptID, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-dod-pos-multi-task",
		Model:           "test-model",
		Role:            "test-runner",
		Permission:      "workspace_write",
		AddDirs:         []string{wtDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"test prompt","receipt_id":%q,"allowed_paths":["internal/verifier/verifier.go"]}`, receiptID)),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	resultJSON := fmt.Sprintf(`{"deliverable":{"mode":"worktree","dir":%q},"dod_matrix":[{"id":"D1","status":"MET","evidence":"passed locally"},{"id":"D2","status":"MET","evidence":"passed locally"}]}`, wtDir)
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("--- PASS: TestMulti (0.01s)\nPASS\nok  testpkg 0.1s\n"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--brief", briefID, "--repo", wtDir, "--json"}, &stdout, &stderr, dbPath, mockRunner)
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

	if v.Outcome != verifier.OutcomePass {
		t.Fatalf("expected OutcomePass, got %q", v.Outcome)
	}
	if len(v.DoDMatrix) != 2 {
		t.Fatalf("expected 2 items in DoDMatrix, got %d: %+v", len(v.DoDMatrix), v.DoDMatrix)
	}
	if v.DoDMatrix[0].Status != "MET" || v.DoDMatrix[1].Status != "MET" {
		t.Errorf("expected both criteria MET, got %+v", v.DoDMatrix)
	}
}

// TestVerifyCLI_DoDMechanicalBinding_UnrelatedCheckDoesNotSatisfy verifies that
// when an unrelated check succeeds, unexecuted DoD criteria fail and outcome is FAIL (#648).
func TestVerifyCLI_DoDMechanicalBinding_UnrelatedCheckDoesNotSatisfy(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)
	ctx := context.Background()

	wtDir := t.TempDir()
	regDir := filepath.Join(wtDir, ".g8s")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regYAML := `
classes:
  - name: wt-partial-class
    status: hard
    founding_catch: "#208"
    priority: 100
    paths:
      - "internal/verifier/**"
    checks:
      - type: go-test
        packages:
          - "./internal/verifier"
`
	if err := os.WriteFile(filepath.Join(regDir, "verifier-classes.yml"), []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	briefID := "brief-partial-01"
	if err := store.CreateBrief(ctx, controlplane.BriefRow{
		ID:        briefID,
		Title:     "Multi DoD Brief",
		PayloadMD: "Goal",
		DodMD:     "- [ ] D1: Verifier tests pass [hint: gotest:./internal/verifier]\n- [ ] D2: Security check passes [hint: script:tools/sec_scan.sh]",
		IssuedBy:  "operator",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	receiptID := "rcpt-dod-partial-1"
	pathsJSON := `["internal/verifier/verifier.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES (?, 'brain', ?, 9999999999, 100)
	`, receiptID, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-dod-partial-task",
		Model:           "test-model",
		Role:            "test-runner",
		Permission:      "workspace_write",
		AddDirs:         []string{wtDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"test prompt","receipt_id":%q,"allowed_paths":["internal/verifier/verifier.go"]}`, receiptID)),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	resultJSON := fmt.Sprintf(`{"deliverable":{"mode":"worktree","dir":%q},"dod_matrix":[{"id":"D1","status":"MET","evidence":"passed"},{"id":"D2","status":"MET","evidence":"ok"}]}`, wtDir)
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("--- PASS: TestSomething (0.01s)\nPASS\nok  internal/verifier 0.1s\n"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--brief", briefID, "--repo", wtDir, "--json"}, &stdout, &stderr, dbPath, mockRunner)
	if code != 1 {
		t.Errorf("expected exit code 1 due to unexecuted D2, got %d", code)
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

	if v.Outcome != verifier.OutcomeFail {
		t.Fatalf("expected OutcomeFail, got %q", v.Outcome)
	}
	if len(v.DoDMatrix) != 2 {
		t.Fatalf("expected 2 items, got %d", len(v.DoDMatrix))
	}
	if v.DoDMatrix[0].ID != "D1" || v.DoDMatrix[0].Status != "MET" {
		t.Errorf("expected D1 MET, got %+v", v.DoDMatrix[0])
	}
	if v.DoDMatrix[1].ID != "D2" || v.DoDMatrix[1].Status != "UNMET" {
		t.Errorf("expected D2 UNMET due to missing check execution, got %+v", v.DoDMatrix[1])
	}
}

// TestVerifyCLI_DoDMechanicalBinding_RejectsBareProse verifies that worker self-report
// bare words/prose without executed check records fail verification (#648).
func TestVerifyCLI_DoDMechanicalBinding_RejectsBareProse(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)
	ctx := context.Background()

	wtDir := t.TempDir()
	regDir := filepath.Join(wtDir, ".g8s")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regYAML := `
classes:
  - name: wt-prose-class
    status: hard
    founding_catch: "#208"
    priority: 100
    paths:
      - "internal/verifier/**"
    checks:
      - type: go-test
        packages:
          - "./internal/verifier"
`
	if err := os.WriteFile(filepath.Join(regDir, "verifier-classes.yml"), []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	briefID := "brief-prose-01"
	if err := store.CreateBrief(ctx, controlplane.BriefRow{
		ID:        briefID,
		Title:     "Bare Prose Brief",
		PayloadMD: "Goal",
		DodMD:     "- [ ] D1: Nonexistent check [hint: gotest:./nonexistent/pkg]",
		IssuedBy:  "operator",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	receiptID := "rcpt-dod-prose-1"
	pathsJSON := `["internal/verifier/verifier.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES (?, 'brain', ?, 9999999999, 100)
	`, receiptID, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-dod-prose-task",
		Model:           "test-model",
		Role:            "test-runner",
		Permission:      "workspace_write",
		AddDirs:         []string{wtDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"test prompt","receipt_id":%q,"allowed_paths":["internal/verifier/verifier.go"]}`, receiptID)),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	// Worker reports bare "ok"
	resultJSON := fmt.Sprintf(`{"deliverable":{"mode":"worktree","dir":%q},"dod_matrix":[{"id":"D1","status":"MET","evidence":"ok"}]}`, wtDir)
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("--- PASS: TestSomething (0.01s)\nPASS\nok  internal/verifier 0.1s\n"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--brief", briefID, "--repo", wtDir, "--json"}, &stdout, &stderr, dbPath, mockRunner)
	if code != 1 {
		t.Errorf("expected exit code 1 for bare prose rejection, got %d", code)
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

	if v.Outcome != verifier.OutcomeFail {
		t.Errorf("expected OutcomeFail, got %q", v.Outcome)
	}
	if len(v.DoDMatrix) != 1 || v.DoDMatrix[0].Status != "UNMET" {
		t.Errorf("expected D1 UNMET, got %+v", v.DoDMatrix)
	}
}

// TestVerifyCLI_DoDMechanicalBinding_Negative_ArtifactTraversalRejected verifies that
// an artifact hint escaping the worktree via traversal is rejected by the CLI (#648).
func TestVerifyCLI_DoDMechanicalBinding_Negative_ArtifactTraversalRejected(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)
	ctx := context.Background()

	parentDir := t.TempDir()
	wtDir := filepath.Join(parentDir, "worktree")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(parentDir, "outside.md")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o644); err != nil {
		t.Fatal(err)
	}

	regDir := filepath.Join(wtDir, ".g8s")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regYAML := `
classes:
  - name: wt-traversal-class
    status: hard
    founding_catch: "#208"
    priority: 100
    paths:
      - "internal/verifier/**"
    checks:
      - type: go-test
        packages:
          - "./internal/verifier"
`
	if err := os.WriteFile(filepath.Join(regDir, "verifier-classes.yml"), []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	briefID := "brief-traversal-01"
	if err := store.CreateBrief(ctx, controlplane.BriefRow{
		ID:        briefID,
		Title:     "Traversal Escape Brief",
		PayloadMD: "Goal",
		DodMD:     "- [ ] D1: Traversal escape [hint: gotest:./internal/verifier, file:../outside.md]",
		IssuedBy:  "operator",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	receiptID := "rcpt-traversal-1"
	pathsJSON := `["internal/verifier/verifier.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES (?, 'brain', ?, 9999999999, 100)
	`, receiptID, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-dod-traversal-task",
		Model:           "test-model",
		Role:            "test-runner",
		Permission:      "workspace_write",
		AddDirs:         []string{wtDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"test prompt","receipt_id":%q,"allowed_paths":["internal/verifier/verifier.go"]}`, receiptID)),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	resultJSON := fmt.Sprintf(`{"deliverable":{"mode":"worktree","dir":%q},"dod_matrix":[{"id":"D1","status":"MET","evidence":"passed"}]}`, wtDir)
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("--- PASS: TestVerifier (0.01s)\nPASS\nok  internal/verifier 0.1s\n"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--brief", briefID, "--repo", wtDir, "--json"}, &stdout, &stderr, dbPath, mockRunner)
	if code != 1 {
		t.Errorf("expected exit code 1 for traversal rejection, got %d", code)
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
	if v.Outcome != verifier.OutcomeFail || len(v.DoDMatrix) != 1 || v.DoDMatrix[0].Status != "UNMET" {
		t.Errorf("expected failure and UNMET for traversal, got %+v", v)
	}
}

// TestVerifyCLI_DoDMechanicalBinding_Negative_ArbitraryFileWithHashRejected verifies that
// an artifact with a valid hash cannot substitute for a check in the CLI (#648).
func TestVerifyCLI_DoDMechanicalBinding_Negative_ArbitraryFileWithHashRejected(t *testing.T) {
	dbPath, store := setupTestDB(t)
	defer store.Close()
	rawDB := openReceiptsDB(t, dbPath)
	ctx := context.Background()

	wtDir := t.TempDir()
	docFile := filepath.Join(wtDir, "report.json")
	content := []byte(`{"status": "ok"}`)
	if err := os.WriteFile(docFile, content, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	docSHA := hex.EncodeToString(h[:])

	regDir := filepath.Join(wtDir, ".g8s")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regYAML := `
classes:
  - name: wt-art-alone-class
    status: hard
    founding_catch: "#208"
    priority: 100
    paths:
      - "internal/verifier/**"
    checks:
      - type: go-test
        packages:
          - "./internal/verifier"
`
	if err := os.WriteFile(filepath.Join(regDir, "verifier-classes.yml"), []byte(regYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	briefID := "brief-art-alone-01"
	if err := store.CreateBrief(ctx, controlplane.BriefRow{
		ID:        briefID,
		Title:     "Artifact Alone Brief",
		PayloadMD: "Goal",
		DodMD:     "- [ ] D1: Report file exists [hint: file:report.json]",
		IssuedBy:  "operator",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	receiptID := "rcpt-art-alone-1"
	pathsJSON := `["internal/verifier/verifier.go"]`
	_, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES (?, 'brain', ?, 9999999999, 100)
	`, receiptID, pathsJSON)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-dod-art-alone-task",
		Model:           "test-model",
		Role:            "test-runner",
		Permission:      "workspace_write",
		AddDirs:         []string{wtDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(fmt.Sprintf(`{"prompt":"test prompt","receipt_id":%q,"allowed_paths":["internal/verifier/verifier.go"]}`, receiptID)),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()

	resultJSON := fmt.Sprintf(`{"deliverable":{"mode":"worktree","dir":%q},"dod_matrix":[{"id":"D1","status":"MET","evidence":"sha256:%s"}]}`, wtDir, docSHA)
	if _, err := g8sDB.Exec("UPDATE tasks SET state = 'SUCCEEDED', result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
		t.Fatalf("update task result: %v", err)
	}

	mockRunner := verifier.WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("--- PASS: TestVerifier (0.01s)\nPASS\nok  internal/verifier 0.1s\n"), nil, 0, nil
	})

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", task.TaskID, "--brief", briefID, "--repo", wtDir, "--json"}, &stdout, &stderr, dbPath, mockRunner)
	if code != 1 {
		t.Errorf("expected exit code 1 because artifact alone cannot substitute check, got %d", code)
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
	if v.Outcome != verifier.OutcomeFail || len(v.DoDMatrix) != 1 || v.DoDMatrix[0].Status != "UNMET" {
		t.Errorf("expected failure and UNMET when artifact alone is provided, got %+v", v)
	}
}
