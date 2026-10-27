package controlplane

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/pathutil"
)

var auditBearerTokenPattern = regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._\-]{8,}|\b(sk-[A-Za-z0-9._\-]{12,}|crsr_[A-Za-z0-9._\-]{12,})\b`)

func sanitizeAuditDetail(raw string) string {
	out := dispatch.SanitizeOutput(raw)
	return auditBearerTokenPattern.ReplaceAllString(out, "${1}<REDACTED>")
}

// AccessAuditRecord represents one immutable row in the enterprise access_audit table (#441).
type AccessAuditRecord struct {
	ID        int64  `json:"id"`
	Timestamp string `json:"ts"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	SubjectID string `json:"subject_id"`
	TraceID   string `json:"trace_id"`
	Instance  string `json:"instance"`
	Detail    string `json:"detail"`
}

// AccessAuditFilter defines optional query predicates for ListAccessAudit.
type AccessAuditFilter struct {
	Actor     string
	Action    string
	SubjectID string
	Since     string
	Limit     int
}

// applyAccessAuditSchema creates the access_audit table idempotently in g8s.db (#441).
func applyAccessAuditSchema(conn *sql.Conn) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS access_audit (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			ts         TEXT NOT NULL,
			actor      TEXT NOT NULL,
			action     TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			trace_id   TEXT NOT NULL DEFAULT '',
			instance   TEXT NOT NULL DEFAULT '',
			detail     TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_access_audit_ts ON access_audit(ts DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_access_audit_actor ON access_audit(actor, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_access_audit_action ON access_audit(action, id DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("apply access_audit schema: %w", err)
		}
	}
	return nil
}

// mapTaskEventToAuditAction normalizes internal task_events event_type values
// into the canonical dot-separated action taxonomy defined in docs/designs/access-audit-design.md.
func mapTaskEventToAuditAction(eventType string) string {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "TASK_SUBMITTED", "SUBMITTED":
		return "task.submit"
	case "TASK_CLAIMED", "CLAIMED":
		return "task.claim"
	case "TASK_STARTED", "STARTED":
		return "task.start"
	case "TASK_SUCCEEDED", "TASK_FAILED", "ATTEMPT_FINISHED", "FINISHED", "COMPLETED":
		return "task.finish"
	case "TASK_CANCELLED", "CANCEL_REQUESTED", "CANCELLED":
		return "task.cancel"
	case "TASK_PAUSED", "PAUSED":
		return "task.pause"
	case "TASK_RESUMED", "RESUMED":
		return "task.resume"
	default:
		lower := strings.ToLower(strings.TrimSpace(eventType))
		if lower == "" {
			return "task.event"
		}
		return "task." + strings.ReplaceAll(lower, "_", ".")
	}
}

func insertAccessAuditTx(tx *sql.Tx, ts float64, actor, action, subjectID, rawDetail string) error {
	sec := int64(ts)
	nsec := int64((ts - float64(sec)) * 1e9)
	rfcTS := time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
	cleanActor := strings.TrimSpace(actor)
	if cleanActor == "" {
		cleanActor = "system"
	}
	cleanAction := strings.TrimSpace(action)
	if cleanAction == "" {
		cleanAction = "controlplane.mutation"
	}
	sanitizedDetail := sanitizeAuditDetail(rawDetail)
	instanceID := pathutil.InstanceID()

	_, err := tx.ExecContext(context.Background(),
		`INSERT INTO access_audit (ts, actor, action, subject_id, trace_id, instance, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rfcTS, cleanActor, cleanAction, subjectID, "", instanceID, sanitizedDetail,
	)
	if err != nil {
		return fmt.Errorf("insert access_audit: %w", err)
	}
	return nil
}

// RecordAccessAudit appends a sanitized access-audit entry to g8s.db (#441).
func (s *Store) RecordAccessAudit(ctx context.Context, rec AccessAuditRecord) error {
	ts := strings.TrimSpace(rec.Timestamp)
	if ts == "" {
		ts = s.clock().UTC().Format(time.RFC3339Nano)
	}
	actor := strings.TrimSpace(rec.Actor)
	if actor == "" {
		actor = "operator"
	}
	action := strings.TrimSpace(rec.Action)
	if action == "" {
		action = "controlplane.mutation"
	}
	instance := strings.TrimSpace(rec.Instance)
	if instance == "" {
		instance = pathutil.InstanceID()
	}
	sanitizedDetail := sanitizeAuditDetail(rec.Detail)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO access_audit (ts, actor, action, subject_id, trace_id, instance, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ts, actor, action, strings.TrimSpace(rec.SubjectID), strings.TrimSpace(rec.TraceID), instance, sanitizedDetail,
	)
	if err != nil {
		return fmt.Errorf("record access audit: %w", err)
	}
	return nil
}

// ListAccessAudit queries the access_audit table with optional actor, action, subject, and since filters.
func (s *Store) ListAccessAudit(ctx context.Context, filter AccessAuditFilter) ([]AccessAuditRecord, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var (
		clauses []string
		args    []any
	)
	if actor := strings.TrimSpace(filter.Actor); actor != "" {
		clauses = append(clauses, "actor = ?")
		args = append(args, actor)
	}
	if action := strings.TrimSpace(filter.Action); action != "" {
		clauses = append(clauses, "action = ?")
		args = append(args, action)
	}
	if subject := strings.TrimSpace(filter.SubjectID); subject != "" {
		clauses = append(clauses, "subject_id = ?")
		args = append(args, subject)
	}
	if since := strings.TrimSpace(filter.Since); since != "" {
		clauses = append(clauses, "ts >= ?")
		args = append(args, since)
	}

	query := `SELECT id, ts, actor, action, subject_id, trace_id, instance, detail FROM access_audit`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query access_audit: %w", err)
	}
	defer rows.Close()

	var out []AccessAuditRecord
	for rows.Next() {
		var r AccessAuditRecord
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.Actor, &r.Action, &r.SubjectID, &r.TraceID, &r.Instance, &r.Detail); err != nil {
			return nil, fmt.Errorf("scan access_audit: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access_audit: %w", err)
	}
	return out, nil
}
