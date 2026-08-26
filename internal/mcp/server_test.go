package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
)

type fakeControlPlane struct {
	submitted       controlplane.SubmitTaskRequest
	task            *controlplane.Task
	err             error
	tasks           []*controlplane.Task
	listFilter      controlplane.TaskFilter
	cancelledID     string
	cancelledReason string
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

func (f *fakeControlPlane) ListTasks(_ context.Context, filter controlplane.TaskFilter) ([]*controlplane.Task, error) {
	f.listFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*controlplane.Task, 0, len(f.tasks))
	for _, task := range f.tasks {
		if filter.State == nil || task.State == *filter.State {
			out = append(out, task)
		}
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (f *fakeControlPlane) CancelTask(_ context.Context, taskID, reason string) error {
	f.cancelledID = taskID
	f.cancelledReason = reason
	if f.task != nil && f.task.TaskID == taskID {
		f.task.State = "CANCELLED"
	}
	return nil
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

func TestToolsListExposesExactlyTheSpecElevenTools(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	resp := s.handleLine(context.Background(), requestLine("tools/list", nil))
	var result struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{
		"g8s_run", "g8s_submit", "g8s_get", "g8s_receipt_issue", "g8s_self_awareness", "g8s_blast_radius",
		"g8s_dispatch", "g8s_list_tasks", "g8s_cancel_task", "g8s_list_roles", "g8s_list_permissions",
	}
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

// --- T015 Amendment A: surface expansion (spec 04 §2, §4) ---

type fakeDispatcher struct {
	calls   int
	lastReq SyncDispatchRequest
	result  dispatch.Result
	err     error
}

func (f *fakeDispatcher) Dispatch(_ context.Context, req SyncDispatchRequest) (dispatch.Result, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return dispatch.Result{}, f.err
	}
	return f.result, nil
}

type stubProber struct {
	path string
	err  error
}

func (s stubProber) Resolve(string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.path, nil
}

func newExtServer(t *testing.T) (*Server, *fakeControlPlane, *fakeDispatcher) {
	t.Helper()
	cp := &fakeControlPlane{}
	d := &fakeDispatcher{}
	s, _, _, _ := newTestServer(t)
	s.cp = cp
	s.dispatch = d
	s.prober = stubProber{path: "/fake/agy"}
	return s, cp, d
}

func TestInitializeEchoesClientProtocolVersion(t *testing.T) {
	s, _, _ := newExtServer(t)
	resp := s.handleLine(context.Background(), requestLine("initialize", map[string]any{"protocolVersion": "2024-11-05"}))
	var echoed struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(resp.Result, &echoed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if echoed.ProtocolVersion != "2024-11-05" {
		t.Fatalf("client version not echoed: %q", echoed.ProtocolVersion)
	}

	resp = s.handleLine(context.Background(), requestLine("initialize", map[string]any{}))
	if err := json.Unmarshal(resp.Result, &echoed); err != nil {
		t.Fatalf("decode default: %v", err)
	}
	if echoed.ProtocolVersion != defaultProtocolVersion {
		t.Fatalf("default = %q, want %q", echoed.ProtocolVersion, defaultProtocolVersion)
	}
}

func TestListRolesReturnsAllRegisteredProfiles(t *testing.T) {
	s, _, _ := newExtServer(t)
	result, rpcErr := callTool(t, s, "g8s_list_roles", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	var out struct {
		Roles []struct {
			Name      string   `json:"name"`
			Purpose   string   `json:"purpose"`
			Forbidden []string `json:"forbidden"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Roles) != 6 {
		t.Fatalf("got %d roles, want 6", len(out.Roles))
	}
	byName := map[string]struct {
		Purpose   string
		Forbidden []string
	}{}
	for _, r := range out.Roles {
		byName[r.Name] = struct {
			Purpose   string
			Forbidden []string
		}{r.Purpose, r.Forbidden}
	}
	for _, want := range []string{"collector", "scout", "verifier"} {
		r, ok := byName[want]
		if !ok || r.Purpose == "" || len(r.Forbidden) == 0 {
			t.Fatalf("role %q missing purpose/forbidden: %+v", want, r)
		}
	}
}

func TestListPermissionsMarksWorkspaceWriteDisabled(t *testing.T) {
	s, _, _ := newExtServer(t)
	result, rpcErr := callTool(t, s, "g8s_list_permissions", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	var out struct {
		Permissions []struct {
			Name           string `json:"name"`
			MCPEnabled     bool   `json:"mcp_enabled"`
			DisabledReason string `json:"disabled_reason,omitempty"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	enabled := map[string]bool{}
	reasons := map[string]string{}
	for _, p := range out.Permissions {
		enabled[p.Name] = p.MCPEnabled
		reasons[p.Name] = p.DisabledReason
	}
	if len(out.Permissions) != 3 {
		t.Fatalf("got %d permissions, want 3", len(out.Permissions))
	}
	if enabled["workspace_write"] {
		t.Fatal("workspace_write must be disabled on the MCP surface")
	}
	if !strings.Contains(reasons["workspace_write"], "workspace_write") {
		t.Fatalf("disabled_reason must name workspace_write: %q", reasons["workspace_write"])
	}
	if !enabled["read_only"] || !enabled["automation_read"] {
		t.Fatalf("read profiles must stay enabled: %+v", enabled)
	}
}

func TestDispatchRejectsWorkspaceWriteBeforeDispatcherRuns(t *testing.T) {
	s, _, d := newExtServer(t)
	_, rpcErr := callTool(t, s, "g8s_dispatch", map[string]any{
		"prompt":     "mutate things",
		"add_dirs":   []string{"/tmp/a"},
		"permission": "workspace_write",
	})
	if rpcErr == nil || rpcErr.Data == nil {
		t.Fatalf("want blocked_by_policy error, got %+v", rpcErr)
	}
	data := rpcErr.Data.(map[string]any)
	if data["status"] != "blocked_by_policy" {
		t.Fatalf("status = %v, want blocked_by_policy", data["status"])
	}
	if d.calls != 0 {
		t.Fatalf("dispatcher must never run for mutation requests without receipt, ran %d times", d.calls)
	}
}

func TestDispatchRejectsSkipPermissionsForReadOnly(t *testing.T) {
	s, _, d := newExtServer(t)
	_, rpcErr := callTool(t, s, "g8s_dispatch", map[string]any{
		"prompt":           "look around",
		"add_dirs":         []string{"/tmp/a"},
		"skip_permissions": true,
	})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "skip-permissions") {
		t.Fatalf("want skip-permissions guard, got %+v", rpcErr)
	}
	if d.calls != 0 {
		t.Fatal("dispatcher must not run")
	}
}

func TestDispatchRejectsNoSandbox(t *testing.T) {
	s, _, d := newExtServer(t)
	_, rpcErr := callTool(t, s, "g8s_dispatch", map[string]any{
		"prompt":     "x",
		"add_dirs":   []string{"/tmp/a"},
		"no_sandbox": true,
	})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "no_sandbox") {
		t.Fatalf("want no_sandbox guard, got %+v", rpcErr)
	}
	if d.calls != 0 {
		t.Fatal("dispatcher must not run")
	}
}

func TestDispatchRequiresExplicitAddDirs(t *testing.T) {
	s, _, d := newExtServer(t)
	_, rpcErr := callTool(t, s, "g8s_dispatch", map[string]any{"prompt": "x"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "explicit add_dirs") {
		t.Fatalf("want add_dirs guard, got %+v", rpcErr)
	}
	if d.calls != 0 {
		t.Fatal("dispatcher must not run")
	}
}

func TestDispatchReportsSetupRequiredWhenBinaryMissing(t *testing.T) {
	s, _, _ := newExtServer(t)
	s.prober = stubProber{err: fmt.Errorf("exec: %w", fmt.Errorf("not found"))}
	_, rpcErr := callTool(t, s, "g8s_dispatch", map[string]any{
		"prompt": "x", "add_dirs": []string{"/tmp/a"},
	})
	if rpcErr == nil || rpcErr.Data == nil {
		t.Fatalf("want setup_required error, got %+v", rpcErr)
	}
	data := rpcErr.Data.(map[string]any)
	if data["status"] != "setup_required" {
		t.Fatalf("status = %v, want setup_required", data["status"])
	}
	hint, _ := data["setup_hint"].(string)
	if hint == "" {
		t.Fatalf("setup_hint missing in %+v", data)
	}
}

func TestDispatchPassthroughSuccessEnvelopeAndOptions(t *testing.T) {
	s, _, d := newExtServer(t)
	d.result = dispatch.Result{
		OK: true, ReturnCode: 0, HarnessReturnCode: 0, DurationSeconds: 1.5,
		CommandPreview: "agy --sandbox --dangerously-skip-permissions --print-prompt <x>",
		Permission:     "automation_read",
		Stdout:         "worker output", Stderr: "",
	}
	result, rpcErr := callTool(t, s, "g8s_dispatch", map[string]any{
		"prompt":           "inventory the module",
		"role":             "collector",
		"permission":       "automation_read",
		"add_dirs":         []string{"/tmp/a", "/tmp/b"},
		"skip_permissions": true,
	})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if d.calls != 1 {
		t.Fatalf("dispatcher calls = %d, want 1", d.calls)
	}
	if d.lastReq.Permission != "automation_read" || !d.lastReq.SkipPermissions || len(d.lastReq.AddDirs) != 2 || d.lastReq.Role != "collector" {
		t.Fatalf("dispatch options mismatch: %+v", d.lastReq)
	}
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["ok"] != true || out["returncode"] != float64(0) || out["duration_seconds"] != 1.5 || out["permission"] != "automation_read" {
		t.Fatalf("envelope mismatch: %s", result)
	}
	if !strings.Contains(out["command_preview"].(string), "--sandbox") ||
		!strings.Contains(out["command_preview"].(string), "--dangerously-skip-permissions") {
		t.Fatalf("command_preview must keep sandbox and permission flags: %v", out["command_preview"])
	}
	if out["stdout"] != "worker output" {
		t.Fatalf("stdout passthrough failed: %v", out["stdout"])
	}
}

func TestDispatchSurfacesContractViolationAsError(t *testing.T) {
	s, _, d := newExtServer(t)
	d.result = dispatch.Result{
		OK: false, ReturnCode: 0, HarnessReturnCode: dispatch.ReadOnlyContractExit,
		Permission: "read_only",
		ContractViolation: &dispatch.ContractViolationReport{
			Policy:   "read_only",
			ExitCode: dispatch.ReadOnlyContractExit,
			Violations: []dispatch.Violation{
				{Type: "wiki_reflect_side_effect", Snippet: "Session logged to log.md"},
			},
		},
	}
	_, rpcErr := callTool(t, s, "g8s_dispatch", map[string]any{
		"prompt": "reflect please", "add_dirs": []string{"/tmp/a"},
	})
	if rpcErr == nil || rpcErr.Data == nil {
		t.Fatalf("want contract_violation error, got %+v", rpcErr)
	}
	data := rpcErr.Data.(map[string]any)
	if data["status"] != "contract_violation" {
		t.Fatalf("status = %v, want contract_violation", data["status"])
	}
	if rc, ok := data["harness_returncode"].(int); !ok || rc != dispatch.ReadOnlyContractExit {
		t.Fatalf("harness_returncode = %v, want %d", data["harness_returncode"], dispatch.ReadOnlyContractExit)
	}
	report, ok := data["contract_violation"].(*dispatch.ContractViolationReport)
	if !ok {
		t.Fatalf("contract_violation payload missing: %+v", data)
	}
	if len(report.Violations) != 1 || report.Violations[0].Type != "wiki_reflect_side_effect" {
		t.Fatalf("violation list mismatch: %+v", report.Violations)
	}
}

func TestListTasksReturnsSanitizedViewsAndFilter(t *testing.T) {
	s, cp, _ := newExtServer(t)
	cp.tasks = []*controlplane.Task{
		{TaskID: "t1", State: "QUEUED", Attempts: 0},
		{TaskID: "t2", State: "RUNNING", Attempts: 1},
	}
	result, rpcErr := callTool(t, s, "g8s_list_tasks", map[string]any{"state": "QUEUED", "limit": 10})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if cp.listFilter.State == nil || *cp.listFilter.State != "QUEUED" || cp.listFilter.Limit != 10 {
		t.Fatalf("filter mismatch: %+v", cp.listFilter)
	}
	text := string(result)
	if !strings.Contains(text, `"task_id":"t1"`) || !strings.Contains(text, `"count":1`) {
		t.Fatalf("state filter not applied: %s", text)
	}
	if strings.Contains(text, "prompt") {
		t.Fatalf("task views must omit prompt payloads: %s", text)
	}
}

func TestCancelTaskForwardsIDAndReason(t *testing.T) {
	s, cp, _ := newExtServer(t)
	cp.task = &controlplane.Task{TaskID: "t7", State: "RUNNING"}
	result, rpcErr := callTool(t, s, "g8s_cancel_task", map[string]any{"task_id": "t7", "reason": "owner requested"})
	if rpcErr != nil {
		t.Fatalf("rpc error: %+v", rpcErr)
	}
	if cp.cancelledID != "t7" || cp.cancelledReason != "owner requested" {
		t.Fatalf("cancel args mismatch: id=%q reason=%q", cp.cancelledID, cp.cancelledReason)
	}
	if !strings.Contains(string(result), `"state":"CANCELLED"`) {
		t.Fatalf("result should reflect cancelled state: %s", result)
	}
}

func TestSubmitDurableGuardsBlockWriteAndNoSandbox(t *testing.T) {
	s, cp, _ := newExtServer(t)
	_, rpcErr := callTool(t, s, "g8s_submit", map[string]any{
		"idempotency_key": "k-w", "payload": map[string]any{"prompt": "p"}, "permission": "workspace_write",
	})
	if rpcErr == nil || rpcErr.Data == nil {
		t.Fatalf("want blocked_by_policy on submit, got %+v", rpcErr)
	}
	if rpcErr.Data.(map[string]any)["status"] != "blocked_by_policy" {
		t.Fatalf("submit status = %v", rpcErr.Data)
	}
	_, rpcErr = callTool(t, s, "g8s_submit", map[string]any{
		"idempotency_key": "k-s", "payload": map[string]any{"prompt": "p"}, "no_sandbox": true,
	})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "no_sandbox") {
		t.Fatalf("want no_sandbox guard on submit, got %+v", rpcErr)
	}
	if cp.submitted.IdempotencyKey == "k-w" || cp.submitted.IdempotencyKey == "k-s" {
		t.Fatal("blocked submissions must not reach the control plane")
	}
}

func TestDurableRoundTripAgainstRealStoreOmitsPrompt(t *testing.T) {
	dir := t.TempDir()
	store, err := controlplane.NewControlPlane(filepath.Join(dir, "cp.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	issuer := &fakeIssuer{rc: &receipt.WriteReceipt{ReceiptID: "rc-d"}}
	providers := &fakeProviders{}
	s := NewServer(strings.NewReader(""), &bytes.Buffer{}, store, issuer, providers)

	first, rpcErr := callTool(t, s, "g8s_submit", map[string]any{
		"idempotency_key": "d1",
		"model":           "gemini-3.7-flash-high",
		"add_dirs":        []string{"/workspace"},
		"payload":         map[string]any{"prompt": "secret-prompt-text"},
	})
	if rpcErr != nil {
		t.Fatalf("submit: %+v", rpcErr)
	}
	var submitted struct {
		TaskID string `json:"task_id"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(first, &submitted); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if submitted.State != "QUEUED" {
		t.Fatalf("state = %q, want QUEUED", submitted.State)
	}

	dup, rpcErr := callTool(t, s, "g8s_submit", map[string]any{
		"idempotency_key": "d1",
		"model":           "gemini-3.7-flash-high",
		"add_dirs":        []string{"/workspace"},
		"payload":         map[string]any{"prompt": "secret-prompt-text"},
	})
	if rpcErr != nil {
		t.Fatalf("duplicate submit: %+v", rpcErr)
	}
	var dedup struct {
		TaskID       string `json:"task_id"`
		Deduplicated bool   `json:"deduplicated"`
	}
	if err := json.Unmarshal(dup, &dedup); err != nil {
		t.Fatalf("decode dup: %v", err)
	}
	if dedup.TaskID != submitted.TaskID || !dedup.Deduplicated {
		t.Fatalf("idempotency broken: %+v vs %+v", dedup, submitted)
	}

	view, rpcErr := callTool(t, s, "g8s_get", map[string]any{"task_id": submitted.TaskID})
	if rpcErr != nil {
		t.Fatalf("get: %+v", rpcErr)
	}
	if strings.Contains(string(view), "secret-prompt-text") {
		t.Fatalf("get view leaked prompt: %s", view)
	}
	var got struct {
		Request map[string]any `json:"request"`
	}
	if err := json.Unmarshal(view, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if _, present := got.Request["prompt"]; present {
		t.Fatalf("sanitized request view still carries prompt: %v", got.Request)
	}

	listed, rpcErr := callTool(t, s, "g8s_list_tasks", map[string]any{"state": "QUEUED"})
	if rpcErr != nil {
		t.Fatalf("list: %+v", rpcErr)
	}
	if strings.Contains(string(listed), "secret-prompt-text") {
		t.Fatalf("list view leaked prompt: %s", listed)
	}

	cancelled, rpcErr := callTool(t, s, "g8s_cancel_task", map[string]any{
		"task_id": submitted.TaskID, "reason": "round-trip complete",
	})
	if rpcErr != nil {
		t.Fatalf("cancel: %+v", rpcErr)
	}
	if !strings.Contains(string(cancelled), `"state":"CANCELLED"`) {
		t.Fatalf("cancel did not transition state: %s", cancelled)
	}
}
