package mcp

// Authority Sink Gate red tests — issue #977 PR-2 (enforcement half).
//
// Threat model: docs/security/context-authority-firewall-threat-model.md
//   - V8 / B-12: g8s_charter_issue passes a client-chosen issuer_stratum
//     straight into charter.Store.Issue; the only checks live deep inside
//     internal/charter (process env). Post-PR-2 the tool mirrors the mesh
//     tool's worker-tier guard (mcp/mesh.go, the reference GATED pattern),
//     derives the issuer from the SERVER process env (G8S_STRATUM/G8S_TIER),
//     and rejects client-supplied issuer identity outright.
//   - V8 / UNGUARDED (M): g8s_receipt_issue minted with a hardcoded issuer and
//     no tool-tier guard. Post-PR-2 the same worker-tier guard applies and the
//     issuer identity is derived from the server process env.
//   - B-7: resolveMCPDelegation/consumeMCPReceipt silently skipped verification
//     and consumption when the server's ReceiptIssuer did not implement the
//     optional ReceiptVerifier/ReceiptConsumer extensions. Post-PR-2 the MCP
//     mutation paths fail closed, mirroring internal/server/server.go
//     resolveFSDelegation.
//
// Env ordering note: newTestServer scrubs the ambient delegation env at
// construction, so authority-context env must be set AFTER the server is
// built (the guards read os.Getenv at tool-call time).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/charter"
	"github.com/tamld/g8s/internal/receipt"
)

// charterIssueArgs builds a g8s_charter_issue argument object.
func charterIssueArgs(issuerStratum string) json.RawMessage {
	a := map[string]any{
		"scope_repo":    "tamld/g8s",
		"objective":     "authority gate: sink guard probe",
		"allowed_paths": []string{"internal/**"},
	}
	if issuerStratum != "" {
		a["issuer_stratum"] = issuerStratum
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		panic(err)
	}
	return encoded
}

// TestAuthorityGate_CharterIssueRejectsWorkerTier — V8/B-12 tool-tier guard.
// A server process running inside a worker tier (G8S_TIER=F1/F2) must refuse
// charter minting at the tool layer, mirroring coordinatorMeshTool.
func TestAuthorityGate_CharterIssueRejectsWorkerTier(t *testing.T) {
	for _, tier := range []string{"F1", "F2"} {
		t.Run(tier+" denies", func(t *testing.T) {
			t.Setenv("G8S_STATE_DIR", t.TempDir())
			s, _, _, _ := newTestServer(t)
			t.Setenv("G8S_TIER", tier)
			t.Setenv("G8S_STRATUM", "S1")
			res, rpcErr := s.charterIssueTool(context.Background(), charterIssueArgs("S1"))
			if rpcErr == nil {
				t.Fatalf("G8S_TIER=%s must refuse charter minting at the tool layer, got %+v", tier, res)
			}
			if data, ok := rpcErr.Data.(map[string]any); !ok || data["status"] != "blocked_by_policy" {
				t.Fatalf("expected typed blocked_by_policy guard error, got %+v", rpcErr)
			}
			if !strings.Contains(rpcErr.Message, tier) {
				t.Fatalf("refusal must cite the offending tier %q, got %q", tier, rpcErr.Message)
			}
		})
	}
}

// TestAuthorityGate_CharterIssueRejectsClientIssuerStratum — the measured V8
// gap (corpus scenario mcp-issuer-args): a client-chosen issuer_stratum used
// to mint a charter from an unauthenticated client. Post-PR-2 client-supplied
// issuer identity is rejected outright; the issuer is a server-side fact.
func TestAuthorityGate_CharterIssueRejectsClientIssuerStratum(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	s, _, _, _ := newTestServer(t)
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_STRATUM", "") // server declares no issuing stratum
	res, rpcErr := s.charterIssueTool(context.Background(), charterIssueArgs("S1"))
	if rpcErr == nil {
		t.Fatalf("client-supplied issuer_stratum=S1 must be refused at the tool layer (V8/B-12), got minted %+v", res)
	}
	if data, ok := rpcErr.Data.(map[string]any); !ok || data["status"] != "blocked_by_policy" {
		t.Fatalf("expected typed blocked_by_policy guard error, got %+v", rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "issuer_stratum") {
		t.Fatalf("refusal must cite the rejected field, got %q", rpcErr.Message)
	}
}

// TestAuthorityGate_CharterIssueIssuerDerivedFromServerEnv — deterministic
// issuer identity: a coordinator-tier server (G8S_STRATUM=S1) mints charters
// under its own declared stratum. There is no client input that can influence
// the issuer (a client-supplied issuer_stratum is refused outright — see
// TestAuthorityGate_CharterIssueRejectsClientIssuerStratum).
func TestAuthorityGate_CharterIssueIssuerDerivedFromServerEnv(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	s, _, _, _ := newTestServer(t)
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_STRATUM", "S1")
	res, rpcErr := s.charterIssueTool(context.Background(), charterIssueArgs(""))
	if rpcErr != nil {
		t.Fatalf("coordinator-tier server must mint under its env stratum, got refusal %+v", rpcErr)
	}
	var issued struct {
		IssuerStratum string `json:"issuer_stratum"`
		CharterID     string `json:"charter_id"`
	}
	switch v := res.(type) {
	case *charter.Charter:
		issued.IssuerStratum = string(v.IssuerStratum)
		issued.CharterID = v.CharterID
	case json.RawMessage:
		if err := json.Unmarshal(v, &issued); err != nil {
			t.Fatalf("decode issued charter: %v (raw %s)", err, v)
		}
	default:
		t.Fatalf("unexpected charter issue result type %T: %+v", res, res)
	}
	if issued.IssuerStratum != "S1" || issued.CharterID == "" {
		t.Fatalf("issuer must derive from server env (S1), got %+v", issued)
	}
}

// TestAuthorityGate_CharterIssueRefusesNonIssuingServerStratum — fail-closed:
// a server process that does not declare an issuing stratum (S0/S1) cannot
// mint charters at all (S2 default, clamped S3 for F1/F2 or receipt-bound).
func TestAuthorityGate_CharterIssueRefusesNonIssuingServerStratum(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stratum, tier string
		receiptID     string
	}{
		{name: "undeclared_stratum_refused", stratum: "", tier: ""},
		{name: "s2_server_refused", stratum: "S2", tier: ""},
		{name: "receipt_bound_server_refused", stratum: "S1", tier: "", receiptID: "rc-binding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("G8S_STATE_DIR", t.TempDir())
			s, _, _, _ := newTestServer(t)
			t.Setenv("G8S_TIER", tc.tier)
			t.Setenv("G8S_STRATUM", tc.stratum)
			if tc.receiptID != "" {
				t.Setenv("G8S_RECEIPT_ID", tc.receiptID)
			}
			res, rpcErr := s.charterIssueTool(context.Background(), charterIssueArgs(""))
			if rpcErr == nil {
				t.Fatalf("server stratum (env STRATUM=%q TIER=%q RECEIPT_ID=%q) must not mint charters, got %+v",
					tc.stratum, tc.tier, tc.receiptID, res)
			}
			if !strings.Contains(rpcErr.Message, "E_STRATUM_ADJACENCY_VIOLATION") {
				t.Fatalf("expected adjacency refusal, got %q", rpcErr.Message)
			}
		})
	}
}

// TestAuthorityGate_ReceiptIssueRejectsWorkerTier — V8 (M) tool-tier guard on
// the receipt minting sink: a worker-tier server process must refuse.
func TestAuthorityGate_ReceiptIssueRejectsWorkerTier(t *testing.T) {
	for _, tier := range []string{"F1", "F2"} {
		t.Run(tier+" denies", func(t *testing.T) {
			s, _, _, _ := newTestServer(t)
			t.Setenv("G8S_TIER", tier)
			res, rpcErr := s.receiptIssueTool(context.Background(), json.RawMessage(`{"allowed_paths":["src/**"],"ttl_seconds":600}`))
			if rpcErr == nil {
				t.Fatalf("G8S_TIER=%s must refuse receipt minting at the tool layer, got %+v", tier, res)
			}
			if data, ok := rpcErr.Data.(map[string]any); !ok || data["status"] != "blocked_by_policy" {
				t.Fatalf("expected typed blocked_by_policy guard error, got %+v", rpcErr)
			}
		})
	}
}

// TestAuthorityGate_ReceiptIssueIssuerDerivedFromServerEnv — deterministic
// issuer identity for receipts: the recorded issuer reflects the SERVER
// process env (G8S_STRATUM/G8S_TIER); the client has no issuer field at all.
func TestAuthorityGate_ReceiptIssueIssuerDerivedFromServerEnv(t *testing.T) {
	t.Run("declared_stratum_stamped_on_issuer", func(t *testing.T) {
		s, _, issuer, _ := newTestServer(t)
		t.Setenv("G8S_TIER", "")
		t.Setenv("G8S_STRATUM", "S1")
		if _, rpcErr := s.receiptIssueTool(context.Background(), json.RawMessage(`{"allowed_paths":["src/**"],"ttl_seconds":600}`)); rpcErr != nil {
			t.Fatalf("receipt issue refused: %+v", rpcErr)
		}
		if issuer.issuer == "mcp-server" || !strings.HasPrefix(issuer.issuer, "mcp-server/") || !strings.Contains(issuer.issuer, "S1") {
			t.Fatalf("issuer must derive from server env stratum, got %q", issuer.issuer)
		}
	})
	t.Run("undeclared_env_keeps_base_identity", func(t *testing.T) {
		s, _, issuer, _ := newTestServer(t)
		t.Setenv("G8S_TIER", "")
		t.Setenv("G8S_STRATUM", "")
		if _, rpcErr := s.receiptIssueTool(context.Background(), json.RawMessage(`{"allowed_paths":["src/**"],"ttl_seconds":600}`)); rpcErr != nil {
			t.Fatalf("receipt issue refused: %+v", rpcErr)
		}
		if issuer.issuer != "mcp-server" {
			t.Fatalf("undeclared env must keep the base server identity, got %q", issuer.issuer)
		}
	})
}

// ---------------------------------------------------------------------------
// Item 3: fail-closed optional interfaces (B-7) at the MCP mutation paths.
// ---------------------------------------------------------------------------

// issuerOnlyStub satisfies ReceiptIssuer but NOT ReceiptVerifier — the B-7
// gap shape: a server wired for minting only.
type issuerOnlyStub struct{ rc *receipt.WriteReceipt }

func (s *issuerOnlyStub) IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration, opts ...receipt.IssueOption) (*receipt.WriteReceipt, error) {
	return s.rc, nil
}

// verifierOnlyStub additionally implements ReceiptVerifier but NOT
// ReceiptConsumer — the second B-7 gap shape: verification present, atomic
// single-use consumption silently skipped.
type verifierOnlyStub struct{ rc *receipt.WriteReceipt }

func (s *verifierOnlyStub) IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration, opts ...receipt.IssueOption) (*receipt.WriteReceipt, error) {
	return s.rc, nil
}

func (s *verifierOnlyStub) VerifyReceipt(receiptID string) (*receipt.WriteReceipt, error) {
	if s.rc == nil || s.rc.ReceiptID != receiptID {
		return nil, &receiptErrorStub{msg: "unknown receipt " + receiptID}
	}
	return s.rc, nil
}

// receiptErrorStub is a plain error carrier for the stub verifier.
type receiptErrorStub struct{ msg string }

func (e *receiptErrorStub) Error() string { return e.msg }

func newEditFixture(t *testing.T) (dir, target string) {
	t.Helper()
	dir = t.TempDir()
	target = filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(target, []byte("line one\nline two\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir, target
}

func editArgs(dir, target string) json.RawMessage {
	encoded, err := json.Marshal(map[string]any{
		"path":       target,
		"old_string": "line two",
		"new_string": "line TWO",
		"scope_root": dir,
		"receipt_id": "rc-authority-gate",
	})
	if err != nil {
		panic(err)
	}
	return encoded
}

func receiptFor(id, target string) *receipt.WriteReceipt {
	return &receipt.WriteReceipt{
		ReceiptID:    id,
		Issuer:       "brain",
		AllowedPaths: []string{filepath.Base(target)},
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	}
}

// TestAuthorityGate_MutationFailsClosedWithoutVerifier — B-7: a server whose
// ReceiptIssuer cannot verify receipts must REFUSE receipt-bound mutations
// instead of silently proceeding unverified (mirror of the HTTP server's
// fail-closed posture at internal/server/server.go resolveFSDelegation).
func TestAuthorityGate_MutationFailsClosedWithoutVerifier(t *testing.T) {
	dir, target := newEditFixture(t)
	s := NewServer(strings.NewReader(""), &strings.Builder{}, &fakeControlPlane{}, &issuerOnlyStub{rc: receiptFor("rc-authority-gate", target)}, &fakeProviders{})
	res, rpcErr := s.editTool(context.Background(), editArgs(dir, target))
	if rpcErr == nil {
		t.Fatalf("mutation with unverifiable receipt must fail closed, got %+v", res)
	}
	if data, ok := rpcErr.Data.(map[string]any); !ok || data["status"] != "blocked_by_policy" {
		t.Fatalf("expected typed blocked_by_policy guard error, got %+v", rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "receipt verification is not configured") {
		t.Fatalf("expected fail-closed verifier message, got %q", rpcErr.Message)
	}
}

// TestAuthorityGate_MutationFailsClosedWithoutConsumer — B-7: a server that
// can verify but cannot atomically consume must refuse the mutation; single-use
// enforcement must never degrade to optional.
func TestAuthorityGate_MutationFailsClosedWithoutConsumer(t *testing.T) {
	dir, target := newEditFixture(t)
	s := NewServer(strings.NewReader(""), &strings.Builder{}, &fakeControlPlane{}, &verifierOnlyStub{rc: receiptFor("rc-authority-gate", target)}, &fakeProviders{})
	res, rpcErr := s.editTool(context.Background(), editArgs(dir, target))
	if rpcErr == nil {
		t.Fatalf("mutation with unconsumable receipt must fail closed, got %+v", res)
	}
	if data, ok := rpcErr.Data.(map[string]any); !ok || data["status"] != "blocked_by_policy" {
		t.Fatalf("expected typed blocked_by_policy guard error, got %+v", rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "receipt consumption is not configured") {
		t.Fatalf("expected fail-closed consumer message, got %q", rpcErr.Message)
	}
}

// TestAuthorityGate_MutationFailsClosedWithNilReceipts — a server constructed
// without any receipt issuer must refuse receipt-bound mutations outright.
func TestAuthorityGate_MutationFailsClosedWithNilReceipts(t *testing.T) {
	dir, target := newEditFixture(t)
	s := NewServer(strings.NewReader(""), &strings.Builder{}, &fakeControlPlane{}, nil, &fakeProviders{})
	res, rpcErr := s.editTool(context.Background(), editArgs(dir, target))
	if rpcErr == nil {
		t.Fatalf("mutation on a server without receipt infrastructure must fail closed, got %+v", res)
	}
	if data, ok := rpcErr.Data.(map[string]any); !ok || data["status"] != "blocked_by_policy" {
		t.Fatalf("expected typed blocked_by_policy guard error, got %+v", rpcErr)
	}
}

// TestAuthorityGate_MutationSucceedsWithFullVerifierConsumer — positive
// control: the guards are not vacuously denying. A server whose receipts
// implement BOTH ReceiptVerifier and ReceiptConsumer (the production
// *receipt.Manager shape, mirrored by the test fake) still mutates and the
// receipt is consumed.
func TestAuthorityGate_MutationSucceedsWithFullVerifierConsumer(t *testing.T) {
	dir, target := newEditFixture(t)
	s := NewServer(strings.NewReader(""), &strings.Builder{}, &fakeControlPlane{}, &fakeIssuer{rc: receiptFor("rc-authority-gate", target)}, &fakeProviders{})
	if _, rpcErr := s.editTool(context.Background(), editArgs(dir, target)); rpcErr != nil {
		t.Fatalf("positive control: valid receipt + full verifier/consumer must mutate, got %+v", rpcErr)
	}
}
