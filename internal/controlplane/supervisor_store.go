package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateSupervisorTask inserts a new supervisor_tasks row.
func (s *Store) CreateSupervisorTask(ctx context.Context, st SupervisorTaskRow) error {
	if strings.TrimSpace(st.ID) == "" {
		return errors.New("controlplane: supervisor task id is required")
	}
	if strings.TrimSpace(st.State) == "" {
		return errors.New("controlplane: supervisor task state is required")
	}
	createdAt := st.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.clock()
	}
	updatedAt := st.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO supervisor_tasks(
			id, state, envelope_json, approach_idx, attempt_idx,
			parent_task_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		st.ID, st.State, st.EnvelopeJSON, st.ApproachIdx, st.AttemptIdx,
		st.ParentTaskID, floatUnix(createdAt), floatUnix(updatedAt),
	)
	if err != nil {
		return fmt.Errorf("controlplane: create supervisor task: %w", err)
	}
	return nil
}

// GetSupervisorTask returns the row, or ErrUnknownSupervisorTask if no such id.
func (s *Store) GetSupervisorTask(ctx context.Context, id string) (SupervisorTaskRow, error) {
	if strings.TrimSpace(id) == "" {
		return SupervisorTaskRow{}, errors.New("controlplane: supervisor task id is required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, state, envelope_json, approach_idx, attempt_idx,
		        parent_task_id, created_at, updated_at
		 FROM supervisor_tasks WHERE id = ?`, id,
	)
	return scanSupervisorTaskRow(row)
}

// UpdateSupervisorTask overwrites the mutable columns (state, envelope_json,
// approach_idx, attempt_idx, updated_at). updated_at is reset to the clock.
// Returns ErrUnknownSupervisorTask if no such id.
func (s *Store) UpdateSupervisorTask(ctx context.Context, st SupervisorTaskRow) error {
	if strings.TrimSpace(st.ID) == "" {
		return errors.New("controlplane: supervisor task id is required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE supervisor_tasks
		 SET state = ?, envelope_json = ?, approach_idx = ?, attempt_idx = ?,
		     updated_at = ?
		 WHERE id = ?`,
		st.State, st.EnvelopeJSON, st.ApproachIdx, st.AttemptIdx,
		floatUnix(s.clock()), st.ID,
	)
	if err != nil {
		return fmt.Errorf("controlplane: update supervisor task: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return fmt.Errorf("%w: %s", ErrUnknownSupervisorTask, st.ID)
	}
	return nil
}

// ListSupervisorTasks returns every supervisor task ordered chronologically
// by created_at. Limit is unbounded; the caller paginates if needed.
func (s *Store) ListSupervisorTasks(ctx context.Context) ([]SupervisorTaskRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, state, envelope_json, approach_idx, attempt_idx,
		        parent_task_id, created_at, updated_at
		 FROM supervisor_tasks ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("controlplane: list supervisor tasks: %w", err)
	}
	defer rows.Close()

	var out []SupervisorTaskRow
	for rows.Next() {
		row, err := scanSupervisorTaskRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlplane: iterate supervisor tasks: %w", err)
	}
	return out, nil
}

// AppendDecision writes one immutable audit row. ID is auto-generated if
// empty (so callers don't need to mint UUIDs for every decision event).
func (s *Store) AppendDecision(ctx context.Context, dec SupervisorDecisionRow) error {
	if strings.TrimSpace(dec.TaskID) == "" {
		return errors.New("controlplane: decision task_id is required")
	}
	if strings.TrimSpace(dec.Kind) == "" {
		return errors.New("controlplane: decision kind is required")
	}
	id := dec.ID
	if id == "" {
		id = fmt.Sprintf("dec-%d", time.Now().UnixNano())
	}
	createdAt := dec.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.clock()
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO supervisor_decisions(id, task_id, kind, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, dec.TaskID, dec.Kind, dec.PayloadJSON, floatUnix(createdAt),
	); err != nil {
		return fmt.Errorf("controlplane: append supervisor decision: %w", err)
	}
	return nil
}

// SaveMetrics writes (or overwrites) the post-run telemetry row.
func (s *Store) SaveMetrics(ctx context.Context, supervisorTaskID string, m MetricsRow) error {
	if strings.TrimSpace(supervisorTaskID) == "" {
		return errors.New("controlplane: metrics supervisor_task_id is required")
	}
	firstAttempt := 0
	if m.FirstAttemptSuccess {
		firstAttempt = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO supervisor_metrics(
			supervisor_task_id, envelope_score, first_attempt_success,
			attempts_to_success, approaches_to_success, rca_confence_avg,
			cycle_duration_seconds, escalation_count, false_escalation_rate
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		supervisorTaskID, m.EnvelopeScore, firstAttempt,
		m.AttemptsToSuccess, m.ApproachesToSuccess, m.RCAConfidenceAvg,
		m.CycleDurationSeconds, m.EscalationCount, m.FalseEscalationRate,
	); err != nil {
		return fmt.Errorf("controlplane: save supervisor metrics: %w", err)
	}
	return nil
}

// GetMetrics returns the persisted bundle, or ErrUnknownSupervisorTask.
func (s *Store) GetMetrics(ctx context.Context, supervisorTaskID string) (MetricsRow, error) {
	if strings.TrimSpace(supervisorTaskID) == "" {
		return MetricsRow{}, errors.New("controlplane: metrics supervisor_task_id is required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT supervisor_task_id, envelope_score, first_attempt_success,
		        attempts_to_success, approaches_to_success, rca_confidence_avg,
		        cycle_duration_seconds, escalation_count, false_escalation_rate
		 FROM supervisor_metrics WHERE supervisor_task_id = ?`, supervisorTaskID,
	)
	var (
		m            MetricsRow
		firstAttempt int
	)
	err := row.Scan(
		&m.SupervisorTaskID, &m.EnvelopeScore, &firstAttempt,
		&m.AttemptsToSuccess, &m.ApproachesToSuccess, &m.RCAConfidenceAvg,
		&m.CycleDurationSeconds, &m.EscalationCount, &m.FalseEscalationRate,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MetricsRow{}, fmt.Errorf("%w: %s", ErrUnknownSupervisorTask, supervisorTaskID)
	}
	if err != nil {
		return MetricsRow{}, fmt.Errorf("controlplane: get supervisor metrics: %w", err)
	}
	m.FirstAttemptSuccess = firstAttempt != 0
	return m, nil
}

// scanSupervisorTaskRow decodes one supervisor_tasks row using the same
// floatUnix convention as the rest of the package.
func scanSupervisorTaskRow(scanner interface{ Scan(...any) error }) (SupervisorTaskRow, error) {
	var (
		st         SupervisorTaskRow
		parent     sql.NullString
		createdAt  float64
		updatedAt  float64
	)
	err := scanner.Scan(
		&st.ID, &st.State, &st.EnvelopeJSON, &st.ApproachIdx, &st.AttemptIdx,
		&parent, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SupervisorTaskRow{}, fmt.Errorf("%w: not found", ErrUnknownSupervisorTask)
	}
	if err != nil {
		return SupervisorTaskRow{}, fmt.Errorf("controlplane: scan supervisor task: %w", err)
	}
	if parent.Valid {
		v := parent.String
		st.ParentTaskID = &v
	}
	st.CreatedAt = unixFloat(createdAt)
	st.UpdatedAt = unixFloat(updatedAt)
	return st, nil
}

// floatUnix renders a time.Time as fractional unix seconds so the value
// round-trips through REAL columns exactly like the v3 schema's created_at
// / updated_at fields.
func floatUnix(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// unixFloat inverts floatUnix. Times are reconstructed at second resolution
// because the supervisor layer only needs ordering, not sub-second fidelity
// at the row boundary; Clock() callers still get nanosecond precision for
// metrics.
func unixFloat(f float64) time.Time {
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	return time.Unix(sec, nsec)
}

