package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tamld/g8s/internal/mesh"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
)

// Discover the binding from the canonical receipt, including expired receipts.
// Payloads are hints only: non-CLI submitters cannot omit the fence to bypass it.
func receiptClaimBinding(id string) (*receipt.ClaimBinding, error) {
	if id == "" {
		return nil, nil
	}
	db := filepath.Join(filepath.Dir(pathutil.DefaultDatabasePath()), "receipts.db")
	if _, err := os.Stat(db); os.IsNotExist(err) {
		return nil, nil
	}
	m, err := receipt.NewReceiptManager(db, nil)
	if err != nil {
		return nil, err
	}
	defer m.Close()
	rows, err := m.ListReceipts("", time.Time{}, "")
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.ReceiptID == id {
			return r.ClaimBinding, nil
		}
	}
	return nil, nil
}

// renewClaimFence runs only in the harness, never in the delegated edit tool.
// Missing mesh configuration for a bound receipt is a hard failure.
func renewClaimFence(ctx context.Context, id string, renew bool) error {
	if id == "" {
		return nil
	}
	mgr, err := receipt.NewReceiptManager(filepath.Join(filepath.Dir(pathutil.DefaultDatabasePath()), "receipts.db"), nil)
	if err != nil {
		return err
	}
	defer mgr.Close()
	r, err := mgr.VerifyReceipt(id)
	if err != nil {
		return err
	}
	b := r.ClaimBinding
	if b == nil {
		return receipt.ErrClaimFenceStale
	}
	ledger, err := mesh.Open(os.Getenv("G8S_MESH_DIR"))
	if err != nil {
		return fmt.Errorf("%w: mesh unavailable", receipt.ErrClaimFenceStale)
	}
	defer ledger.Close()
	c, err := ledger.ActiveClaim(ctx, b.TaskID, b.NodeID, b.Incarnation)
	if err != nil {
		return err
	}
	// Compare every binding field before renewal; scope and revision cannot drift.
	live := receipt.BindingFromClaim(*c)
	if !b.SameScope(live) {
		return receipt.ErrClaimFenceStale
	}
	if !renew {
		return live.Validate(time.Now())
	}
	c, err = ledger.RenewClaim(ctx, *c)
	if err != nil {
		return err
	}
	return mgr.RenewClaimBinding(id, receipt.BindingFromClaim(*c))
}
