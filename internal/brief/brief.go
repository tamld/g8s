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
	"github.com/tamld/g8s/internal/state"
)

// Status constants for Brief.
const (
	StatusActive   = string(state.BriefStateActive)
	StatusConsumed = string(state.BriefStateConsumed)
	StatusExpired  = string(state.BriefStateExpired)
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

	_ = store.LogStateEvent(context.Background(), id, state.SubjectBrief, "", state.BriefStateActive, "issue", issuedBy, "brief issued")

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
		if _, aErr := state.Apply(state.SubjectBrief, state.State(row.Status), state.BriefEventExpire, nil, now); aErr == nil {
			_ = store.UpdateBriefStatus(ctx, id, StatusExpired)
			_ = store.LogStateEvent(ctx, id, state.SubjectBrief, state.State(row.Status), state.BriefStateExpired, state.BriefEventExpire, "system", "brief expired")
		}
		return Brief{}, fmt.Errorf("%w: %s", ErrExpired, id)
	}

	nextState, err := state.Apply(state.SubjectBrief, state.State(row.Status), state.BriefEventConsume, nil, now)
	if err != nil {
		return Brief{}, fmt.Errorf("consume brief invalid transition: %w", err)
	}

	if err := store.UpdateBriefStatus(ctx, id, string(nextState)); err != nil {
		return Brief{}, fmt.Errorf("consume brief: %w", err)
	}
	_ = store.LogStateEvent(ctx, id, state.SubjectBrief, state.State(row.Status), nextState, state.BriefEventConsume, row.IssuedBy, "brief consumed")

	row.Status = string(nextState)
	return fromRow(row), nil
}

// ListActive returns all unconsumed, unexpired briefs ordered chronologically by issued_at ASC.
func ListActive(store *controlplane.Store) ([]Brief, error) {
	if store == nil {
		return nil, errors.New("brief: store is required")
	}
	rows, err := store.ListActiveBriefs(context.Background())
	if err != nil {
		return nil, fmt.Errorf("list active briefs: %w", err)
	}
	now := store.Clock()
	out := make([]Brief, 0, len(rows))
	for _, r := range rows {
		if now.After(r.ExpiresAt) {
			_ = store.UpdateBriefStatus(context.Background(), r.ID, StatusExpired)
			_ = store.LogStateEvent(context.Background(), r.ID, state.SubjectBrief, state.State(r.Status), state.BriefStateExpired, state.BriefEventExpire, "system", "brief expired")
			continue
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
