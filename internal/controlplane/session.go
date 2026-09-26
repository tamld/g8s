package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session lifecycle states for the sessions registry (#393).
const (
	SessionStatusActive = "active"
	SessionStatusDead   = "dead"
)

// Session dispatch modes. A session runs either inside the shared checkout
// (holding the session lock) or inside an isolated pool worktree it was
// auto-promoted to after lock contention.
const (
	SessionModeInPlace  = "in_place"
	SessionModeWorktree = "worktree"
)

// ErrSessionNotActive is returned when a lifecycle call targets a session
// that is unknown or already marked dead.
var ErrSessionNotActive = errors.New("session not active")

// Session is one supervisor/orchestrate session registered in the shared
// control plane. The registry is the truth source for live-vs-zombie
// decisions: a session_id alone cannot distinguish the two — status and
// heartbeat_at do (heartbeat timeout is the cleanup contract, Phase 5).
type Session struct {
	ID           string
	StartedAt    time.Time
	HeartbeatAt  time.Time
	Status       string
	Mode         string
	WorktreePath string
}

const sessionColumns = "id, started_at, heartbeat_at, status, mode, worktree_path"

func scanSession(row *sql.Row) (*Session, error) {
	var sess Session
	var started, heartbeat float64
	if err := row.Scan(&sess.ID, &started, &heartbeat, &sess.Status, &sess.Mode, &sess.WorktreePath); err != nil {
		return nil, err
	}
	sess.StartedAt = time.Unix(int64(started), 0).UTC()
	sess.HeartbeatAt = time.Unix(int64(heartbeat), 0).UTC()
	return &sess, nil
}

// RegisterSession inserts a session row, or refreshes an existing one.
// Re-registering a live session refreshes its timestamps; re-registering a
// dead session revives it (fresh started_at, status active).
func (s *Store) RegisterSession(ctx context.Context, sess Session) error {
	if sess.ID == "" {
		return errors.New("session id is required")
	}
	if sess.Mode == "" {
		sess.Mode = SessionModeInPlace
	}
	if sess.Mode != SessionModeInPlace && sess.Mode != SessionModeWorktree {
		return fmt.Errorf("register session: unknown mode %q", sess.Mode)
	}
	// Registration always (re)enters the registry as active — callers that
	// want a dead row must go through MarkSessionDead explicitly.
	now := s.clock()
	if sess.StartedAt.IsZero() {
		sess.StartedAt = now
	}
	started := float64(sess.StartedAt.Unix())
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, started_at, heartbeat_at, status, mode, worktree_path)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			started_at = excluded.started_at,
			heartbeat_at = excluded.heartbeat_at,
			status = excluded.status,
			mode = excluded.mode,
			worktree_path = excluded.worktree_path`,
		sess.ID, started, float64(now.Unix()), SessionStatusActive, sess.Mode, sess.WorktreePath); err != nil {
		return fmt.Errorf("register session: %w", err)
	}
	return nil
}

// HeartbeatSession refreshes the liveness timestamp of an active session.
// It fails with ErrSessionNotActive when the session is unknown or dead —
// a caller holding a lock that lost its registry row must re-arbitrate.
func (s *Store) HeartbeatSession(ctx context.Context, sessionID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET heartbeat_at = ? WHERE id = ? AND status = ?`,
		float64(s.clock().Unix()), sessionID, SessionStatusActive)
	if err != nil {
		return fmt.Errorf("heartbeat session: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("heartbeat session: %w", err)
	} else if n == 0 {
		return ErrSessionNotActive
	}
	return nil
}

// MarkSessionDead flags a session as no longer running. Dead sessions are
// reap candidates for the cleanup sweep (Phase 5) after a grace period.
func (s *Store) MarkSessionDead(ctx context.Context, sessionID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET status = ? WHERE id = ?`,
		SessionStatusDead, sessionID)
	if err != nil {
		return fmt.Errorf("mark session dead: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("mark session dead: %w", err)
	} else if n == 0 {
		return ErrSessionNotActive
	}
	return nil
}

// GetSession returns one session row, or sql.ErrNoRows when unknown.
func (s *Store) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE id = ?", sessionID)
	return scanSession(row)
}

// ListActiveSessions returns every session whose status is active.
func (s *Store) ListActiveSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE status = ? ORDER BY started_at ASC", SessionStatusActive)
	if err != nil {
		return nil, fmt.Errorf("list active sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var sess Session
		var started, heartbeat float64
		if err := rows.Scan(&sess.ID, &started, &heartbeat, &sess.Status, &sess.Mode, &sess.WorktreePath); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sess.StartedAt = time.Unix(int64(started), 0).UTC()
		sess.HeartbeatAt = time.Unix(int64(heartbeat), 0).UTC()
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return out, nil
}

// AttachWriteReceiptSession stamps session provenance on a worker-side
// write_receipts mirror row. Provenance is the bulk-revoke path: when a
// session turns out compromised, every shared write it made is traceable
// and revocable by session_id. Re-stamping a receipt that already carries
// a session_id is allowed — the last writer owns the attribution — because
// consumption hand-offs (retry under a new session) legitimately move it.
// The mirror table is ensured on first use — worker-only processes never
// created it.
func (s *Store) AttachWriteReceiptSession(ctx context.Context, receiptID, sessionID string) error {
	if sessionID == "" {
		return errors.New("session id is required")
	}
	if _, err := s.db.ExecContext(ctx, writeReceiptsEnsureDDL); err != nil {
		return fmt.Errorf("ensure write_receipts schema: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE write_receipts SET session_id = ? WHERE receipt_id = ?`, sessionID, receiptID)
	if err != nil {
		return fmt.Errorf("attach receipt session: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("attach receipt session: %w", err)
	} else if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
