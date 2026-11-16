package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
)

const verifyAcceptHardRegistry = `schema_version: "verifier-classes.v1"
classes:
  - name: worktree-go-hard
    status: hard
    founding_catch: "#510"
    priority: 10
    paths:
      - "pkg/**"
      - "**/*_test.go"
    checks:
      - type: go-test
        packages: ["./pkg/app"]
`

func initVerifyAcceptWorktree(t *testing.T, passTests bool) (dbPath string, store *controlplane.Store, taskID, wtDir string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AGY_MCP_ALLOW_WORKSPACE_WRITE", "1")
	t.Setenv("G8S_DB", filepath.Join(root, "g8s.db"))
	t.Setenv("G8S_STATE_DIR", root)
	t.Setenv("G8S_JIT_WHISPER", "0")

	dbPath = filepath.Join(root, "g8s.db")
	var err error
	store, err = controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	wtDir = filepath.Join(root, "worktree")
	if err := os.MkdirAll(filepath.Join(wtDir, "pkg", "app"), 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(wtDir, ".g8s"), 0o755); err != nil {
		t.Fatalf("mkdir .g8s: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "go.mod"), []byte("module example.com/accept\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".g8s", "verifier-classes.yml"), []byte(verifyAcceptHardRegistry), 0o644); err != nil {
		t.Fatalf("write verifier-classes.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "pkg", "app", "app.go"), []byte("package app\n\nfunc Sum(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write app.go: %v", err)
	}
	testBody := `package app

import "testing"

func TestSum(t *testing.T) {
	if Sum(1, 2) != 3 {
		t.Fatalf("unexpected sum")
	}
}
`
	if !passTests {
		testBody = `package app

import "testing"

func TestSum(t *testing.T) {
	if Sum(1, 2) == 3 {
		t.Fatalf("forced failure")
	}
}
`
	}
	if err := os.WriteFile(filepath.Join(wtDir, "pkg", "app", "app_test.go"), []byte(testBody), 0o644); err != nil {
		t.Fatalf("write app_test.go: %v", err)
	}

	cmd := exec.Command("git", "init")
	cmd.Dir = wtDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	rawDB := openReceiptsDB(t, dbPath)
	pathsJSON := `["pkg/app/app.go", "pkg/app/app_test.go"]`
	if _, err := rawDB.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at)
		VALUES ('rcpt-verify-accept', 'brain', ?, 9999999999, 100)
	`, pathsJSON); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}

	ctx := context.Background()
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey:  "k-verify-accept",
		Model:           "test-model",
		Role:            "scout",
		Permission:      "workspace_write",
		AddDirs:         []string{wtDir},
		SkipPermissions: true,
		Payload:         json.RawMessage(`{"prompt":"verify accept path","receipt_id":"rcpt-verify-accept"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	taskID = task.TaskID

	g8sDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open g8s db: %v", err)
	}
	defer g8sDB.Close()
	if _, err := g8sDB.Exec("UPDATE tasks SET state = ? WHERE task_id = ?", controlplane.StateWorkerCompleted, taskID); err != nil {
		t.Fatalf("update task state: %v", err)
	}

	return dbPath, store, taskID, wtDir
}

func TestVerifyAcceptsWorkerCompletedTaskOnPass(t *testing.T) {
	dbPath, store, taskID, wtDir := initVerifyAcceptWorktree(t, true)

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", taskID, "--worktree", wtDir, "--json"}, &stdout, &stderr, dbPath)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	ctx := context.Background()
	updated, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.State != controlplane.StateSupervisorAccepted {
		t.Fatalf("state = %q, want %q", updated.State, controlplane.StateSupervisorAccepted)
	}
}

func TestVerifyRejectsWorkerCompletedTaskOnHardFail(t *testing.T) {
	dbPath, store, taskID, wtDir := initVerifyAcceptWorktree(t, false)

	var stdout, stderr bytes.Buffer
	code := runVerifyWithIO([]string{"--task", taskID, "--worktree", wtDir, "--json"}, &stdout, &stderr, dbPath)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	ctx := context.Background()
	updated, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.State != controlplane.StateSupervisorRejected {
		t.Fatalf("state = %q, want %q", updated.State, controlplane.StateSupervisorRejected)
	}
}
