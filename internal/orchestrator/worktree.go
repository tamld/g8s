package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
)

// Pool allocates per-worker git worktrees so N workers can edit the same
// repo without stepping on each other. Acquire is race-safe across
// goroutines within the process via an in-memory sync.Mutex (cross-process
// file-level locking is handled by the SQLite control-plane task leases).
// One worktree = one task = one worker.
type Pool struct {
	root   string
	repo   string // absolute path of the git working tree
	base   string // branch or commit to cut from
	prefix string

	mu        sync.Mutex
	allocated map[string]Worktree // active leases
}

// PoolOptions configures a Pool. Repo is required; Root + Base + Prefix
// have sensible defaults.
type PoolOptions struct {
	Repo   string // absolute path to the git working tree
	Root   string // pool root for worktree dirs (default: os.TempDir()/g8s-worktrees)
	Base   string // branch/commit to cut each worktree from (default: "HEAD")
	Prefix string // branch prefix for worktree branches (default: "agy")
}

// NewPool builds a Pool. It validates that Repo is a git working tree.
// Worktrees are created lazily on Acquire, not eagerly.
func NewPool(opts PoolOptions) (*Pool, error) {
	if opts.Repo == "" {
		return nil, errors.New("orchestrator: Pool.Repo required")
	}
	abs, err := filepath.Abs(opts.Repo)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: abs repo: %w", err)
	}
	if !isGitRepo(abs) {
		return nil, fmt.Errorf("orchestrator: %s is not a git working tree", abs)
	}
	root := opts.Root
	if root == "" {
		// #465: pool worktrees under THIS instance's state dir, not the
		// host-global TMPDIR — a host-wide cleanup sweep treating the
		// shared TMPDIR root as its scan set destroyed other sessions'
		// preserved worktrees. The state dir is the ownership boundary.
		root = filepath.Join(pathutil.DefaultStateDir(), "worktrees")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("orchestrator: mkdir pool root: %w", err)
	}
	base := opts.Base
	if base == "" {
		base = "HEAD"
	}
	prefix := opts.Prefix
	if prefix == "" {
		prefix = "agy"
	}
	return &Pool{
		root:      root,
		repo:      abs,
		base:      base,
		prefix:    prefix,
		allocated: map[string]Worktree{},
	}, nil
}

// Acquire creates a fresh worktree at <root>/wt-<short>-<nanos> on a
// branch `<prefix>/<taskID>`. Caller MUST Release on completion.
//
// The first call also resolves the base SHA so receipts record exactly
// which commit the worker was anchored to (lets us bisect any post-hoc
// file change back to the worker that made it).
func (p *Pool) Acquire(_ context.Context, taskID string) (Worktree, error) {
	if taskID == "" {
		return Worktree{}, errors.New("orchestrator: taskID required")
	}
	p.mu.Lock()
	if wt, ok := p.allocated[taskID]; ok {
		p.mu.Unlock()
		return wt, nil
	}

	branch := fmt.Sprintf("%s/%s", p.prefix, taskID)
	_ = os.MkdirAll(p.root, 0o755)

	var (
		id     string
		wtPath string
	)
	for attempt := 1; attempt <= 5; attempt++ {
		id = "wt-" + shortIDFn()
		wtPath = filepath.Join(p.root, id)
		if isLiveWorktree(wtPath) {
			if attempt == 5 {
				p.mu.Unlock()
				return Worktree{}, fmt.Errorf("worktree path collision with a live worktree after 5 attempts: %s", wtPath)
			}
			continue
		}
		break
	}

	_ = os.RemoveAll(wtPath)
	if err := gitAddWorktree(p.repo, wtPath, branch, p.base); err != nil {
		p.mu.Unlock()
		return Worktree{}, fmt.Errorf("git worktree add: %w", err)
	}
	baseSHA, err := gitRevParse(p.repo, "HEAD")
	if err != nil {
		_ = p.forceCleanup(wtPath)
		p.mu.Unlock()
		return Worktree{}, fmt.Errorf("rev-parse HEAD: %w", err)
	}
	wt := Worktree{
		ID:      id,
		Path:    wtPath,
		Branch:  branch,
		BaseSHA: baseSHA,
	}
	p.allocated[taskID] = wt
	p.mu.Unlock()
	return wt, nil
}

// Release removes the worktree and prunes its branch. If keep is true,
// the branch is retained and a dirty worktree (uncommitted deliverables)
// or one containing receipt-scoped deliverables is preserved instead of being removed (issue #551).
func (p *Pool) Release(_ context.Context, wt Worktree, keep bool, protected ...string) error {
	p.mu.Lock()
	var taskID string
	for tid, allocated := range p.allocated {
		if allocated.ID == wt.ID {
			taskID = tid
			delete(p.allocated, tid)
			break
		}
	}
	p.mu.Unlock()

	effectiveProtected := protected
	if len(effectiveProtected) == 0 && taskID != "" {
		if rcPaths := resolveReceiptPaths(taskID); len(rcPaths) > 0 {
			effectiveProtected = rcPaths
		}
	}

	if keep {
		if dirty, derr := worktreeDirty(wt.Path); derr == nil && dirty {
			// preserve: skip removal, keep branch implicitly (it exists)
			fmt.Fprintf(os.Stderr, "[info] orchestrator: worktree %s preserved (uncommitted deliverables)\n", wt.Path)
			return nil
		}
		if hasProtectedPath(wt.Path, effectiveProtected) {
			// preserve: skip removal, keep branch implicitly (receipt-scoped deliverables)
			fmt.Fprintf(os.Stderr, "[info] orchestrator: worktree %s preserved (receipt-scoped deliverables)\n", wt.Path)
			return nil
		}
	}

	if err := gitRemoveWorktree(p.repo, wt.Path); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	if !keep {
		_ = gitDeleteBranch(p.repo, wt.Branch)
	}
	return nil
}

// hasProtectedPath reports whether any path in protected exists on disk under wtPath (issue #551).
//
// Rule:
//  1. Whitespace is trimmed; empty entries are ignored.
//  2. Leading "./" prefixes are stripped to normalize relative paths.
//  3. Literal paths (containing no glob characters "*?[") are checked via os.Stat.
//     If the file or directory exists on disk, the worktree is preserved.
//  4. Glob patterns (e.g. "./internal/verifier/*", "./.g8s/*") extract the literal directory prefix
//     prior to the first glob character.
//     - If the prefix names a non-root directory (e.g. "internal/verifier"), os.Stat is performed on
//     filepath.Join(wtPath, prefixDir). If that directory exists on disk, the worktree is preserved.
//     - If the prefix is empty or root (e.g. "*", "*.go"), filepath.Glob is evaluated against
//     filepath.Join(wtPath, rel) and the worktree is preserved if one or more matching files exist.
func hasProtectedPath(wtPath string, protected []string) bool {
	for _, p := range protected {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		rel := strings.TrimPrefix(p, "./")
		var target string
		if filepath.IsAbs(rel) {
			target = rel
		} else {
			target = filepath.Join(wtPath, rel)
		}

		if !strings.ContainsAny(rel, "*?[") {
			if _, err := os.Stat(target); err == nil {
				return true
			}
			continue
		}

		// Glob entry: extract literal prefix directory before first glob character
		idx := strings.IndexAny(rel, "*?[")
		literalPart := rel[:idx]
		prefixDir := literalPart
		if !strings.HasSuffix(prefixDir, "/") && !strings.HasSuffix(prefixDir, string(filepath.Separator)) {
			prefixDir = filepath.Dir(prefixDir)
		}
		prefixDir = filepath.Clean(prefixDir)

		if prefixDir != "." && prefixDir != "" {
			targetDir := filepath.Join(wtPath, prefixDir)
			if fi, err := os.Stat(targetDir); err == nil && fi.IsDir() {
				return true
			}
		} else {
			// Empty or root prefix (e.g. "*", "*.go"): evaluate glob directly
			matches, err := filepath.Glob(filepath.Join(wtPath, rel))
			if err == nil && len(matches) > 0 {
				return true
			}
		}
	}
	return false
}

// resolveReceiptPaths reads allowed paths for a receipt ID from the canonical receipt ledger (receipts.db).
func resolveReceiptPaths(receiptID string) []string {
	if receiptID == "" {
		return nil
	}
	dbPath := filepath.Join(pathutil.DefaultStateDir(), "receipts.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	mgr, err := receipt.NewReceiptManager(dbPath, nil)
	if err != nil {
		return nil
	}
	defer mgr.Close()
	rc, err := mgr.VerifyReceipt(receiptID)
	if err != nil {
		return nil
	}
	return rc.AllowedPaths
}

// resolveTaskProtectedPaths returns the declared protected paths for a task,
// falling back to looking up the receipt's AllowedPaths in the canonical receipt
// ledger (receipts.db) when task.AllowedFiles is empty and task.ReceiptID is set.
func resolveTaskProtectedPaths(task Task) []string {
	if len(task.AllowedFiles) > 0 {
		return task.AllowedFiles
	}
	if task.ReceiptID != "" {
		return resolveReceiptPaths(task.ReceiptID)
	}
	return nil
}

// Active returns the snapshot of currently-leased worktrees. For tests
// and observability.
func (p *Pool) Active() []Worktree {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Worktree, 0, len(p.allocated))
	for _, wt := range p.allocated {
		out = append(out, wt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (p *Pool) forceCleanup(path string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", "--", path)
	cmd.Dir = p.repo
	return cmd.Run()
}

// shortID returns 8 hex chars from crypto/rand. Used for worktree IDs.
func shortID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// shortIDFn is swappable in tests for deterministic collision testing.
var shortIDFn = shortID

// isLiveWorktree reports whether path exists and contains a .git entry (file or dir).
func isLiveWorktree(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

// isGitRepo runs `git rev-parse --git-dir` in dir. Returns true if it
// exits 0 and emits a path.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func gitAddWorktree(repo, path, branch, base string) error {
	cmd := exec.Command("git", "worktree", "add", "-b", branch, "--", path, base)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitRemoveWorktree(repo, path string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", "--", path)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitDeleteBranch(repo, branch string) error {
	cmd := exec.Command("git", "branch", "-D", "--", branch)
	cmd.Dir = repo
	return cmd.Run()
}

func gitRevParse(repo, ref string) (string, error) {
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func worktreeDirty(path string) (bool, error) {
	cmd := exec.Command("git", "-C", path, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}
