package autopilot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v60/github"
)

func TestParseRepo(t *testing.T) {
	owner, repo, err := parseRepo("owner/repo")
	if err != nil || owner != "owner" || repo != "repo" {
		t.Errorf("parseRepo('owner/repo') unexpected: %s, %s, %v", owner, repo, err)
	}

	if _, _, err := parseRepo("invalid"); err == nil {
		t.Error("expected error for 'invalid'")
	}

	if _, _, err := parseRepo("a/b/c"); err == nil {
		t.Error("expected error for 'a/b/c'")
	}
}

func TestConfig_LoadAndValidate(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistent := filepath.Join(tmpDir, "missing.json")

	// Missing file returns defaults
	cfg, err := LoadConfig(nonExistent)
	if err != nil {
		t.Fatalf("unexpected err for missing file: %v", err)
	}
	if cfg.MaxItemsPerTick != 50 {
		t.Errorf("expected default max items, got %d", cfg.MaxItemsPerTick)
	}

	// Valid JSON file
	validPath := filepath.Join(tmpDir, "valid.json")
	if err := os.WriteFile(validPath, []byte(`{"cron": "0 0 * * *", "max_items_per_tick": 25}`), 0o600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}
	cfgValid, err := LoadConfig(validPath)
	if err != nil || cfgValid.MaxItemsPerTick != 25 {
		t.Errorf("unexpected load valid config: %+v, %v", cfgValid, err)
	}

	// Invalid JSON file
	invalidPath := filepath.Join(tmpDir, "invalid.json")
	if err := os.WriteFile(invalidPath, []byte(`{invalid: json}`), 0o600); err != nil {
		t.Fatalf("failed to write invalid json: %v", err)
	}
	if _, err := LoadConfig(invalidPath); err == nil {
		t.Error("expected error for invalid json")
	}

	// Read error (reading directory)
	if _, err := LoadConfig(tmpDir); err == nil {
		t.Error("expected error when reading directory")
	}

	// Validate branches
	cfgEmptyCron := Config{Cron: ""}
	if err := cfgEmptyCron.Validate(); err != nil {
		t.Errorf("expected nil error for empty cron: %v", err)
	}

	cfgEmptyRepo := Config{
		Cron:        "0 * * * *",
		Repository:  "",
		ScanSources: ScanSources{GitHubIssues: true},
	}
	if err := cfgEmptyRepo.Validate(); err != nil {
		t.Errorf("expected nil error for empty repo: %v", err)
	}

	cfgDefaults := Config{
		Cron:            "0 * * * *",
		Repository:      "owner/repo",
		MaxItemsPerTick: -1,
		LookbackWindow:  -1,
		CodebasePath:    "",
	}
	if err := cfgDefaults.Validate(); err != nil {
		t.Errorf("Validate unexpected error: %v", err)
	}
	if cfgDefaults.MaxItemsPerTick != 50 || cfgDefaults.LookbackWindow != 24*time.Hour || cfgDefaults.CodebasePath != "." {
		t.Errorf("expected defaults set by Validate, got %+v", cfgDefaults)
	}
}

func TestPriorityQueue_EdgeCases(t *testing.T) {
	var raw priorityQueue
	if raw.Len() != 0 {
		t.Errorf("expected len 0, got %d", raw.Len())
	}
	if raw.Pop() != nil {
		t.Errorf("expected nil from empty raw.Pop()")
	}

	pq := NewPriorityQueue()
	if pq.Pop() != nil {
		t.Errorf("expected nil from empty pq.Pop()")
	}
	if pq.Dequeue() != nil {
		t.Errorf("expected nil from empty pq.Dequeue()")
	}

	// UpdateScore for missing item
	pq.UpdateScore("non-existent", 1.0, 1.0, 1.0, DefaultPriorityWeights())
}

func TestWorkItemCreation_LabelsAndEdges(t *testing.T) {
	scanner := &Scanner{
		config: DefaultConfig(),
	}

	// Nil issue
	if item := scanner.createWorkItemFromIssue(nil); item != nil {
		t.Errorf("expected nil from nil issue, got %+v", item)
	}

	// Issue with nil number
	if item := scanner.createWorkItemFromIssue(&github.Issue{}); item != nil {
		t.Errorf("expected nil from issue with nil number, got %+v", item)
	}

	// Test different label triggers
	num := 10
	labelTests := []struct {
		labelName    string
		wantSeverity float64
	}{
		{"bug", 0.9},
		{"critical", 0.9},
		{"security", 0.9},
		{"agy-fix", 0.7},
		{"good-first-issue", 0.3},
		{"enhancement", 0.4},
		{"feature", 0.4},
	}

	for _, lt := range labelTests {
		lbl := lt.labelName
		issue := &github.Issue{
			Number: &num,
			Labels: []*github.Label{
				{Name: &lbl},
				{Name: nil}, // Label with nil name
			},
		}
		item := scanner.createWorkItemFromIssue(issue)
		if item == nil || item.Severity != lt.wantSeverity {
			t.Errorf("label %s: expected severity %f, got %+v", lt.labelName, lt.wantSeverity, item)
		}
		if item.Description != "No description provided" {
			t.Errorf("expected default description when empty, got %q", item.Description)
		}
	}

	// Nil CI run
	if item := scanner.createWorkItemFromCI(nil); item != nil {
		t.Errorf("expected nil from nil CI run, got %+v", item)
	}

	// CI run with nil ID
	if item := scanner.createWorkItemFromCI(&github.WorkflowRun{}); item != nil {
		t.Errorf("expected nil from CI run with nil ID, got %+v", item)
	}

	// Valid CI run
	runID := int64(123)
	runName := "CI Build"
	branch := "feature/foo"
	conclusion := "failure"
	run := &github.WorkflowRun{
		ID:         &runID,
		Name:       &runName,
		HeadBranch: &branch,
		Conclusion: &conclusion,
	}
	ciItem := scanner.createWorkItemFromCI(run)
	if ciItem == nil || ciItem.ID != "ci-failure-123" {
		t.Errorf("unexpected ciItem: %+v", ciItem)
	}
}

func TestScanner_Extended(t *testing.T) {
	// NewScanner token variations
	cfg := DefaultConfig()
	cfg.GitHubToken = "token-123"
	s1, err := NewScanner(cfg, NewPriorityQueue())
	if err != nil || s1.githubClient == nil {
		t.Errorf("expected scanner with githubClient from config: %v", err)
	}

	t.Setenv("GITHUB_TOKEN", "env-token-456")
	cfgNoToken := DefaultConfig()
	cfgNoToken.GitHubToken = ""
	s2, err := NewScanner(cfgNoToken, NewPriorityQueue())
	if err != nil || s2.githubClient == nil {
		t.Errorf("expected scanner with githubClient from env: %v", err)
	}

	t.Setenv("GITHUB_TOKEN", "")
	cfgDisabled := DefaultConfig()
	cfgDisabled.GitHubToken = ""
	cfgDisabled.ScanSources = ScanSources{}
	s3, err := NewScanner(cfgDisabled, NewPriorityQueue())
	if err != nil || s3.githubClient != nil {
		t.Errorf("expected scanner with nil client: %v", err)
	}

	// Scan with queue truncation (MaxItemsPerTick)
	pq := NewPriorityQueue()
	for i := 0; i < 5; i++ {
		pq.Enqueue(&WorkItem{ID: fmt.Sprintf("item-%d", i), Score: float64(i + 1)})
	}
	cfgTruncate := DefaultConfig()
	cfgTruncate.MaxItemsPerTick = 2
	cfgTruncate.ScanSources = ScanSources{StaticAnalysis: true}
	sTrunc, _ := NewScanner(cfgTruncate, pq)
	if err := sTrunc.Scan(context.Background()); err != nil {
		t.Errorf("Scan unexpected error: %v", err)
	}
	if pq.Len() != 2 {
		t.Errorf("expected queue truncated to 2, got %d", pq.Len())
	}

	// Scan with errors (invalid repo format)
	cfgErr := DefaultConfig()
	cfgErr.Repository = "invalid-repo"
	cfgErr.GitHubToken = "fake-tok"
	cfgErr.ScanSources = ScanSources{GitHubIssues: true, FailingCI: true}
	sErr, _ := NewScanner(cfgErr, NewPriorityQueue())
	if err := sErr.Scan(context.Background()); err == nil {
		t.Error("expected scan error for invalid repository format")
	}

	// Stale todos in temp dir with skipped dirs (.git, vendor, node_modules, bin)
	tmpDir := t.TempDir()
	_ = os.Mkdir(filepath.Join(tmpDir, ".hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(tmpDir, ".hidden", "a.go"), []byte("// TODO: @agy-fix-me ignore"), 0o600)
	_ = os.Mkdir(filepath.Join(tmpDir, "vendor"), 0o755)
	_ = os.WriteFile(filepath.Join(tmpDir, "vendor", "b.go"), []byte("// TODO: @agy-fix-me ignore"), 0o600)
	_ = os.Mkdir(filepath.Join(tmpDir, "node_modules"), 0o755)
	_ = os.WriteFile(filepath.Join(tmpDir, "node_modules", "c.go"), []byte("// TODO: @agy-fix-me ignore"), 0o600)
	_ = os.Mkdir(filepath.Join(tmpDir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(tmpDir, "bin", "d.go"), []byte("// TODO: @agy-fix-me ignore"), 0o600)

	// Non-go file
	_ = os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("// TODO: @agy-fix-me ignore text"), 0o600)

	// Valid go files: one with description, one without description
	_ = os.WriteFile(filepath.Join(tmpDir, "desc.go"), []byte("package test\n// FIXME: @agy-fix-me fix this specific issue\n"), 0o600)
	_ = os.WriteFile(filepath.Join(tmpDir, "nodesc.go"), []byte("package test\n// TODO: @agy-fix-me\n"), 0o600)

	cfgTodos := DefaultConfig()
	cfgTodos.CodebasePath = tmpDir
	cfgTodos.ScanSources = ScanSources{StaleTodos: true}
	pqTodos := NewPriorityQueue()
	sTodos, _ := NewScanner(cfgTodos, pqTodos)
	if err := sTodos.Scan(context.Background()); err != nil {
		t.Fatalf("Scan stale todos failed: %v", err)
	}
	if pqTodos.Len() != 2 {
		t.Errorf("expected 2 TODO items, got %d", pqTodos.Len())
	}
}

func TestScanner_GitHubMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/issues") {
			w.Header().Set("Content-Type", "application/json")
			// One regular issue, one pull request
			_, _ = w.Write([]byte(`[
				{"number": 1, "title": "Issue 1", "body": "details", "labels": [{"name": "bug"}]},
				{"number": 2, "title": "PR 2", "pull_request": {"url": "https://api.github.com/..."}}
			]`))
			return
		}
		if strings.Contains(r.URL.Path, "/actions/runs") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"total_count": 1,
				"workflow_runs": [
					{"id": 42, "name": "Build", "head_branch": "main", "conclusion": "failure"}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := github.NewClient(srv.Client())
	baseURL, _ := url.Parse(srv.URL + "/")
	client.BaseURL = baseURL

	cfg := DefaultConfig()
	cfg.Repository = "test-owner/test-repo"
	cfg.ScanSources = ScanSources{
		GitHubIssues: true,
		FailingCI:    true,
	}
	pq := NewPriorityQueue()
	scanner, err := NewScanner(cfg, pq)
	if err != nil {
		t.Fatalf("NewScanner failed: %v", err)
	}
	scanner.githubClient = client

	if err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if pq.Len() != 2 {
		t.Errorf("expected 2 items (1 issue + 1 CI failure), got %d", pq.Len())
	}
}

func TestScheduler_Extended(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Cron = ""

	s, err := NewScheduler(cfg, nil)
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}

	// Stop when not running
	if err := s.Stop(); err != nil {
		t.Errorf("unexpected error stopping non-running scheduler: %v", err)
	}

	// TriggerScan when not running
	if err := s.TriggerScan(context.Background()); err == nil {
		t.Error("expected error triggering scan when scheduler not running")
	}

	// UpdateConfig with invalid cron
	validCfg := DefaultConfig()
	validCfg.Cron = "0 0 * * * *"
	sWithCron, err := NewScheduler(validCfg, nil)
	if err != nil {
		t.Fatalf("NewScheduler with 6-field cron failed: %v", err)
	}

	badCronCfg := validCfg
	badCronCfg.Cron = "bad cron expression"
	if err := sWithCron.UpdateConfig(badCronCfg); err == nil {
		t.Error("expected error updating config with bad cron")
	}

	// UpdateConfig with valid cron transition
	validCronCfg := validCfg
	validCronCfg.Cron = "0 30 * * * *"
	if err := sWithCron.UpdateConfig(validCronCfg); err != nil {
		t.Errorf("UpdateConfig valid cron failed: %v", err)
	}

	// Process queue with handler error and retry score reduction
	failCount := 0
	pq := NewPriorityQueue()
	pq.Enqueue(&WorkItem{ID: "fail-item", Score: 2.0})

	sHandler, _ := NewScheduler(cfg, func(ctx context.Context, item *WorkItem) error {
		failCount++
		if failCount == 1 {
			return errors.New("temporary handler failure")
		}
		return nil
	})
	sHandler.queue = pq
	sHandler.running = true

	sHandler.processQueue(context.Background())

	if failCount != 2 {
		t.Errorf("expected handler to be called twice (initial + retry), called %d times", failCount)
	}
	if pq.Len() != 0 {
		t.Errorf("expected queue to be empty after successful retry, got len %d", pq.Len())
	}
}
