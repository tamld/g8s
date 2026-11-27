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
// g8s_charter_issue. PR-2 (#977) landed the enforcement half: the tool mirrors
// the mesh worker-tier guard (coordinatorMeshTool, mcp/mesh.go — the reference
// GATED pattern), derives the issuer SERVER-side from the process env via
// charter.IssuingStratumFromEnv, and rejects client-supplied issuer fields.
// The V8 skip recorded by PR-1 is FLIPPED into hard-refusal assertions here.
//
// Scenario shape (all refusals are typed blocked_by_policy guard errors):
//  1. the post-#976 initialize handshake gate exists (tools/call before
//     initialize -> -32002);
//  2. a client-supplied issuer_stratum (ANY value, valid or not) is refused
//     at the tool layer — the issuer is a server-side fact, never an input;
//  3. a server that does not declare an issuing stratum (S0/S1) cannot mint
//     at all (E_STRATUM_ADJACENCY_VIOLATION, fail-closed);
//  4. positive control: a coordinator-tier server (G8S_STRATUM=S1) mints under
//     its own declared stratum, proving the gate is not vacuously denying.
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

	t.Run("client_chosen_issuer_stratum_refused_at_tool_layer", func(t *testing.T) {
		// V8 laundering attempt (was the PR-1 skip): an unauthenticated client
		// asserts issuer_stratum=S1. PR-2 enforcement: the tool refuses the
		// client-chosen issuer outright with a typed guard error — nothing is
		// minted, regardless of the claimed stratum's validity.
		for _, issuer := range []string{"S1", "S0", "F1"} {
			res, rpcErr := callTool(t, s, "g8s_charter_issue", charterArgs(issuer))
			if rpcErr == nil {
				t.Fatalf("client-supplied issuer_stratum=%q must be refused at the tool layer (V8/B-12), got minted %+v", issuer, res)
			}
			if rpcErr.Code != codeInvalidParams {
				t.Fatalf("Code = %d, want %d (blocked_by_policy guard), got %+v", rpcErr.Code, codeInvalidParams, rpcErr)
			}
			if data, ok := rpcErr.Data.(map[string]any); !ok || data["status"] != "blocked_by_policy" {
				t.Fatalf("expected typed blocked_by_policy guard error for issuer %q, got %+v", issuer, rpcErr)
			}
			if !strings.Contains(rpcErr.Message, "issuer_stratum") {
				t.Fatalf("refusal must cite the rejected client field, got %q", rpcErr.Message)
			}
		}
	})

	t.Run("undeclared_server_stratum_cannot_mint", func(t *testing.T) {
		// Fail-closed: with no issuing stratum declared on the server process
		// (G8S_STRATUM unset -> S2 default), NO charter can be minted even
		// without a client issuer field (E_STRATUM_ADJACENCY_VIOLATION).
		res, rpcErr := callTool(t, s, "g8s_charter_issue", charterArgs(""))
		if rpcErr == nil {
			t.Fatalf("undeclared server stratum must not mint charters, got %+v", res)
		}
		if !strings.Contains(rpcErr.Message, "E_STRATUM_ADJACENCY_VIOLATION") {
			t.Fatalf("expected adjacency refusal in message, got %q", rpcErr.Message)
		}
	})

	t.Run("positive_control_coordinator_tier_server_mints_under_own_stratum", func(t *testing.T) {
		// The gate is not vacuously denying: a coordinator-tier server
		// (G8S_STRATUM=S1) mints, and the signed charter records the SERVER's
		// stratum — the client has no field that can influence it.
		t.Setenv("G8S_STRATUM", "S1")
		res, rpcErr := callTool(t, s, "g8s_charter_issue", charterArgs(""))
		if rpcErr != nil {
			t.Fatalf("coordinator-tier server must mint under its declared stratum, got %+v", rpcErr)
		}
		var issued struct {
			CharterID     string `json:"charter_id"`
			IssuerStratum string `json:"issuer_stratum"`
			State         string `json:"state"`
		}
		if err := json.Unmarshal(res, &issued); err != nil {
			t.Fatalf("decode issued charter: %v (raw %s)", err, res)
		}
		if issued.IssuerStratum != "S1" || issued.CharterID == "" || issued.State == "" {
			t.Fatalf("unexpected issued charter: %+v (raw %s)", issued, res)
		}
	})
}
