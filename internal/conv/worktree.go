package conv

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BlindWorktree represents an isolated workspace created for a blind worker.
type BlindWorktree struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	IsGit    bool   `json:"is_git"`
	RepoRoot string `json:"repo_root,omitempty"`
}

// CreateBlindWorktree provisions an isolated worktree under baseDir.
// If repo is a valid git repository, it creates a git worktree on branch `<prefix>/<id>`.
// Otherwise, it creates an isolated directory.
func CreateBlindWorktree(repo, baseDir, prefix, id string) (*BlindWorktree, error) {
	if id == "" {
		id = randomHex(4)
	}
	if prefix == "" {
		prefix = "blind"
	}
	if baseDir == "" {
		baseDir = filepath.Join(os.TempDir(), fmt.Sprintf("g8s-blind-%s", randomHex(6)))
	}

	wtPath := filepath.Join(baseDir, fmt.Sprintf("wt-%s", id))
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("conv: create base dir: %w", err)
	}

	bw := &BlindWorktree{
		ID:   id,
		Path: wtPath,
	}

	if repo != "" && isGitDir(repo) {
		branch := fmt.Sprintf("%s/%s", prefix, id)
		cmd := exec.Command("git", "worktree", "add", "-b", branch, wtPath, "HEAD")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			// If worktree add fails (e.g. detached HEAD or existing dir), fallback to directory creation
			if err := os.MkdirAll(wtPath, 0o755); err != nil {
				return nil, fmt.Errorf("conv: worktree add failed (%s): %w", strings.TrimSpace(string(out)), err)
			}
		} else {
			bw.IsGit = true
			bw.Branch = branch
			bw.RepoRoot = repo
		}
	} else {
		if err := os.MkdirAll(wtPath, 0o755); err != nil {
			return nil, fmt.Errorf("conv: mkdir blind worktree: %w", err)
		}
	}

	return bw, nil
}

// Cleanup removes the worktree and its git branch (if created).
func (bw *BlindWorktree) Cleanup() error {
	if bw == nil || bw.Path == "" {
		return nil
	}

	if bw.IsGit && bw.RepoRoot != "" {
		cmd := exec.Command("git", "worktree", "remove", "--force", bw.Path)
		cmd.Dir = bw.RepoRoot
		_ = cmd.Run()

		if bw.Branch != "" {
			delCmd := exec.Command("git", "branch", "-D", bw.Branch)
			delCmd.Dir = bw.RepoRoot
			_ = delCmd.Run()
		}
	}

	_ = os.RemoveAll(bw.Path)
	return nil
}

func isGitDir(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
