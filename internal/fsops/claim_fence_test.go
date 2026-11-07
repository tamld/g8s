package fsops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

func TestFenceEditOutsideClaimPathsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, path       string
		bound, delegated bool
		want             error
	}{
		{"in scope", "a.go", true, true, nil},
		{"outside claim", "b.go", true, true, receipt.ErrClaimFenceOutOfScope},
		{"no binding", "b.go", false, true, nil},
		{"no delegated env", "b.go", true, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			state := t.TempDir()
			t.Setenv("G8S_STATE_DIR", state)
			dbPath := filepath.Join(state, "receipts.db")
			m, err := receipt.NewReceiptManager(dbPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			r, err := m.IssueReceipt("brain", []string{"src/**"}, time.Minute,
				receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{SchemaURI: "g8s://envelope/write-receipt/v1", FieldOrder: []string{"receipt_id"}}),
				receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{RulesetVersion: "v1", PipelineDigest: "test", ADRRef: "ADR-0038"}))
			if err != nil {
				t.Fatal(err)
			}
			if tc.bound {
				// Seed a legacy broad receipt to test the mutation gate independently
				// of issuance-time clipping, which already rejects this combination.
				b := receipt.ClaimBinding{TaskID: "task", NodeID: "node", Incarnation: "inc", Resource: "repo", Paths: []string{"src/a.go"}, ExpiresAt: time.Now().Add(time.Minute)}
				raw, err := json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.Exec("UPDATE write_receipts SET claim_binding_json = ? WHERE receipt_id = ?", string(raw), r.ReceiptID); err != nil {
					t.Fatal(err)
				}
			}
			id := ""
			if tc.delegated {
				id = r.ReceiptID
			}
			t.Setenv("G8S_RECEIPT_ID", id)
			if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(root, "src", tc.path)
			if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = Edit(context.Background(), EditOptions{Path: p, ScopeRoot: root, AllowedPaths: []string{"src/**"}, OldString: "old", NewString: "new"})
			if !errors.Is(err, tc.want) {
				canonical, resolveErr := filepath.EvalSymlinks(p)
				t.Fatalf("edit error = %v, want %v; root=%q target=%q canonical=%q resolveErr=%v", err, tc.want, root, p, canonical, resolveErr)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			want := "new"
			if tc.want != nil {
				want = "old"
			}
			if string(data) != want {
				t.Fatalf("content = %q, want %q (gate must precede write)", data, want)
			}
		})
	}
}
