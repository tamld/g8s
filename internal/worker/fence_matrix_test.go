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

func TestDiffFirewallDetectsExternalCLIViolation(t *testing.T) {
	for _, path := range []string{"src/external.go", "external.go"} {
		t.Run(path, func(t *testing.T) {
			dir := initGitRepoForDiffFirewall(t)
			claim := &receipt.ClaimBinding{
				TaskID: "external-cli-task", NodeID: "node", Incarnation: "inc",
				// Claim paths use canonical directory names without a trailing slash.
				Resource: "repo", Paths: []string{"src"}, ExpiresAt: time.Now().Add(time.Minute),
			}
			// Bypass fsops as an external CLI would, inside this disposable repo.
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte("external CLI write\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// This is the claim-aware backstop called by sweep-collect at harvest.
			// A broad receipt ensures only the claim can reject the outside write.
			violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"**"}, claim)
			if strings.HasPrefix(path, "src/") {
				if err != nil || len(violations) != 0 {
					t.Fatalf("in-claim write rejected: violations=%v err=%v", violations, err)
				}
				return
			}
			if !errors.Is(err, receipt.ErrClaimFenceOutOfScope) || len(violations) != 1 || violations[0] != path {
				t.Fatalf("external CLI artifact accepted: violations=%v err=%v", violations, err)
			}
		})
	}
}

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
