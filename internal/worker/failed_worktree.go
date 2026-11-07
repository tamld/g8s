package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Failed attempts stay in place: moving a Git worktree would invalidate its
// registration. The task-local -failed marker survives supervisor restarts and
// names the single retained artifact, without changing success delivery.
const failedWorktreeMarker = "worktree-failed"

func worktreeGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("E_FAILED_WORKTREE_GIT: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func releaseFailedWorktree(taskDir, dir string, release func(bool)) {
	if dir == "" {
		release(false)
		return
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0) {
		release(false)
		return
	}
	status, statusErr := worktreeGit(dir, "status", "--porcelain", "--untracked-files=all")
	if statusErr == nil && status == "" {
		release(false)
		return
	}
	// Inspection errors must preserve evidence too, rather than authorize loss.
	if statusErr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: %v; preserving %s for inspection\n", statusErr, dir)
	}
	if err := os.WriteFile(filepath.Join(taskDir, failedWorktreeMarker), []byte(dir), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: E_FAILED_WORKTREE_MARKER: %v (worktree retained at %s)\n", err, dir)
	}
	fmt.Fprintf(os.Stderr, "[warn] worker: worktree %s preserved (uncommitted deliverables) [failed attempt; marker %s]\n", dir, filepath.Join(taskDir, failedWorktreeMarker))
	if statusErr != nil {
		// The existing isolator rechecks dirtiness even for release(true),
		// and may destroy the directory if Git inspection fails. Retain the
		// allocation too rather than call a potentially destructive callback.
		fmt.Fprintln(os.Stderr, "[warn] worker: E_FAILED_WORKTREE_RELEASE_DEFERRED: Git inspection failed; release callback skipped")
		return
	}
	release(true)
}

// Called only once the same task has actually claimed its next write attempt.
// Git performs removal and branch deletion; never recursively delete a path
// supplied by a marker, and never clean any other task's quarantine.
func cleanupFailedWorktree(taskDir string) error {
	marker := filepath.Join(taskDir, failedWorktreeMarker)
	raw, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("E_FAILED_WORKTREE_MARKER: %w", err)
	}
	dir := string(raw)
	common, err := worktreeGit(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	// Refuse to remove an ordinary checkout, even if a marker is corrupted.
	gitDir, err := worktreeGit(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	if filepath.Clean(common) == filepath.Clean(gitDir) {
		return fmt.Errorf("E_FAILED_WORKTREE_UNSAFE: %s is not a linked worktree", dir)
	}
	branch, err := worktreeGit(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	if _, err := worktreeGit(taskDir, "--git-dir", common, "worktree", "remove", "--force", "--", dir); err != nil {
		return err
	}
	if _, err := worktreeGit(taskDir, "--git-dir", common, "branch", "-D", "--", branch); err != nil {
		return err
	}
	if err := os.Remove(marker); err != nil {
		return fmt.Errorf("E_FAILED_WORKTREE_MARKER: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[info] worker: prior failed worktree %s cleaned for next attempt\n", dir)
	return nil
}
