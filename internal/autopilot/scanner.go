// Package autopilot implements the cron-based supervisor trigger that scans
// GitHub issues, failing CI runs, static analysis findings, and stale
// @agy-fix-me TODOs. It uses a priority queue weighted by severity,
// confidence, and cost-inverse.
package autopilot

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v60/github"
	"golang.org/x/oauth2"
)

// StaticAnalysisRunner executes static analysis in codebasePath and returns combined output.
type StaticAnalysisRunner func(ctx context.Context, codebasePath string) ([]byte, error)

// Scanner discovers work items from various sources.
type Scanner struct {
	config               Config
	githubClient         *github.Client
	queue                *PriorityQueue
	staticAnalysisRunner StaticAnalysisRunner
}

// NewScanner creates a new scanner with the given configuration and queue.
func NewScanner(config Config, queue *PriorityQueue) (*Scanner, error) {
	var githubClient *github.Client
	if config.GitHubToken != "" || config.ScanSources.GitHubIssues || config.ScanSources.FailingCI {
		token := config.GitHubToken
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
		if token != "" {
			ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
			tc := oauth2.NewClient(context.Background(), ts)
			githubClient = github.NewClient(tc)
		}
	}

	return &Scanner{
		config:       config,
		githubClient: githubClient,
		queue:        queue,
	}, nil
}

// Scan runs all enabled scans and enqueues discovered work items.
func (s *Scanner) Scan(ctx context.Context) error {
	var errors []error

	if s.config.ScanSources.GitHubIssues && s.githubClient != nil {
		if err := s.scanGitHubIssues(ctx); err != nil {
			errors = append(errors, fmt.Errorf("github issues scan: %w", err))
		}
	}

	if s.config.ScanSources.FailingCI && s.githubClient != nil {
		if err := s.scanFailingCI(ctx); err != nil {
			errors = append(errors, fmt.Errorf("failing CI scan: %w", err))
		}
	}

	if s.config.ScanSources.StaticAnalysis {
		if err := s.scanStaticAnalysis(ctx); err != nil {
			errors = append(errors, fmt.Errorf("static analysis scan: %w", err))
		}
	}

	if s.config.ScanSources.StaleTodos {
		if err := s.scanStaleTodos(ctx); err != nil {
			errors = append(errors, fmt.Errorf("stale todos scan: %w", err))
		}
	}

	// Limit queue size
	s.queue.mu.Lock()
	if s.queue.Len() > s.config.MaxItemsPerTick {
		// Keep only top MaxItemsPerTick items
		items := s.queue.items
		s.queue.items = items[:s.config.MaxItemsPerTick]
		s.queue.index = make(map[string]int)
		for i, item := range s.queue.items {
			s.queue.index[item.ID] = i
		}
		heap.Init(s.queue)
	}
	s.queue.mu.Unlock()

	if len(errors) > 0 {
		return fmt.Errorf("scan errors: %v", errors)
	}
	return nil
}

// scanGitHubIssues scans for issues with specific labels.
func (s *Scanner) scanGitHubIssues(ctx context.Context) error {
	owner, repo, err := parseRepo(s.config.Repository)
	if err != nil {
		return err
	}

	opts := &github.IssueListByRepoOptions{
		State:       "open",
		Labels:      []string{"agy-fix", "good-first-issue"},
		Since:       time.Now().Add(-s.config.LookbackWindow),
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		issues, resp, err := s.githubClient.Issues.ListByRepo(ctx, owner, repo, opts)
		if err != nil {
			return err
		}

		for _, issue := range issues {
			if issue.IsPullRequest() {
				continue // Skip PRs, only process issues
			}

			item := s.createWorkItemFromIssue(issue)
			if item != nil && item.Score >= s.config.MinScoreThreshold {
				heap.Push(s.queue, item)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return nil
}

// scanFailingCI scans for failing workflow runs.
func (s *Scanner) scanFailingCI(ctx context.Context) error {
	owner, repo, err := parseRepo(s.config.Repository)
	if err != nil {
		return err
	}

	opts := &github.ListWorkflowRunsOptions{
		Status:      "failure",
		Created:     fmt.Sprintf(">%s", time.Now().Add(-s.config.LookbackWindow).Format(time.RFC3339)),
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		runs, resp, err := s.githubClient.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, opts)
		if err != nil {
			return err
		}

		for _, run := range runs.WorkflowRuns {
			item := s.createWorkItemFromCI(run)
			if item != nil && item.Score >= s.config.MinScoreThreshold {
				heap.Push(s.queue, item)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return nil
}

var vetLinePattern = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?:\s*(.+)$`)

func defaultStaticAnalysisRunner(ctx context.Context, codebasePath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "vet", "./...")
	cmd.Dir = codebasePath
	return cmd.CombinedOutput()
}

// scanStaticAnalysis runs go vet (or an injected runner) and enqueues findings.
func (s *Scanner) scanStaticAnalysis(ctx context.Context) error {
	dir := strings.TrimSpace(s.config.CodebasePath)
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		if os.IsNotExist(err) && s.staticAnalysisRunner == nil {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check go.mod in %q: %w", dir, err)
		}
	}

	runner := s.staticAnalysisRunner
	if runner == nil {
		runner = defaultStaticAnalysisRunner
	}

	out, err := runner(ctx, dir)
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("static analysis: %w", err)
		}
		if len(out) == 0 {
			out = exitErr.Stderr
			if len(out) == 0 {
				out = []byte(exitErr.Error())
			}
		}
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := vetLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		filePath := m[1]
		lineNum, _ := strconv.Atoi(m[2])
		col := 0
		if m[3] != "" {
			col, _ = strconv.Atoi(m[3])
		}
		msg := strings.TrimSpace(m[4])

		item := &WorkItem{
			ID:           fmt.Sprintf("vet-%s-%d-%d", filepath.Base(filePath), lineNum, col),
			Source:       SourceStaticAnalysis,
			Title:        fmt.Sprintf("Static analysis: %s:%d", filePath, lineNum),
			Description:  msg,
			FilePath:     filePath,
			LineNumber:   lineNum,
			Severity:     0.6,
			Confidence:   0.85,
			Cost:         2.5,
			DiscoveredAt: time.Now(),
			Metadata: map[string]string{
				"column":  strconv.Itoa(col),
				"message": msg,
				"tool":    "go vet",
			},
		}
		item.Score = CalculateScore(item, s.config.PriorityWeights)
		if item.Score >= s.config.MinScoreThreshold {
			heap.Push(s.queue, item)
		}
	}
	return nil
}

var todoRegex = regexp.MustCompile(`(?i)(TODO|FIXME|XXX):\s*@agy-fix-me\s*(.*)`)

// scanStaleTodos scans for @agy-fix-me TODO comments in the codebase.
func (s *Scanner) scanStaleTodos(ctx context.Context) error {
	err := filepath.Walk(s.config.CodebasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}
		if info.IsDir() {
			// Skip hidden dirs, vendor, .git, node_modules, etc.
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only scan Go files for now
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			matches := todoRegex.FindStringSubmatch(line)
			if matches == nil {
				continue
			}

			// Extract the TODO description
			desc := strings.TrimSpace(matches[2])
			if desc == "" {
				desc = "Stale @agy-fix-me TODO"
			}

			item := &WorkItem{
				ID:           fmt.Sprintf("todo-%s-%d", filepath.Base(path), i+1),
				Source:       SourceStaleTodo,
				Title:        fmt.Sprintf("Stale TODO in %s:%d", path, i+1),
				Description:  desc,
				FilePath:     path,
				LineNumber:   i + 1,
				Severity:     0.3, // Medium-low severity
				Confidence:   0.7, // High confidence it's a real TODO
				Cost:         2.0, // Low effort to fix
				DiscoveredAt: time.Now(),
				Metadata: map[string]string{
					"file":     path,
					"line":     fmt.Sprintf("%d", i+1),
					"todo_tag": matches[1],
				},
			}
			item.Score = CalculateScore(item, s.config.PriorityWeights)

			if item.Score >= s.config.MinScoreThreshold {
				heap.Push(s.queue, item)
			}
		}

		return nil
	})

	return err
}

// createWorkItemFromIssue creates a WorkItem from a GitHub issue.
func (s *Scanner) createWorkItemFromIssue(issue *github.Issue) *WorkItem {
	if issue == nil || issue.Number == nil {
		return nil
	}

	severity := 0.5
	confidence := 0.8
	cost := 3.0

	// Adjust based on labels
	for _, label := range issue.Labels {
		if label.Name != nil {
			switch *label.Name {
			case "bug", "critical", "security":
				severity = 0.9
				confidence = 0.9
			case "agy-fix":
				severity = 0.7
				confidence = 0.85
				cost = 2.0
			case "good-first-issue":
				severity = 0.3
				confidence = 0.7
				cost = 1.5
			case "enhancement", "feature":
				severity = 0.4
				confidence = 0.6
				cost = 4.0
			}
		}
	}

	item := &WorkItem{
		ID:           fmt.Sprintf("gh-issue-%d", *issue.Number),
		Source:       SourceGitHubIssue,
		Title:        issue.GetTitle(),
		Description:  issue.GetBody(),
		URL:          issue.GetHTMLURL(),
		Severity:     severity,
		Confidence:   confidence,
		Cost:         cost,
		DiscoveredAt: issue.GetCreatedAt().Time,
		Metadata: map[string]string{
			"number": fmt.Sprintf("%d", *issue.Number),
			"state":  issue.GetState(),
		},
	}

	if item.Description == "" {
		item.Description = "No description provided"
	}

	item.Score = CalculateScore(item, s.config.PriorityWeights)
	return item
}

// createWorkItemFromCI creates a WorkItem from a failing CI run.
func (s *Scanner) createWorkItemFromCI(run *github.WorkflowRun) *WorkItem {
	if run == nil || run.ID == nil {
		return nil
	}

	item := &WorkItem{
		ID:           fmt.Sprintf("ci-failure-%d", *run.ID),
		Source:       SourceFailingCI,
		Title:        fmt.Sprintf("Failing CI: %s", run.GetName()),
		Description:  fmt.Sprintf("Workflow %s failed on %s", run.GetName(), run.GetHeadBranch()),
		URL:          run.GetHTMLURL(),
		Severity:     0.8, // High severity for failing CI
		Confidence:   0.9, // High confidence it needs fixing
		Cost:         3.0, // Medium effort to investigate
		DiscoveredAt: run.GetCreatedAt().Time,
		Metadata: map[string]string{
			"workflow":   run.GetName(),
			"branch":     run.GetHeadBranch(),
			"run_id":     fmt.Sprintf("%d", *run.ID),
			"conclusion": run.GetConclusion(),
		},
	}

	item.Score = CalculateScore(item, s.config.PriorityWeights)
	return item
}

// parseRepo parses "owner/repo" into owner and repo strings.
func parseRepo(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repository format: %s (expected owner/repo)", repo)
	}
	return parts[0], parts[1], nil
}
