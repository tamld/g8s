package mesh

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrClaimFenceOutOfScope = errors.New("E_CLAIM_FENCE_OUT_OF_SCOPE")
	ErrClaimFenceStale      = errors.New("E_CLAIM_FENCE_STALE")
)

// ActiveClaim is a coordinator-side projection, not a new mesh operation.
// It verifies the actor incarnation and global revision in the same transaction.
func (s *Store) ActiveClaim(ctx context.Context, taskID, nodeID, incarnation string) (*Claim, error) {
	var c Claim
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := actorTx(ctx, tx, nodeID, incarnation); err != nil {
			return err
		}
		var paths string
		var revision int64
		if err := tx.QueryRowContext(ctx, "SELECT revision FROM meta WHERE id = 1").Scan(&revision); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT task_id, node_id, incarnation, resource, paths, context_revision, expires_at, state FROM claims WHERE task_id = ?", taskID).Scan(&c.TaskID, &c.NodeID, &c.Incarnation, &c.Resource, &paths, &c.ContextRevision, &c.ExpiresAt, &c.State); err != nil {
			return err
		}
		if c.NodeID != nodeID || c.Incarnation != incarnation || c.State != "active" || c.ContextRevision != revision || c.ExpiresAt <= s.now().Unix() {
			return ErrClaimFenceStale
		}
		return json.Unmarshal([]byte(paths), &c.Paths)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrClaimFenceStale, err)
	}
	return &c, nil
}

// RenewClaim uses the existing idempotent claim operation; it cannot change scope.
func (s *Store) RenewClaim(ctx context.Context, c Claim) (*Claim, error) {
	raw, err := json.Marshal(claimRequest{NodeID: c.NodeID, Incarnation: c.Incarnation, TaskID: c.TaskID, Resource: c.Resource, Paths: c.Paths, ContextRevision: c.ContextRevision, LeaseSeconds: 300})
	if err != nil {
		return nil, err
	}
	out, err := claimHandler(ctx, s, raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrClaimFenceStale, err)
	}
	result := out.(claimResponse).Claim
	return &result, nil
}
