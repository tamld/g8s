package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Injection corpus scenario `mcp-issuer-args` (threat model V8, issue #977).
//
// docs/security/context-authority-firewall-threat-model.md §1 V8 / §2 B-12:
// an MCP client on a shared host supplies a client-chosen `issuer_stratum` to
// g8s_charter_issue. Expected post-PR-3: a tool-tier guard refuses F1/F2-tier
// clients, mirroring the worker-tier guard on g8s_coordinator_mesh
// (coordinatorMeshTool, mcp/mesh.go:46-49 — the reference GATED pattern).
//
// This scenario asserts the CURRENT gate behavior (PR-1: structural assertions
// only, no enforcement changes):
//  1. the post-#976 initialize handshake gate exists (tools/call before
//     initialize -> -32002);
//  2. an issuer_stratum that charter.Issue rejects (anything outside S0/S1)
//     is refused — deep inside internal/charter, not at the tool layer;
//  3. a client-chosen VALID issuer stratum ("S1") from an unauthenticated
//     client currently MINTS a signed charter: this is the documented B-12
//     gap. The test records the observed behavior and skips with the corpus
//     finding so PR-3 can flip it into a refusal assertion.
func TestTrustCorpus_MCPIssuerArgs(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_STRATUM", "")
	t.Setenv("G8S_RECEIPT_ID", "")
	s, _, _, _ := newTestServer(t)

	charterArgs := func(issuer string) map[string]any {
		return map[string]any{
			"issuer_stratum": issuer,
			"scope_repo":     "tamld/g8s",
			"objective":      "corpus V8: issuer_stratum chosen by client",
			"allowed_paths":  []string{"internal/**"},
		}
	}

	t.Run("handshake_gate_blocks_uninitialized_tool_call", func(t *testing.T) {
		// Post-#976: tools/call before the initialize handshake must be refused
		// with codeServerNotInitialized (-32002). Run FIRST: no initSession yet.
		params := map[string]any{
			"name":      "g8s_charter_issue",
			"arguments": charterArgs("S1"),
		}
		resp := s.handleLine(context.Background(), requestLine("tools/call", params))
		if resp == nil || resp.Error == nil {
			t.Fatalf("unauthenticated pre-handshake tools/call must be refused, got %+v", resp)
		}
		if resp.Error.Code != codeServerNotInitialized {
			t.Fatalf("Code = %d, want %d (codeServerNotInitialized), got %+v", resp.Error.Code, codeServerNotInitialized, resp.Error)
		}
	})

	t.Run("invalid_issuer_stratum_refused_deep_in_charter_issue", func(t *testing.T) {
		// charter.Issue only accepts S0/S1 issuers (E_STRATUM_ADJACENCY_VIOLATION),
		// surfaced by the tool as codeInvalidParams. PASSES today — but note the
		// check lives deep in internal/charter, not at the MCP tool layer.
		res, rpcErr := callTool(t, s, "g8s_charter_issue", charterArgs("F1"))
		if rpcErr == nil {
			t.Fatalf("client-supplied issuer_stratum=F1 must be refused, got charter %s", res)
		}
		if rpcErr.Code != codeInvalidParams {
			t.Fatalf("Code = %d, want %d (codeInvalidParams), got %+v", rpcErr.Code, codeInvalidParams, rpcErr)
		}
		if !strings.Contains(rpcErr.Message, "E_STRATUM_ADJACENCY_VIOLATION") {
			t.Fatalf("expected charter adjacency refusal in message, got %q", rpcErr.Message)
		}
	})

	t.Run("client_chosen_valid_issuer_stratum_current_behavior", func(t *testing.T) {
		// V8 laundering attempt: unauthenticated client asserts issuer_stratum=S1.
		res, rpcErr := callTool(t, s, "g8s_charter_issue", charterArgs("S1"))
		if rpcErr != nil {
			// PR-3 enforcement landed (tool-tier guard mirroring mesh): flip this
			// scenario into a hard refusal assertion.
			t.Logf("tool-tier guard present (PR-3 landed): refusal %+v", rpcErr)
			return
		}
		// CURRENT GAP (B-12, threat model §2 sink table "Charter issue — MCP
		// g8s_charter_issue ... UNGUARDED at tool layer"): the client-chosen
		// issuer stratum was honored and a signed, epoch-fenced charter was
		// minted with no tool-tier guard. Document the observed behavior and
		// skip — PR-1 ships no enforcement (threat model §5); PR-3 closes this.
		var issued struct {
			CharterID     string `json:"charter_id"`
			IssuerStratum string `json:"issuer_stratum"`
			State         string `json:"state"`
		}
		if err := json.Unmarshal(res, &issued); err != nil {
			t.Fatalf("decode issued charter: %v (raw %s)", err, res)
		}
		if issued.IssuerStratum != "S1" || issued.CharterID == "" {
			t.Fatalf("unexpected issued charter: %+v (raw %s)", issued, res)
		}
		t.Skipf("V8 gap documented for PR-3 (issue #977 / B-12): unauthenticated MCP client supplied issuer_stratum=%q and g8s_charter_issue minted charter %s (state=%s) with NO tool-tier guard; expected post-PR-3 refusal mirroring the mesh worker-tier guard (mcp/mesh.go:46-49).", issued.IssuerStratum, issued.CharterID, issued.State)
	})
}
