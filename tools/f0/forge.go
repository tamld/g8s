package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ForgeCheck represents a CI check run or status reported by the forge.
type ForgeCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Bucket string `json:"bucket"` // "pass", "fail", "pending", "skipping", "cancel"
	Link   string `json:"link"`
}

// ForgeAdapter defines the forge operations required by F0 verification gates.
type ForgeAdapter interface {
	ResolvePR(repoDir string, pr int) (headSHA, baseRef, body string, err error)
	ListChecks(repoDir string, pr int, headSHA string) ([]ForgeCheck, error)
	VerifyIssue(repoDir string, issueURL string) (sameRepo bool, open bool, titleAndBody string, err error)
	VerifyWorkflowRun(repoDir string, runID string) (headSHA string, conclusion string, err error)
}

// GitHubForge implements ForgeAdapter using the GitHub CLI (gh).
type GitHubForge struct{}

var _ ForgeAdapter = (*GitHubForge)(nil)

type ghPRView struct {
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	Body        string `json:"body"`
}

func (g *GitHubForge) ResolvePR(repoDir string, pr int) (headSHA, baseRef, body string, err error) {
	if repoDir == "" {
		repoDir = "."
	}

	// 1. Query PR via gh pr view
	cmd := exec.Command("gh", "pr", "view", strconv.Itoa(pr), "--json", "headRefOid,baseRefName,body")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", "", "", fmt.Errorf("gh pr view %d: %w", pr, err)
	}

	var view ghPRView
	if err := json.Unmarshal(out, &view); err != nil {
		return "", "", "", fmt.Errorf("parsing gh pr view output: %w", err)
	}

	// 2. Best-effort fetch
	fetchCmd := exec.Command("git", "fetch", "origin", fmt.Sprintf("pull/%d/head", pr))
	fetchCmd.Dir = repoDir
	_ = fetchCmd.Run()

	return view.HeadRefOid, view.BaseRefName, view.Body, nil
}

func (g *GitHubForge) ListChecks(repoDir string, pr int, headSHA string) ([]ForgeCheck, error) {
	if repoDir == "" {
		repoDir = "."
	}

	cmd := exec.Command("gh", "pr", "checks", strconv.Itoa(pr), "--json", "name,state,bucket,link")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr checks %d: %w", pr, err)
	}

	var checks []ForgeCheck
	if err := json.Unmarshal(out, &checks); err != nil {
		return nil, fmt.Errorf("parsing gh pr checks output: %w", err)
	}

	return checks, nil
}

func extractRepoOwnerAndName(remoteURL string) (owner, repo string) {
	s := strings.TrimSpace(remoteURL)
	s = strings.TrimSuffix(s, ".git")

	// Match git@github.com:owner/repo
	if idx := strings.Index(s, "git@github.com:"); idx != -1 {
		parts := strings.Split(s[idx+len("git@github.com:"):], "/")
		if len(parts) >= 2 {
			return parts[0], parts[1]
		}
	}

	// Match https://github.com/owner/repo or ssh://git@github.com/owner/repo
	if u, err := url.Parse(s); err == nil {
		trimmed := strings.Trim(u.Path, "/")
		parts := strings.Split(trimmed, "/")
		if len(parts) >= 2 {
			return parts[0], parts[1]
		}
	}

	return "", ""
}

var issueURLRegex = regexp.MustCompile(`(?i)github\.com/([^/]+)/([^/]+)/issues/(\d+)`)

func (g *GitHubForge) VerifyIssue(repoDir string, issueURL string) (sameRepo bool, open bool, titleAndBody string, err error) {
	if repoDir == "" {
		repoDir = "."
	}

	m := issueURLRegex.FindStringSubmatch(issueURL)
	if len(m) < 4 {
		return false, false, "", fmt.Errorf("invalid github issue url: %s", issueURL)
	}
	issueOwner := m[1]
	issueRepo := m[2]

	// Get local repo owner/name
	remoteOut, _ := exec.Command("git", "-C", repoDir, "remote", "get-url", "origin").Output()
	localOwner, localRepo := extractRepoOwnerAndName(string(remoteOut))

	if localOwner == "" || localRepo == "" {
		// Fallback to gh repo view
		ghOut, err := exec.Command("gh", "repo", "view", "--json", "owner,name").Output()
		if err == nil {
			var rv struct {
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name string `json:"name"`
			}
			if err := json.Unmarshal(ghOut, &rv); err == nil {
				localOwner = rv.Owner.Login
				localRepo = rv.Name
			}
		}
	}

	if !strings.EqualFold(localOwner, issueOwner) || !strings.EqualFold(localRepo, issueRepo) {
		return false, false, "", nil
	}

	// Issue belongs to same repo, now inspect state and body
	cmd := exec.Command("gh", "issue", "view", issueURL, "--json", "state,title,body")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return true, false, "", fmt.Errorf("gh issue view %s: %w", issueURL, err)
	}

	var issueView struct {
		State string `json:"state"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(out, &issueView); err != nil {
		return true, false, "", fmt.Errorf("parsing gh issue view output: %w", err)
	}

	isOpen := strings.EqualFold(issueView.State, "OPEN")
	text := issueView.Title + "\n" + issueView.Body
	return true, isOpen, text, nil
}

func (g *GitHubForge) VerifyWorkflowRun(repoDir string, runID string) (headSHA string, conclusion string, err error) {
	if repoDir == "" {
		repoDir = "."
	}

	cmd := exec.Command("gh", "run", "view", runID, "--json", "headSha,conclusion")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("gh run view %s: %w", runID, err)
	}

	var runView struct {
		HeadSHA    string `json:"headSha"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal(out, &runView); err != nil {
		return "", "", fmt.Errorf("parsing gh run view output: %w", err)
	}

	return runView.HeadSHA, runView.Conclusion, nil
}

// FakePR holds stubbed data for a pull request in FakeForge.
type FakePR struct {
	HeadSHA string `json:"head_sha"`
	BaseRef string `json:"base_ref"`
	Body    string `json:"body"`
}

// FakeIssue holds stubbed data for an issue in FakeForge.
type FakeIssue struct {
	SameRepo bool   `json:"same_repo"`
	Open     bool   `json:"open"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

// FakeRun holds stubbed data for a workflow run in FakeForge.
type FakeRun struct {
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
}

// FakeForge provides an in-memory or fixture-driven implementation of ForgeAdapter.
type FakeForge struct {
	PRs          map[int]FakePR       `json:"prs"`
	Checks       map[int][]ForgeCheck `json:"checks"`
	Issues       map[string]FakeIssue `json:"issues"`
	WorkflowRuns map[string]FakeRun   `json:"workflow_runs"`
}

var _ ForgeAdapter = (*FakeForge)(nil)

func NewFakeForge() *FakeForge {
	return &FakeForge{
		PRs:          make(map[int]FakePR),
		Checks:       make(map[int][]ForgeCheck),
		Issues:       make(map[string]FakeIssue),
		WorkflowRuns: make(map[string]FakeRun),
	}
}

func (f *FakeForge) ResolvePR(repoDir string, pr int) (headSHA, baseRef, body string, err error) {
	if p, ok := f.PRs[pr]; ok {
		return p.HeadSHA, p.BaseRef, p.Body, nil
	}
	return "", "", "", fmt.Errorf("fake forge: pr #%d not found", pr)
}

func (f *FakeForge) ListChecks(repoDir string, pr int, headSHA string) ([]ForgeCheck, error) {
	if checks, ok := f.Checks[pr]; ok {
		return checks, nil
	}
	return []ForgeCheck{}, nil
}

func (f *FakeForge) VerifyIssue(repoDir string, issueURL string) (sameRepo bool, open bool, titleAndBody string, err error) {
	if is, ok := f.Issues[issueURL]; ok {
		text := is.Title
		if is.Body != "" {
			text += "\n" + is.Body
		}
		return is.SameRepo, is.Open, text, nil
	}
	// Try without trailing slash
	trimmed := strings.TrimRight(issueURL, "/")
	if is, ok := f.Issues[trimmed]; ok {
		text := is.Title
		if is.Body != "" {
			text += "\n" + is.Body
		}
		return is.SameRepo, is.Open, text, nil
	}
	return false, false, "", fmt.Errorf("fake forge: issue %s not found", issueURL)
}

func (f *FakeForge) VerifyWorkflowRun(repoDir string, runID string) (headSHA string, conclusion string, err error) {
	if r, ok := f.WorkflowRuns[runID]; ok {
		return r.HeadSHA, r.Conclusion, nil
	}
	return "", "", fmt.Errorf("fake forge: workflow run %s not found", runID)
}

var globalForge ForgeAdapter

// SetGlobalForge allows tests to inject a ForgeAdapter directly.
func SetGlobalForge(f ForgeAdapter) {
	globalForge = f
}

// GetForgeAdapter resolves the appropriate ForgeAdapter.
func GetForgeAdapter(repoDir string) (ForgeAdapter, error) {
	if globalForge != nil {
		return globalForge, nil
	}
	fakePath := os.Getenv("F0_FAKE_FORGE_JSON")
	if fakePath != "" {
		data, err := os.ReadFile(fakePath)
		if err != nil {
			return nil, fmt.Errorf("reading fake forge JSON %s: %w", fakePath, err)
		}
		var fake FakeForge
		if err := json.Unmarshal(data, &fake); err != nil {
			return nil, fmt.Errorf("unmarshaling fake forge JSON %s: %w", fakePath, err)
		}
		return &fake, nil
	}
	return &GitHubForge{}, nil
}
