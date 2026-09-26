package cleanup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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
		item, pruned := reapOneSession(ctx, cfg, db, s)
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
func reapOneSession(ctx context.Context, cfg CleanupConfig, db *sql.DB, s reapableSession) (CleanupItem, bool) {
	var ops []string
	prefix := ""
	if s.zombie {
		prefix = "zombie (heartbeat stale): "
	}
	prune := false
	if s.worktreePath != "" {
		if _, err := os.Stat(s.worktreePath); err == nil {
			ops = append(ops, "worktree")
			if !cfg.DryRun {
				if err := cfg.GitRunner.WorktreeRemove(ctx, cfg.RepoDir, s.worktreePath); err != nil {
					// The session is dead — its worktree is garbage even if
					// git refuses (dirty files, stale registration). Remove
					// the directory and let the prune fix the metadata.
					if rmErr := os.RemoveAll(s.worktreePath); rmErr != nil {
						return CleanupItem{
							Target: TargetOrphanSession, ID: s.id, Action: "skipped",
							Detail: prefix + fmt.Sprintf("worktree remove failed: %v (rm: %v)", err, rmErr),
						}, false
					}
					ops[len(ops)-1] += " (forced: rm + prune)"
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
