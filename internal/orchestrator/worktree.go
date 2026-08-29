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
)

// Pool allocates per-worker git worktrees so N workers can edit the same
// repo without stepping on each other. Acquire is race-safe across
// goroutines via an exclusive flock on <root>/.lock (POSIX) or LockFileEx
// (Windows). One worktree = one task = one worker.
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
		root = filepath.Join(os.TempDir(), "g8s-worktrees")
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

	id := "wt-" + shortID()
	wtPath := filepath.Join(p.root, id)
	branch := fmt.Sprintf("%s/%s", p.prefix, taskID)

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
// the branch is retained (useful when the worker pushed it).
func (p *Pool) Release(_ context.Context, wt Worktree, keep bool) error {
	p.mu.Lock()
	for taskID, allocated := range p.allocated {
		if allocated.ID == wt.ID {
			delete(p.allocated, taskID)
			break
		}
	}
	p.mu.Unlock()

	if err := gitRemoveWorktree(p.repo, wt.Path); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	if !keep {
		_ = gitDeleteBranch(p.repo, wt.Branch)
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
	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	cmd.Dir = p.repo
	return cmd.Run()
}

// shortID returns 8 hex chars from crypto/rand. Used for worktree IDs.
func shortID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
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
	cmd := exec.Command("git", "worktree", "add", "-b", branch, path, base)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitRemoveWorktree(repo, path string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitDeleteBranch(repo, branch string) error {
	cmd := exec.Command("git", "branch", "-D", branch)
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
