package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

func initScratchGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	runGit("init")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "src/a.txt")
	runGit("commit", "-m", "seed")
	return dir
}

func issueCommitTestReceipt(t *testing.T, dir string, allowed []string) string {
	t.Helper()
	t.Setenv("G8S_STATE_DIR", dir)
	mgr, err := receipt.NewReceiptManager(filepath.Join(dir, "receipts.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	rc, err := mgr.IssueReceipt("brain", allowed, time.Minute,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI: "g8s://envelope/write-receipt/v1", FieldOrder: []string{"receipt_id", "issuer", "allowed_paths"}, RequiredFields: []string{"receipt_id"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{RulesetVersion: "v1", PipelineDigest: "deadbeef", ADRRef: "docs/adr/0001.md"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return rc.ReceiptID
}

func writeCommitRequest(t *testing.T, worktree, id string, req gitCommitRequestFile) string {
	t.Helper()
	dir := gitCommitRequestsPath(worktree)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCommitResult(t *testing.T, worktree, id string) gitCommitResultFile {
	t.Helper()
	path := filepath.Join(gitCommitRequestsPath(worktree), id+".result.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result %s: %v", path, err)
	}
	var res gitCommitResultFile
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCommitRequestValidationMatrix(t *testing.T) {
	dir := initScratchGitRepo(t)
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)
	receiptID := issueCommitTestReceipt(t, stateDir, []string{"src/**"})
	sup := &Supervisor{clock: time.Now}

	baseReq := taskRequest{
		Permission:        "workspace_write",
		AllowWorkerCommit: true,
		ReceiptID:         receiptID,
		Provider:          "codex",
	}

	cases := []struct {
		name string
		req  gitCommitRequestFile
		task taskRequest
		want string
	}{
		{"not_enabled", gitCommitRequestFile{V: 1, Op: "commit", Message: "m", AddPaths: []string{"src/a.txt"}}, taskRequest{Permission: "workspace_write", ReceiptID: receiptID}, "E_COMMIT_REQUEST_NOT_ENABLED"},
		{"read_only", gitCommitRequestFile{V: 1, Op: "commit", Message: "m", AddPaths: []string{"src/a.txt"}}, taskRequest{Permission: "read_only", AllowWorkerCommit: true, ReceiptID: receiptID}, "E_COMMIT_REQUEST_FORBIDDEN_READ_ONLY"},
		{"empty_message", gitCommitRequestFile{V: 1, Op: "commit", Message: "  ", AddPaths: []string{"src/a.txt"}}, baseReq, "E_COMMIT_MESSAGE_EMPTY"},
		{"no_add_paths", gitCommitRequestFile{V: 1, Op: "commit", Message: "m", AddPaths: nil}, baseReq, "E_COMMIT_ADD_PATHS_REQUIRED"},
		{"bad_version", gitCommitRequestFile{V: 2, Op: "commit", Message: "m", AddPaths: []string{"src/a.txt"}}, baseReq, "E_COMMIT_REQUEST_UNSUPPORTED_VERSION"},
		{"unknown_op", gitCommitRequestFile{V: 1, Op: "push", Message: "m", AddPaths: []string{"src/a.txt"}}, baseReq, "E_COMMIT_REQUEST_UNKNOWN_OP"},
		{"path_escape", gitCommitRequestFile{V: 1, Op: "commit", Message: "m", AddPaths: []string{"../escape.txt"}}, baseReq, "E_COMMIT_PATH_OUT_OF_SCOPE"},
		{"out_of_scope", gitCommitRequestFile{V: 1, Op: "commit", Message: "m", AddPaths: []string{"other/x.txt"}}, baseReq, "E_COMMIT_PATH_OUT_OF_SCOPE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "req-" + tc.name
			reqPath := writeCommitRequest(t, dir, id, tc.req)
			resPath := resultMarkerPath(reqPath)
			sup.processGitCommitRequest(dir, "task-822", tc.task, reqPath, resPath, mustJSONBytes(tc.req), time.Now())
			got := readCommitResult(t, dir, id)
			if got.OK || !strings.Contains(got.Reason, tc.want) {
				t.Fatalf("want %q, got %+v", tc.want, got)
			}
		})
	}
}

func TestCommitRequestHappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git commit proxy execution is gated off on Windows")
	}
	dir := initScratchGitRepo(t)
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)
	receiptID := issueCommitTestReceipt(t, stateDir, []string{"src/**"})
	if err := os.WriteFile(filepath.Join(dir, "src", "b.txt"), []byte("more"), 0o644); err != nil {
		t.Fatal(err)
	}
	sup := &Supervisor{clock: time.Now}
	id := "happy"
	req := gitCommitRequestFile{V: 1, Op: "commit", Message: "worker commit", AddPaths: []string{"src/b.txt"}}
	reqPath := writeCommitRequest(t, dir, id, req)
	resPath := resultMarkerPath(reqPath)
	task := taskRequest{Permission: "workspace_write", AllowWorkerCommit: true, ReceiptID: receiptID, Provider: "codex"}
	sup.processGitCommitRequest(dir, "task-happy", task, reqPath, resPath, mustJSONBytes(req), time.Now())

	got := readCommitResult(t, dir, id)
	if !got.OK || got.Commit == "" {
		t.Fatalf("expected successful commit, got %+v", got)
	}
	logCmd := exec.Command("git", "-C", dir, "log", "-1", "--format=%B%n---%an%n%ae")
	out, err := logCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "Receipt: task-happy") {
		t.Fatalf("missing receipt trailer: %q", text)
	}
	if !strings.Contains(text, "g8s-worker task-happy codex") {
		t.Fatalf("unexpected author: %q", text)
	}
}

func TestCommitRequestSkipsAnswered(t *testing.T) {
	dir := initScratchGitRepo(t)
	id := "skip"
	req := gitCommitRequestFile{V: 1, Op: "commit", Message: "m", AddPaths: []string{"."}}
	reqPath := writeCommitRequest(t, dir, id, req)
	resPath := resultMarkerPath(reqPath)
	writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: "E_COMMIT_REQUEST_NOT_ENABLED"})
	sup := &Supervisor{clock: time.Now}
	sup.sweepGitCommitRequests(dir, "task", taskRequest{Permission: "workspace_write", AllowWorkerCommit: false})
	if _, err := os.Stat(resPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(resPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "E_COMMIT_REQUEST_NOT_ENABLED") {
		t.Fatalf("result marker rewritten: %s", string(raw))
	}
}

func TestCommitRequestFinalSweepAfterChildExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git commit proxy execution is gated off on Windows")
	}
	dir := initScratchGitRepo(t)
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)
	receiptID := issueCommitTestReceipt(t, stateDir, []string{"src/**"})
	if err := os.WriteFile(filepath.Join(dir, "src", "late.txt"), []byte("late"), 0o644); err != nil {
		t.Fatal(err)
	}

	childDone := make(chan struct{})
	sup := &Supervisor{clock: time.Now}
	task := taskRequest{Permission: "workspace_write", AllowWorkerCommit: true, ReceiptID: receiptID, Provider: "cursor"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		sup.superviseGitCommitRequests(context.Background(), dir, "task-late", task, childDone)
	}()

	time.Sleep(100 * time.Millisecond)
	id := "late"
	req := gitCommitRequestFile{V: 1, Op: "commit", Message: "late commit", AddPaths: []string{"src/late.txt"}}
	writeCommitRequest(t, dir, id, req)
	close(childDone)
	<-done

	got := readCommitResult(t, dir, id)
	if !got.OK {
		t.Fatalf("expected final sweep commit, got %+v", got)
	}
}

func TestCommitRequestWindowsGate(t *testing.T) {
	orig := gitCommitHostOS
	gitCommitHostOS = "windows"
	t.Cleanup(func() { gitCommitHostOS = orig })

	// The gate sits at the execution boundary (after validation and scope
	// resolution), so the fixture must be a fully valid request on a real
	// git repo — exactly what a Windows worker would reach before the
	// platform gate rejects execution (ADR-0041).
	dir := initScratchGitRepo(t)
	id := "win"
	sup := &Supervisor{clock: time.Now}
	receiptID := issueCommitTestReceipt(t, dir, []string{"**"})
	req := gitCommitRequestFile{V: 1, Op: "commit", Message: "m", AddPaths: []string{"."}}
	reqPath := writeCommitRequest(t, dir, id, req)
	resPath := resultMarkerPath(reqPath)
	task := taskRequest{Permission: "workspace_write", AllowWorkerCommit: true, ReceiptID: receiptID}
	sup.processGitCommitRequest(dir, "task-win", task, reqPath, resPath, mustJSONBytes(req), time.Now())
	got := readCommitResult(t, dir, id)
	if got.OK || got.Reason != "E_COMMIT_REQUEST_UNSUPPORTED_OS" {
		t.Fatalf("want Windows gate, got %+v", got)
	}
}

func mustJSONBytes(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
