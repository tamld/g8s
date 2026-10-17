package mcp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

func TestMCPServer_DAST_AdversarialSuite(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_SCOPE_ROOT", "")
	t.Setenv("G8S_RECEIPT_ID", "")
	t.Setenv("G8S_PERMISSION", "")

	tmpDir := t.TempDir()
	scopeDir := filepath.Join(tmpDir, "scope")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(scopeDir, 0o700); err != nil {
		t.Fatalf("mkdir scope: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("secret-outside\n"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}

	symlinkFile := filepath.Join(scopeDir, "symlink.txt")
	if err := os.Symlink(outsideFile, symlinkFile); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	rcDBPath := filepath.Join(tmpDir, "receipts.db")
	mgr, err := receipt.NewReceiptManager(rcDBPath, nil)
	if err != nil {
		t.Fatalf("new receipt manager: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := mgr.Close(); closeErr != nil {
			t.Errorf("close receipt manager: %v", closeErr)
		}
	})

	srv := NewServer(strings.NewReader(""), &bytes.Buffer{}, &fakeControlPlane{}, mgr, &fakeProviders{})

	issueTestReceipt := func(paths []string) *receipt.WriteReceipt {
		t.Helper()
		rc, issueErr := mgr.IssueReceipt("brain-dast", paths, 10*time.Minute,
			receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
				SchemaURI:      "g8s://envelope/write-receipt/v1",
				FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
				RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
			}),
			receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
				RulesetVersion: "v1",
				PipelineDigest: "mcp-dast",
			}),
			receipt.WithProvenanceContext("g8s/mcp", "HEAD", "trace-mcp-dast"),
		)
		if issueErr != nil {
			t.Fatalf("issue receipt: %v", issueErr)
		}
		return rc
	}

	t.Run("single_use_receipt_replay_blocked_on_g8s_edit", func(t *testing.T) {
		target := filepath.Join(scopeDir, "allowed.txt")
		if writeErr := os.WriteFile(target, []byte("v1\n"), 0o600); writeErr != nil {
			t.Fatalf("write target: %v", writeErr)
		}

		rc := issueTestReceipt([]string{"allowed.txt"})

		// First edit consumes the receipt
		_, rpcErr1 := callTool(t, srv, "g8s_edit", map[string]any{
			"path":       target,
			"old_string": "v1",
			"new_string": "v2",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
		})
		if rpcErr1 != nil {
			t.Fatalf("first g8s_edit failed: %+v", rpcErr1)
		}

		// Second edit with the same receipt MUST be rejected
		_, rpcErr2 := callTool(t, srv, "g8s_edit", map[string]any{
			"path":       target,
			"old_string": "v2",
			"new_string": "v3",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
		})
		if rpcErr2 == nil {
			t.Fatal("expected second g8s_edit with consumed receipt to fail, got nil")
		}
	})

	t.Run("symlink_escape_blocked_on_read_and_edit", func(t *testing.T) {
		_, rpcReadErr := callTool(t, srv, "g8s_read", map[string]any{
			"path":       symlinkFile,
			"scope_root": scopeDir,
		})
		if rpcReadErr == nil {
			t.Fatal("expected g8s_read on escaping symlink to fail")
		}

		rc := issueTestReceipt([]string{"symlink.txt"})
		_, rpcEditErr := callTool(t, srv, "g8s_edit", map[string]any{
			"path":       symlinkFile,
			"old_string": "secret-outside",
			"new_string": "hacked",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
		})
		if rpcEditErr == nil {
			t.Fatal("expected g8s_edit on escaping symlink to fail")
		}
	})

	t.Run("delegated_F1_scope_root_widening_blocked", func(t *testing.T) {
		t.Setenv("G8S_TIER", "F1")
		t.Setenv("G8S_SCOPE_ROOT", scopeDir)

		_, rpcReadErr := callTool(t, srv, "g8s_read", map[string]any{
			"path":       outsideFile,
			"scope_root": tmpDir,
		})
		if rpcReadErr == nil {
			t.Fatal("expected g8s_read with widened scope_root under G8S_TIER=F1 to fail")
		}

		_, rpcSearchErr := callTool(t, srv, "g8s_search", map[string]any{
			"pattern":    "secret-outside",
			"roots":      []string{outsideDir},
			"scope_root": tmpDir,
		})
		if rpcSearchErr == nil {
			t.Fatal("expected g8s_search with widened scope_root under G8S_TIER=F1 to fail")
		}
	})
}
