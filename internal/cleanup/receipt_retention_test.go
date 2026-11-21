package cleanup

// Red test for #986 part-2 batch 2 (blind-audit registry C-9): receipt
// PurgeExpired has no production caller — the receipts.db ledger grows
// forever. The stale-receipt sweep now prunes consumed/expired ledger rows
// (bounded per sweep) via receipt.Manager.PurgeExpired.

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

// TestStaleReceiptSweepPrunesReceiptLedger: with the stale-receipt target
// enabled, the sweep prunes the sibling receipts.db ledger — consumed
// receipts past the retention window and old expired receipts are deleted;
// unconsumed unexpired receipts and fresh consumed receipts survive.
func TestStaleReceiptSweepPrunesReceiptLedger(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "g8s.db")

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clk := newSweepClock(base)

	ledgerPath := filepath.Join(dir, "receipts.db")
	m, err := receipt.NewReceiptManager(ledgerPath, clk.Now)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}

	mkReceipt := func(label string, ttl time.Duration) string {
		t.Helper()
		r, err := m.IssueReceipt("brain-"+label, []string{label + "/**"}, ttl,
			receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
				SchemaURI:  "g8s://receipt/test",
				FieldOrder: []string{"receipt_id", "issuer", "allowed_paths"},
			}),
			receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{RulesetVersion: "test"}),
		)
		if err != nil {
			t.Fatalf("IssueReceipt %s: %v", label, err)
		}
		return r.ReceiptID
	}

	// r1: consumed 48h ago — past retention, must be pruned.
	r1 := mkReceipt("old-consumed", time.Hour)
	if _, err := m.ValidateAndConsume(r1, "w"); err != nil {
		t.Fatalf("consume r1: %v", err)
	}
	clk.Advance(48 * time.Hour)

	// r2: consumed just now — inside the retention window, must survive.
	r2 := mkReceipt("fresh-consumed", time.Hour)
	if _, err := m.ValidateAndConsume(r2, "w"); err != nil {
		t.Fatalf("consume r2: %v", err)
	}

	// r3: unconsumed, not expired (TTL max is 1h; the sweep clock only
	// advances during seeding) — must survive.
	r3 := mkReceipt("live-unconsumed", time.Hour)

	if err := m.Close(); err != nil {
		t.Fatalf("close ledger: %v", err)
	}

	cfg := CleanupConfig{
		DBPath:  dbPath,
		Targets: []string{TargetStaleReceipt},
		Clock:   clk.Now,
		Writer:  io.Discard,
	}
	report, err := RunCleanupSweep(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunCleanupSweep: %v", err)
	}

	// The report must reflect the ledger retention pass.
	found := false
	for _, item := range report.Items {
		if item.ID == "ledger-retention" && item.Action == "deleted" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a ledger-retention cleanup item with action=deleted, got %+v", report.Items)
	}

	// Ledger contents: exactly the two survivors remain.
	m2, err := receipt.NewReceiptManager(ledgerPath, clk.Now)
	if err != nil {
		t.Fatalf("reopen ledger: %v", err)
	}
	defer m2.Close()

	if _, err := m2.VerifyReceipt(r1); err == nil {
		t.Errorf("r1 (consumed %s ago) must be pruned, still verifiable", 48*time.Hour)
	}
	if _, err := m2.VerifyReceipt(r2); err != nil {
		t.Errorf("r2 (fresh consumed) must survive the retention sweep: %v", err)
	}
	if _, err := m2.VerifyReceipt(r3); err != nil {
		t.Errorf("r3 (unconsumed, unexpired) must survive: %v", err)
	}
}

type sweepClock struct{ now time.Time }

// newSweepClock returns a mutable fixed clock for the sweep test.
func newSweepClock(now time.Time) *sweepClock {
	return &sweepClock{now: now}
}

func (c *sweepClock) Now() time.Time { return c.now }

func (c *sweepClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
