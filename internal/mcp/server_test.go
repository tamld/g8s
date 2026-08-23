package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
)

type fakeControlPlane struct {
	submitted controlplane.SubmitTaskRequest
	task      *controlplane.Task
	err       error
}

func (f *fakeControlPlane) SubmitTask(_ context.Context, req controlplane.SubmitTaskRequest) (*controlplane.Task, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.submitted = req
	return &controlplane.Task{TaskID: "task-123", State: "QUEUED"}, nil
}

func (f *fakeControlPlane) GetTask(_ context.Context, taskID string) (*controlplane.Task, error) {
	if f.task == nil {
		return nil, nil
	}
	return f.task, nil
}

type fakeIssuer struct {
	issuer string
	paths  []string
	ttl    time.Duration
	rc     *receipt.WriteReceipt
	err    error
}

func (f *fakeIssuer) IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration) (*receipt.WriteReceipt, error) {
	f.issuer = issuer
	f.paths = allowedPaths
	f.ttl = ttl
	if f.err != nil {
		return nil, f.err
	}
	return f.rc, nil
}

type fakeProviders struct {
	infos []provider.ProviderInfo
}

func (f *fakeProviders) DiscoverAll(_ context.Context) ([]provider.ProviderInfo, error) {
	return f.infos, nil
}

func newTestServer(t *testing.T) (*Server, *fakeControlPlane, *fakeIssuer, *fakeProviders) {
	t.Helper()
	cp := &fakeControlPlane{}
	issuer := &fakeIssuer{rc: &receipt.WriteReceipt{ReceiptID: "rc-1", Issuer: "mcp-server"}}
	providers := &fakeProviders{infos: []provider.ProviderInfo{{Name: "agy", Status: provider.StatusReady}}}
	return NewServer(strings.NewReader(""), &bytes.Buffer{}, cp, issuer, providers), cp, issuer, providers
}

// callTool drives tools/call directly and decodes the content envelope.
func callTool(t *testing.T, s *Server, name string, args map[string]any) (json.RawMessage, *jsonRPCError) {
	t.Helper()
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	params := json.RawMessage(mustParams(map[string]json.RawMessage{"name": rawString(name), "arguments": encodedArgs}))
	resp := s.handleLine(context.Background(), requestLine("tools/call", params))
	if resp.Error != nil {
		return nil, resp.Error
	}
	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if len(envelope.Content) != 1 || envelope.Content[0].Type != "text" {
		t.Fatalf("unexpected envelope: %s", resp.Result)
	}
	return json.RawMessage(envelope.Content[0].Text), nil
}

func rawString(v string) json.RawMessage {
	encoded, _ := json.Marshal(v)
	return encoded
}

func requestLine(method string, params any) []byte {
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return line
}

func mustParams(v any) []byte {
	encoded, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return encoded
}

func TestInitializeHandshakeReturnsServerInfo(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	resp := s.handleLine(context.Background(), requestLine("initialize", map[string]any{}))
	if resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp.Error)
	}
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.ServerInfo.Name != "g8s" {
		t.Fatalf("serverInfo.name = %q, want g8s", result.ServerInfo.Name)
	}
}

func TestToolsListExposesExactlyTheSpecSixTools(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	resp := s.handleLine(context.Background(), requestLine("tools/list", nil))
	var result struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"g8s_run", "g8s_submit", "g8s_get", "g8s_receipt_issue", "g8s_self_awareness", "g8s_blast_radius"}
	if len(result.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(result.Tools), len(want))
	}
	for i, tool := range result.Tools {
		if tool.Name != want[i] {
			t.Fatalf("tool[%d] = %q, want %q", i, tool.Name, want[i])
		}
	}
}

func TestUnknownMethodReturnsMinus32601(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	resp := s.handleLine(context.Background(), requestLine("resources/list", nil))
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("want -32601, got %+v", resp.Error)
	}
}

func TestUnknownToolReturnsInvalidParams(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	_, rpcErr := callTool(t, s, "g8s_nonexistent", nil)
	if rpcErr == nil || rpcErr.Code != codeInvalidParams {
		t.Fatalf("want invalid params, got %+v", rpcErr)
	}
}

func TestMalformedJSONLineYieldsParseErrorWithNullID(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	resp := s.handleLine(context.Background(), []byte("{not json"))
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Fatalf("want parse error, got %+v", resp.Error)
	}
	if len(resp.ID) != 0 {
		t.Fatalf("parse-error id must be empty, got %s", resp.ID)
	}
}

func TestSubmitToolMapsToControlPlaneSubmission(t *testing.T) {
	s, cp, _, _ := newTestServer(t)
	result, rpcErr := callTool(t, s, "g8s_submit", map[string]any{
		"idempotency_key": "k1",
		"payload":         map[string]any{"prompt": "hi"},
		"priority":        5,
		"max_attempts":    3,
		"model":           "gemini-3.7-flash-high",
	})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if cp.submitted.IdempotencyKey != "k1" || cp.submitted.Priority != 5 || cp.submitted.MaxAttempts != 3 || cp.submitted.Model != "gemini-3.7-flash-high" {
		t.Fatalf("submitted request mismatch: %+v", cp.submitted)
	}
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if out["task_id"] != "task-123" || out["state"] != "QUEUED" {
		t.Fatalf("result = %s", result)
	}
}

func TestSubmitToolRejectsMissingIdempotencyKey(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	_, rpcErr := callTool(t, s, "g8s_submit", map[string]any{"payload": map[string]any{}})
	if rpcErr == nil || rpcErr.Code != codeInvalidParams {
		t.Fatalf("want invalid params, got %+v", rpcErr)
	}
}

func TestGetToolReturnsSanitizedTaskView(t *testing.T) {
	s, cp, _, _ := newTestServer(t)
	cp.task = &controlplane.Task{TaskID: "t9", State: "RUNNING", Attempts: 2, CancelRequested: true}
	result, rpcErr := callTool(t, s, "g8s_get", map[string]any{"task_id": "t9"})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	text := string(result)
	for _, want := range []string{`"task_id":"t9"`, `"state":"RUNNING"`, `"cancel_requested":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("result missing %s: %s", want, text)
		}
	}
}

func TestGetToolReportsUnknownTaskWithoutPanic(t *testing.T) {
	s, cp, _, _ := newTestServer(t)
	cp.task = nil
	_, rpcErr := callTool(t, s, "g8s_get", map[string]any{"task_id": "missing"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "unknown task: missing") {
		t.Fatalf("want unknown task message, got %+v", rpcErr)
	}
}

func TestReceiptIssueToolPassesTTLAndPaths(t *testing.T) {
	s, _, issuer, _ := newTestServer(t)
	result, rpcErr := callTool(t, s, "g8s_receipt_issue", map[string]any{
		"allowed_paths": []string{"src/**"},
		"ttl_seconds":   600,
	})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if issuer.ttl != 10*time.Minute || len(issuer.paths) != 1 || issuer.paths[0] != "src/**" || issuer.issuer != "mcp-server" {
		t.Fatalf("issuer args mismatch: ttl=%v paths=%v issuer=%q", issuer.ttl, issuer.paths, issuer.issuer)
	}
	var rc receipt.WriteReceipt
	if err := json.Unmarshal(result, &rc); err != nil || rc.ReceiptID != "rc-1" {
		t.Fatalf("receipt roundtrip failed: %v %s", err, result)
	}
}

func TestSelfAwarenessListsProviderInfos(t *testing.T) {
	s, _, _, providers := newTestServer(t)
	result, rpcErr := callTool(t, s, "g8s_self_awareness", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if !strings.Contains(string(result), `"name":"agy"`) || !strings.Contains(string(result), `"status":"READY"`) {
		t.Fatalf("provider info missing: %s", result)
	}
	if len(providers.infos) != 1 {
		t.Fatalf("providers stub consumed incorrectly")
	}
}

func TestRunAndBlastRadiusDisclosePendingDependencies(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	for _, name := range []string{"g8s_run", "g8s_blast_radius"} {
		_, rpcErr := callTool(t, s, name, map[string]any{})
		if rpcErr == nil || rpcErr.Code != codeInternal {
			t.Fatalf("%s should return internal error, got %+v", name, rpcErr)
		}
	}
}

func TestServeStdioRoundTripsTwoRequestsEndToEnd(t *testing.T) {
	cp := &fakeControlPlane{}
	issuer := &fakeIssuer{rc: &receipt.WriteReceipt{ReceiptID: "rc-9"}}
	providers := &fakeProviders{infos: nil}
	in := strings.Join([]string{
		string(requestLine("tools/list", nil)),
		string(requestLine("tools/call", map[string]any{
			"name":      "g8s_submit",
			"arguments": map[string]any{"idempotency_key": "e2e", "payload": map[string]any{"prompt": "p"}},
		})),
	}, "\n") + "\n"
	var out bytes.Buffer
	server := NewServer(strings.NewReader(in), &out, cp, issuer, providers)
	if err := server.ServeStdio(context.Background()); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 responses, got %d: %q", len(lines), out.String())
	}
	var second jsonRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if second.Error != nil || !strings.Contains(string(second.Result), "task-123") {
		t.Fatalf("submit response unexpected: %s", lines[1])
	}
}

func TestInternalErrorsSurfaceAsRPCErrorData(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	s.cp = &fakeControlPlane{err: fmt.Errorf("boom")}
	_, rpcErr := callTool(t, s, "g8s_submit", map[string]any{"idempotency_key": "x", "payload": map[string]any{}})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "boom") {
		t.Fatalf("want injected failure surfaced, got %+v", rpcErr)
	}
}
