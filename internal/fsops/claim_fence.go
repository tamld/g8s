package fsops

import (
	"os"
	"path/filepath"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
)

// verifyDelegatedClaim reads only the receipt projection, never the mesh ledger.
// Literal claim paths intersect receipt globs; no claim path is itself a glob.
func verifyDelegatedClaim(filePath, scopeRoot string) error {
	id := os.Getenv("G8S_RECEIPT_ID")
	if id == "" {
		return nil
	}
	db := filepath.Join(filepath.Dir(pathutil.DefaultDatabasePath()), "receipts.db")
	m, err := receipt.NewReceiptManager(db, nil)
	if err != nil {
		return err
	}
	defer m.Close()
	r, err := m.VerifyReceipt(id)
	if err != nil {
		return err
	}
	if r.Consumed {
		return &receipt.AlreadyConsumedError{ReceiptID: id}
	}
	if r.ClaimBinding == nil {
		return nil
	}
	if err := r.ClaimBinding.Validate(time.Now()); err != nil {
		return err
	}
	root, err := filepath.Abs(scopeRoot)
	if err != nil {
		return err
	}
	target := filePath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if !r.ClaimBinding.Contains(filepath.ToSlash(rel)) {
		return receipt.ErrClaimFenceOutOfScope
	}
	return nil
}
