package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// OWASP Agentic AI ASI02 — Tool misuse: rogue or over-broad tool registration must
// not expand the MCP server's callable surface beyond RegisterTools().
//
// OWASP Agentic AI ASI04 — Agentic supply chain / schema drift: hostile stdio frames
// and drifted descriptors must be rejected without panic or scope widening.

func TestRed_ASI02_ASI04_RogueToolRegistrationAndSchemaDrift(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	baseline := toolsListNames(t, s)
	if len(baseline) == 0 {
		t.Fatal("expected non-empty tools/list baseline")
	}

	t.Run("unknown_tool_call_rejected", func(t *testing.T) {
		_, rpcErr := callTool(t, s, "g8s_evil_unregistered", map[string]any{"prompt": "p"})
		if rpcErr == nil {
			t.Fatal("expected unknown tool to be rejected")
		}
		if !strings.Contains(rpcErr.Message, "unknown tool") {
			t.Fatalf("want unknown tool error, got: %s", rpcErr.Message)
		}
	})

	t.Run("tools_list_injection_via_params_ignored", func(t *testing.T) {
		resp := s.handleLine(context.Background(), requestLine("tools/list", map[string]any{
			"tools": []map[string]any{{
				"name":        "injected_rogue_tool",
				"description": "must never appear",
				"inputSchema": map[string]any{"type": "object"},
			}},
		}))
		if resp.Error != nil {
			t.Fatalf("tools/list should ignore hostile params, got error: %+v", resp.Error)
		}
		got := toolsListNames(t, s)
		if len(got) != len(baseline) {
			t.Fatalf("ASI02: tools/list injection changed tool count %d -> %d", len(baseline), len(got))
		}
		for i := range baseline {
			if got[i] != baseline[i] {
				t.Fatalf("ASI02: tool registry drift at %d: %q -> %q", i, baseline[i], got[i])
			}
		}
	})

	t.Run("unregistered_method_cannot_register_tools", func(t *testing.T) {
		resp := s.handleLine(context.Background(), requestLine("tools/register", map[string]any{
			"name": "rogue",
		}))
		if resp.Error == nil {
			t.Fatal("expected tools/register method not found")
		}
		if resp.Error.Code != codeMethodNotFound {
			t.Fatalf("want method not found, got code %d", resp.Error.Code)
		}
	})

	t.Run("schema_drifted_tools_call_invalid_arguments", func(t *testing.T) {
		params := json.RawMessage(`{"name":"g8s_read","arguments":["not-an-object"]}`)
		resp := s.handleLine(context.Background(), requestLine("tools/call", params))
		if resp.Error == nil {
			t.Fatal("expected invalid params for drifted arguments shape")
		}
		if resp.Error.Code != codeInvalidParams {
			t.Fatalf("want invalid params, got code %d msg=%s", resp.Error.Code, resp.Error.Message)
		}
	})

	t.Run("stdio_replay_does_not_widen_tool_scope", func(t *testing.T) {
		in := strings.Join([]string{
			string(requestLine("initialize", map[string]any{})),
			string(requestLine("tools/list", nil)),
			string(requestLine("tools/list", nil)),
			string(requestLine("tools/call", map[string]any{
				"name":      "g8s_not_real",
				"arguments": map[string]any{},
			})),
		}, "\n") + "\n"
		var out bytes.Buffer
		replaySrv := NewServer(strings.NewReader(in), &out, &fakeControlPlane{}, &fakeIssuer{}, &fakeProviders{})
		if err := replaySrv.ServeStdio(context.Background()); err != nil {
			t.Fatalf("ServeStdio replay: %v", err)
		}
		after := toolsListNames(t, replaySrv)
		if len(after) != len(baseline) {
			t.Fatalf("ASI04: replay changed tool count %d -> %d", len(baseline), len(after))
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 4 {
			t.Fatalf("want 4 JSON-RPC responses, got %d", len(lines))
		}
		var fourth jsonRPCResponse
		if err := json.Unmarshal([]byte(lines[3]), &fourth); err != nil {
			t.Fatalf("decode fourth response: %v", err)
		}
		if fourth.Error == nil || !strings.Contains(fourth.Error.Message, "unknown tool") {
			t.Fatalf("ASI04: replayed rogue tools/call should fail closed, got %+v", fourth.Error)
		}
	})
}

func toolsListNames(t *testing.T, s *Server) []string {
	t.Helper()
	initSession(t, s)
	resp := s.handleLine(context.Background(), requestLine("tools/list", nil))
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var result struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	names := make([]string, 0, len(result.Tools))
	for _, td := range result.Tools {
		names = append(names, td.Name)
	}
	return names
}
