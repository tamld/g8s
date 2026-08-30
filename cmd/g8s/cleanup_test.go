package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// MockProcessManager provides controllable process inspection and signaling for tests.
type MockProcessManager struct {
	mu           sync.Mutex
	processes    []ProcessInfo
	killedWith   map[int][]syscall.Signal
	aliveStatus  map[int]bool
	aliveQueries map[int]int
}

func NewMockProcessManager(procs []ProcessInfo) *MockProcessManager {
	alive := make(map[int]bool)
	for _, p := range procs {
		alive[p.PID] = true
	}
	return &MockProcessManager{
		processes:    procs,
		killedWith:   make(map[int][]syscall.Signal),
		aliveStatus:  alive,
		aliveQueries: make(map[int]int),
	}
}

func (m *MockProcessManager) FindGhostProcesses(ctx context.Context, heartbeatDir string, maxAge time.Duration, clock func() time.Time) ([]ProcessInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processes, nil
}

func (m *MockProcessManager) KillProcess(pid int, sig syscall.Signal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.killedWith[pid] = append(m.killedWith[pid], sig)
	m.aliveStatus[pid] = false
	return nil
}

func (m *MockProcessManager) IsProcessAlive(pid int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aliveQueries[pid]++
	return m.aliveStatus[pid]
}

// MockCleanupGitRunner provides scripted responses for Git operations.
type MockCleanupGitRunner struct {
	PorcelainOutput   string
	PrunedOutput      string
	MergedBranchesRes []string
	RemoteBranchesRes []string
	ClosedPRRes       []string
	LocalTagsRes      []TagInfo
	RemoteTagsRes     []string

	DeletedBranches []string
	DeletedTags     []string
	RemovedPaths    []string
	PruneCalled     bool
}

func (m *MockCleanupGitRunner) WorktreeListPorcelain(ctx context.Context, repoDir string) (string, error) {
	return m.PorcelainOutput, nil
}

func (m *MockCleanupGitRunner) WorktreePrune(ctx context.Context, repoDir string) (string, error) {
	m.PruneCalled = true
	return m.PrunedOutput, nil
}

func (m *MockCleanupGitRunner) WorktreeRemove(ctx context.Context, repoDir, wtPath string) error {
	m.RemovedPaths = append(m.RemovedPaths, wtPath)
	return nil
}

func (m *MockCleanupGitRunner) MergedBranches(ctx context.Context, repoDir string) ([]string, error) {
	return m.MergedBranchesRes, nil
}

func (m *MockCleanupGitRunner) RemoteBranches(ctx context.Context, repoDir string) ([]string, error) {
	return m.RemoteBranchesRes, nil
}

func (m *MockCleanupGitRunner) DeleteBranch(ctx context.Context, repoDir, branch string, force bool) error {
	m.DeletedBranches = append(m.DeletedBranches, branch)
	return nil
}

func (m *MockCleanupGitRunner) ClosedPRBranches(ctx context.Context, repoDir string) ([]string, error) {
	return m.ClosedPRRes, nil
}

func (m *MockCleanupGitRunner) LocalTags(ctx context.Context, repoDir string) ([]TagInfo, error) {
	return m.LocalTagsRes, nil
}

func (m *MockCleanupGitRunner) RemoteTags(ctx context.Context, repoDir string) ([]string, error) {
	return m.RemoteTagsRes, nil
}

func (m *MockCleanupGitRunner) DeleteTag(ctx context.Context, repoDir, tag string) error {
	m.DeletedTags = append(m.DeletedTags, tag)
	return nil
}

func TestGhostProcessCleanup(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	ghosts := []ProcessInfo{
		{
			PID:          1001,
			Binary:       "agy",
			CommandLine:  "agy -p test",
			Reason:       "no live heartbeat file",
			HasHeartbeat: false,
		},
		{
			PID:          1002,
			Binary:       "claude",
			CommandLine:  "claude --resume test",
			LastUpdate:   now.Add(-10 * time.Minute),
			Reason:       "heartbeat stale (>5m)",
			HasHeartbeat: true,
		},
	}

	t.Run("dry-run does not kill processes", func(t *testing.T) {
		procMgr := NewMockProcessManager(ghosts)
		cfg := CleanupConfig{
			Targets:        []string{TargetGhostProcess},
			DryRun:         true,
			Clock:          clock,
			ProcessManager: procMgr,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(report.Items))
		}
		for _, item := range report.Items {
			if item.Action != "would_kill" {
				t.Errorf("expected action would_kill, got %s", item.Action)
			}
		}
		if len(procMgr.killedWith) != 0 {
			t.Fatalf("expected 0 kill invocations in dry-run, got %d", len(procMgr.killedWith))
		}
	})

	t.Run("force mode without force-missing skips missing heartbeats and kills stale", func(t *testing.T) {
		procMgr := NewMockProcessManager(ghosts)
		cfg := CleanupConfig{
			Targets:        []string{TargetGhostProcess},
			DryRun:         false,
			ForceMissing:   false,
			GracePeriod:    100 * time.Millisecond,
			Clock:          clock,
			ProcessManager: procMgr,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(report.Items))
		}

		// 1001 (missing heartbeat) must be skipped
		if report.Items[0].ID != "1001" || report.Items[0].Action != "skipped" {
			t.Errorf("expected 1001 skipped without --force-missing, got %+v", report.Items[0])
		}
		if len(procMgr.killedWith[1001]) != 0 {
			t.Errorf("expected 1001 NOT killed without --force-missing, got %v", procMgr.killedWith[1001])
		}

		// 1002 (stale heartbeat) must be killed
		if report.Items[1].ID != "1002" || report.Items[1].Action != "killed" {
			t.Errorf("expected 1002 killed, got %+v", report.Items[1])
		}
		if len(procMgr.killedWith[1002]) == 0 || procMgr.killedWith[1002][0] != syscall.SIGTERM {
			t.Errorf("expected SIGTERM sent to 1002, got %v", procMgr.killedWith[1002])
		}
	})

	t.Run("force mode with force-missing kills both", func(t *testing.T) {
		procMgr := NewMockProcessManager(ghosts)
		cfg := CleanupConfig{
			Targets:        []string{TargetGhostProcess},
			DryRun:         false,
			ForceMissing:   true,
			GracePeriod:    100 * time.Millisecond,
			Clock:          clock,
			ProcessManager: procMgr,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(report.Items))
		}
		for _, item := range report.Items {
			if item.Action != "killed" {
				t.Errorf("expected action killed, got %s", item.Action)
			}
		}

		if len(procMgr.killedWith[1001]) == 0 || procMgr.killedWith[1001][0] != syscall.SIGTERM {
			t.Errorf("expected SIGTERM sent to 1001 with --force-missing, got %v", procMgr.killedWith[1001])
		}
		if len(procMgr.killedWith[1002]) == 0 || procMgr.killedWith[1002][0] != syscall.SIGTERM {
			t.Errorf("expected SIGTERM sent to 1002 with --force-missing, got %v", procMgr.killedWith[1002])
		}
	})
}

func TestConfirmForceMissing(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"invalid\n", false},
	}

	for _, tt := range tests {
		in := strings.NewReader(tt.input)
		out := new(bytes.Buffer)
		got := confirmForceMissing(in, out)
		if got != tt.want {
			t.Errorf("confirmForceMissing(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestOrphanWorktreeCleanup(t *testing.T) {
	tempDir := t.TempDir()
	mainDir := filepath.Join(tempDir, "main-repo")
	_ = os.MkdirAll(mainDir, 0o755)

	missingPath := filepath.Join(tempDir, "wt-deleted")

	porcelain := fmt.Sprintf(`worktree %s
HEAD abc1234
branch refs/heads/main

worktree %s
HEAD def5678
branch refs/heads/agy/sup-sub-1
prunable gitdir file points to non-existent location
`, mainDir, missingPath)

	t.Run("dry-run reports prunable worktrees", func(t *testing.T) {
		gitRunner := &MockCleanupGitRunner{PorcelainOutput: porcelain}
		cfg := CleanupConfig{
			RepoDir:   mainDir,
			Targets:   []string{TargetOrphanWT},
			DryRun:    true,
			GitRunner: gitRunner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 orphan worktree, got %d", len(report.Items))
		}
		if report.Items[0].Action != "would_prune" {
			t.Errorf("expected would_prune, got %s", report.Items[0].Action)
		}
		if gitRunner.PruneCalled {
			t.Errorf("expected git worktree prune not called in dry run")
		}
	})

	t.Run("force mode invokes git worktree prune", func(t *testing.T) {
		gitRunner := &MockCleanupGitRunner{PorcelainOutput: porcelain}
		cfg := CleanupConfig{
			RepoDir:   mainDir,
			Targets:   []string{TargetOrphanWT},
			DryRun:    false,
			GitRunner: gitRunner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 orphan worktree, got %d", len(report.Items))
		}
		if report.Items[0].Action != "pruned" {
			t.Errorf("expected pruned, got %s", report.Items[0].Action)
		}
		if !gitRunner.PruneCalled {
			t.Errorf("expected git worktree prune to be called in force mode")
		}
	})
}

func TestStaleWorktreeDirsCleanup(t *testing.T) {
	tempDir := t.TempDir()
	wtBase := filepath.Join(tempDir, "g8s-worktrees")
	_ = os.MkdirAll(wtBase, 0o755)

	activeDir := filepath.Join(wtBase, "wt-active")
	staleDir := filepath.Join(wtBase, "wt-stale")
	_ = os.MkdirAll(activeDir, 0o755)
	_ = os.MkdirAll(staleDir, 0o755)

	porcelain := fmt.Sprintf(`worktree %s
HEAD abc1234
branch refs/heads/main

worktree %s
HEAD 1112223
branch refs/heads/feat/active
`, tempDir, activeDir)

	t.Run("dry-run detects unregistered dir", func(t *testing.T) {
		gitRunner := &MockCleanupGitRunner{PorcelainOutput: porcelain}
		cfg := CleanupConfig{
			RepoDir:         tempDir,
			WorktreeBaseDir: wtBase,
			Targets:         []string{TargetOrphanDir},
			DryRun:          true,
			GitRunner:       gitRunner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 stale dir item, got %d", len(report.Items))
		}
		if report.Items[0].ID != staleDir {
			t.Errorf("expected ID %s, got %s", staleDir, report.Items[0].ID)
		}
		if report.Items[0].Action != "would_remove" {
			t.Errorf("expected action would_remove, got %s", report.Items[0].Action)
		}
		if _, err := os.Stat(staleDir); os.IsNotExist(err) {
			t.Fatalf("staleDir should not be removed in dry-run")
		}
	})

	t.Run("force mode deletes unregistered dir", func(t *testing.T) {
		gitRunner := &MockCleanupGitRunner{PorcelainOutput: porcelain}
		cfg := CleanupConfig{
			RepoDir:         tempDir,
			WorktreeBaseDir: wtBase,
			Targets:         []string{TargetOrphanDir},
			DryRun:          false,
			GitRunner:       gitRunner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 stale dir item, got %d", len(report.Items))
		}
		if report.Items[0].Action != "removed" {
			t.Errorf("expected action removed, got %s", report.Items[0].Action)
		}
		if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
			t.Fatalf("staleDir should be deleted in force mode")
		}
	})
}

func TestOrphanBranchCleanup(t *testing.T) {
	merged := []string{
		"agy/sup-1788003841828808000-5-sub-1",
		"agy-sup-task-1",
		"feat/feature-not-subagent",
		"agy/sup-on-remote",
	}
	remote := []string{
		"agy/sup-on-remote",
	}

	gitRunner := &MockCleanupGitRunner{
		MergedBranchesRes: merged,
		RemoteBranchesRes: remote,
	}

	t.Run("dry-run detects only merged subagent branches not on remote", func(t *testing.T) {
		cfg := CleanupConfig{
			Targets:   []string{TargetOrphanBranch},
			DryRun:    true,
			GitRunner: gitRunner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 2 {
			t.Fatalf("expected 2 orphan branches, got %d", len(report.Items))
		}
		expectedIDs := map[string]bool{
			"agy/sup-1788003841828808000-5-sub-1": true,
			"agy-sup-task-1":                      true,
		}
		for _, item := range report.Items {
			if !expectedIDs[item.ID] {
				t.Errorf("unexpected branch detected: %s", item.ID)
			}
			if item.Action != "would_delete" {
				t.Errorf("expected would_delete, got %s", item.Action)
			}
		}
	})

	t.Run("force mode deletes orphan branches", func(t *testing.T) {
		runner := &MockCleanupGitRunner{
			MergedBranchesRes: merged,
			RemoteBranchesRes: remote,
		}
		cfg := CleanupConfig{
			Targets:   []string{TargetOrphanBranch},
			DryRun:    false,
			GitRunner: runner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 2 {
			t.Fatalf("expected 2 orphan branches, got %d", len(report.Items))
		}
		if len(runner.DeletedBranches) != 2 {
			t.Fatalf("expected 2 deleted branches, got %d", len(runner.DeletedBranches))
		}
	})
}

func TestStaleReceiptCleanup(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", url.PathEscape(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
CREATE TABLE write_receipts (
	receipt_id TEXT PRIMARY KEY,
	issuer TEXT NOT NULL,
	allowed_paths_json TEXT NOT NULL,
	expires_at REAL NOT NULL,
	consumed INTEGER NOT NULL DEFAULT 0,
	consumer_task_id TEXT,
	created_at REAL NOT NULL
);`)
	if err != nil {
		t.Fatalf("failed to create write_receipts table: %v", err)
	}

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	// 1. Expired unconsumed receipt
	_, _ = db.Exec("INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, consumed, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		"rc-expired", "issuer", "[]", float64(now.Add(-1*time.Hour).Unix()), 0, float64(now.Add(-2*time.Hour).Unix()))
	// 2. Active unconsumed receipt
	_, _ = db.Exec("INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, consumed, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		"rc-active", "issuer", "[]", float64(now.Add(1*time.Hour).Unix()), 0, float64(now.Unix()))
	// 3. Expired consumed receipt
	_, _ = db.Exec("INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, consumed, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		"rc-consumed", "issuer", "[]", float64(now.Add(-1*time.Hour).Unix()), 1, float64(now.Add(-2*time.Hour).Unix()))

	t.Run("dry-run detects only unconsumed expired receipts", func(t *testing.T) {
		cfg := CleanupConfig{
			DBPath:  dbPath,
			Targets: []string{TargetStaleReceipt},
			DryRun:  true,
			Clock:   clock,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 stale receipt, got %d", len(report.Items))
		}
		if report.Items[0].ID != "rc-expired" {
			t.Errorf("expected rc-expired, got %s", report.Items[0].ID)
		}
		if report.Items[0].Action != "would_delete" {
			t.Errorf("expected action would_delete, got %s", report.Items[0].Action)
		}
	})

	t.Run("force mode deletes unconsumed expired receipts", func(t *testing.T) {
		cfg := CleanupConfig{
			DBPath:  dbPath,
			Targets: []string{TargetStaleReceipt},
			DryRun:  false,
			Clock:   clock,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 stale receipt, got %d", len(report.Items))
		}
		if report.Items[0].Action != "deleted" {
			t.Errorf("expected action deleted, got %s", report.Items[0].Action)
		}

		// Verify in DB
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM write_receipts WHERE receipt_id = 'rc-expired'").Scan(&count)
		if count != 0 {
			t.Fatalf("expected rc-expired to be deleted from DB")
		}
	})
}

func TestClosedPRBranchCleanup(t *testing.T) {
	closedPRs := []string{"feat/pr-1", "feat/pr-2"}
	runner := &MockCleanupGitRunner{ClosedPRRes: closedPRs}

	t.Run("dry-run detects closed PR branches", func(t *testing.T) {
		cfg := CleanupConfig{
			Targets:   []string{TargetClosedPRBranch},
			DryRun:    true,
			GitRunner: runner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(report.Items))
		}
	})

	t.Run("force mode deletes closed PR branches", func(t *testing.T) {
		r := &MockCleanupGitRunner{ClosedPRRes: closedPRs}
		cfg := CleanupConfig{
			Targets:   []string{TargetClosedPRBranch},
			DryRun:    false,
			GitRunner: r,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(report.Items))
		}
		if len(r.DeletedBranches) != 2 {
			t.Fatalf("expected 2 deleted branches, got %d", len(r.DeletedBranches))
		}
	})
}

func TestOldTagCleanup(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	tags := []TagInfo{
		{Name: "v0.1.0-old", Date: now.Add(-40 * 24 * time.Hour)},
		{Name: "v0.2.0-recent", Date: now.Add(-10 * 24 * time.Hour)},
		{Name: "v0.1.0-remote", Date: now.Add(-40 * 24 * time.Hour)},
	}
	remoteTags := []string{"v0.1.0-remote"}

	runner := &MockCleanupGitRunner{
		LocalTagsRes:  tags,
		RemoteTagsRes: remoteTags,
	}

	t.Run("dry-run detects old local tags not on remote", func(t *testing.T) {
		cfg := CleanupConfig{
			Targets:   []string{TargetOldTag},
			DryRun:    true,
			Clock:     clock,
			GitRunner: runner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 old tag, got %d", len(report.Items))
		}
		if report.Items[0].ID != "v0.1.0-old" {
			t.Errorf("expected v0.1.0-old, got %s", report.Items[0].ID)
		}
	})

	t.Run("force mode deletes old local tags", func(t *testing.T) {
		r := &MockCleanupGitRunner{
			LocalTagsRes:  tags,
			RemoteTagsRes: remoteTags,
		}
		cfg := CleanupConfig{
			Targets:   []string{TargetOldTag},
			DryRun:    false,
			Clock:     clock,
			GitRunner: r,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected 1 old tag, got %d", len(report.Items))
		}
		if len(r.DeletedTags) != 1 || r.DeletedTags[0] != "v0.1.0-old" {
			t.Errorf("expected v0.1.0-old deleted, got %v", r.DeletedTags)
		}
	})
}

func TestTargetFiltering(t *testing.T) {
	procMgr := NewMockProcessManager([]ProcessInfo{
		{PID: 1001, Binary: "agy", CommandLine: "agy", Reason: "no live heartbeat file"},
	})
	gitRunner := &MockCleanupGitRunner{
		ClosedPRRes: []string{"feat/pr-1"},
	}

	t.Run("runs only targeted sweep", func(t *testing.T) {
		cfg := CleanupConfig{
			Targets:        []string{TargetGhostProcess},
			DryRun:         true,
			ProcessManager: procMgr,
			GitRunner:      gitRunner,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Items) != 1 {
			t.Fatalf("expected exactly 1 item, got %d", len(report.Items))
		}
		if report.Items[0].Target != TargetGhostProcess {
			t.Errorf("expected target %s, got %s", TargetGhostProcess, report.Items[0].Target)
		}
	})
}

func TestRenderCleanupReport(t *testing.T) {
	report := &FullCleanupReport{
		DryRun: true,
		Items: []CleanupItem{
			{Target: TargetGhostProcess, ID: "1001", Action: "would_kill", Detail: "no live heartbeat file"},
		},
		Summary: map[string]int{
			TargetGhostProcess: 1,
		},
	}

	buf := new(bytes.Buffer)
	renderCleanupReport(report)
	_ = buf.String()
}

func TestLoadHeartbeats(t *testing.T) {
	tempDir := t.TempDir()
	agyDir := filepath.Join(tempDir, "agy")
	_ = os.MkdirAll(agyDir, 0o755)

	hb := HeartbeatData{
		SessionID:   "sess-1",
		PID:         1234,
		Binary:      "agy",
		CommandLine: "agy test",
		StartedAt:   "2026-08-29T12:00:00Z",
		LastUpdate:  "2026-08-29T12:30:00Z",
		Status:      "running",
	}
	data, _ := json.Marshal(hb)
	_ = os.WriteFile(filepath.Join(agyDir, "1234.json"), data, 0o644)

	hbs := loadHeartbeats(tempDir)
	if len(hbs) != 1 {
		t.Fatalf("expected 1 heartbeat, got %d", len(hbs))
	}
	if hbs[1234].SessionID != "sess-1" {
		t.Errorf("expected sess-1, got %s", hbs[1234].SessionID)
	}
}
