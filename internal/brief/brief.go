// Package brief implements structured brief dispatch and consumption contracts
// backed by the control plane SQLite store.
package brief

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tamld/g8s/internal/controlplane"
)

// Status constants for Brief.
const (
	StatusActive   = "active"
	StatusConsumed = "consumed"
	StatusExpired  = "expired"
)

// Sentinel errors.
var (
	ErrNotFound        = errors.New("brief not found")
	ErrAlreadyConsumed = errors.New("brief already consumed")
	ErrExpired         = errors.New("brief expired")
	ErrInvalidInput    = errors.New("invalid brief input")
)

// Brief is the structured dispatch contract for a task specification.
type Brief struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	PayloadMD string    `json:"payload_md"`
	DodMD     string    `json:"dod_md"`
	IssuedBy  string    `json:"issued_by"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    string    `json:"status"` // active, consumed, expired
}

// Issue creates and persists a new brief with status "active" and the given TTL.
func Issue(store *controlplane.Store, title, payload, dod, issuedBy string, ttl time.Duration) (Brief, error) {
	if store == nil {
		return Brief{}, errors.New("brief: store is required")
	}
	if strings.TrimSpace(title) == "" {
		return Brief{}, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}
	if strings.TrimSpace(payload) == "" {
		return Brief{}, fmt.Errorf("%w: payload is required", ErrInvalidInput)
	}
	if strings.TrimSpace(dod) == "" {
		return Brief{}, fmt.Errorf("%w: dod is required", ErrInvalidInput)
	}
	if strings.TrimSpace(issuedBy) == "" {
		return Brief{}, fmt.Errorf("%w: issued_by is required", ErrInvalidInput)
	}
	if ttl <= 0 {
		return Brief{}, fmt.Errorf("%w: ttl must be positive", ErrInvalidInput)
	}

	now := store.Clock()
	expiresAt := now.Add(ttl)
	id := fmt.Sprintf("brief-%s", uuid.NewString())

	row := controlplane.BriefRow{
		ID:        id,
		Title:     title,
		PayloadMD: payload,
		DodMD:     dod,
		IssuedBy:  issuedBy,
		IssuedAt:  now,
		ExpiresAt: expiresAt,
		Status:    StatusActive,
	}

	if err := store.CreateBrief(context.Background(), row); err != nil {
		return Brief{}, fmt.Errorf("issue brief: %w", err)
	}

	return fromRow(row), nil
}

// Consume marks an active, unexpired brief as "consumed" and returns the updated brief.
// If the brief does not exist, is already consumed, or has expired, an error is returned.
func Consume(store *controlplane.Store, id string) (Brief, error) {
	if store == nil {
		return Brief{}, errors.New("brief: store is required")
	}
	if strings.TrimSpace(id) == "" {
		return Brief{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	ctx := context.Background()
	row, err := store.GetBrief(ctx, id)
	if err != nil {
		if errors.Is(err, controlplane.ErrUnknownBrief) {
			return Brief{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return Brief{}, fmt.Errorf("consume brief: %w", err)
	}

	if row.Status == StatusConsumed {
		return Brief{}, fmt.Errorf("%w: %s", ErrAlreadyConsumed, id)
	}

	now := store.Clock()
	if row.Status == StatusExpired || now.After(row.ExpiresAt) {
		_ = store.UpdateBriefStatus(ctx, id, StatusExpired)
		return Brief{}, fmt.Errorf("%w: %s", ErrExpired, id)
	}

	if err := store.UpdateBriefStatus(ctx, id, StatusConsumed); err != nil {
		return Brief{}, fmt.Errorf("consume brief: %w", err)
	}

	row.Status = StatusConsumed
	return fromRow(row), nil
}

// ListActive returns all unconsumed, unexpired briefs ordered chronologically by issued_at ASC.
func ListActive(store *controlplane.Store) ([]Brief, error) {
	return List(store, StatusActive)
}

// List returns briefs filtered by status ('active', 'consumed', 'expired', or 'all'/empty for all)
// ordered chronologically by issued_at ASC.
func List(store *controlplane.Store, status string) ([]Brief, error) {
	if store == nil {
		return nil, errors.New("brief: store is required")
	}
	status = strings.TrimSpace(strings.ToLower(status))
	rows, err := store.ListBriefs(context.Background(), status)
	if err != nil {
		return nil, fmt.Errorf("list briefs: %w", err)
	}
	now := store.Clock()
	out := make([]Brief, 0, len(rows))
	for _, r := range rows {
		if r.Status == StatusActive && now.After(r.ExpiresAt) {
			_ = store.UpdateBriefStatus(context.Background(), r.ID, StatusExpired)
			r.Status = StatusExpired
			if status == StatusActive {
				continue
			}
		}
		out = append(out, fromRow(r))
	}
	return out, nil
}

func fromRow(r controlplane.BriefRow) Brief {
	return Brief{
		ID:        r.ID,
		Title:     r.Title,
		PayloadMD: r.PayloadMD,
		DodMD:     r.DodMD,
		IssuedBy:  r.IssuedBy,
		IssuedAt:  r.IssuedAt,
		ExpiresAt: r.ExpiresAt,
		Status:    r.Status,
	}
}
