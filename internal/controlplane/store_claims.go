package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"time"

	"github.com/google/uuid"
	"github.com/tamld/g8s/internal/pathutil"

	"modernc.org/sqlite"
)

// Sentinel errors surfaced by query validation.

func (s *Store) ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*Task, error) {
	return s.ClaimTaskFiltered(ctx, workerID, leaseDurationSeconds, ClaimFilter{})
}

// ClaimTaskProvider leases the highest-priority QUEUED task matching provider to workerID.
// Empty provider = no filter (delegates to existing ClaimTask logic).
func (s *Store) ClaimTaskProvider(ctx context.Context, workerID string, leaseDurationSeconds int, provider string) (*Task, error) {
	return s.ClaimTaskFiltered(ctx, workerID, leaseDurationSeconds, ClaimFilter{Provider: provider})
}

// ClaimTaskFiltered leases the highest-priority QUEUED task matching filter to workerID (issue #657).
// Non-empty Provider and Model restrict claims with exact match; both set uses logical AND.
func (s *Store) ClaimTaskFiltered(ctx context.Context, workerID string, leaseDurationSeconds int, filter ClaimFilter) (*Task, error) {
	return s.claimTaskInternal(ctx, workerID, leaseDurationSeconds, filter)
}

func (s *Store) claimTaskInternal(ctx context.Context, workerID string, leaseDurationSeconds int, filter ClaimFilter) (*Task, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("worker_id is required")
	}
	if leaseDurationSeconds <= 0 {
		return nil, errors.New("lease_seconds must be positive")
	}
	now := float64(s.clock().UnixNano()) / 1e9
	leaseSeconds := float64(leaseDurationSeconds)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer tx.Rollback()

	var reconcileSignals []pendingSignal
	if _, reconcileSignals, err = reconcileExpiredTx(ctx, tx, now); err != nil {
		return nil, err
	}
	commitAndSignal := func() error {
		if err := tx.Commit(); err != nil {
			return err
		}
		for _, sig := range reconcileSignals {
			s.appendTaskSignal(sig.taskID, sig.from, sig.to, s.clock())
		}
		return nil
	}
	var expiresAt float64
	err = tx.QueryRowContext(ctx,
		"SELECT expires_at FROM control_plane_maintenance WHERE singleton = 1").Scan(&expiresAt)
	switch {
	case err == nil && expiresAt > now:
		return nil, commitAndSignal()
	case err == nil:
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM control_plane_maintenance WHERE singleton = 1"); err != nil {
			return nil, fmt.Errorf("clear stale maintenance: %w", err)
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("read maintenance: %w", err)
	}

	var query strings.Builder
	query.WriteString(`SELECT ` + taskColumns + ` FROM tasks
		WHERE state = 'QUEUED' AND cancel_requested = 0 AND attempts < max_attempts`)
	var queryArgs []any

	if filter.Provider != "" {
		query.WriteString(` AND json_extract(request_json, '$.provider') = ?`)
		queryArgs = append(queryArgs, filter.Provider)
	}
	if filter.Model != "" {
		query.WriteString(` AND json_extract(request_json, '$.model') = ?`)
		queryArgs = append(queryArgs, filter.Model)
	}

	query.WriteString(` AND (json_extract(request_json, '$.not_before') IS NULL OR json_extract(request_json, '$.not_before') <= ?)
		ORDER BY priority DESC, created_at ASC
		LIMIT 1`)
	queryArgs = append(queryArgs, now)

	row := tx.QueryRowContext(ctx, query.String(), queryArgs...)
	candidate, scanErr := scanTask(row)
	if scanErr != nil {
		return nil, fmt.Errorf("select claim candidate: %w", scanErr)
	}
	if candidate == nil {
		return nil, commitAndSignal()
	}

	leaseToken := uuid.NewString()
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET state = 'LEASED', lease_owner = ?, lease_token = ?,
		    lease_expires_at = ?, attempts = attempts + 1, updated_at = ?
		WHERE task_id = ? AND state = 'QUEUED' AND cancel_requested = 0`,
		workerID, leaseToken, now+leaseSeconds, now, candidate.TaskID)
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return nil, commitAndSignal()
	}
	if err := insertTaskEvent(tx, candidate.TaskID, "task_claimed", workerID, map[string]any{
		"lease_token":   leaseToken,
		"lease_seconds": leaseSeconds,
	}, now); err != nil {
		return nil, err
	}
	if err := commitAndSignal(); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, candidate.TaskID)
}

// ClaimTaskInSession leases the highest-priority QUEUED task in a session to workerID.
func (s *Store) ClaimTaskInSession(ctx context.Context, workerID, sessionID string, leaseDurationSeconds int) (*Task, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("worker_id is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	if leaseDurationSeconds <= 0 {
		return nil, errors.New("lease_seconds must be positive")
	}
	now := float64(s.clock().UnixNano()) / 1e9
	leaseSeconds := float64(leaseDurationSeconds)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer tx.Rollback()

	var reconcileSignals []pendingSignal
	if _, reconcileSignals, err = reconcileExpiredTx(ctx, tx, now); err != nil {
		return nil, err
	}
	commitAndSignal := func() error {
		if err := tx.Commit(); err != nil {
			return err
		}
		for _, sig := range reconcileSignals {
			s.appendTaskSignal(sig.taskID, sig.from, sig.to, s.clock())
		}
		return nil
	}
	var expiresAt float64
	err = tx.QueryRowContext(ctx,
		"SELECT expires_at FROM control_plane_maintenance WHERE singleton = 1").Scan(&expiresAt)
	switch {
	case err == nil && expiresAt > now:
		return nil, commitAndSignal()
	case err == nil:
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM control_plane_maintenance WHERE singleton = 1"); err != nil {
			return nil, fmt.Errorf("clear stale maintenance: %w", err)
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("read maintenance: %w", err)
	}

	row := tx.QueryRowContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE state = 'QUEUED' AND cancel_requested = 0 AND attempts < max_attempts
		  AND session_id = ?
		  AND (json_extract(request_json, '$.not_before') IS NULL OR json_extract(request_json, '$.not_before') <= ?)
		ORDER BY priority DESC, created_at ASC
		LIMIT 1`, sessionID, now)
	candidate, scanErr := scanTask(row)
	if scanErr != nil {
		return nil, fmt.Errorf("select claim candidate: %w", scanErr)
	}
	if candidate == nil {
		return nil, commitAndSignal()
	}

	leaseToken := uuid.NewString()
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET state = 'LEASED', lease_owner = ?, lease_token = ?,
		    lease_expires_at = ?, attempts = attempts + 1, updated_at = ?
		WHERE task_id = ? AND state = 'QUEUED' AND cancel_requested = 0`,
		workerID, leaseToken, now+leaseSeconds, now, candidate.TaskID)
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return nil, commitAndSignal()
	}
	if err := insertTaskEvent(tx, candidate.TaskID, "task_claimed", workerID, map[string]any{
		"lease_token":   leaseToken,
		"lease_seconds": leaseSeconds,
	}, now); err != nil {
		return nil, err
	}
	if err := commitAndSignal(); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, candidate.TaskID)
}

// StartTask promotes a LEASED task to RUNNING when the caller presents the
// matching worker and lease token; it reports false on any mismatch.
func (s *Store) StartTask(taskID, workerID, leaseToken string) bool {
	now := float64(s.clock().UnixNano()) / 1e9
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		UPDATE tasks SET state = 'RUNNING', updated_at = ?
		WHERE task_id = ? AND state = 'LEASED'
		  AND lease_owner = ? AND lease_token = ? AND cancel_requested = 0`,
		now, taskID, workerID, leaseToken)
	if err != nil {
		return false
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return false
	}
	if err := insertTaskEvent(tx, taskID, "task_started", workerID, map[string]any{}, now); err != nil {
		return false
	}
	return tx.Commit() == nil
}

// RenewHeartbeat extends the lease of a LEASED or RUNNING task held by
// workerID. It implements the spec interface exactly (owner-scoped, no
// token argument); callers holding a lease token should prefer Heartbeat.
// A mismatched owner yields ErrLeaseLost.
func (s *Store) RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error {
	return s.extendLease(ctx, taskID, workerID, "", extensionSeconds)
}

// Heartbeat extends the lease only when the caller presents the exact
// lease token issued at claim time; any other token (including empty)
// yields ErrLeaseLost.
func (s *Store) Heartbeat(ctx context.Context, taskID, workerID, leaseToken string, extensionSeconds int) error {
	if leaseToken == "" {
		return fmt.Errorf("%w: task %s (instance %s)", ErrLeaseLost, taskID, pathutil.InstanceID())
	}
	return s.extendLease(ctx, taskID, workerID, leaseToken, extensionSeconds)
}

// Package-level retry configuration for SQLITE_BUSY handling in controlplane (#465).
var (
	busyRetryAttempts = 3
	busyBackoff       = func(attempt int) time.Duration {
		return time.Duration(50+rand.IntN(151)) * time.Millisecond
	}
)

// isBusyErr reports whether err is a SQLITE_BUSY-class error.
// Controlplane must not import internal/memory (#465 P1), so this helper is
// replicated locally following the #440 idiom.
func isBusyErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrBusy) {
		return true
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code()
		if code == 5 || (code&0xff) == 5 || code == 6 || (code&0xff) == 6 {
			return true
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked") {
		return true
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "database is locked") ||
		strings.Contains(lower, "database table is locked") ||
		strings.Contains(lower, "sqlite_busy")
}

// withBusyRetry runs op, retrying on SQLITE_BUSY-class errors up to busyRetryAttempts.
// Controlplane must not import internal/memory (#465 P1), so this helper is
// replicated locally with jittered backoff following the #440 idiom.
func withBusyRetry(op func() error) error {
	attempts := busyRetryAttempts
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = op()
		if err == nil {
			return nil
		}
		if !isBusyErr(err) {
			return err
		}
		if attempt < attempts-1 {
			if busyBackoff != nil {
				d := busyBackoff(attempt)
				if d > 0 {
					time.Sleep(d)
				}
			}
		}
	}
	if isBusyErr(err) && !errors.Is(err, ErrBusy) {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return err
}

func (s *Store) extendLease(ctx context.Context, taskID, workerID, leaseToken string, extensionSeconds int) error {
	now := float64(s.clock().UnixNano()) / 1e9
	query := `
		UPDATE tasks SET lease_expires_at = ?, updated_at = ?
		WHERE task_id = ? AND state IN ('LEASED', 'RUNNING')
		  AND lease_owner = ? AND cancel_requested = 0
		  AND (lease_expires_at IS NULL OR lease_expires_at > ?)`
	args := []any{now + float64(extensionSeconds), now, taskID, workerID, now}
	if leaseToken != "" {
		query += ` AND lease_token = ?`
		args = append(args, leaseToken)
	}

	var affected int64
	err := withBusyRetry(func() error {
		var res sql.Result
		var execErr error
		if s.leaseExecOverride != nil {
			res, execErr = s.leaseExecOverride(ctx, query, args...)
		} else {
			res, execErr = s.db.ExecContext(ctx, query, args...)
		}
		if execErr != nil {
			return execErr
		}
		aff, affErr := res.RowsAffected()
		if affErr != nil {
			return affErr
		}
		affected = aff
		return nil
	})
	if err != nil {
		return fmt.Errorf("heartbeat (instance %s): %w", pathutil.InstanceID(), err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: task %s (instance %s)", ErrLeaseLost, taskID, pathutil.InstanceID())
	}
	return nil
}

// ExecutionSignal tells a RUNNING worker whether to continue ("active"),
// stop ("cancel_requested"), or treat its lease as gone ("lease_lost").
func (s *Store) ExecutionSignal(taskID, workerID, leaseToken string) string {
	var (
		state           string
		cancelRequested int
		leaseOwner      sql.NullString
		storedToken     sql.NullString
	)
	err := s.db.QueryRow(`
		SELECT state, cancel_requested, lease_owner, lease_token
		FROM tasks WHERE task_id = ?`, taskID,
	).Scan(&state, &cancelRequested, &leaseOwner, &storedToken)
	if err != nil || state != StateRunning ||
		leaseOwner.String != workerID || storedToken.String != leaseToken {
		return "lease_lost"
	}
	if cancelRequested != 0 {
		return "cancel_requested"
	}
	return "active"
}

// pendingSignal captures a committed terminal transition waiting to be appended to tasks.jsonl.
type pendingSignal struct {
	taskID string
	from   string
	to     string
}

// ReconcileExpired requeues expired leases (or finalizes them once their
// retry budget is exhausted) and returns how many tasks were reconciled.
func (s *Store) ReconcileExpired(ctx context.Context) (int, error) {
	now := float64(s.clock().UnixNano()) / 1e9
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin reconcile: %w", err)
	}
	defer tx.Rollback()
	count, signals, err := reconcileExpiredTx(ctx, tx, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, sig := range signals {
		s.appendTaskSignal(sig.taskID, sig.from, sig.to, s.clock())
	}
	return count, nil
}

// reconcileExpiredTx applies lease-expiry transitions inside an open
// transaction: CANCELLED for cancel-requested tasks, FAILED at retry-budget
// exhaustion, otherwise requeue.
func reconcileExpiredTx(ctx context.Context, tx *sql.Tx, now float64) (int, []pendingSignal, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE state IN ('LEASED', 'RUNNING')
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= ?`, now)
	if err != nil {
		return 0, nil, fmt.Errorf("select expired leases: %w", err)
	}
	var expired []*Task
	for rows.Next() {
		t, scanErr := scanTask(rows)
		if scanErr != nil {
			rows.Close()
			return 0, nil, fmt.Errorf("scan expired lease: %w", scanErr)
		}
		expired = append(expired, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, nil, fmt.Errorf("iterate expired leases: %w", err)
	}
	rows.Close()

	var signals []pendingSignal
	reconciled := 0
	for _, t := range expired {
		nextState := StateQueued
		lastError := "lease expired; task requeued"
		switch {
		case t.CancelRequested:
			nextState = StateCancelled
			lastError = "cancelled after lease expiry"
		case t.Attempts >= t.MaxAttempts:
			nextState = StateFailed
			lastError = "lease expired and retry budget exhausted"
		}
		completedAt := any(nil)
		if nextState == StateFailed || nextState == StateCancelled {
			completedAt = now
			signals = append(signals, pendingSignal{
				taskID: t.TaskID,
				from:   t.State,
				to:     nextState,
			})
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET state = ?, lease_owner = NULL, lease_token = NULL,
			    lease_expires_at = NULL, updated_at = ?,
			    completed_at = CASE WHEN ? IN ('FAILED', 'CANCELLED') THEN ? ELSE NULL END,
			    last_error = ?
			WHERE task_id = ? AND lease_token = ?`,
			nextState, now, nextState, completedAt, lastError, t.TaskID, deref(t.LeaseToken)); err != nil {
			return reconciled, signals, fmt.Errorf("reconcile task %s: %w", t.TaskID, err)
		}
		owner := ""
		if t.LeaseOwner != nil {
			owner = *t.LeaseOwner
		}
		if err := insertTaskEvent(tx, t.TaskID, "lease_expired", "reconciler",
			map[string]any{"next_state": nextState, "previous_owner": owner}, now); err != nil {
			return reconciled, signals, err
		}

		if nextState == StateCancelled || nextState == StateFailed {
			requestJSON := string(t.Request)
			if err := redactPayload(&requestJSON); err != nil {
				return reconciled, signals, fmt.Errorf("redact task %s: %w", t.TaskID, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET request_json = ? WHERE task_id = ?`,
				requestJSON, t.TaskID); err != nil {
				return reconciled, signals, fmt.Errorf("store redacted request %s: %w", t.TaskID, err)
			}
			fresh := *t
			fresh.State = nextState
			fresh.CompletedAt = &now
			_, receiptHash, err := buildReceiptTx(tx, &fresh)
			if err != nil {
				return reconciled, signals, fmt.Errorf("receipt for task %s: %w", t.TaskID, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET receipt_hash = ? WHERE task_id = ?`,
				receiptHash, t.TaskID); err != nil {
				return reconciled, signals, fmt.Errorf("seal receipt %s: %w", t.TaskID, err)
			}
		}
		reconciled++
	}
	return reconciled, signals, nil
}

// taskSignalEvent models one record in <db-dir>/signals/tasks.jsonl (#481).
type taskSignalEvent struct {
	TS     string `json:"ts"`
	TaskID string `json:"task_id"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// isTerminalSignalState returns true for the five terminal states monitored by
// the signal file: FAILED, WORKER_COMPLETED, SUCCEEDED, CANCELLED, NEEDS_INFO (#481).
func isTerminalSignalState(to string) bool {
	switch to {
	case StateFailed, StateWorkerCompleted, StateSucceeded, StateCancelled, StateNeedsInfo:
		return true
	default:
		return false
	}
}

const defaultMaxSignalFileBytes int64 = 1 << 20 // 1 MiB (#510)

// maxSignalFileBytes is the size cap for signals/tasks.jsonl before rotating to tasks.jsonl.1.
// Package-level var overridable by tests to test rotation with small byte limits (#510).
var maxSignalFileBytes = defaultMaxSignalFileBytes

// appendTaskSignal appends a single terminal-transition signal JSON line to
// <db-dir>/signals/tasks.jsonl.
//
// Crash window design note:
// The signal line is written AFTER the transition's DB transaction COMMITS
// (a file append cannot join a SQL tx). The crash window (committed transition
// without signal line) is acceptable BY DESIGN: the DB is the truth and the
// supervisor's fallback deadline-check re-derives from state_event_log.
func (s *Store) appendTaskSignal(taskID, from, to string, ts time.Time) {
	if !isTerminalSignalState(to) {
		return
	}
	if ts.IsZero() {
		ts = s.clock()
	}

	sig := taskSignalEvent{
		TS:     ts.UTC().Format(time.RFC3339),
		TaskID: taskID,
		From:   from,
		To:     to,
	}
	data, err := json.Marshal(sig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[warn] controlplane: marshal task signal: %v\n", err)
		return
	}
	line := append(data, '\n')

	s.signalMu.Lock()
	defer s.signalMu.Unlock()

	if s.signalPath == "" && s.dbPath != "" {
		s.signalPath = filepath.Join(filepath.Dir(s.dbPath), "signals", "tasks.jsonl")
	}
	if s.signalPath == "" {
		return
	}

	// Rotate before appending if current signals file exceeds maxSignalFileBytes (#510).
	if maxSignalFileBytes > 0 {
		var size int64
		if s.signalFile != nil {
			if fi, err := s.signalFile.Stat(); err == nil {
				size = fi.Size()
			} else if fi2, err2 := os.Stat(s.signalPath); err2 == nil {
				size = fi2.Size()
			}
		} else if fi, err := os.Stat(s.signalPath); err == nil {
			size = fi.Size()
		}
		if size > maxSignalFileBytes {
			if s.signalFile != nil {
				if closeErr := s.signalFile.Close(); closeErr != nil {
					fmt.Fprintf(os.Stderr, "[warn] controlplane: close signals file: %v\n", closeErr)
				}
				s.signalFile = nil
			}
			rotatedPath := s.signalPath + ".1"
			if err := os.Rename(s.signalPath, rotatedPath); err != nil {
				fmt.Fprintf(os.Stderr, "[warn] controlplane: rotate signals file: %v\n", err)
			}
		}
	}

	if s.signalFile == nil {
		dir := filepath.Dir(s.signalPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] controlplane: mkdir signals dir %s: %v\n", dir, err)
			return
		}
		f, err := os.OpenFile(s.signalPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[warn] controlplane: open signals file %s: %v\n", s.signalPath, err)
			return
		}
		s.signalFile = f
	}

	if _, err := s.signalFile.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] controlplane: write signals file: %v\n", err)
		if closeErr := s.signalFile.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] controlplane: close signals file: %v\n", closeErr)
		}
		s.signalFile = nil
		return
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// SessionQuota represents the quota configuration for a session.
type SessionQuota struct {
	SessionID          string  `json:"session_id"`
	MaxConcurrentTasks int     `json:"max_concurrent_tasks"`
	MaxTasksPerHour    int     `json:"max_tasks_per_hour"`
	CreatedAt          float64 `json:"created_at"`
	UpdatedAt          float64 `json:"updated_at"`
}

// GetSessionQuota returns the quota configuration for a session.
// Returns default quotas if no explicit configuration exists.
func (s *Store) GetSessionQuota(ctx context.Context, sessionID string) (*SessionQuota, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	now := float64(s.clock().UnixNano()) / 1e9
	var sq SessionQuota
	err := s.db.QueryRowContext(ctx,
		`SELECT session_id, max_concurrent_tasks, max_tasks_per_hour, created_at, updated_at
		 FROM session_quotas WHERE session_id = ?`, sessionID,
	).Scan(&sq.SessionID, &sq.MaxConcurrentTasks, &sq.MaxTasksPerHour, &sq.CreatedAt, &sq.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Return defaults
		return &SessionQuota{
			SessionID:          sessionID,
			MaxConcurrentTasks: 10,
			MaxTasksPerHour:    100,
			CreatedAt:          now,
			UpdatedAt:          now,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session quota: %w", err)
	}
	return &sq, nil
}

// SetSessionQuota sets or updates the quota configuration for a session.
func (s *Store) SetSessionQuota(ctx context.Context, quota SessionQuota) error {
	if strings.TrimSpace(quota.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if quota.MaxConcurrentTasks <= 0 {
		quota.MaxConcurrentTasks = 10
	}
	if quota.MaxTasksPerHour <= 0 {
		quota.MaxTasksPerHour = 100
	}
	now := float64(s.clock().UnixNano()) / 1e9
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_quotas (session_id, max_concurrent_tasks, max_tasks_per_hour, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			max_concurrent_tasks = excluded.max_concurrent_tasks,
			max_tasks_per_hour = excluded.max_tasks_per_hour,
			updated_at = excluded.updated_at`,
		quota.SessionID, quota.MaxConcurrentTasks, quota.MaxTasksPerHour, now, now)
	if err != nil {
		return fmt.Errorf("set session quota: %w", err)
	}
	return nil
}

// CheckSessionQuota checks if a session can accept a new task.
// Returns true if within quota, false if quota exceeded.
func (s *Store) CheckSessionQuota(ctx context.Context, sessionID string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return true, nil // No session = no quota enforcement
	}
	quota, err := s.GetSessionQuota(ctx, sessionID)
	if err != nil {
		return false, err
	}
	// Check concurrent tasks
	active, err := s.ActiveTaskCountInSession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if active >= quota.MaxConcurrentTasks {
		return false, nil
	}
	if quota.MaxTasksPerHour > 0 {
		now := float64(s.clock().UnixNano()) / 1e9
		windowStart := now - 3600.0
		var count int
		err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM tasks
			WHERE session_id = ? AND created_at >= ?`, sessionID, windowStart).Scan(&count)
		if err != nil {
			return false, fmt.Errorf("check session hourly quota: %w", err)
		}
		if count >= quota.MaxTasksPerHour {
			return false, nil
		}
	}
	return true, nil
}

// sqlitePathEscapeFor escapes a database file path for a SQLite file: URI on
// the given GOOS (#336). On Windows url.PathEscape percent-encodes drive
// colons (%3A) and separators (%5C) — the modernc driver does not decode
// them back, producing an unusable URI. Windows keeps the path literal and
// escapes only the URI-significant characters, preserving the v0.9.2
// connection-string injection guarantee (?, # and % cannot smuggle query
// parameters). Non-Windows keeps the proven url.PathEscape behavior.
func sqlitePathEscapeFor(goos, path string) string {
	if goos != "windows" {
		return url.PathEscape(path)
	}
	p := strings.ReplaceAll(path, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return p
}

// sqlitePathEscape escapes the database path for the running platform.
func sqlitePathEscape(path string) string {
	return sqlitePathEscapeFor(runtime.GOOS, path)
}
