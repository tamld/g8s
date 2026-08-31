package cleanup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SubagentBranchPattern matches branches created for agy subagents,
// such as agy/sup-1788003093729656000-5-sub-2 or agy/sup-task-sub-1.
var SubagentBranchPattern = regexp.MustCompile(`^agy/sup-.*-sub-\d+$`)

// BlindBranchPattern matches branches created for blind worktrees,
// such as blind/1-50ecde or blind/2-7e6a7d.
var BlindBranchPattern = regexp.MustCompile(`^blind/\d+-[a-f0-9]+$`)

// WorktreeEntry represents a single git worktree parsed from
// git worktree list --porcelain output.
type WorktreeEntry struct {
	Path     string
	Head     string
	Branch   string
	Ref      string
	IsBare   bool
	Detached bool
	Locked   bool
	Prunable bool
	IsMain   bool
}

// WorktreeCandidate stores details about a worktree evaluated for cleanup.
type WorktreeCandidate struct {
	Path   string        `json:"path"`
	Branch string        `json:"branch"`
	Age    time.Duration `json:"age"`
}

// SkippedWorktree stores details about a worktree that was skipped.
type SkippedWorktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Reason string `json:"reason"`
}

// CleanupReport summarizes the results of the worktree cleanup operation.
type CleanupReport struct {
	Removed []WorktreeCandidate `json:"removed,omitempty"`
	Skipped []SkippedWorktree   `json:"skipped,omitempty"`
	DryRun  bool                `json:"dry_run"`
	Pruned  string              `json:"pruned,omitempty"`
}

// GitRunner encapsulates Git CLI operations for worktree management and testing.
type GitRunner interface {
	WorktreeListPorcelain(ctx context.Context, repoDir string) (string, error)
	WorktreeRemove(ctx context.Context, repoDir, wtPath string, force bool) error
	WorktreePrune(ctx context.Context, repoDir string) (string, error)
	StatusPorcelain(ctx context.Context, wtPath string) (string, error)
}

// DefaultGitRunner implements GitRunner using the real git executable.
type DefaultGitRunner struct{}

// WorktreeListPorcelain lists all worktrees in machine-readable porcelain format.
func (d *DefaultGitRunner) WorktreeListPorcelain(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "list", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree list --porcelain: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// WorktreeRemove removes a linked worktree from git.
func (d *DefaultGitRunner) WorktreeRemove(ctx context.Context, repoDir, wtPath string, force bool) error {
	args := []string{"-C", repoDir, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreePrune prunes worktree administrative data in .git/worktrees.
func (d *DefaultGitRunner) WorktreePrune(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "prune", "--verbose")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree prune: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// StatusPorcelain checks for uncommitted changes in a worktree.
func (d *DefaultGitRunner) StatusPorcelain(ctx context.Context, wtPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", wtPath, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git status --porcelain: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// CleanupOptions defines parameters for the CleanupWorktrees function.
type CleanupOptions struct {
	RepoDir   string
	OlderThan time.Duration
	DryRun    bool
	Clock     func() time.Time
	Runner    GitRunner
	Writer    io.Writer
}

// CleanupWorktrees identifies and removes stale agy subagent worktrees.
func CleanupWorktrees(ctx context.Context, opts CleanupOptions) (*CleanupReport, error) {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Runner == nil {
		opts.Runner = &DefaultGitRunner{}
	}
	if opts.Writer == nil {
		opts.Writer = io.Discard
	}
	if opts.RepoDir == "" {
		opts.RepoDir = "."
	}

	absRepo, err := filepath.Abs(opts.RepoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve repo dir: %w", err)
	}

	porcelain, err := opts.Runner.WorktreeListPorcelain(ctx, absRepo)
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}

	entries := ParseWorktreeListPorcelain(porcelain)
	report := &CleanupReport{
		DryRun:  opts.DryRun,
		Removed: make([]WorktreeCandidate, 0),
		Skipped: make([]SkippedWorktree, 0),
	}

	var candidates []WorktreeCandidate
	for _, entry := range entries {
		if entry.IsMain || IsMainWorktree(absRepo, entry.Path) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: "main worktree",
			})
			continue
		}

		if !SubagentBranchPattern.MatchString(entry.Branch) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: "branch does not match subagent pattern",
			})
			continue
		}

		age, isOlder, err := CheckWorktreeAge(entry.Path, opts.OlderThan, opts.Clock)
		if err != nil && !os.IsNotExist(err) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: fmt.Sprintf("stat error: %v", err),
			})
			continue
		}
		if !isOlder {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: fmt.Sprintf("worktree age (%s) below threshold (%s)", age.Round(time.Second), opts.OlderThan),
			})
			continue
		}

		dirty, err := HasUncommittedChanges(ctx, opts.Runner, entry.Path)
		if err != nil && !os.IsNotExist(err) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: fmt.Sprintf("status error: %v", err),
			})
			continue
		}
		if dirty {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: "uncommitted changes present",
			})
			fmt.Fprintf(opts.Writer, "[skip] Worktree %s has uncommitted changes\n", entry.Path)
			continue
		}

		candidates = append(candidates, WorktreeCandidate{
			Path:   entry.Path,
			Branch: entry.Branch,
			Age:    age,
		})
	}

	if opts.DryRun {
		for _, cand := range candidates {
			fmt.Fprintf(opts.Writer, "[dry-run] Would remove worktree %s (branch: %s, age: %s)\n", cand.Path, cand.Branch, cand.Age.Round(time.Second))
			report.Removed = append(report.Removed, cand)
		}
		return report, nil
	}

	for _, cand := range candidates {
		if err := opts.Runner.WorktreeRemove(ctx, absRepo, cand.Path, true); err != nil {
			fmt.Fprintf(opts.Writer, "[error] Failed to remove worktree %s: %v\n", cand.Path, err)
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   cand.Path,
				Branch: cand.Branch,
				Reason: fmt.Sprintf("remove failed: %v", err),
			})
			continue
		}
		fmt.Fprintf(opts.Writer, "[removed] Worktree %s (branch: %s)\n", cand.Path, cand.Branch)
		report.Removed = append(report.Removed, cand)
	}

	pruneOut, err := opts.Runner.WorktreePrune(ctx, absRepo)
	if err != nil {
		fmt.Fprintf(opts.Writer, "[warn] Worktree prune failed: %v\n", err)
	} else if strings.TrimSpace(pruneOut) != "" {
		fmt.Fprintf(opts.Writer, "[prune] %s\n", strings.TrimSpace(pruneOut))
		report.Pruned = strings.TrimSpace(pruneOut)
	}

	return report, nil
}

// CleanupBlindWorktreesOptions defines parameters for the CleanupBlindWorktrees function.
type CleanupBlindWorktreesOptions struct {
	RepoDir           string
	OlderThan         time.Duration
	DryRun            bool
	Clock             func() time.Time
	Runner            GitRunner
	Writer            io.Writer
	ForceRemoveDirty  bool
}

// CleanupBlindWorktrees identifies and removes stale blind worktrees (branch pattern: blind/*).
func CleanupBlindWorktrees(ctx context.Context, opts CleanupBlindWorktreesOptions) (*CleanupReport, error) {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Runner == nil {
		opts.Runner = &DefaultGitRunner{}
	}
	if opts.Writer == nil {
		opts.Writer = io.Discard
	}
	if opts.RepoDir == "" {
		opts.RepoDir = "."
	}

	absRepo, err := filepath.Abs(opts.RepoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve repo dir: %w", err)
	}

	porcelain, err := opts.Runner.WorktreeListPorcelain(ctx, absRepo)
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}

	entries := ParseWorktreeListPorcelain(porcelain)
	report := &CleanupReport{
		DryRun:  opts.DryRun,
		Removed: make([]WorktreeCandidate, 0),
		Skipped: make([]SkippedWorktree, 0),
	}

	var candidates []WorktreeCandidate
	for _, entry := range entries {
		if entry.IsMain || IsMainWorktree(absRepo, entry.Path) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: "main worktree",
			})
			continue
		}

		if !BlindBranchPattern.MatchString(entry.Branch) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: "branch does not match blind pattern",
			})
			continue
		}

		age, isOlder, err := CheckWorktreeAge(entry.Path, opts.OlderThan, opts.Clock)
		if err != nil && !os.IsNotExist(err) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: fmt.Sprintf("stat error: %v", err),
			})
			continue
		}
		if !isOlder {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: fmt.Sprintf("worktree age (%s) below threshold (%s)", age.Round(time.Second), opts.OlderThan),
			})
			continue
		}

		dirty, err := HasUncommittedChanges(ctx, opts.Runner, entry.Path)
		if err != nil && !os.IsNotExist(err) {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: fmt.Sprintf("status error: %v", err),
			})
			continue
		}
		if dirty && !opts.ForceRemoveDirty {
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   entry.Path,
				Branch: entry.Branch,
				Reason: "uncommitted changes present",
			})
			fmt.Fprintf(opts.Writer, "[skip] Worktree %s has uncommitted changes\n", entry.Path)
			continue
		}
		if dirty && opts.ForceRemoveDirty {
			fmt.Fprintf(opts.Writer, "[force] Worktree %s has uncommitted changes but --force-remove-dirty enabled\n", entry.Path)
		}

		candidates = append(candidates, WorktreeCandidate{
			Path:   entry.Path,
			Branch: entry.Branch,
			Age:    age,
		})
	}

	if opts.DryRun {
		for _, cand := range candidates {
			fmt.Fprintf(opts.Writer, "[dry-run] Would remove blind worktree %s (branch: %s, age: %s)\n", cand.Path, cand.Branch, cand.Age.Round(time.Second))
			report.Removed = append(report.Removed, cand)
		}
		return report, nil
	}

	for _, cand := range candidates {
		if err := opts.Runner.WorktreeRemove(ctx, absRepo, cand.Path, true); err != nil {
			fmt.Fprintf(opts.Writer, "[error] Failed to remove worktree %s: %v\n", cand.Path, err)
			report.Skipped = append(report.Skipped, SkippedWorktree{
				Path:   cand.Path,
				Branch: cand.Branch,
				Reason: fmt.Sprintf("remove failed: %v", err),
			})
			continue
		}
		fmt.Fprintf(opts.Writer, "[removed] Blind worktree %s (branch: %s)\n", cand.Path, cand.Branch)
		report.Removed = append(report.Removed, cand)
	}

	pruneOut, err := opts.Runner.WorktreePrune(ctx, absRepo)
	if err != nil {
		fmt.Fprintf(opts.Writer, "[warn] Worktree prune failed: %v\n", err)
	} else if strings.TrimSpace(pruneOut) != "" {
		fmt.Fprintf(opts.Writer, "[prune] %s\n", strings.TrimSpace(pruneOut))
		report.Pruned = strings.TrimSpace(pruneOut)
	}

	return report, nil
}

// ParseWorktreeListPorcelain parses porcelain output from `git worktree list --porcelain`.
func ParseWorktreeListPorcelain(output string) []WorktreeEntry {
	var entries []WorktreeEntry
	lines := strings.Split(output, "\n")
	var current *WorktreeEntry

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if current != nil {
				if len(entries) == 0 {
					current.IsMain = true
				}
				entries = append(entries, *current)
				current = nil
			}
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			if current != nil {
				if len(entries) == 0 {
					current.IsMain = true
				}
				entries = append(entries, *current)
			}
			current = &WorktreeEntry{
				Path: filepath.Clean(strings.TrimPrefix(line, "worktree ")),
			}
		} else if current != nil {
			switch {
			case strings.HasPrefix(line, "HEAD "):
				current.Head = strings.TrimPrefix(line, "HEAD ")
			case strings.HasPrefix(line, "branch "):
				current.Ref = strings.TrimPrefix(line, "branch ")
				current.Branch = strings.TrimPrefix(current.Ref, "refs/heads/")
			case line == "bare":
				current.IsBare = true
			case line == "detached":
				current.Detached = true
			case strings.HasPrefix(line, "locked"):
				current.Locked = true
			case strings.HasPrefix(line, "prunable"):
				current.Prunable = true
			}
		}
	}
	if current != nil {
		if len(entries) == 0 {
			current.IsMain = true
		}
		entries = append(entries, *current)
	}
	return entries
}

// IsMainWorktree checks if the given path is the primary working tree.
func IsMainWorktree(repoDir, wtPath string) bool {
	cleanRepo := filepath.Clean(repoDir)
	cleanWT := filepath.Clean(wtPath)
	if strings.EqualFold(cleanRepo, cleanWT) {
		return true
	}
	if strings.EqualFold(strings.TrimPrefix(cleanRepo, "/private"), strings.TrimPrefix(cleanWT, "/private")) {
		return true
	}
	realRepo, err1 := filepath.EvalSymlinks(cleanRepo)
	realWT, err2 := filepath.EvalSymlinks(cleanWT)
	if err1 == nil && err2 == nil && strings.EqualFold(filepath.Clean(realRepo), filepath.Clean(realWT)) {
		return true
	}
	gitPath := filepath.Join(wtPath, ".git")
	info, err := os.Stat(gitPath)
	if err == nil && info.IsDir() {
		return true
	}
	return false
}

// CheckWorktreeAge checks if the worktree directory modtime is older than threshold.
func CheckWorktreeAge(wtPath string, olderThan time.Duration, clock func() time.Time) (time.Duration, bool, error) {
	stat, err := os.Stat(wtPath)
	if err != nil {
		return 0, false, err
	}
	now := clock()
	age := now.Sub(stat.ModTime())
	if age < olderThan {
		return age, false, nil
	}
	return age, true, nil
}

// HasUncommittedChanges checks if the worktree contains dirty files.
func HasUncommittedChanges(ctx context.Context, runner GitRunner, wtPath string) (bool, error) {
	out, err := runner.StatusPorcelain(ctx, wtPath)
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(out)) > 0, nil
}
