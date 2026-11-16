package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/signing"
)

func TestValidateSubmitRequestWorkspaceWriteRejectionHint(t *testing.T) {
	t.Setenv("AGY_MCP_ALLOW_WORKSPACE_WRITE", "")
	payload, err := json.Marshal(map[string]string{"prompt": "mutate"})
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateSubmitRequest(SubmitTaskRequest{
		Payload:    payload,
		Model:      "test-model",
		AddDirs:    []string{"internal/config"},
		Permission: "workspace_write",
	})
	if err == nil {
		t.Fatal("expected workspace_write without opt-in to be rejected")
	}
	msg := err.Error()
	for _, sub := range []string{
		"workspace_write is disabled by default",
		"--allow-workspace-write",
		"AGY_MCP_ALLOW_WORKSPACE_WRITE",
	} {
		if !strings.Contains(msg, sub) {
			t.Fatalf("rejection hint missing %q: %s", sub, msg)
		}
	}
}

func seedSignedLegacyReceipt(t *testing.T, cp *Store, receiptID, issuer, pathsJSON string, expiresAt, createdAt float64, consumed int, consumerTask string) {
	t.Helper()
	stateDir := filepath.Dir(cp.dbPath)
	priv, _, err := signing.EnsureOrLoadF0Signer(stateDir)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	tag := receipt.FormatMonotoneReceiptSig(priv, receiptID, issuer, pathsJSON, expiresAt, createdAt, "")
	if _, err := cp.db.Exec(
		`INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at, sig_tag)
		 VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		receiptID, issuer, pathsJSON, expiresAt, consumed, consumerTask, createdAt, tag); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
}

func TestConsumeWriteReceiptRejectsUnsignedMirrorRow(t *testing.T) {
	cp, _ := newTestStore(t)
	ctx := context.Background()
	now := float64(cp.clock().UnixNano()) / 1e9
	if _, err := cp.db.Exec(writeReceiptsEnsureDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.db.Exec(
		`INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at, consumed)
		 VALUES (?, 'issuer', '[]', ?, ?, 0)`, "rcpt-unsigned", now+500, now); err != nil {
		t.Fatal(err)
	}
	err := cp.ConsumeWriteReceipt(ctx, "rcpt-unsigned", "task-x")
	if err == nil || !errors.Is(err, signing.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

// #346: delegated-write receipts consume exactly once; replay by another
// task trips ErrReceiptAlreadyConsumed; re-consume by the owner is
// idempotent; unknown receipts trip ErrReceiptUnknownOrExpired.
func TestConsumeWriteReceipt(t *testing.T) {
	cp, _ := newTestStore(t)
	ctx := context.Background()

	// First consume on a worker-only store lazily creates the receipt
	// schema (#346) and reports the receipt as unknown.
	if err := cp.ConsumeWriteReceipt(ctx, "rcpt-missing", "task-A"); !errors.Is(err, ErrReceiptUnknownOrExpired) {
		t.Fatalf("unknown receipt should trip ErrReceiptUnknownOrExpired, got %v", err)
	}

	now := float64(cp.clock().UnixNano()) / 1e9
	seedSignedLegacyReceipt(t, cp, "rcpt-test-1", "brain", `["README.vi.md"]`, now+600, now, 0, "")

	if err := cp.ConsumeWriteReceipt(ctx, "rcpt-test-1", "task-A"); err != nil {
		t.Fatalf("first consume should succeed: %v", err)
	}
	if err := cp.ConsumeWriteReceipt(ctx, "rcpt-test-1", "task-A"); err != nil {
		t.Fatalf("re-consume by owning task should be idempotent: %v", err)
	}
	err := cp.ConsumeWriteReceipt(ctx, "rcpt-test-1", "task-B")
	if !errors.Is(err, ErrReceiptAlreadyConsumed) {
		t.Fatalf("replay by another task should trip ErrReceiptAlreadyConsumed, got %v", err)
	}
}
