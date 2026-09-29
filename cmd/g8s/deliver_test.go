package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/receipt"
	_ "modernc.org/sqlite"
)

func createWorktree(t *testing.T, repoDir, branch string) string {
	t.Helper()
	wtDir := filepath.Join(t.TempDir(), "wt-"+branch)
	cmd := exec.Command("git", "worktree", "add", wtDir, "-b", branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return wtDir
}

func issueTestReceipt(t *testing.T, rcDbPath string, allowedPaths []string) string {
	t.Helper()
	mgr, err := receipt.NewReceiptManager(rcDbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	defer mgr.Close()

	rc, err := mgr.IssueReceipt("test-issuer", allowedPaths, time.Hour,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
			RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: "cli-default",
		}),
		receipt.WithProvenanceContext("g8s/"+Version, Commit, "trace-test"),
	)
	if err != nil {
		t.Fatalf("IssueReceipt: %v", err)
	}
	return rc.ReceiptID
}

func setupDeliverTask(t *testing.T, dbPath string, receiptID string, wtDir string) string {
	t.Helper()
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	payloadMap := map[string]any{
		"prompt": "deliver test",
	}
	if receiptID != "" {
		payloadMap["receipt_id"] = receiptID
	}
	payloadBytes, _ := json.Marshal(payloadMap)
	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: "test-task-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{"."},
		Payload:        payloadBytes,
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	if wtDir != "" {
		resultJSON := fmt.Sprintf(`{"status":"succeeded","deliverable":{"mode":"worktree","dir":%q}}`, wtDir)
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer db.Close()
		if _, err := db.Exec("UPDATE tasks SET result_json = ? WHERE task_id = ?", resultJSON, task.TaskID); err != nil {
			t.Fatalf("update task result: %v", err)
		}
	}
	return task.TaskID
}

type deliverRunResult struct {
	exitCode int
	stdout   string
	stderr   string
	envelope testEnvelope
}

func runDeliverCmd(t *testing.T, binPath, cwd, dbPath string, args ...string) deliverRunResult {
	t.Helper()
	cmdArgs := append([]string{"deliver"}, args...)
	cmd := exec.Command(binPath, cmdArgs...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "G8S_DB="+dbPath)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("exec deliver failed: %v", err)
		}
	}
	stdoutStr := strings.TrimSpace(outBuf.String())
	stderrStr := strings.TrimSpace(errBuf.String())
	var env testEnvelope
	if stdoutStr != "" && strings.HasPrefix(stdoutStr, "{") {
		_ = json.Unmarshal([]byte(stdoutStr), &env)
	}
	return deliverRunResult{
		exitCode: exitCode,
		stdout:   stdoutStr,
		stderr:   stderrStr,
		envelope: env,
	}
}

// 1. Task with pointer + worktree containing 2 in-scope dirty files + receipt covering them → apply copies both to cwd, envelope lists both.
func TestDeliverInScopeFilesApplied(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)
	wtDir := createWorktree(t, repoDir, "case-1")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	f1Path := filepath.Join(wtDir, "file1.txt")
	if err := os.WriteFile(f1Path, []byte("content 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2Dir := filepath.Join(wtDir, "pkg", "sub")
	if err := os.MkdirAll(f2Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f2Path := filepath.Join(f2Dir, "file2.txt")
	if err := os.WriteFile(f2Path, []byte("content 2"), 0o755); err != nil {
		t.Fatal(err)
	}

	rcID := issueTestReceipt(t, rcDbPath, []string{"file1.txt", "pkg/**"})
	taskID := setupDeliverTask(t, dbPath, rcID, wtDir)

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID)
	if res.exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s, stdout: %s", res.exitCode, res.stderr, res.stdout)
	}

	if res.envelope.Kind != "deliver" || res.envelope.Command != "deliver" {
		t.Fatalf("unexpected envelope: %+v", res.envelope)
	}

	var data deliverData
	if err := json.Unmarshal(res.envelope.Data, &data); err != nil {
		t.Fatalf("unmarshal envelope data: %v", err)
	}

	if len(data.Applied) != 2 {
		t.Fatalf("expected 2 applied files, got %d: %v", len(data.Applied), data.Applied)
	}
	if data.TaskID != taskID {
		t.Errorf("expected task_id %q, got %q", taskID, data.TaskID)
	}
	if data.ReceiptID != rcID {
		t.Errorf("expected receipt_id %q, got %q", rcID, data.ReceiptID)
	}
	if data.DeliverableDir != wtDir {
		t.Errorf("expected deliverable_dir %q, got %q", wtDir, data.DeliverableDir)
	}

	// Verify files in cwd (repoDir)
	c1, err := os.ReadFile(filepath.Join(repoDir, "file1.txt"))
	if err != nil {
		t.Fatalf("file1.txt not copied to cwd: %v", err)
	}
	if string(c1) != "content 1" {
		t.Errorf("file1.txt content mismatch: %q", string(c1))
	}

	c2, err := os.ReadFile(filepath.Join(repoDir, "pkg", "sub", "file2.txt"))
	if err != nil {
		t.Fatalf("pkg/sub/file2.txt not copied to cwd: %v", err)
	}
	if string(c2) != "content 2" {
		t.Errorf("file2.txt content mismatch: %q", string(c2))
	}

	fi2, err := os.Stat(filepath.Join(repoDir, "pkg", "sub", "file2.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi2.Mode().Perm() != 0o755 {
		t.Errorf("expected mode 0755 preserved, got %o", fi2.Mode().Perm())
	}
}

// 2. --dry-run → files listed, nothing copied.
func TestDeliverDryRun(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)
	wtDir := createWorktree(t, repoDir, "case-2")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	f1Path := filepath.Join(wtDir, "dry1.txt")
	if err := os.WriteFile(f1Path, []byte("dry 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2Path := filepath.Join(wtDir, "dry2.txt")
	if err := os.WriteFile(f2Path, []byte("dry 2"), 0o644); err != nil {
		t.Fatal(err)
	}

	rcID := issueTestReceipt(t, rcDbPath, []string{"dry1.txt", "dry2.txt"})
	taskID := setupDeliverTask(t, dbPath, rcID, wtDir)

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID, "--dry-run")
	if res.exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", res.exitCode, res.stderr)
	}

	var data deliverData
	if err := json.Unmarshal(res.envelope.Data, &data); err != nil {
		t.Fatalf("unmarshal envelope data: %v", err)
	}
	if len(data.Applied) != 2 {
		t.Fatalf("expected 2 files listed in applied under dry-run, got %d: %v", len(data.Applied), data.Applied)
	}

	// Verify nothing was copied to cwd
	if _, err := os.Stat(filepath.Join(repoDir, "dry1.txt")); !os.IsNotExist(err) {
		t.Errorf("dry1.txt should NOT have been copied in dry-run mode")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "dry2.txt")); !os.IsNotExist(err) {
		t.Errorf("dry2.txt should NOT have been copied in dry-run mode")
	}
}

// 3. One out-of-scope file among in-scope ones → NOTHING applied (both in-scope files absent from cwd), non-zero exit, offending path listed.
func TestDeliverOutOfScopeRefusal(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)
	wtDir := createWorktree(t, repoDir, "case-3")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	f1Path := filepath.Join(wtDir, "good1.txt")
	if err := os.WriteFile(f1Path, []byte("good 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2Path := filepath.Join(wtDir, "good2.txt")
	if err := os.WriteFile(f2Path, []byte("good 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(wtDir, "secret_key.pem")
	if err := os.WriteFile(badPath, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}

	rcID := issueTestReceipt(t, rcDbPath, []string{"good1.txt", "good2.txt"})
	taskID := setupDeliverTask(t, dbPath, rcID, wtDir)

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID)
	if res.exitCode == 0 {
		t.Fatalf("expected non-zero exit code for out-of-scope files, got 0")
	}

	combinedOutput := res.stderr + " " + res.stdout
	if !strings.Contains(combinedOutput, "secret_key.pem") {
		t.Errorf("expected offending path 'secret_key.pem' in output, got:\nstderr: %s\nstdout: %s", res.stderr, res.stdout)
	}

	// Verify NOTHING applied (both good1.txt and good2.txt absent from repoDir)
	if _, err := os.Stat(filepath.Join(repoDir, "good1.txt")); !os.IsNotExist(err) {
		t.Errorf("good1.txt was copied despite out-of-scope failure (atomic breach)")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "good2.txt")); !os.IsNotExist(err) {
		t.Errorf("good2.txt was copied despite out-of-scope failure (atomic breach)")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "secret_key.pem")); !os.IsNotExist(err) {
		t.Errorf("secret_key.pem was copied despite out-of-scope failure")
	}
}

// 4. Task without pointer → clear usage error.
func TestDeliverTaskWithoutPointer(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	rcID := issueTestReceipt(t, rcDbPath, []string{"*"})
	taskID := setupDeliverTask(t, dbPath, rcID, "") // No pointer dir

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID)
	if res.exitCode != 2 {
		t.Fatalf("expected usage error exit code 2, got %d. stderr: %s", res.exitCode, res.stderr)
	}

	expectedMsg := fmt.Sprintf("no deliverable pointer for %s (attempt was not worktree-isolated, failed, or predates the pointer contract)", taskID)
	combined := res.stderr + " " + res.stdout
	if !strings.Contains(combined, expectedMsg) {
		t.Errorf("expected error message %q, got:\nstderr: %s\nstdout: %s", expectedMsg, res.stderr, res.stdout)
	}
}

// 5. Missing receipt_id in payload → refusal, nothing applied.
func TestDeliverMissingReceiptID(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)
	wtDir := createWorktree(t, repoDir, "case-5")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")

	fPath := filepath.Join(wtDir, "mod.txt")
	if err := os.WriteFile(fPath, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	taskID := setupDeliverTask(t, dbPath, "", wtDir) // Missing receipt_id

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID)
	if res.exitCode == 0 {
		t.Fatalf("expected refusal exit code > 0 when receipt_id is missing, got 0")
	}

	combined := res.stderr + " " + res.stdout
	if !strings.Contains(combined, "receipt_id") {
		t.Errorf("expected error mentioning receipt_id, got:\nstderr: %s\nstdout: %s", res.stderr, res.stdout)
	}

	if _, err := os.Stat(filepath.Join(repoDir, "mod.txt")); !os.IsNotExist(err) {
		t.Errorf("mod.txt should not have been copied without receipt")
	}
}

// 6. Untracked file in scope is applied; renamed entry reported + skipped.
func TestDeliverUntrackedAppliedRenamedSkipped(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)

	// Add an initial committed file in repo
	origFile := filepath.Join(repoDir, "original.txt")
	if err := os.WriteFile(origFile, []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAdd := exec.Command("git", "add", "original.txt")
	gitAdd.Dir = repoDir
	if out, err := gitAdd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	gitCommit := exec.Command("git", "commit", "-m", "add original")
	gitCommit.Dir = repoDir
	if out, err := gitCommit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	wtDir := createWorktree(t, repoDir, "case-6")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	// In worktree: rename original.txt -> renamed.txt
	gitMv := exec.Command("git", "mv", "original.txt", "renamed.txt")
	gitMv.Dir = wtDir
	if out, err := gitMv.CombinedOutput(); err != nil {
		t.Fatalf("git mv: %v\n%s", err, out)
	}

	// Also add an untracked file
	untrackedPath := filepath.Join(wtDir, "untracked.txt")
	if err := os.WriteFile(untrackedPath, []byte("new untracked file"), 0o644); err != nil {
		t.Fatal(err)
	}

	rcID := issueTestReceipt(t, rcDbPath, []string{"untracked.txt"})
	taskID := setupDeliverTask(t, dbPath, rcID, wtDir)

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID)
	if res.exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s, stdout: %s", res.exitCode, res.stderr, res.stdout)
	}

	var data deliverData
	if err := json.Unmarshal(res.envelope.Data, &data); err != nil {
		t.Fatalf("unmarshal envelope data: %v", err)
	}

	// Untracked file applied
	if len(data.Applied) != 1 || data.Applied[0] != "untracked.txt" {
		t.Errorf("expected Applied to contain ['untracked.txt'], got: %v", data.Applied)
	}

	// Renamed entry skipped
	if len(data.Skipped) != 1 || data.Skipped[0] != "renamed.txt" {
		t.Errorf("expected Skipped to contain ['renamed.txt'], got: %v", data.Skipped)
	}

	// Verify stderr reported the skip warning
	if !strings.Contains(res.stderr, "skipping") || !strings.Contains(res.stderr, "rename") {
		t.Errorf("expected skip warning in stderr, got: %s", res.stderr)
	}

	// Verify untracked.txt copied to repoDir
	c, err := os.ReadFile(filepath.Join(repoDir, "untracked.txt"))
	if err != nil {
		t.Fatalf("untracked.txt not copied to cwd: %v", err)
	}
	if string(c) != "new untracked file" {
		t.Errorf("untracked.txt content mismatch: %q", string(c))
	}

	// Verify renamed.txt NOT copied to repoDir
	if _, err := os.Stat(filepath.Join(repoDir, "renamed.txt")); !os.IsNotExist(err) {
		t.Errorf("renamed.txt should not have been copied")
	}
}
