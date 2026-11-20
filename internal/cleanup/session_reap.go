package cleanup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
)

const (
	sessionBranchPrefix = "sess/"
	// DefaultSessionGrace is the zombie threshold for active sessions. It is
	// deliberately minutes-scale: a laptop that sleeps >grace must not come
	// back to a reaped worktree. The trade-off (zombie resources linger for
	// the grace window) is documented in the openspec delta.
	DefaultSessionGrace = 10 * time.Minute
	// reapEvidencePrefix names the per-session evidence directory created
	// under the evidence root when a dirty worktree must be force-removed
	// (#982): the uncommitted diff, porcelain status, and untracked file
	// contents are captured there before anything is deleted.
	reapEvidencePrefix = "session-reap-"
)

// reapableSession is one dead-or-zombie row from the sessions registry.
type reapableSession struct {
	id           string
	worktreePath string
	zombie       bool // was active with a stale heartbeat
}

// sweepOrphanSessions reaps dead and zombie supervisor sessions from the
// sessions registry (#393, SchemaVersion 11). A session whose heartbeat is
// older than the grace period is a zombie — marked dead, then reaped with
// the rest: worktree removed, sess/* branch deleted (unique unmerged tips
// preserved under keep/session-* tags), registry row deleted. Live sessions
// (fresh heartbeat) are never touched. Dry-run reports would_delete for the
// full reapable set — including zombies — and changes nothing. Follows the
// sweepStaleReceipts idiom: raw SQL over cfg.DBPath via pathutil.SQLiteURI,
// so the cleanup package takes no controlplane dependency.
func sweepOrphanSessions(ctx context.Context, cfg CleanupConfig) ([]CleanupItem, error) {
	if cfg.DBPath == "" {
		return nil, nil
	}
	if _, err := os.Stat(cfg.DBPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	grace := cfg.SessionGracePeriod
	if grace <= 0 {
		grace = DefaultSessionGrace
	}
	dsn := pathutil.SQLiteURI(cfg.DBPath, "_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("orphan-session sweep: %w", err)
	}
	defer db.Close()

	// Pre-v11 databases have no sessions registry — nothing to reap.
	var tableCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&tableCount); err != nil {
		return nil, fmt.Errorf("orphan-session sweep: inspect registry: %w", err)
	}
	if tableCount == 0 {
		return nil, nil
	}

	cutoff := float64(cfg.Clock().Add(-grace).Unix())

	// 1. Zombie detection: a stale heartbeat means the holder is gone.
	// Marking happens only on a real run; dry-run still REPORTS zombies (H2).
	var zombieIDs []string
	zrows, err := db.QueryContext(ctx,
		`SELECT id FROM sessions WHERE status = 'active' AND heartbeat_at < ?`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("orphan-session sweep: find zombies: %w", err)
	}
	for zrows.Next() {
		var id string
		if err := zrows.Scan(&id); err != nil {
			zrows.Close()
			return nil, fmt.Errorf("orphan-session sweep: scan zombie: %w", err)
		}
		zombieIDs = append(zombieIDs, id)
	}
	zrows.Close()
	if err := zrows.Err(); err != nil {
		return nil, fmt.Errorf("orphan-session sweep: iterate zombies: %w", err)
	}
	if len(zombieIDs) > 0 && !cfg.DryRun {
		if _, err := db.ExecContext(ctx,
			`UPDATE sessions SET status = 'dead' WHERE status = 'active' AND heartbeat_at < ?`, cutoff); err != nil {
			return nil, fmt.Errorf("orphan-session sweep: mark zombies: %w", err)
		}
	}
	zombieSet := make(map[string]bool, len(zombieIDs))
	for _, id := range zombieIDs {
		zombieSet[id] = true
	}

	// 2. Reap every dead session — plus zombies in dry-run, where the mark
	// step above was skipped and stale-active rows still read as active.
	reapableSQL := `SELECT id, worktree_path FROM sessions WHERE status = 'dead'`
	var reapableArgs []any
	if cfg.DryRun {
		reapableSQL += ` OR (status = 'active' AND heartbeat_at < ?)`
		reapableArgs = append(reapableArgs, cutoff)
	}
	rows, err := db.QueryContext(ctx, reapableSQL+` ORDER BY id`, reapableArgs...)
	if err != nil {
		return nil, fmt.Errorf("orphan-session sweep: list reapable: %w", err)
	}
	var reapable []reapableSession
	for rows.Next() {
		var s reapableSession
		if err := rows.Scan(&s.id, &s.worktreePath); err != nil {
			rows.Close()
			return nil, fmt.Errorf("orphan-session sweep: scan: %w", err)
		}
		s.zombie = zombieSet[s.id]
		reapable = append(reapable, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orphan-session sweep: iterate: %w", err)
	}

	items := make([]CleanupItem, 0, len(reapable))
	needsPrune := false
	for _, s := range reapable {
		item, pruned := reapOneSession(ctx, cfg, db, s, cutoff)
		items = append(items, item)
		needsPrune = needsPrune || pruned
	}
	if needsPrune && !cfg.DryRun {
		if _, err := cfg.GitRunner.WorktreePrune(ctx, cfg.RepoDir); err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "[warn] orphan-session worktree prune: %v\n", err)
		}
	}
	return items, nil
}

// reapOneSession removes one reapable session's resources and registry row.
// The returned bool reports whether git metadata was disturbed and a
// WorktreePrune is due. Failed steps yield a "skipped" item; the next sweep
// retries — one bad session never blocks the rest of the reap.
//
// Two guards run before anything is deleted (#982): first, the session row is
// re-read inside one immediate transaction and the reap proceeds only if it
// still reads status='dead' with a heartbeat older than the zombie cutoff —
// a session that came back between the listing and the reap keeps all its
// resources. Second, when git refuses the worktree removal (a dirty
// worktree), the uncommitted state is captured to the evidence dir BEFORE the
// force-removal; if the capture fails the worktree is left in place for the
// next sweep. Uncommitted work is never destroyed uncaptured.
func reapOneSession(ctx context.Context, cfg CleanupConfig, db *sql.DB, s reapableSession, cutoff float64) (CleanupItem, bool) {
	var ops []string
	prefix := ""
	if s.zombie {
		prefix = "zombie (heartbeat stale): "
	}
	prune := false
	if !cfg.DryRun {
		// Transactional liveness re-verification: the row was listed a moment
		// ago — before ANY removal, re-read it and reap only a row that still
		// reads status='dead' with a stale heartbeat. Dry-run mutates nothing
		// (and reports stale-active zombies, which must not be aborted here).
		reason, ok, err := verifySessionStillReapable(ctx, db, s.id, cutoff)
		if err != nil {
			return CleanupItem{
				Target: TargetOrphanSession, ID: s.id, Action: "skipped",
				Detail: prefix + fmt.Sprintf("liveness re-verify failed: %v", err),
			}, false
		}
		if !ok {
			return CleanupItem{
				Target: TargetOrphanSession, ID: s.id, Action: "skipped",
				Detail: prefix + reason,
			}, false
		}
	}
	if s.worktreePath != "" {
		if _, err := os.Stat(s.worktreePath); err == nil {
			ops = append(ops, "worktree")
			if !cfg.DryRun {
				if err := cfg.GitRunner.WorktreeRemove(ctx, cfg.RepoDir, s.worktreePath); err != nil {
					// git refuses dirty worktrees (WorktreeRemove passes no
					// --force). The session is dead, but dead must never mean
					// "destroy uncommitted work" (#982): capture the
					// uncommitted state to the evidence dir first, then force-
					// remove. If the capture fails, leave the worktree for the
					// next sweep instead of removing blind.
					evDir, evErr := captureSessionWorktreeEvidence(ctx, cfg, s.id, s.worktreePath)
					if evErr != nil {
						return CleanupItem{
							Target: TargetOrphanSession, ID: s.id, Action: "skipped",
							Detail: prefix + fmt.Sprintf("worktree remove failed: %v; uncommitted state could not be captured (%v) — worktree left in place", err, evErr),
						}, false
					}
					if rmErr := os.RemoveAll(s.worktreePath); rmErr != nil {
						return CleanupItem{
							Target: TargetOrphanSession, ID: s.id, Action: "skipped",
							Detail: prefix + fmt.Sprintf("worktree remove failed: %v (rm: %v)", err, rmErr),
						}, false
					}
					// The directory is gone but git still registers the
					// worktree — prune now so the sess/* branch delete below
					// can proceed within this same sweep.
					if _, pErr := cfg.GitRunner.WorktreePrune(ctx, cfg.RepoDir); pErr != nil && cfg.Writer != nil {
						_, _ = fmt.Fprintf(cfg.Writer, "[warn] orphan-session worktree prune: %v\n", pErr)
					}
					ops[len(ops)-1] += " (forced: uncommitted state captured to " + evDir + ", rm + prune)"
				}
			}
			prune = !cfg.DryRun
		} else {
			// Directory already gone but git may still register the
			// worktree — prune so the branch delete below can proceed (M2).
			prune = !cfg.DryRun
		}
	}
	branch := sessionBranchPrefix + s.id
	if branches, err := cfg.GitRunner.LocalBranches(ctx, cfg.RepoDir); err == nil {
		for _, b := range branches {
			if b != branch {
				continue
			}
			ops = append(ops, "branch "+branch)
			if !cfg.DryRun {
				// A crashed promoted session may hold unique commits on its
				// branch — preserve the unmerged tip before the force
				// delete, same verify-tag-delete protocol as the scratch
				// sweep (#327).
				if tip, _, tipErr := cfg.GitRunner.BranchTipInfo(ctx, cfg.RepoDir, branch); tipErr == nil {
					refs, refsErr := cfg.GitRunner.BranchesContaining(ctx, cfg.RepoDir, tip)
					if refsErr != nil || len(refs) <= 1 {
						tag := "keep/session-" + s.id
						if tagErr := cfg.GitRunner.CreateTag(ctx, cfg.RepoDir, tag, tip); tagErr == nil {
							ops[len(ops)-1] += " (tip preserved as " + tag + ")"
						}
					}
				}
				if err := cfg.GitRunner.DeleteBranch(ctx, cfg.RepoDir, branch, true); err != nil {
					return CleanupItem{
						Target: TargetOrphanSession, ID: s.id, Action: "skipped",
						Detail: prefix + fmt.Sprintf("branch delete failed: %v", err),
					}, prune
				}
			}
			break
		}
	} else {
		// Cannot enumerate branches: abort before the registry row goes
		// away, otherwise the sess/* branch would be orphaned forever (M1).
		return CleanupItem{
			Target: TargetOrphanSession, ID: s.id, Action: "skipped",
			Detail: prefix + fmt.Sprintf("branch list failed: %v", err),
		}, prune
	}
	if !cfg.DryRun {
		// status='dead' guard: a session revived between listing and delete
		// must keep its row (M4).
		if _, err := db.ExecContext(ctx,
			`DELETE FROM sessions WHERE id = ? AND status = 'dead'`, s.id); err != nil {
			return CleanupItem{
				Target: TargetOrphanSession, ID: s.id, Action: "skipped",
				Detail: prefix + fmt.Sprintf("registry delete failed: %v", err),
			}, prune
		}
	}
	action := "deleted"
	if cfg.DryRun {
		action = "would_delete"
	}
	detail := prefix + "registry row"
	if len(ops) > 0 {
		detail = prefix + strings.Join(ops, ", ") + " + registry row"
	}
	return CleanupItem{Target: TargetOrphanSession, ID: s.id, Action: action, Detail: detail}, prune
}

// verifySessionStillReapable re-reads one session row inside a single
// immediate transaction (the sweep DSN pins _txlock=immediate) and reports
// whether the reap may proceed (#982). Proceed only when the row still reads
// status='dead' with a heartbeat older than the zombie cutoff. Any change —
// a revived status, a refreshed heartbeat, or a vanished row — aborts the
// reap with a reason before a single file is touched. A non-nil error means
// the verification itself failed (never reap on an unverifiable row).
func verifySessionStillReapable(ctx context.Context, db *sql.DB, id string, cutoff float64) (reason string, ok bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var status string
	var heartbeat float64
	switch qerr := tx.QueryRowContext(ctx,
		`SELECT status, heartbeat_at FROM sessions WHERE id = ?`, id).Scan(&status, &heartbeat); {
	case errors.Is(qerr, sql.ErrNoRows):
		return "session row vanished during reap — aborting", false, nil
	case qerr != nil:
		return "", false, qerr
	}
	if status != "dead" {
		return fmt.Sprintf("session came back (status=%q) during reap — aborting", status), false, nil
	}
	if heartbeat >= cutoff {
		return "session row changed (heartbeat not older than reap cutoff) — aborting", false, nil
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return "", true, nil
}

// captureSessionWorktreeEvidence preserves a session worktree's uncommitted
// state (#982) before a dirty-worktree force-removal: the tracked diff
// (`git diff HEAD`, unborn-HEAD fallback `git diff`), the porcelain status,
// and the full contents of every untracked non-ignored file are written to
// <evidence root>/session-reap-<id>/. The evidence root resolves like the
// evidence retention sweep: explicit config, then G8S_EVIDENCE_DIR, then the
// platform default. Any capture failure is fatal to the removal — the caller
// leaves the worktree in place rather than destroying uncaptured work.
func captureSessionWorktreeEvidence(ctx context.Context, cfg CleanupConfig, sessionID, wtPath string) (string, error) {
	ev, ok := cfg.GitRunner.(SessionWorktreeEvidence)
	if !ok {
		return "", fmt.Errorf("git runner does not implement SessionWorktreeEvidence")
	}
	root := filepath.Join(reapEvidenceDir(cfg), reapEvidencePrefix+sanitizeSessionIDForPath(sessionID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create evidence dir: %w", err)
	}
	diff, headErr := ev.WorktreeDiffHead(ctx, wtPath)
	if headErr != nil {
		// Unborn HEAD (worktree without commits): `git diff HEAD` cannot
		// resolve, but the index-vs-worktree diff still works and everything
		// else is untracked and captured below.
		plainDiff, plainErr := ev.WorktreeDiff(ctx, wtPath)
		if plainErr != nil {
			// Tracked changes are uncapturable — refuse the removal.
			return "", fmt.Errorf("capture diff: git diff HEAD: %v; git diff: %v", headErr, plainErr)
		}
		diff = plainDiff
	}
	if err := os.WriteFile(filepath.Join(root, "worktree.diff"), []byte(diff), 0o600); err != nil {
		return "", fmt.Errorf("write worktree.diff: %w", err)
	}
	status, err := ev.WorktreeStatusPorcelain(ctx, wtPath)
	if err != nil {
		return "", fmt.Errorf("capture status: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "status.txt"), []byte(status), 0o600); err != nil {
		return "", fmt.Errorf("write status.txt: %w", err)
	}
	untracked, err := ev.WorktreeUntrackedFiles(ctx, wtPath)
	if err != nil {
		return "", fmt.Errorf("list untracked files: %w", err)
	}
	copied, skipped := 0, 0
	for _, rel := range untracked {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			skipped++ // never read outside the worktree boundary
			continue
		}
		dst := filepath.Join(root, "untracked", clean)
		skippedCopy, cErr := copyFileRegular(filepath.Join(wtPath, clean), dst)
		if cErr != nil {
			return "", fmt.Errorf("copy untracked %s: %w", rel, cErr)
		}
		if skippedCopy {
			skipped++
			continue
		}
		copied++
	}
	// Non-regular files (symlinks, sockets) are deliberately not copied —
	// their paths are already listed in status.txt. A symlink open could
	// point outside the worktree or block on a FIFO; contents stay in git.
	note := fmt.Sprintf("session %s\nworktree: %s (dirty — force-removed after capture, #982)\n"+
		"artifacts: worktree.diff (tracked changes), status.txt (porcelain), untracked/ (file contents)\n"+
		"untracked files copied: %d, skipped (non-regular or outside worktree): %d\n"+
		"recovery: tracked changes via `git apply worktree.diff`; untracked files are verbatim copies\n",
		sessionID, wtPath, copied, skipped)
	if err := os.WriteFile(filepath.Join(root, "README.txt"), []byte(note), 0o600); err != nil {
		return "", fmt.Errorf("write README.txt: %w", err)
	}
	return root, nil
}

// reapEvidenceDir resolves the evidence root for reap artifacts the same way
// the evidence retention sweep (#476) does: explicit config first, then the
// G8S_EVIDENCE_DIR environment override, then the platform default.
func reapEvidenceDir(cfg CleanupConfig) string {
	if cfg.EvidenceDir != "" {
		return filepath.Clean(cfg.EvidenceDir)
	}
	if env := os.Getenv("G8S_EVIDENCE_DIR"); env != "" {
		return filepath.Clean(env)
	}
	return pathutil.DefaultEvidenceDir()
}

// sanitizeSessionIDForPath makes a session ID safe as one path component:
// anything outside [A-Za-z0-9._-] becomes '_', and an empty result falls
// back to "unknown".
func sanitizeSessionIDForPath(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// copyFileRegular copies one regular file to dst (creating parent dirs).
// It reports skipped=true for non-regular entries (symlinks, FIFOs, sockets)
// without following them — evidence capture must never block on or traverse
// out-of-tree symlinks.
func copyFileRegular(src, dst string) (skipped bool, err error) {
	fi, err := os.Lstat(src)
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return true, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		if cErr := out.Close(); cErr != nil {
			return false, fmt.Errorf("%w (close: %v)", err, cErr)
		}
		return false, err
	}
	return false, out.Close()
}
