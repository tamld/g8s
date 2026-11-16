package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/lockfile"
	"github.com/tamld/g8s/internal/pathutil"
)

func setDeliverTaskState(t *testing.T, dbPath, taskID, state string) {
	t.Helper()
	db, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=rw"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE tasks SET state = ? WHERE task_id = ?", state, taskID); err != nil {
		t.Fatalf("update task state: %v", err)
	}
}

func deliverTestErrCode(res deliverRunResult) string {
	for _, stream := range []string{res.stderr, res.stdout} {
		stream = strings.TrimSpace(stream)
		if stream == "" || !strings.HasPrefix(stream, "{") {
			continue
		}
		var env testEnvelope
		if json.Unmarshal([]byte(stream), &env) == nil && env.Error != nil {
			return env.Error.Code
		}
	}
	return ""
}

func TestDeliverRejectsUnverifiedAndRejectedTaskWithoutOverride(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)
	wtDir := createWorktree(t, repoDir, "verified-gate")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	targetRel := "gate.txt"
	if err := os.WriteFile(filepath.Join(wtDir, targetRel), []byte("gated content"), 0o644); err != nil {
		t.Fatal(err)
	}
	rcID := issueTestReceipt(t, rcDbPath, []string{targetRel})

	newTask := func() string {
		return setupDeliverTask(t, dbPath, rcID, wtDir)
	}

	// WORKER_COMPLETED -> E_DENIED, repo untouched
	taskWC := newTask()
	setDeliverTaskState(t, dbPath, taskWC, controlplane.StateWorkerCompleted)
	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskWC, "--json")
	if res.exitCode == 0 {
		t.Fatalf("expected non-zero exit for WORKER_COMPLETED, got stdout=%s stderr=%s", res.stdout, res.stderr)
	}
	if code := deliverTestErrCode(res); code != cli.CodeDenied {
		t.Fatalf("expected %s, got code=%q exit=%d stderr=%s", cli.CodeDenied, code, res.exitCode, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(repoDir, targetRel)); !os.IsNotExist(err) {
		t.Fatalf("repo must be untouched after WORKER_COMPLETED denial")
	}

	// SUPERVISOR_REJECTED with --worktree -> E_DENIED
	taskRej := newTask()
	setDeliverTaskState(t, dbPath, taskRej, controlplane.StateSupervisorRejected)
	res = runDeliverCmd(t, binPath, repoDir, dbPath, taskRej, "--worktree", wtDir, "--json")
	if res.exitCode == 0 {
		t.Fatalf("expected non-zero for SUPERVISOR_REJECTED + --worktree")
	}
	if code := deliverTestErrCode(res); code != cli.CodeDenied {
		t.Fatalf("expected %s for rejected + worktree, got %q", cli.CodeDenied, code)
	}

	// SUPERVISOR_REJECTED + --allow-unverified-override with empty --override-reason -> E_USAGE
	taskUsage := newTask()
	setDeliverTaskState(t, dbPath, taskUsage, controlplane.StateSupervisorRejected)
	res = runDeliverCmd(t, binPath, repoDir, dbPath, taskUsage, "--allow-unverified-override", "--json")
	if res.exitCode != 2 {
		t.Fatalf("expected usage exit 2, got %d stderr=%s", res.exitCode, res.stderr)
	}
	if code := deliverTestErrCode(res); code != cli.CodeUsage {
		t.Fatalf("expected %s, got %q", cli.CodeUsage, code)
	}

	// SUPERVISOR_REJECTED + override + reason -> success + file applied
	taskOverride := newTask()
	setDeliverTaskState(t, dbPath, taskOverride, controlplane.StateSupervisorRejected)
	res = runDeliverCmd(t, binPath, repoDir, dbPath, taskOverride,
		"--allow-unverified-override", "--override-reason", "emergency hotfix", "--json")
	if res.exitCode != 0 {
		t.Fatalf("expected exit 0 on override, got %d stderr=%s stdout=%s", res.exitCode, res.stderr, res.stdout)
	}
	got, err := os.ReadFile(filepath.Join(repoDir, targetRel))
	if err != nil || string(got) != "gated content" {
		t.Fatalf("expected gate.txt applied after override: err=%v content=%q", err, string(got))
	}

	// SUPERVISOR_ACCEPTED -> exit 0
	_ = os.Remove(filepath.Join(repoDir, targetRel))
	taskOK := newTask()
	setDeliverTaskState(t, dbPath, taskOK, controlplane.StateSupervisorAccepted)
	res = runDeliverCmd(t, binPath, repoDir, dbPath, taskOK, "--json")
	if res.exitCode != 0 {
		t.Fatalf("expected exit 0 for SUPERVISOR_ACCEPTED, got %d stderr=%s", res.exitCode, res.stderr)
	}
	got, err = os.ReadFile(filepath.Join(repoDir, targetRel))
	if err != nil || string(got) != "gated content" {
		t.Fatalf("expected gate.txt after accepted deliver: err=%v", err)
	}
}

func TestDeliverWorkspaceLockContention(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)
	wtDir := createWorktree(t, repoDir, "lock-contention")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	rel := "locked.txt"
	if err := os.WriteFile(filepath.Join(wtDir, rel), []byte("lock test"), 0o644); err != nil {
		t.Fatal(err)
	}
	rcID := issueTestReceipt(t, rcDbPath, []string{rel})
	taskID := setupDeliverTask(t, dbPath, rcID, wtDir)
	setDeliverTaskState(t, dbPath, taskID, controlplane.StateSupervisorAccepted)

	repoKey := filepath.Clean(repoDir)
	if eval, evalErr := filepath.EvalSymlinks(repoKey); evalErr == nil {
		repoKey = eval
	}
	sum := sha256.Sum256([]byte(repoKey))
	lockPath := filepath.Join(filepath.Dir(dbPath), fmt.Sprintf("deliver-%x.lock", sum[:8]))
	hold, err := lockfile.TryLock(lockPath)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	defer func() {
		if relErr := hold.Release(); relErr != nil && !os.IsNotExist(relErr) {
			t.Logf("release lock: %v", relErr)
		}
	}()

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID, "--json")
	if res.exitCode == 0 {
		t.Fatalf("expected failure under lock contention")
	}
	if code := deliverTestErrCode(res); code != cli.CodeConflict {
		t.Fatalf("expected %s, got %q stderr=%s", cli.CodeConflict, code, res.stderr)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, rel)); !os.IsNotExist(statErr) {
		t.Fatalf("file must not be written under lock contention")
	}

	if relErr := hold.Release(); relErr != nil {
		t.Fatalf("Release: %v", relErr)
	}
	res = runDeliverCmd(t, binPath, repoDir, dbPath, taskID, "--json")
	if res.exitCode != 0 {
		t.Fatalf("expected success after lock release, got %d stderr=%s", res.exitCode, res.stderr)
	}
}

func TestDeliverBaseSHADivergenceGuard(t *testing.T) {
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)

	headC1 := strings.TrimSpace(gitRevParse(t, repoDir))
	wtDir := createWorktree(t, repoDir, "base-sha-guard")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cp.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	rel := "diverge.txt"
	if err := os.WriteFile(filepath.Join(wtDir, rel), []byte("from worktree at C1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Advance main repo to C2 while worktree remains at C1
	if err := os.WriteFile(filepath.Join(repoDir, "advance.txt"), []byte("main moved"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInRepo(t, repoDir, "add", "advance.txt")
	gitInRepo(t, repoDir, "commit", "-qm", "advance to C2")
	headC2 := strings.TrimSpace(gitRevParse(t, repoDir))
	if headC1 == headC2 {
		t.Fatalf("expected distinct commits for divergence test")
	}

	rcID := issueTestReceipt(t, rcDbPath, []string{rel})
	taskID := setupDeliverTask(t, dbPath, rcID, wtDir)
	setDeliverTaskState(t, dbPath, taskID, controlplane.StateSupervisorAccepted)

	res := runDeliverCmd(t, binPath, repoDir, dbPath, taskID, "--json")
	if res.exitCode == 0 {
		t.Fatalf("expected STALE_HEAD conflict without --expected-head-oid")
	}
	if code := deliverTestErrCode(res); code != cli.CodeConflict {
		t.Fatalf("expected %s, got %q", cli.CodeConflict, code)
	}
	combined := res.stderr + res.stdout
	if !strings.Contains(combined, "STALE_HEAD") {
		t.Fatalf("expected STALE_HEAD in output: %s", combined)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, rel)); !os.IsNotExist(statErr) {
		t.Fatalf("diverge.txt must not be copied on STALE_HEAD")
	}

	okRes := runDeliverCmd(t, binPath, repoDir, dbPath, taskID, "--expected-head-oid", headC2, "--json")
	if okRes.exitCode != 0 {
		t.Fatalf("expected success with matching --expected-head-oid, got %d stderr=%s", okRes.exitCode, okRes.stderr)
	}
	got, err := os.ReadFile(filepath.Join(repoDir, rel))
	if err != nil || string(got) != "from worktree at C1" {
		t.Fatalf("expected diverge.txt delivered: err=%v", err)
	}
}

func gitRevParse(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return string(out)
}

func gitInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
