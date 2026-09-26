// Package cleanup implements ghost process and orphan resource detection
// and purging for g8s lifecycle management per DEBT-28 and DEBT-32.
package cleanup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/process"
	_ "modernc.org/sqlite"
)

// Supported cleanup target identifiers.
const (
	TargetGhostProcess   = "ghost-process"
	TargetOrphanWT       = "orphan-wt"
	TargetOrphanDir      = "orphan-dir"
	TargetOrphanBranch   = "orphan-branch"
	TargetStaleReceipt   = "stale-receipt"
	TargetOrphanSession  = "orphan-session"
	TargetClosedPRBranch = "closed-pr-branch"
	TargetOldTag         = "old-tag"
	TargetScratchBranch  = "scratch-branch"
)

// AllCleanupTargets lists all available cleanup target flags.
var AllCleanupTargets = []string{
	TargetGhostProcess,
	TargetOrphanWT,
	TargetOrphanDir,
	TargetOrphanBranch,
	TargetStaleReceipt,
	TargetOrphanSession,
	TargetClosedPRBranch,
	TargetOldTag,
	TargetScratchBranch,
}

// CleanupItem describes an individual resource identified for or subjected to cleanup.
type CleanupItem struct {
	Target string `json:"target"`
	ID     string `json:"id"`
	Detail string `json:"detail"`
	Action string `json:"action"` // would_kill, killed, would_remove, removed, would_prune, pruned, would_delete, deleted, skipped
	Error  string `json:"error,omitempty"`
}

// FullCleanupReport aggregates all results from the cleanup sweep.
type FullCleanupReport struct {
	DryRun  bool           `json:"dry_run"`
	Items   []CleanupItem  `json:"items"`
	Summary map[string]int `json:"summary"`
}

// ProcessInfo represents a running process evaluated by the ghost process detector.
type ProcessInfo struct {
	PID          int       `json:"pid"`
	ParentPID    int       `json:"parent_pid,omitempty"`
	Binary       string    `json:"binary"`
	CommandLine  string    `json:"command_line"`
	CWD          string    `json:"cwd,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	LastUpdate   time.Time `json:"last_update,omitempty"`
	Reason       string    `json:"reason"`
	HasHeartbeat bool      `json:"has_heartbeat"`
}

// TagInfo represents a local git tag and its timestamp.
type TagInfo struct {
	Name string    `json:"name"`
	Date time.Time `json:"date"`
}

// HeartbeatData matches the JSON payload structure stored in .heartbeat/agy/<id>.json.
type HeartbeatData struct {
	SessionID   string         `json:"session_id"`
	PID         int            `json:"pid"`
	Binary      string         `json:"binary"`
	CommandLine string         `json:"command_line"`
	StartedAt   string         `json:"started_at"`
	LastUpdate  string         `json:"last_update"`
	Status      string         `json:"status"`
	CurrentStep string         `json:"current_step,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ProcessManager abstracts OS process inspection and signals for testing and production.
type ProcessManager interface {
	FindGhostProcesses(ctx context.Context, heartbeatDir string, maxAge time.Duration, clock func() time.Time) ([]ProcessInfo, error)
	KillProcess(pid int, sig syscall.Signal) error
	IsProcessAlive(pid int) bool
}

// CleanupGitRunner abstracts git and github CLI invocations for cleanup operations.
type CleanupGitRunner interface {
	WorktreeListPorcelain(ctx context.Context, repoDir string) (string, error)
	WorktreePrune(ctx context.Context, repoDir string) (string, error)
	WorktreeRemove(ctx context.Context, repoDir, wtPath string) error
	MergedBranches(ctx context.Context, repoDir string) ([]string, error)
	RemoteBranches(ctx context.Context, repoDir string) ([]string, error)
	DeleteBranch(ctx context.Context, repoDir, branch string, force bool) error
	ClosedPRBranches(ctx context.Context, repoDir string) ([]string, error)
	LocalTags(ctx context.Context, repoDir string) ([]TagInfo, error)
	RemoteTags(ctx context.Context, repoDir string) ([]string, error)
	DeleteTag(ctx context.Context, repoDir, tag string) error
	// LocalBranches lists local branch names (short format, no HEAD marker).
	LocalBranches(ctx context.Context, repoDir string) ([]string, error)
	// BranchesContaining lists every ref (local and remote, short names) that
	// contains the given commit — used to prove a tip is preserved elsewhere.
	BranchesContaining(ctx context.Context, repoDir, sha string) ([]string, error)
	// BranchTipInfo returns the commit SHA and commit date of a branch tip.
	BranchTipInfo(ctx context.Context, repoDir, branch string) (string, time.Time, error)
	// CreateTag creates a lightweight tag at commit (used by the scratch
	// sweep to preserve unique unmerged tips before branch deletion).
	CreateTag(ctx context.Context, repoDir, tag, commit string) error
}

// DefaultProcessManager is the production implementation of ProcessManager using OS primitives and ProcessLister.
type DefaultProcessManager struct {
	RepoDir string
	Lister  process.ProcessLister
}

func (d *DefaultProcessManager) getLister() process.ProcessLister {
	if d.Lister != nil {
		return d.Lister
	}
	return process.NewLister()
}

// isPathUnderRepo checks whether target path is equal to or inside repoDir.
func isPathUnderRepo(target, repoDir string) bool {
	if target == "" || repoDir == "" {
		return false
	}

	cleanTarget := filepath.Clean(target)
	cleanRepo := filepath.Clean(repoDir)

	absTarget, err1 := filepath.Abs(cleanTarget)
	absRepo, err2 := filepath.Abs(cleanRepo)
	if err1 != nil || err2 != nil {
		return false
	}

	if isSubpath(absTarget, absRepo) {
		return true
	}

	// Try symlink evaluation for macOS /var -> /private/var
	evalTarget, errT := filepath.EvalSymlinks(absTarget)
	evalRepo, errR := filepath.EvalSymlinks(absRepo)
	if errT == nil && errR == nil {
		if isSubpath(evalTarget, evalRepo) {
			return true
		}
	} else if errT == nil {
		if isSubpath(evalTarget, absRepo) {
			return true
		}
	} else if errR == nil {
		if isSubpath(absTarget, evalRepo) {
			return true
		}
	}

	// Check /private prefix normalization
	trimTarget := strings.TrimPrefix(absTarget, "/private")
	trimRepo := strings.TrimPrefix(absRepo, "/private")
	return isSubpath(trimTarget, trimRepo)
}

func isSubpath(target, base string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// cmdlineReferencesRepo checks if the command line contains --add-dir or explicit path pointing to repoDir.
func cmdlineReferencesRepo(cmdLine, repoDir string) bool {
	if cmdLine == "" || repoDir == "" {
		return false
	}

	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	// 1. Exact or subpath match of absRepo in cmdLine
	if strings.Contains(cmdLine, absRepo) {
		return true
	}
	evalRepo, err := filepath.EvalSymlinks(absRepo)
	if err == nil && evalRepo != absRepo && strings.Contains(cmdLine, evalRepo) {
		return true
	}
	trimRepo := strings.TrimPrefix(absRepo, "/private")
	if trimRepo != absRepo && strings.Contains(cmdLine, trimRepo) {
		return true
	}

	// 2. Parse --add-dir arguments: e.g. --add-dir <path> or --add-dir=<path>
	fields := strings.Fields(cmdLine)
	for i, f := range fields {
		var dirArg string
		if strings.HasPrefix(f, "--add-dir=") {
			dirArg = strings.TrimPrefix(f, "--add-dir=")
		} else if f == "--add-dir" && i+1 < len(fields) {
			dirArg = fields[i+1]
		}
		if dirArg != "" {
			dirArg = strings.Trim(dirArg, `"'`)
			if isPathUnderRepo(dirArg, repoDir) {
				return true
			}
		}
	}

	return false
}

// FindGhostProcesses inspects host processes matching agy or claude and cross-references heartbeats within the project scope.
func (d *DefaultProcessManager) FindGhostProcesses(ctx context.Context, heartbeatDir string, maxAge time.Duration, clock func() time.Time) ([]ProcessInfo, error) {
	if clock == nil {
		clock = time.Now
	}

	l := d.getLister()
	procs, err := l.List()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}

	repoDir := d.RepoDir
	if repoDir == "" {
		if heartbeatDir != "" && heartbeatDir != ".heartbeat" {
			repoDir = filepath.Dir(heartbeatDir)
		} else {
			repoDir = "."
		}
	}

	var ghosts []ProcessInfo
	selfPID := os.Getpid()
	parentPID := os.Getppid()

	// Load all known heartbeats
	heartbeats := LoadHeartbeats(heartbeatDir)

	for _, p := range procs {
		pid := p.PID
		ppid := p.PPID
		if pid <= 0 || pid == selfPID || pid == parentPID {
			continue
		}

		cmdLine := p.CommandLine
		binName := filepath.Base(p.Binary)
		binName = strings.TrimSuffix(binName, ".exe")

		// Ignore grep / test / g8s self invocations
		if strings.Contains(cmdLine, "grep") || strings.Contains(cmdLine, "g8s cleanup") || strings.Contains(cmdLine, "go test") {
			continue
		}

		// ⚡ Bolt Optimization: Use EqualFold on a sliced substring for zero-allocation case-insensitive prefix checking
		// instead of strings.HasPrefix(strings.ToLower()) which allocates a new string on the heap.
		isAgy := strings.EqualFold(binName, "agy") || (len(binName) >= 3 && strings.EqualFold(binName[:3], "agy"))
		isClaude := strings.EqualFold(binName, "claude") || (len(binName) >= 6 && strings.EqualFold(binName[:6], "claude"))
		if !isAgy && !isClaude {
			continue
		}

		// Cross-reference with heartbeat by PID
		hb, hasHeartbeat := heartbeats[pid]
		now := clock()

		// Skip-by-name (mandatory): NEVER touch claude unless its session_id is in heartbeat store of this repo
		if isClaude && !hasHeartbeat {
			continue
		}

		// Resolve CWD
		cwd := p.CWD
		if cwd == "" {
			cwd = l.ResolveCWD(pid)
		}
		if cwd == "" {
			cwd = resolveProcessCWD(pid)
		}

		// Project association checks:
		cwdInProject := cwd != "" && isPathUnderRepo(cwd, repoDir)
		cmdInProject := cmdlineReferencesRepo(cmdLine, repoDir)

		// Candidate ghost qualification:
		// A process is associated with this project if:
		// 1. It has a heartbeat in this repo's heartbeatDir, OR
		// 2. Its CWD is inside the project repo, OR
		// 3. Its command line references the project repo (e.g. --add-dir)
		if !hasHeartbeat && !cwdInProject && !cmdInProject {
			// Foreign process completely unrelated to this project repo -> skip
			continue
		}

		if !hasHeartbeat {
			ghosts = append(ghosts, ProcessInfo{
				PID:          pid,
				ParentPID:    ppid,
				Binary:       binName,
				CommandLine:  cmdLine,
				CWD:          cwd,
				Reason:       "no live heartbeat file in project repo",
				HasHeartbeat: false,
			})
			continue
		}

		lastUpdate, parseErr := time.Parse(time.RFC3339, hb.LastUpdate)
		if parseErr != nil {
			ghosts = append(ghosts, ProcessInfo{
				PID:          pid,
				ParentPID:    ppid,
				Binary:       binName,
				CommandLine:  cmdLine,
				CWD:          cwd,
				Reason:       "invalid heartbeat timestamp",
				HasHeartbeat: true,
			})
			continue
		}

		if now.Sub(lastUpdate) > maxAge {
			ghosts = append(ghosts, ProcessInfo{
				PID:          pid,
				ParentPID:    ppid,
				Binary:       binName,
				CommandLine:  cmdLine,
				CWD:          cwd,
				LastUpdate:   lastUpdate,
				Reason:       fmt.Sprintf("heartbeat stale (last update %s ago > %s)", now.Sub(lastUpdate).Round(time.Second), maxAge),
				HasHeartbeat: true,
			})
		}
	}

	return ghosts, nil
}

// LoadHeartbeats loads all heartbeat records from baseDir and child directories.
func LoadHeartbeats(baseDir string) map[int]HeartbeatData {
	res := make(map[int]HeartbeatData)
	if baseDir == "" {
		baseDir = ".heartbeat"
	}

	dirs := []string{
		baseDir,
		filepath.Join(baseDir, "agy"),
		filepath.Join(baseDir, "claude"),
	}

	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(d, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var hb HeartbeatData
			if err := json.Unmarshal(data, &hb); err == nil && hb.PID > 0 {
				if existing, exists := res[hb.PID]; exists {
					tExisting, err1 := time.Parse(time.RFC3339, existing.LastUpdate)
					tNew, err2 := time.Parse(time.RFC3339, hb.LastUpdate)
					if err1 == nil && err2 == nil && tNew.After(tExisting) {
						res[hb.PID] = hb
					}
				} else {
					res[hb.PID] = hb
				}
			}
		}
	}

	return res
}

// KillProcess sends the specified signal to the process or uses ProcessLister.
func (d *DefaultProcessManager) KillProcess(pid int, sig syscall.Signal) error {
	l := d.getLister()
	if sig == syscall.SIGKILL {
		return l.KillForce(pid)
	}
	return l.Kill(pid)
}

// IsProcessAlive checks whether the process is alive.
func (d *DefaultProcessManager) IsProcessAlive(pid int) bool {
	return d.getLister().IsAlive(pid)
}

// DefaultCleanupGitRunner is the production implementation of CleanupGitRunner using git/gh CLI.
type DefaultCleanupGitRunner struct{}

func (g *DefaultCleanupGitRunner) WorktreeListPorcelain(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "list", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree list: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (g *DefaultCleanupGitRunner) WorktreePrune(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "prune", "--expire=now", "--verbose")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree prune: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (g *DefaultCleanupGitRunner) WorktreeRemove(ctx context.Context, repoDir, wtPath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "remove", wtPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (g *DefaultCleanupGitRunner) MergedBranches(ctx context.Context, repoDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "branch", "--merged", "main")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback to plain git branch --merged if main ref differs
		cmd = exec.CommandContext(ctx, "git", "-C", repoDir, "branch", "--merged")
		out, err = cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("git branch --merged: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}

	var branches []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "+ ")
		if line == "" || line == "main" || line == "master" {
			continue
		}
		branches = append(branches, line)
	}
	return branches, nil
}

func (g *DefaultCleanupGitRunner) RemoteBranches(ctx context.Context, repoDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "ls-remote", "--heads", "origin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var branches []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			ref := parts[1]
			branch := strings.TrimPrefix(ref, "refs/heads/")
			branches = append(branches, branch)
		}
	}
	return branches, nil
}

func (g *DefaultCleanupGitRunner) DeleteBranch(ctx context.Context, repoDir, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "branch", flag, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch %s %s: %w (%s)", flag, branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (g *DefaultCleanupGitRunner) ClosedPRBranches(ctx context.Context, repoDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--state", "closed", "--limit", "100", "--json", "headRefName", "--jq", ".[].headRefName")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var branches []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "main" && line != "master" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

// LocalBranches lists local branch names in short format.
func (g *DefaultCleanupGitRunner) LocalBranches(ctx context.Context, repoDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "branch", "--format=%(refname:short)")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git branch --format: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var branches []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

// BranchesContaining lists refs that contain the given commit (local + remote).
func (g *DefaultCleanupGitRunner) BranchesContaining(ctx context.Context, repoDir, sha string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "branch", "-a", "--contains", sha, "--format=%(refname:short)")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git branch -a --contains: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var refs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

// BranchTipInfo returns the commit SHA and commit date of a branch tip.
func (g *DefaultCleanupGitRunner) BranchTipInfo(ctx context.Context, repoDir, branch string) (string, time.Time, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "log", "-1", "--format=%H|%cI", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("git log -1 %s: %w (%s)", branch, err, strings.TrimSpace(string(out)))
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) != 2 {
		return "", time.Time{}, fmt.Errorf("git log -1 %s: unexpected output %q", branch, strings.TrimSpace(string(out)))
	}
	t, perr := time.Parse(time.RFC3339, parts[1])
	if perr != nil {
		return "", time.Time{}, fmt.Errorf("parse tip date for %s: %w", branch, perr)
	}
	return parts[0], t, nil
}

// CreateTag creates a lightweight tag at commit.
func (g *DefaultCleanupGitRunner) CreateTag(ctx context.Context, repoDir, tag, commit string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "tag", tag, commit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git tag %s %s: %w (%s)", tag, commit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (g *DefaultCleanupGitRunner) LocalTags(ctx context.Context, repoDir string) ([]TagInfo, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "tag", "-l", "--format=%(refname:short)|%(creatordate:iso8601)|%(authordate:iso8601)")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git tag list: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var tags []TagInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		name := parts[0]
		dateStr := ""
		if len(parts) > 1 && parts[1] != "" {
			dateStr = parts[1]
		} else if len(parts) > 2 && parts[2] != "" {
			dateStr = parts[2]
		}
		var tagDate time.Time
		if dateStr != "" {
			if t, err := time.Parse("2006-01-02 15:04:05 -0700", dateStr); err == nil {
				tagDate = t
			} else if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
				tagDate = t
			}
		}
		tags = append(tags, TagInfo{Name: name, Date: tagDate})
	}
	return tags, nil
}

func (g *DefaultCleanupGitRunner) RemoteTags(ctx context.Context, repoDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "ls-remote", "--tags", "origin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote --tags: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var tags []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, "^{}") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			ref := parts[1]
			tag := strings.TrimPrefix(ref, "refs/tags/")
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

func (g *DefaultCleanupGitRunner) DeleteTag(ctx context.Context, repoDir, tag string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "tag", "-d", tag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git tag -d %s: %w (%s)", tag, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CleanupAuditEntry defines the schema for .cleanup-audit.jsonl records.
type CleanupAuditEntry struct {
	Timestamp   string `json:"ts"`
	PID         int    `json:"pid"`
	Binary      string `json:"binary"`
	CommandLine string `json:"cmdline"`
	CWD         string `json:"cwd"`
	ParentPID   int    `json:"parent_pid"`
	Reason      string `json:"reason"`
	OperatorPID int    `json:"operator_pid"`
	Signal      string `json:"signal"`
	ExitCode    int    `json:"exit_code"`
}

func appendAuditLog(logPath string, entry CleanupAuditEntry) error {
	if logPath == "" {
		return nil
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(logPath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
}

// CleanupConfig holds parameters and dependencies for executing a full cleanup sweep.
type CleanupConfig struct {
	RepoDir         string
	HeartbeatDir    string
	DBPath          string
	WorktreeBaseDir string
	Targets         []string
	DryRun          bool
	ForceForeign    bool
	ForceMissing    bool // Alias for ForceForeign for backwards compatibility
	AuditLogPath    string
	GracePeriod     time.Duration
	Clock           func() time.Time
	GitRunner       CleanupGitRunner
	ProcessManager  ProcessManager
	Writer          io.Writer
	// Scratch branch sweep (#327): when ScratchEnabled, local branches whose
	// name matches any ScratchPattern and whose tip is older than
	// ScratchOlderThan are deleted after their unique unmerged tip commits
	// are preserved under keep/scratch-auto-* tags.
	ScratchEnabled   bool
	ScratchPatterns  []string
	ScratchOlderThan time.Duration
	// Orphan-session sweep (#393): active sessions whose heartbeat is older
	// than SessionGracePeriod are zombies — marked dead, then reaped along
	// with every dead session (worktree, sess/* branch with tip preserved
	// under keep/session-* tags, registry row). Zero means the 10-minute
	// default (DefaultSessionGrace) — minutes-scale so a machine that
	// sleeps does not come back to a reaped worktree.
	SessionGracePeriod time.Duration
}

// RunCleanupSweep executes the lifecycle cleanup sweep across all selected targets.
func RunCleanupSweep(ctx context.Context, cfg CleanupConfig) (*FullCleanupReport, error) {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.GitRunner == nil {
		cfg.GitRunner = &DefaultCleanupGitRunner{}
	}
	if cfg.ProcessManager == nil {
		cfg.ProcessManager = &DefaultProcessManager{}
	}
	if cfg.Writer == nil {
		cfg.Writer = io.Discard
	}
	if cfg.RepoDir == "" {
		cfg.RepoDir = "."
	}
	if cfg.GracePeriod <= 0 {
		cfg.GracePeriod = 10 * time.Second
	}

	targetSet := make(map[string]bool)
	if len(cfg.Targets) == 0 {
		for _, t := range AllCleanupTargets {
			targetSet[t] = true
		}
	} else {
		for _, t := range cfg.Targets {
			targetSet[t] = true
		}
	}

	report := &FullCleanupReport{
		DryRun:  cfg.DryRun,
		Items:   make([]CleanupItem, 0),
		Summary: make(map[string]int),
	}

	// 1. Ghost processes
	if targetSet[TargetGhostProcess] {
		items, err := sweepGhostProcesses(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] ghost-process sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetGhostProcess] = len(items)
		}
	}

	// 2. Orphan worktree refs
	if targetSet[TargetOrphanWT] {
		items, err := sweepOrphanWorktrees(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] orphan-wt sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetOrphanWT] = len(items)
		}
	}

	// 3. Stale worktree dirs
	if targetSet[TargetOrphanDir] {
		items, err := sweepOrphanWorktreeDirs(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] orphan-dir sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetOrphanDir] = len(items)
		}
	}

	// 4. Old agy-sup-* branches
	if targetSet[TargetOrphanBranch] {
		items, err := sweepOrphanBranches(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] orphan-branch sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetOrphanBranch] = len(items)
		}
	}

	// 5. Stale receipts
	if targetSet[TargetStaleReceipt] {
		items, err := sweepStaleReceipts(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] stale-receipt sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetStaleReceipt] = len(items)
		}
	}

	// 5b. Orphan sessions (#393): dead + zombie supervisor sessions.
	if targetSet[TargetOrphanSession] {
		items, err := sweepOrphanSessions(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] orphan-session sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetOrphanSession] = len(items)
		}
	}

	// 6. Closed PR branches
	if targetSet[TargetClosedPRBranch] {
		items, err := sweepClosedPRBranches(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] closed-pr-branch sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetClosedPRBranch] = len(items)
		}
	}

	// 7. Old local tags
	if targetSet[TargetOldTag] {
		items, err := sweepOldTags(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] old-tag sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetOldTag] = len(items)
		}
	}

	// 8. Scratch branches (#327) — pattern-matched worker litter with
	// verify-tag-delete preservation of unique unmerged tips.
	// Opt-in only: never runs unless --scratch (ScratchEnabled) was passed,
	// because it deletes unmerged branches.
	if targetSet[TargetScratchBranch] && cfg.ScratchEnabled {
		items, err := sweepScratchBranches(ctx, cfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] scratch-branch sweep error: %v\n", err)
		} else {
			report.Items = append(report.Items, items...)
			report.Summary[TargetScratchBranch] = len(items)
		}
	}

	return report, nil
}

// 1. Ghost processes sweep
func sweepGhostProcesses(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	if dpm, ok := cfg.ProcessManager.(*DefaultProcessManager); ok {
		if dpm.RepoDir == "" {
			dpm.RepoDir = cfg.RepoDir
		}
	}

	ghosts, err := cfg.ProcessManager.FindGhostProcesses(ctx, cfg.HeartbeatDir, 5*time.Minute, cfg.Clock)
	if err != nil {
		return nil, err
	}

	forceForeign := cfg.ForceForeign || cfg.ForceMissing

	auditLogPath := cfg.AuditLogPath
	if auditLogPath == "" {
		auditLogPath = ".cleanup-audit.jsonl"
	}
	if !filepath.IsAbs(auditLogPath) && cfg.RepoDir != "" {
		auditLogPath = filepath.Join(cfg.RepoDir, auditLogPath)
	}

	var items []CleanupItem
	for _, proc := range ghosts {
		idStr := strconv.Itoa(proc.PID)
		detail := fmt.Sprintf("%s (%s): %s", proc.Binary, proc.Reason, proc.CommandLine)

		if cfg.DryRun {
			items = append(items, CleanupItem{
				Target: TargetGhostProcess,
				ID:     idStr,
				Detail: detail,
				Action: "would_kill",
			})
			continue
		}

		// Safety rule: If process has no heartbeat file and ForceForeign is false, report and skip (never kill)
		if !proc.HasHeartbeat && !forceForeign {
			items = append(items, CleanupItem{
				Target: TargetGhostProcess,
				ID:     idStr,
				Detail: fmt.Sprintf("%s (no heartbeat in project repo, requires --force-foreign to kill): %s", proc.Binary, proc.CommandLine),
				Action: "skipped",
			})
			continue
		}

		// Warning when killing a foreign / no-heartbeat process
		if !proc.HasHeartbeat && forceForeign {
			if cfg.Writer != nil {
				_, _ = fmt.Fprintf(cfg.Writer, "[WARN] Terminating process PID %d (%s) without heartbeat in project repo (CWD: %s, Parent PID: %d)\n",
					proc.PID, proc.Binary, proc.CWD, proc.ParentPID)
			}
		}

		// Force mode: SIGTERM, grace period, then SIGKILL
		sigSent := "SIGTERM"
		_ = cfg.ProcessManager.KillProcess(proc.PID, syscall.SIGTERM)

		// Wait up to grace period for process to exit
		start := time.Now()
		killed := false
		for time.Since(start) < cfg.GracePeriod {
			if !cfg.ProcessManager.IsProcessAlive(proc.PID) {
				killed = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}

		if !killed && cfg.ProcessManager.IsProcessAlive(proc.PID) {
			sigSent = "SIGKILL"
			_ = cfg.ProcessManager.KillProcess(proc.PID, syscall.SIGKILL)
		}

		// Append to audit log
		now := time.Now().UTC()
		if cfg.Clock != nil {
			now = cfg.Clock().UTC()
		}
		auditEntry := CleanupAuditEntry{
			Timestamp:   now.Format(time.RFC3339),
			PID:         proc.PID,
			Binary:      proc.Binary,
			CommandLine: proc.CommandLine,
			CWD:         proc.CWD,
			ParentPID:   proc.ParentPID,
			Reason:      proc.Reason,
			OperatorPID: os.Getpid(),
			Signal:      sigSent,
			ExitCode:    0,
		}
		_ = appendAuditLog(auditLogPath, auditEntry)

		items = append(items, CleanupItem{
			Target: TargetGhostProcess,
			ID:     idStr,
			Detail: detail,
			Action: "killed",
		})
	}
	return items, nil
}

// 2. Orphan worktrees sweep
func sweepOrphanWorktrees(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	porcelain, err := cfg.GitRunner.WorktreeListPorcelain(ctx, cfg.RepoDir)
	if err != nil {
		return nil, err
	}

	entries := ParseWorktreeListPorcelain(porcelain)
	var items []CleanupItem

	for _, entry := range entries {
		if entry.IsMain || IsMainWorktree(cfg.RepoDir, entry.Path) {
			continue
		}

		// Check if directory exists on disk or if worktree is marked prunable
		_, statErr := os.Stat(entry.Path)
		isMissing := os.IsNotExist(statErr)

		if entry.Prunable || isMissing {
			if cfg.DryRun {
				items = append(items, CleanupItem{
					Target: TargetOrphanWT,
					ID:     entry.Path,
					Detail: fmt.Sprintf("branch: %s (prunable / missing dir)", entry.Branch),
					Action: "would_prune",
				})
			} else {
				items = append(items, CleanupItem{
					Target: TargetOrphanWT,
					ID:     entry.Path,
					Detail: fmt.Sprintf("branch: %s", entry.Branch),
					Action: "pruned",
				})
			}
		}
	}

	if !cfg.DryRun && len(items) > 0 {
		_, _ = cfg.GitRunner.WorktreePrune(ctx, cfg.RepoDir)
	}

	return items, nil
}

// 3. Stale worktree dirs sweep
func sweepOrphanWorktreeDirs(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	porcelain, err := cfg.GitRunner.WorktreeListPorcelain(ctx, cfg.RepoDir)
	if err != nil {
		return nil, err
	}

	activeEntries := ParseWorktreeListPorcelain(porcelain)
	activeMap := make(map[string]bool)
	for _, entry := range activeEntries {
		clean := filepath.Clean(entry.Path)
		activeMap[clean] = true
		activeMap[strings.TrimPrefix(clean, "/private")] = true
	}

	// Determine directories to scan
	var candidateDirs []string
	if cfg.WorktreeBaseDir != "" {
		candidateDirs = append(candidateDirs, cfg.WorktreeBaseDir)
	} else {
		candidateDirs = append(candidateDirs, filepath.Join(os.TempDir(), "g8s-worktrees"))
	}

	var items []CleanupItem
	for _, baseDir := range candidateDirs {
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dirPath := filepath.Join(baseDir, entry.Name())
			cleanPath := filepath.Clean(dirPath)
			trimmedPath := strings.TrimPrefix(cleanPath, "/private")

			if !activeMap[cleanPath] && !activeMap[trimmedPath] {
				if cfg.DryRun {
					items = append(items, CleanupItem{
						Target: TargetOrphanDir,
						ID:     dirPath,
						Detail: "unregistered worktree directory",
						Action: "would_remove",
					})
				} else {
					err := os.RemoveAll(dirPath)
					action := "removed"
					var errStr string
					if err != nil {
						action = "skipped"
						errStr = err.Error()
					}
					items = append(items, CleanupItem{
						Target: TargetOrphanDir,
						ID:     dirPath,
						Detail: "unregistered worktree directory",
						Action: action,
						Error:  errStr,
					})
				}
			}
		}
	}

	return items, nil
}

// 4. Old agy-sup-* branches sweep
var agySupBranchPattern = regexp.MustCompile(`^(agy/sup-|agy-sup-).*`)

func sweepOrphanBranches(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	merged, err := cfg.GitRunner.MergedBranches(ctx, cfg.RepoDir)
	if err != nil {
		return nil, err
	}

	remote, err := cfg.GitRunner.RemoteBranches(ctx, cfg.RepoDir)
	if err != nil {
		// If remote ls-remote fails (e.g. offline/mock), assume empty remote
		remote = []string{}
	}

	remoteMap := make(map[string]bool)
	for _, b := range remote {
		remoteMap[b] = true
	}

	var items []CleanupItem
	for _, branch := range merged {
		if !agySupBranchPattern.MatchString(branch) {
			continue
		}
		if remoteMap[branch] {
			continue
		}

		if cfg.DryRun {
			items = append(items, CleanupItem{
				Target: TargetOrphanBranch,
				ID:     branch,
				Detail: "merged local branch not on remote",
				Action: "would_delete",
			})
		} else {
			delErr := cfg.GitRunner.DeleteBranch(ctx, cfg.RepoDir, branch, false)
			action := "deleted"
			var errStr string
			if delErr != nil {
				action = "skipped"
				errStr = delErr.Error()
			}
			items = append(items, CleanupItem{
				Target: TargetOrphanBranch,
				ID:     branch,
				Detail: "merged local branch not on remote",
				Action: action,
				Error:  errStr,
			})
		}
	}

	return items, nil
}

// 5. Stale receipts sweep
func sweepStaleReceipts(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	if cfg.DBPath == "" {
		return nil, nil
	}

	if _, err := os.Stat(cfg.DBPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	dsn := pathutil.SQLiteURI(cfg.DBPath, "_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db for receipt cleanup: %w", err)
	}
	defer db.Close()

	nowUnix := float64(cfg.Clock().Unix())
	rows, err := db.QueryContext(ctx, "SELECT receipt_id, expires_at FROM write_receipts WHERE consumed = 0 AND expires_at < ?", nowUnix)
	if err != nil {
		// Table might not exist yet
		return nil, nil
	}
	defer rows.Close()

	var staleIDs []string
	for rows.Next() {
		var id string
		var expiresAt float64
		if err := rows.Scan(&id, &expiresAt); err == nil {
			staleIDs = append(staleIDs, id)
		}
	}

	var items []CleanupItem
	for _, id := range staleIDs {
		if cfg.DryRun {
			items = append(items, CleanupItem{
				Target: TargetStaleReceipt,
				ID:     id,
				Detail: "unconsumed expired write receipt",
				Action: "would_delete",
			})
		} else {
			_, _ = db.ExecContext(ctx, "DELETE FROM write_receipts WHERE receipt_id = ?", id)
			items = append(items, CleanupItem{
				Target: TargetStaleReceipt,
				ID:     id,
				Detail: "unconsumed expired write receipt",
				Action: "deleted",
			})
		}
	}

	return items, nil
}

// 6. Closed PR branches sweep
func sweepClosedPRBranches(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	closedBranches, err := cfg.GitRunner.ClosedPRBranches(ctx, cfg.RepoDir)
	if err != nil {
		return nil, nil
	}

	// #326: PR head refs include branches that only exist on the remote
	// (dependabot/*, merged-and-deleted feat/*). Attempting a local
	// `git branch -D` on those always fails with "not found" — report them
	// honestly as skipped remote-only refs instead of error noise.
	localBranches, lerr := cfg.GitRunner.LocalBranches(ctx, cfg.RepoDir)
	localSet := make(map[string]bool)
	if lerr == nil {
		for _, b := range localBranches {
			localSet[b] = true
		}
	}

	var items []CleanupItem
	for _, branch := range closedBranches {
		if branch == "main" || branch == "master" {
			continue
		}

		if !localSet[branch] {
			items = append(items, CleanupItem{
				Target: TargetClosedPRBranch,
				ID:     branch,
				Detail: "remote-only PR head (not a local branch; delete on the remote with git push origin --delete if wanted)",
				Action: "skipped",
			})
			continue
		}

		if cfg.DryRun {
			items = append(items, CleanupItem{
				Target: TargetClosedPRBranch,
				ID:     branch,
				Detail: "branch from closed GitHub pull request",
				Action: "would_delete",
			})
		} else {
			delErr := cfg.GitRunner.DeleteBranch(ctx, cfg.RepoDir, branch, true)
			action := "deleted"
			var errStr string
			if delErr != nil {
				action = "skipped"
				errStr = delErr.Error()
			}
			items = append(items, CleanupItem{
				Target: TargetClosedPRBranch,
				ID:     branch,
				Detail: "branch from closed GitHub pull request",
				Action: action,
				Error:  errStr,
			})
		}
	}

	return items, nil
}

// sweepScratchBranches implements the scratch-class sweep (#327). Local
// worker scratch branches (pattern-matched, tip older than the threshold)
// are deleted only after every unique unmerged tip commit is preserved
// under a keep/scratch-auto-N tag — the verify-tag-delete protocol from
// the 2026-09-25 campaign sweep, promoted to enforce-code.
func sweepScratchBranches(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	if len(cfg.ScratchPatterns) == 0 {
		cfg.ScratchPatterns = []string{"agy/*", "blind/*"}
	}
	threshold := cfg.ScratchOlderThan
	if threshold <= 0 {
		threshold = 72 * time.Hour
	}

	branches, err := cfg.GitRunner.LocalBranches(ctx, cfg.RepoDir)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if cfg.Clock != nil {
		now = cfg.Clock()
	}

	var items []CleanupItem
	taggedTips := make(map[string]bool)
	tagSeq := 0
	for _, branch := range branches {
		matched := false
		for _, p := range cfg.ScratchPatterns {
			if ok, _ := path.Match(strings.TrimSpace(p), branch); ok {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		sha, tipTime, terr := cfg.GitRunner.BranchTipInfo(ctx, cfg.RepoDir, branch)
		if terr != nil {
			items = append(items, CleanupItem{
				Target: TargetScratchBranch,
				ID:     branch,
				Detail: "cannot read tip commit",
				Action: "skipped",
				Error:  terr.Error(),
			})
			continue
		}

		age := now.Sub(tipTime)
		if age <= threshold {
			continue // young scratch branch — still in active use
		}

		// Verify-tag-delete: if this tip commit is not already preserved
		// (tagged in a previous branch iteration of this sweep) and no
		// other ref contains it, tag it before deleting the branch.
		if !taggedTips[sha] {
			refs, cerr := cfg.GitRunner.BranchesContaining(ctx, cfg.RepoDir, sha)
			preserved := cerr == nil && len(refs) > 1 // the scratch branch itself is one ref
			if cerr == nil && !preserved {
				tag := fmt.Sprintf("keep/scratch-auto-%04d", tagSeq)
				tagSeq++
				if terr := cfg.GitRunner.CreateTag(ctx, cfg.RepoDir, tag, sha); terr == nil {
					taggedTips[sha] = true
					items = append(items, CleanupItem{
						Target: TargetScratchBranch,
						ID:     tag,
						Detail: fmt.Sprintf("preserved unique unmerged tip %s (from %s)", sha[:10], branch),
						Action: "created",
					})
				}
			} else if cerr == nil {
				taggedTips[sha] = true // preserved by other refs; no tag needed
			}
		}

		if cfg.DryRun {
			items = append(items, CleanupItem{
				Target: TargetScratchBranch,
				ID:     branch,
				Detail: fmt.Sprintf("scratch branch, tip %s old (threshold %s)", age.Round(24*time.Hour), threshold),
				Action: "would_delete",
			})
		} else {
			delErr := cfg.GitRunner.DeleteBranch(ctx, cfg.RepoDir, branch, true)
			action := "deleted"
			var errStr string
			if delErr != nil {
				action = "skipped"
				errStr = delErr.Error()
			}
			items = append(items, CleanupItem{
				Target: TargetScratchBranch,
				ID:     branch,
				Detail: fmt.Sprintf("scratch branch, tip %s old", age.Round(24*time.Hour)),
				Action: action,
				Error:  errStr,
			})
		}
	}

	return items, nil
}

// 7. Old local tags sweep
func sweepOldTags(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	localTags, err := cfg.GitRunner.LocalTags(ctx, cfg.RepoDir)
	if err != nil {
		return nil, err
	}

	remoteTags, err := cfg.GitRunner.RemoteTags(ctx, cfg.RepoDir)
	if err != nil {
		remoteTags = []string{}
	}

	remoteMap := make(map[string]bool)
	for _, t := range remoteTags {
		remoteMap[t] = true
	}

	now := cfg.Clock()
	threshold := 30 * 24 * time.Hour

	var items []CleanupItem
	for _, tag := range localTags {
		if remoteMap[tag.Name] {
			continue
		}
		if !tag.Date.IsZero() && now.Sub(tag.Date) > threshold {
			if cfg.DryRun {
				items = append(items, CleanupItem{
					Target: TargetOldTag,
					ID:     tag.Name,
					Detail: fmt.Sprintf("local tag created %s ago (>30d), not on remote", now.Sub(tag.Date).Round(24*time.Hour)),
					Action: "would_delete",
				})
			} else {
				delErr := cfg.GitRunner.DeleteTag(ctx, cfg.RepoDir, tag.Name)
				action := "deleted"
				var errStr string
				if delErr != nil {
					action = "skipped"
					errStr = delErr.Error()
				}
				items = append(items, CleanupItem{
					Target: TargetOldTag,
					ID:     tag.Name,
					Detail: fmt.Sprintf("local tag created %s ago (>30d), not on remote", now.Sub(tag.Date).Round(24*time.Hour)),
					Action: action,
					Error:  errStr,
				})
			}
		}
	}

	return items, nil
}
