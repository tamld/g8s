package receipt

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/mesh"
	"github.com/tamld/g8s/internal/search"
)

var (
	ErrClaimFenceOutOfScope = mesh.ErrClaimFenceOutOfScope
	ErrClaimFenceStale      = mesh.ErrClaimFenceStale
)

// ClaimBinding is the coordinator's persisted projection. Workers need no ledger access.
type ClaimBinding struct {
	TaskID          string    `json:"task_id"`
	NodeID          string    `json:"node_id"`
	Incarnation     string    `json:"incarnation"`
	Resource        string    `json:"resource"`
	ContextRevision int64     `json:"context_revision"`
	Paths           []string  `json:"paths"`
	ExpiresAt       time.Time `json:"expires_at"`
}

func BindingFromClaim(c mesh.Claim) *ClaimBinding {
	return &ClaimBinding{TaskID: c.TaskID, NodeID: c.NodeID, Incarnation: c.Incarnation, Resource: c.Resource, ContextRevision: c.ContextRevision, Paths: append([]string(nil), c.Paths...), ExpiresAt: time.Unix(c.ExpiresAt, 0).UTC()}
}

// SameScope compares authority identity and scope, excluding the renewable lease.
func (b *ClaimBinding) SameScope(other *ClaimBinding) bool {
	return b != nil && other != nil && b.TaskID == other.TaskID && b.NodeID == other.NodeID && b.Incarnation == other.Incarnation && b.Resource == other.Resource && b.ContextRevision == other.ContextRevision && slices.Equal(b.Paths, other.Paths)
}

func (b *ClaimBinding) Validate(now time.Time) error {
	if b.TaskID == "" || b.NodeID == "" || b.Incarnation == "" || b.Resource == "" || len(b.Paths) == 0 || !now.Before(b.ExpiresAt) {
		return ErrClaimFenceStale
	}
	for _, p := range b.Paths {
		if p == "" || p == "." || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsAny(p, "*?[]\x00:") {
			return ErrClaimFenceOutOfScope
		}
		for _, segment := range strings.Split(p, "/") {
			if segment == ".." || segment == "." || segment == "" {
				return ErrClaimFenceOutOfScope
			}
		}
	}
	return nil
}

// Contains uses literal paths (including directory descendants), never glob syntax.
func (b *ClaimBinding) Contains(p string) bool {
	for _, root := range b.Paths {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// ValidatePaths is deliberately conservative: no glob can be minted under a claim.
// The dispatch clipper supplies only literal paths, closing issuance widening (T5).
func (b *ClaimBinding) ValidatePaths(paths []string) error {
	copy := *b
	copy.Paths = paths
	if err := copy.Validate(time.Unix(0, 0)); err != nil {
		return err
	}
	for _, p := range paths {
		if strings.ContainsAny(p, "*?[]") || !b.Contains(p) {
			return fmt.Errorf("%w: %s", ErrClaimFenceOutOfScope, p)
		}
	}
	return nil
}

// ClipPaths computes a conservative intersection; unmatched patterns grant nothing.
// Directory literals retain their existing receipt semantics, never becoming broader globs.
func (b *ClaimBinding) ClipPaths(requested []string) ([]string, error) {
	var clipped []string
	for _, claimPath := range b.Paths {
		if search.MatchesAllowedPaths(claimPath, requested) {
			clipped = append(clipped, claimPath)
			continue
		}
		for _, p := range requested {
			if !strings.ContainsAny(p, "*?[]") && b.Contains(p) {
				clipped = append(clipped, p)
			}
		}
	}
	if len(clipped) == 0 {
		return nil, ErrClaimFenceOutOfScope
	}
	return clipped, nil
}

func WithClaimBinding(b *ClaimBinding) IssueOption {
	return func(o *issueOptions) { o.claimBinding = b }
}

// RenewClaimBinding only extends an unchanged, still-live binding after ledger renewal.
// The receipt deadline remains bounded by the newly observed lease deadline.
func (m *Manager) RenewClaimBinding(id string, b *ClaimBinding) error {
	old, err := m.VerifyReceipt(id)
	if err != nil {
		return err
	}
	if old.ClaimBinding == nil {
		return ErrClaimFenceStale
	}
	if !old.ClaimBinding.SameScope(b) || b.ExpiresAt.Before(old.ClaimBinding.ExpiresAt) {
		return ErrClaimFenceStale
	}
	if err := b.Validate(m.clock()); err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result, err := m.db.Exec("UPDATE write_receipts SET claim_binding_json = ?, expires_at = ? WHERE receipt_id = ? AND consumed = 0 AND expires_at >= ?", string(data), timeToUnix(b.ExpiresAt), id, timeToUnix(m.clock()))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrClaimFenceStale
	}
	return nil
}
