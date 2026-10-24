package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tamld/g8s/internal/cli"
)

// FleetRepoStatus represents the git & release hygiene status of a single repository.
type FleetRepoStatus struct {
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	Branch          string   `json:"branch"`
	DirtyCount      int      `json:"dirty_count"`
	UnpushedCount   int      `json:"unpushed_count"`
	WorktreeCount   int      `json:"worktree_count"`
	ExtraBranches   []string `json:"extra_branches"`
	LatestTag       string   `json:"latest_tag"`
	CommitsSinceTag int      `json:"commits_since_tag"`
	Clean           bool     `json:"clean"`
}

// FleetStatusReport aggregates hygiene across all discovered git repositories in a workspace.
type FleetStatusReport struct {
	WorkspaceDir string            `json:"workspace_dir"`
	TotalRepos   int               `json:"total_repos"`
	CleanRepos   int               `json:"clean_repos"`
	DirtyRepos   int               `json:"dirty_repos"`
	AllClean     bool              `json:"all_clean"`
	Repositories []FleetRepoStatus `json:"repositories"`
}

func runFleet(args []string) {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "status":
		runFleetStatus(args)
	case "help", "-h", "--help":
		fmt.Println("Usage: g8s fleet status [--dir <workspace>] [--require-clean] [--json]")
	default:
		exitUsage("fleet", sub, "", fmt.Sprintf("unknown fleet subcommand %q (expected 'status')", sub), "Run 'g8s fleet status --help'", false)
	}
}

func runFleetStatus(args []string) {
	fs := flag.NewFlagSet("fleet status", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor

	dirFlag := fs.String("dir", "", "workspace directory containing git repositories (defaults to parent workspace or cwd)")
	requireCleanFlag := fs.Bool("require-clean", false, "exit non-zero if any repository has dirty files, unpushed commits, extra worktrees, or extra branches")

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("fleet", "status", *traceID, err.Error(), "Run 'g8s fleet status --help'", *jsonl)
	}

	workspaceDir := strings.TrimSpace(*dirFlag)
	if workspaceDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			exitRuntime("fleet", "status", *traceID, cli.CodeIO, err, "Failed to resolve current working directory", *jsonl)
		}
		parentDir := filepath.Dir(cwd)
		if isGitRepo(cwd) && countChildGitRepos(parentDir) > 1 {
			workspaceDir = parentDir
		} else {
			workspaceDir = cwd
		}
	}

	absWorkspace, err := filepath.Abs(workspaceDir)
	if err != nil {
		exitRuntime("fleet", "status", *traceID, cli.CodeIO, err, "Invalid workspace path", *jsonl)
	}

	repoPaths := discoverGitRepos(absWorkspace)
	repos := make([]FleetRepoStatus, 0, len(repoPaths))
	cleanCount := 0
	dirtyCount := 0

	for _, rp := range repoPaths {
		st := inspectGitRepo(rp)
		if st.Clean {
			cleanCount++
		} else {
			dirtyCount++
		}
		repos = append(repos, st)
	}

	report := FleetStatusReport{
		WorkspaceDir: absWorkspace,
		TotalRepos:   len(repos),
		CleanRepos:   cleanCount,
		DirtyRepos:   dirtyCount,
		AllClean:     dirtyCount == 0,
		Repositories: repos,
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("fleet_status", "fleet", "status", report)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("fleet", "status", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		if *requireCleanFlag && !report.AllClean {
			os.Exit(1)
		}
		return
	}

	fmt.Printf("Fleet Workspace: %s (%d repos: %d clean, %d dirty)\n", report.WorkspaceDir, report.TotalRepos, report.CleanRepos, report.DirtyRepos)
	for _, r := range report.Repositories {
		stateLabel := "CLEAN"
		if !r.Clean {
			stateLabel = "DIRTY"
		}
		tagInfo := r.LatestTag
		if tagInfo == "" {
			tagInfo = "no-tag"
		} else if r.CommitsSinceTag > 0 {
			tagInfo = fmt.Sprintf("%s (+%d)", r.LatestTag, r.CommitsSinceTag)
		}
		fmt.Printf("  [%s] %-20s branch=%-10s dirty=%d unpushed=%d worktrees=%d extra_branches=%d tag=%s\n",
			stateLabel, r.Name, r.Branch, r.DirtyCount, r.UnpushedCount, r.WorktreeCount, len(r.ExtraBranches), tagInfo)
	}

	if *requireCleanFlag && !report.AllClean {
		exitRuntime(
			"fleet",
			"status",
			*traceID,
			cli.CodeRuntime,
			fmt.Errorf("%d of %d repositories are not clean", report.DirtyRepos, report.TotalRepos),
			"Commit/push pending work and prune orphan worktrees/branches",
			*jsonl,
		)
	}
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func countChildGitRepos(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if isGitRepo(filepath.Join(dir, e.Name())) {
			count++
		}
	}
	return count
}

func discoverGitRepos(workspaceDir string) []string {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		if isGitRepo(workspaceDir) {
			return []string{workspaceDir}
		}
		return nil
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		candidate := filepath.Join(workspaceDir, e.Name())
		if isGitRepo(candidate) {
			paths = append(paths, candidate)
		}
	}
	if len(paths) == 0 && isGitRepo(workspaceDir) {
		paths = append(paths, workspaceDir)
	}
	sort.Strings(paths)
	return paths
}

func runGitTrimmed(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func inspectGitRepo(repoPath string) FleetRepoStatus {
	st := FleetRepoStatus{
		Name:          filepath.Base(repoPath),
		Path:          repoPath,
		ExtraBranches: make([]string, 0),
	}

	if branch, err := runGitTrimmed(repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		st.Branch = branch
	}

	if statusOut, err := runGitTrimmed(repoPath, "status", "-s"); err == nil && statusOut != "" {
		lines := strings.Split(statusOut, "\n")
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				st.DirtyCount++
			}
		}
	}

	if unpushedStr, err := runGitTrimmed(repoPath, "rev-list", "--count", "@{u}..HEAD"); err == nil {
		if n, pErr := strconv.Atoi(unpushedStr); pErr == nil {
			st.UnpushedCount = n
		}
	}

	if wtOut, err := runGitTrimmed(repoPath, "worktree", "list", "--porcelain"); err == nil && wtOut != "" {
		for _, l := range strings.Split(wtOut, "\n") {
			if strings.HasPrefix(l, "worktree ") {
				st.WorktreeCount++
			}
		}
	}
	if st.WorktreeCount == 0 {
		st.WorktreeCount = 1
	}

	if brOut, err := runGitTrimmed(repoPath, "branch", "--format=%(refname:short)"); err == nil && brOut != "" {
		for _, b := range strings.Split(brOut, "\n") {
			b = strings.TrimSpace(b)
			if b != "" && b != st.Branch {
				st.ExtraBranches = append(st.ExtraBranches, b)
			}
		}
	}

	if tag, err := runGitTrimmed(repoPath, "describe", "--tags", "--abbrev=0"); err == nil && tag != "" {
		st.LatestTag = tag
		if sinceStr, sErr := runGitTrimmed(repoPath, "rev-list", "--count", tag+"..HEAD"); sErr == nil {
			if n, pErr := strconv.Atoi(sinceStr); pErr == nil {
				st.CommitsSinceTag = n
			}
		}
	}

	st.Clean = st.DirtyCount == 0 && st.UnpushedCount == 0 && st.WorktreeCount <= 1 && len(st.ExtraBranches) == 0
	return st
}
