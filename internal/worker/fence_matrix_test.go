package worker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

func TestFenceReceiptClaimIntersection(t *testing.T) {
	for _, file := range []string{"a.go", "b.go"} {
		t.Run(file, func(t *testing.T) {
			dir := initGitRepoForDiffFirewall(t)
			b := &receipt.ClaimBinding{TaskID: "task", NodeID: "node", Incarnation: "inc", Resource: "repo", Paths: []string{"src/a.go"}, ExpiresAt: time.Now().Add(time.Minute)}
			if err := os.WriteFile(filepath.Join(dir, "src", file), []byte("attack"), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}, b)
			if file == "a.go" {
				if err != nil {
					t.Fatalf("valid intersection refused: %v", err)
				}
			} else if !errors.Is(err, receipt.ErrClaimFenceOutOfScope) || len(violations) != 1 || violations[0] != "src/b.go" {
				t.Fatalf("claim widened: violations=%v err=%v", violations, err)
			}
		})
	}
}

func TestDiffFirewallFailsClosedOnGitError(t *testing.T) {
	for _, bound := range []bool{false, true} {
		name := "receipt only"
		if bound {
			name = "claim present"
		}
		t.Run(name, func(t *testing.T) {
			dir := initGitRepoForDiffFirewall(t)
			// .git exists, so this reaches cmd.Output rather than the stat guard.
			if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("invalid HEAD\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var b *receipt.ClaimBinding
			if bound {
				b = &receipt.ClaimBinding{TaskID: "task", NodeID: "node", Incarnation: "inc", Resource: "repo", Paths: []string{"src/a.go"}, ExpiresAt: time.Now().Add(time.Minute)}
			}
			_, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}, b)
			if bound {
				if !errors.Is(err, receipt.ErrClaimFenceStale) || !strings.Contains(err.Error(), "git status") {
					t.Fatalf("claim git failure passed: %v", err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), "E_DIFF_FIREWALL_UNVERIFIABLE: git status failed") {
				t.Fatalf("receipt-only git failure passed: %v", err)
			}
		})
	}
}
