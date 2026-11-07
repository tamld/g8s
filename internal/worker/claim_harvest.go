package worker

import (
	"context"
	"fmt"
	"os"

	"github.com/tamld/g8s/internal/mesh"
	"github.com/tamld/g8s/internal/receipt"
)

// LoadClaimFence validates the current coordinator ledger against the persisted
// receipt projection. It is for the harness and verify CLI, never delegated fsops.
func LoadClaimFence(ctx context.Context, receiptDB, id string) (*receipt.ClaimBinding, error) {
	m, err := receipt.NewReceiptManager(receiptDB, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: receipt unavailable", receipt.ErrClaimFenceStale)
	}
	defer m.Close()
	r, err := m.VerifyReceipt(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", receipt.ErrClaimFenceStale, err)
	}
	b := r.ClaimBinding
	if b == nil {
		return nil, receipt.ErrClaimFenceStale
	}
	ledger, err := mesh.Open(os.Getenv("G8S_MESH_DIR"))
	if err != nil {
		return nil, fmt.Errorf("%w: mesh unavailable", receipt.ErrClaimFenceStale)
	}
	defer ledger.Close()
	c, err := ledger.ActiveClaim(ctx, b.TaskID, b.NodeID, b.Incarnation)
	if err != nil {
		return nil, err
	}
	if !b.SameScope(receipt.BindingFromClaim(*c)) {
		return nil, receipt.ErrClaimFenceStale
	}
	return b, nil
}
