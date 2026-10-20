package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed in %s: %v (%s)", args, dir, err, string(out))
		}
	}

	run("init")
	run("config", "user.email", "f0-test@example.com")
	run("config", "user.name", "F0 Test")
	run("config", "commit.gpgsign", "false")

	// Create initial file and commit
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Test Repo\n"), 0o644); err != nil {
		t.Fatalf("writing readme: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	return dir
}

func TestChainVerifyAndTamper(t *testing.T) {
	repoDir := initTestGitRepo(t)
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("open repo ledger: %v", err)
	}

	// Append 5 entries
	for i := 1; i <= 5; i++ {
		entry := &Entry{
			ID:           fmt.Sprintf("ev-%08x", i),
			Ts:           "2026-10-20T08:00:00Z",
			Kind:         "go-test",
			Repo:         repoDir,
			Branch:       "main",
			SHA:          "abc1234567890abcdef1234567890abcdef1234",
			Dirty:        false,
			Cmd:          []string{"go", "test", "-json", "./..."},
			Exit:         0,
			DurationMS:   100,
			StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			StderrSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Tail:         "PASS",
			ItemsChecked: 10,
			Status:       "PASS",
		}
		if err := ledger.Append(entry); err != nil {
			t.Fatalf("append entry %d: %v", i, err)
		}
	}

	// Verify passes initially
	report, err := ledger.Verify(repoDir)
	if err != nil {
		t.Fatalf("verify failed on pristine ledger: %v", err)
	}
	if report.EntriesCount != 5 {
		t.Fatalf("expected 5 entries, got %d", report.EntriesCount)
	}

	// Tamper middle line (line 3, seq 3)
	content, err := os.ReadFile(ledger.Path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}

	// Tamper line 3 by changing items_checked from 10 to 999
	lines[2] = strings.Replace(lines[2], `"items_checked":10`, `"items_checked":999`, 1)
	tamperedContent := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(ledger.Path, []byte(tamperedContent), 0o644); err != nil {
		t.Fatalf("write tampered ledger: %v", err)
	}

	// Verify must fail at seq 3
	_, err = ledger.Verify(repoDir)
	if err == nil {
		t.Fatalf("expected verify to fail on tampered middle line, but it passed")
	}
	if !strings.Contains(err.Error(), "tamper detected at seq 3") && !strings.Contains(err.Error(), "seq 3") {
		t.Fatalf("expected error to mention seq 3, got: %v", err)
	}
}

func TestTruncationAfterAnchor(t *testing.T) {
	repoDir := initTestGitRepo(t)
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("open repo ledger: %v", err)
	}

	// Append 3 entries
	for i := 1; i <= 3; i++ {
		entry := &Entry{
			ID:           fmt.Sprintf("ev-%08x", i),
			Ts:           "2026-10-20T08:00:00Z",
			Kind:         "go-test",
			Repo:         repoDir,
			Branch:       "main",
			SHA:          "abc1234567890abcdef1234567890abcdef1234",
			Dirty:        false,
			Cmd:          []string{"go", "test", "-json", "./..."},
			Exit:         0,
			DurationMS:   100,
			StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			StderrSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Tail:         "PASS",
			ItemsChecked: 5,
			Status:       "PASS",
		}
		if err := ledger.Append(entry); err != nil {
			t.Fatalf("append entry %d: %v", i, err)
		}
	}

	// Get anchor for head (seq 3)
	anchorLine, err := AnchorLine(repoDir)
	if err != nil {
		t.Fatalf("AnchorLine: %v", err)
	}
	if !strings.HasPrefix(anchorLine, "F0-Ledger-Head: 3:") {
		t.Fatalf("expected anchor for seq 3, got %q", anchorLine)
	}

	// Publish anchor as commit trailer in git repo
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", fmt.Sprintf("merge commit\n\n%s\n", anchorLine))
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit anchor failed: %v (%s)", err, string(out))
	}

	// Append 2 more entries (seq 4 and 5)
	for i := 4; i <= 5; i++ {
		entry := &Entry{
			ID:           fmt.Sprintf("ev-%08x", i),
			Ts:           "2026-10-20T08:01:00Z",
			Kind:         "go-test",
			Repo:         repoDir,
			Branch:       "main",
			SHA:          "abc1234567890abcdef1234567890abcdef1234",
			Dirty:        false,
			Cmd:          []string{"go", "test", "-json", "./..."},
			Exit:         0,
			DurationMS:   100,
			StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			StderrSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Tail:         "PASS",
			ItemsChecked: 5,
			Status:       "PASS",
		}
		if err := ledger.Append(entry); err != nil {
			t.Fatalf("append entry %d: %v", i, err)
		}
	}

	// Verify passes with 5 entries and 1 anchor
	rep, err := ledger.Verify(repoDir)
	if err != nil {
		t.Fatalf("verify pristine before truncation: %v", err)
	}
	if rep.EntriesCount != 5 || rep.AnchorsCount != 1 {
		t.Fatalf("expected 5 entries, 1 anchor, got %+v", rep)
	}

	// Now simulate truncation: truncate ledger so it only contains seq 1 and 2
	content, err := os.ReadFile(ledger.Path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	truncatedContent := strings.Join(lines[:2], "\n") + "\n"
	if err := os.WriteFile(ledger.Path, []byte(truncatedContent), 0o644); err != nil {
		t.Fatalf("write truncated ledger: %v", err)
	}

	// Chain alone for seq 1 and 2 is valid, but anchor for seq 3 is missing!
	// Verify MUST fail due to truncation after anchor!
	_, err = ledger.Verify(repoDir)
	if err == nil {
		t.Fatalf("expected verify to fail on truncated ledger, but it passed")
	}
	if !strings.Contains(err.Error(), "ledger truncated") || !strings.Contains(err.Error(), "sequence 3") {
		t.Fatalf("expected truncation error for seq 3, got: %v", err)
	}
}

func TestConcurrentWriters(t *testing.T) {
	// Child process handler
	if os.Getenv("F0_TEST_WORKER") == "1" {
		ledgerPath := os.Getenv("F0_TEST_LEDGER_PATH")
		workerID := os.Getenv("F0_TEST_WORKER_ID")
		ledger := OpenLedgerAtPath(ledgerPath)
		for i := 0; i < 10; i++ {
			entry := &Entry{
				ID:           fmt.Sprintf("ev-%s-%04d", workerID, i),
				Ts:           "2026-10-20T08:00:00Z",
				Kind:         "concurrent-test",
				Repo:         "/tmp/test",
				Branch:       "main",
				SHA:          "abcdef1234567890abcdef1234567890abcdef12",
				Dirty:        false,
				Cmd:          []string{"test", workerID, strconv.Itoa(i)},
				Exit:         0,
				DurationMS:   10,
				StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				StderrSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				Tail:         "ok",
				ItemsChecked: 1,
				Status:       "PASS",
			}
			if err := ledger.Append(entry); err != nil {
				fmt.Fprintf(os.Stderr, "worker %s append error: %v\n", workerID, err)
				os.Exit(2)
			}
		}
		os.Exit(0)
	}

	// Parent process: spawn 4 concurrent OS worker processes
	tempDir := t.TempDir()
	ledgerPath := filepath.Join(tempDir, "ledger.jsonl")

	numWorkers := 4
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerIndex int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestConcurrentWriters")
			cmd.Env = append(os.Environ(),
				"F0_TEST_WORKER=1",
				"F0_TEST_LEDGER_PATH="+ledgerPath,
				fmt.Sprintf("F0_TEST_WORKER_ID=%d", workerIndex),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errCh <- fmt.Errorf("worker %d failed: %v (%s)", workerIndex, err, string(out))
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent worker error: %v", err)
	}

	// Verify all entries written:
	ledger := OpenLedgerAtPath(ledgerPath)
	entries, err := ledger.ReadEntries()
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}

	expectedTotal := numWorkers * 10
	if len(entries) != expectedTotal {
		t.Fatalf("expected %d entries, got %d", expectedTotal, len(entries))
	}

	// Verify seq is strictly increasing 1..expectedTotal, no gaps, no duplicates
	for i, e := range entries {
		expectedSeq := uint64(i + 1)
		if e.Seq != expectedSeq {
			t.Fatalf("entry %d has seq %d, expected %d", i, e.Seq, expectedSeq)
		}
		if i > 0 && e.PrevHash != entries[i-1].Hash {
			t.Fatalf("entry %d prev_hash mismatch: got %s, expected %s", i, e.PrevHash, entries[i-1].Hash)
		}
	}

	// Verify entire chain integrity
	report, err := ledger.Verify("")
	if err != nil {
		t.Fatalf("ledger chain verify failed on concurrent output: %v", err)
	}
	if report.EntriesCount != expectedTotal {
		t.Fatalf("expected %d entries verified, got %d", expectedTotal, report.EntriesCount)
	}
}

func TestG8SStateDirIsolation(t *testing.T) {
	repoDir := initTestGitRepo(t)

	// Baseline paths without G8S_STATE_DIR
	repoPath1, err := RepoLedgerPath(repoDir)
	if err != nil {
		t.Fatalf("RepoLedgerPath: %v", err)
	}
	globalPath1, err := GlobalLedgerPath()
	if err != nil {
		t.Fatalf("GlobalLedgerPath: %v", err)
	}

	// Set G8S_STATE_DIR to a fake directory
	customStateDir := filepath.Join(t.TempDir(), "fake-state-dir")
	t.Setenv("G8S_STATE_DIR", customStateDir)

	// Call paths again
	repoPath2, err := RepoLedgerPath(repoDir)
	if err != nil {
		t.Fatalf("RepoLedgerPath with G8S_STATE_DIR: %v", err)
	}
	globalPath2, err := GlobalLedgerPath()
	if err != nil {
		t.Fatalf("GlobalLedgerPath with G8S_STATE_DIR: %v", err)
	}

	if repoPath1 != repoPath2 {
		t.Errorf("RepoLedgerPath changed with G8S_STATE_DIR: before=%s, after=%s", repoPath1, repoPath2)
	}
	if strings.Contains(repoPath2, customStateDir) {
		t.Errorf("RepoLedgerPath contains G8S_STATE_DIR: %s", repoPath2)
	}

	if globalPath1 != globalPath2 {
		t.Errorf("GlobalLedgerPath changed with G8S_STATE_DIR: before=%s, after=%s", globalPath1, globalPath2)
	}
	if strings.Contains(globalPath2, customStateDir) {
		t.Errorf("GlobalLedgerPath contains G8S_STATE_DIR: %s", globalPath2)
	}
}

func TestLedgerCLICommands(t *testing.T) {
	repoDir := initTestGitRepo(t)
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}

	// Append sample entry
	entry := &Entry{
		ID:           "ev-aabb1122",
		Ts:           "2026-10-20T08:00:00Z",
		Kind:         "go-test",
		Repo:         repoDir,
		Branch:       "main",
		SHA:          "abcdef1234567890abcdef1234567890abcdef12",
		Dirty:        false,
		Cmd:          []string{"go", "test", "-json", "./..."},
		Exit:         0,
		DurationMS:   150,
		StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		StderrSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Tail:         "PASS",
		ItemsChecked: 15,
		Status:       "PASS",
	}
	if err := ledger.Append(entry); err != nil {
		t.Fatalf("append entry: %v", err)
	}

	// Test show
	if code := runLedgerShow([]string{"ev-aabb1122", "--repo", repoDir}); code != 0 {
		t.Errorf("runLedgerShow failed with code %d", code)
	}
	if code := runLedgerShow([]string{"nonexistent-id", "--repo", repoDir}); code == 0 {
		t.Errorf("expected runLedgerShow to fail for nonexistent id")
	}

	// Test list --json
	if code := runLedgerList([]string{"--repo", repoDir, "--json"}); code != 0 {
		t.Errorf("runLedgerList --json failed with code %d", code)
	}

	// Test list with filters
	if code := runLedgerList([]string{"--repo", repoDir, "--kind", "go-test", "--sha", "abcdef"}); code != 0 {
		t.Errorf("runLedgerList with filters failed with code %d", code)
	}

	// Test verify command
	if code := runLedgerVerify([]string{"--repo", repoDir}); code != 0 {
		t.Errorf("runLedgerVerify failed with code %d", code)
	}
}
