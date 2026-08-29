package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// CreateBrief inserts a new briefs row.
func (s *Store) CreateBrief(ctx context.Context, b BriefRow) error {
	if strings.TrimSpace(b.ID) == "" {
		return errors.New("controlplane: brief id is required")
	}
	if strings.TrimSpace(b.Title) == "" {
		return errors.New("controlplane: brief title is required")
	}
	if strings.TrimSpace(b.PayloadMD) == "" {
		return errors.New("controlplane: brief payload_md is required")
	}
	if strings.TrimSpace(b.DodMD) == "" {
		return errors.New("controlplane: brief dod_md is required")
	}
	if strings.TrimSpace(b.IssuedBy) == "" {
		return errors.New("controlplane: brief issued_by is required")
	}
	issuedAt := b.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = s.clock()
	}
	if b.ExpiresAt.IsZero() {
		return errors.New("controlplane: brief expires_at is required")
	}
	status := strings.TrimSpace(b.Status)
	if status == "" {
		status = "active"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO briefs(
			id, title, payload_md, dod_md, issued_by, issued_at, expires_at, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Title, b.PayloadMD, b.DodMD, b.IssuedBy,
		floatUnix(issuedAt), floatUnix(b.ExpiresAt), status,
	)
	if err != nil {
		return fmt.Errorf("controlplane: create brief: %w", err)
	}
	return nil
}

// GetBrief returns the row, or ErrUnknownBrief if no such id.
func (s *Store) GetBrief(ctx context.Context, id string) (BriefRow, error) {
	if strings.TrimSpace(id) == "" {
		return BriefRow{}, errors.New("controlplane: brief id is required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, title, payload_md, dod_md, issued_by, issued_at, expires_at, status
		 FROM briefs WHERE id = ?`, id,
	)
	return scanBriefRow(row)
}

// ListActiveBriefs returns all briefs with status = 'active' ordered chronologically by issued_at ASC.
func (s *Store) ListActiveBriefs(ctx context.Context) ([]BriefRow, error) {
	return s.ListBriefs(ctx, "active")
}

// ListBriefs returns all briefs matching status ('active', 'consumed', 'expired', or empty/all for all)
// ordered chronologically by issued_at ASC.
func (s *Store) ListBriefs(ctx context.Context, status string) ([]BriefRow, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	var (
		query string
		args  []any
	)
	if status == "" || status == "all" {
		query = `SELECT id, title, payload_md, dod_md, issued_by, issued_at, expires_at, status
		 FROM briefs ORDER BY issued_at ASC`
	} else {
		query = `SELECT id, title, payload_md, dod_md, issued_by, issued_at, expires_at, status
		 FROM briefs WHERE status = ? ORDER BY issued_at ASC`
		args = append(args, status)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("controlplane: list briefs: %w", err)
	}
	defer rows.Close()

	var out []BriefRow
	for rows.Next() {
		row, err := scanBriefRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlplane: iterate briefs: %w", err)
	}
	return out, nil
}

// UpdateBriefStatus overwrites the status column for a brief.
// Returns ErrUnknownBrief if no such id exists.
func (s *Store) UpdateBriefStatus(ctx context.Context, id string, status string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("controlplane: brief id is required")
	}
	if strings.TrimSpace(status) == "" {
		return errors.New("controlplane: brief status is required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE briefs SET status = ? WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("controlplane: update brief status: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return fmt.Errorf("%w: %s", ErrUnknownBrief, id)
	}
	return nil
}

func scanBriefRow(scanner interface{ Scan(...any) error }) (BriefRow, error) {
	var (
		b         BriefRow
		issuedAt  float64
		expiresAt float64
	)
	err := scanner.Scan(
		&b.ID, &b.Title, &b.PayloadMD, &b.DodMD, &b.IssuedBy,
		&issuedAt, &expiresAt, &b.Status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BriefRow{}, fmt.Errorf("%w: not found", ErrUnknownBrief)
	}
	if err != nil {
		return BriefRow{}, fmt.Errorf("controlplane: scan brief: %w", err)
	}
	b.IssuedAt = unixFloat(issuedAt)
	b.ExpiresAt = unixFloat(expiresAt)
	return b, nil
}
