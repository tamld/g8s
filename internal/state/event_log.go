// Package state implements pure FSM transition validation and append-only event logging
// across all g8s lifecycle domains per DEBT-31.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EventRecord represents one durable entry in the append-only event log.
type EventRecord struct {
	ID        int64     `json:"id"`
	SubjectID string    `json:"subject_id"`
	Subject   Subject   `json:"subject,omitempty"`
	FromState State     `json:"from_state"`
	ToState   State     `json:"to_state"`
	Event     Event     `json:"event"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"ts"`
}

// DBTX is the common subset of *sql.DB, *sql.Tx, and *sql.Conn.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Log writes an event record to the event_log table.
func Log(ctx context.Context, db DBTX, subjectID string, subject Subject, from, to State, event Event, actor, reason string, ts time.Time) error {
	if db == nil {
		return errors.New("state: database handle is nil")
	}
	if subjectID == "" {
		return errors.New("state: subject_id is required")
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	query := `INSERT INTO event_log (subject_id, subject, from_state, to_state, event, actor, reason, ts)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	tsEpoch := float64(ts.UnixNano()) / 1e9
	_, err := db.ExecContext(ctx, query, subjectID, string(subject), string(from), string(to), string(event), actor, reason, tsEpoch)
	if err != nil {
		return fmt.Errorf("state: log event: %w", err)
	}
	return nil
}

// Replay retrieves all events for the given subjectID in ascending chronological order.
func Replay(ctx context.Context, db DBTX, subjectID string) ([]EventRecord, error) {
	if db == nil {
		return nil, errors.New("state: database handle is nil")
	}
	if subjectID == "" {
		return nil, errors.New("state: subject_id is required")
	}

	query := `SELECT id, subject_id, subject, from_state, to_state, event, actor, reason, ts
	          FROM event_log
	          WHERE subject_id = ?
	          ORDER BY id ASC`
	rows, err := db.QueryContext(ctx, query, subjectID)
	if err != nil {
		return nil, fmt.Errorf("state: replay events: %w", err)
	}
	defer rows.Close()

	var records []EventRecord
	for rows.Next() {
		var r EventRecord
		var subj, from, to, evt string
		var tsEpoch float64
		if err := rows.Scan(&r.ID, &r.SubjectID, &subj, &from, &to, &evt, &r.Actor, &r.Reason, &tsEpoch); err != nil {
			return nil, fmt.Errorf("state: scan event record: %w", err)
		}
		r.Subject = Subject(subj)
		r.FromState = State(from)
		r.ToState = State(to)
		r.Event = Event(evt)
		sec := int64(tsEpoch)
		nsec := int64((tsEpoch - float64(sec)) * 1e9)
		r.Timestamp = time.Unix(sec, nsec).UTC()
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate event records: %w", err)
	}

	return records, nil
}

// Show retrieves the latest events for the given subjectID up to limit in descending order (or reversed to chronological).
func Show(ctx context.Context, db DBTX, subjectID string, limit int) ([]EventRecord, error) {
	if db == nil {
		return nil, errors.New("state: database handle is nil")
	}
	if subjectID == "" {
		return nil, errors.New("state: subject_id is required")
	}
	if limit <= 0 {
		limit = 20
	}

	query := `SELECT id, subject_id, subject, from_state, to_state, event, actor, reason, ts
	          FROM event_log
	          WHERE subject_id = ?
	          ORDER BY id DESC
	          LIMIT ?`
	rows, err := db.QueryContext(ctx, query, subjectID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: show events: %w", err)
	}
	defer rows.Close()

	var descRecords []EventRecord
	for rows.Next() {
		var r EventRecord
		var subj, from, to, evt string
		var tsEpoch float64
		if err := rows.Scan(&r.ID, &r.SubjectID, &subj, &from, &to, &evt, &r.Actor, &r.Reason, &tsEpoch); err != nil {
			return nil, fmt.Errorf("state: scan event record: %w", err)
		}
		r.Subject = Subject(subj)
		r.FromState = State(from)
		r.ToState = State(to)
		r.Event = Event(evt)
		sec := int64(tsEpoch)
		nsec := int64((tsEpoch - float64(sec)) * 1e9)
		r.Timestamp = time.Unix(sec, nsec).UTC()
		descRecords = append(descRecords, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate event records: %w", err)
	}

	// Reverse to chronological order
	records := make([]EventRecord, len(descRecords))
	for i, r := range descRecords {
		records[len(descRecords)-1-i] = r
	}

	return records, nil
}
