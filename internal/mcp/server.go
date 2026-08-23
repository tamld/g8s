// Package mcp implements the g8s Model Context Protocol (MCP) server over
// stdio using newline-delimited JSON-RPC 2.0 (DELTA-04).
//
// The server exposes six tools that let a Brain-tier client (Claude Desktop,
// Cursor, Codex, Windsurf) drive the g8s runtime:
//
//	g8s_run             synchronous task execution (Phase 4 supervisor dependency)
//	g8s_submit          durable queue submission via internal/controlplane
//	g8s_get             task status lookup via internal/controlplane
//	g8s_receipt_issue   single-use write receipt via internal/receipt
//	g8s_self_awareness  provider/model discovery via internal/provider
//	g8s_blast_radius    LSP impact analysis (DELTA-07, not yet built)
//
// Dependencies are injected as narrow local interfaces so this package stays
// decoupled from concrete store types and remains trivially testable. Per the
// constitution, everything here is Pure Go with Zero CGO.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
)

// JSON-RPC 2.0 error codes used by this server.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// jsonRPCRequest is one inbound JSON-RPC 2.0 request or notification.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is one outbound JSON-RPC 2.0 response (result XOR error).
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError carries a JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// toolDescriptor describes one callable tool for the tools/list response.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ControlPlaneAPI is the slice of internal/controlplane the MCP server needs.
// *controlplane.Store satisfies it directly.
type ControlPlaneAPI interface {
	SubmitTask(ctx context.Context, req controlplane.SubmitTaskRequest) (*controlplane.Task, error)
	GetTask(ctx context.Context, taskID string) (*controlplane.Task, error)
}

// ReceiptIssuer is the slice of internal/receipt the MCP server needs.
type ReceiptIssuer interface {
	IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration) (*receipt.WriteReceipt, error)
}

// ProviderSource is the slice of internal/provider the MCP server needs.
type ProviderSource interface {
	DiscoverAll(ctx context.Context) ([]provider.ProviderInfo, error)
}

// Server is the stdio JSON-RPC MCP server. Construct with NewServer.
type Server struct {
	in        io.Reader
	out       io.Writer
	cp        ControlPlaneAPI
	receipts  ReceiptIssuer
	providers ProviderSource
	tools     map[string]toolHandler
	list      []toolDescriptor
}

// toolHandler executes one tools/call invocation and returns the result value.
type toolHandler func(ctx context.Context, args json.RawMessage) (any, *jsonRPCError)

// NewServer wires dependencies and registers the DELTA-04 tool set.
func NewServer(in io.Reader, out io.Writer, cp ControlPlaneAPI, receipts ReceiptIssuer, providers ProviderSource) *Server {
	s := &Server{in: in, out: out, cp: cp, receipts: receipts, providers: providers}
	_ = s.RegisterTools()
	return s
}

// RegisterTools builds the tool registry required by the MCPServer contract.
func (s *Server) RegisterTools() error {
	s.tools = map[string]toolHandler{
		"g8s_run":            s.runTool,
		"g8s_submit":         s.submitTool,
		"g8s_get":            s.getTool,
		"g8s_receipt_issue":  s.receiptIssueTool,
		"g8s_self_awareness": s.selfAwarenessTool,
		"g8s_blast_radius":   s.blastRadiusTool,
	}
	s.list = []toolDescriptor{
		{Name: "g8s_run", Description: "Synchronously execute an isolated g8s worker task (requires Phase 4 supervisor).", InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"role":{"type":"string"}},"required":["prompt"]}`)},
		{Name: "g8s_submit", Description: "Queue a durable background task in the SQLite-backed control plane.", InputSchema: json.RawMessage(`{"type":"object","properties":{"idempotency_key":{"type":"string"},"payload":{"type":"object"},"priority":{"type":"integer"},"max_attempts":{"type":"integer"},"model":{"type":"string"}},"required":["idempotency_key","payload"]}`)},
		{Name: "g8s_get", Description: "Fetch sanitized task status by task id.", InputSchema: json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string"}},"required":["task_id"]}`)},
		{Name: "g8s_receipt_issue", Description: "Issue a path-scoped, single-use write receipt (TTL seconds, max 3600).", InputSchema: json.RawMessage(`{"type":"object","properties":{"allowed_paths":{"type":"array","items":{"type":"string"}},"ttl_seconds":{"type":"integer"}},"required":["allowed_paths"]}`)},
		{Name: "g8s_self_awareness", Description: "Report discovered providers, model availability, and concurrency slots.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "g8s_blast_radius", Description: "LSP call-hierarchy impact analysis for a symbol (DELTA-07, pending).", InputSchema: json.RawMessage(`{"type":"object","properties":{"symbol":{"type":"string"}},"required":["symbol"]}`)},
	}
	return nil
}

// ServeStdio reads newline-delimited JSON-RPC requests from stdin and writes
// responses to stdout until ctx is cancelled or input ends. It satisfies the
// DELTA-04 MCPServer interface together with RegisterTools.
func (s *Server) ServeStdio(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		resp := s.handleLine(ctx, line)
		if resp == nil {
			continue // notification: no response required
		}
		encoded, err := json.Marshal(resp)
		if err != nil {
			return fmt.Errorf("mcp: encode response: %w", err)
		}
		if _, err := fmt.Fprintf(s.out, "%s\n", encoded); err != nil {
			return fmt.Errorf("mcp: write response: %w", err)
		}
	}
	return scanner.Err()
}

// handleLine parses one request line and dispatches it. A nil return means
// the line was a notification and needs no response.
func (s *Server) handleLine(ctx context.Context, line []byte) *jsonRPCResponse {
	var req jsonRPCRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, &jsonRPCError{Code: codeParseError, Message: "parse error: request is not valid JSON"})
	}
	if req.Method == "" {
		return errorResponse(req.ID, &jsonRPCError{Code: codeInvalidParams, Message: "missing method"})
	}
	switch req.Method {
	case "initialize":
		return okResponse(req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "g8s", "version": "0.1.0-alpha"},
		})
	case "tools/list":
		return okResponse(req.ID, map[string]any{"tools": s.list})
	case "tools/call":
		return s.handleToolCall(ctx, req.ID, req.Params)
	default:
		return errorResponse(req.ID, &jsonRPCError{Code: codeMethodNotFound, Message: fmt.Sprintf("method not found: %s", req.Method)})
	}
}

// handleToolCall unwraps params.arguments and dispatches to the named tool.
func (s *Server) handleToolCall(ctx context.Context, id json.RawMessage, params json.RawMessage) *jsonRPCResponse {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResponse(id, &jsonRPCError{Code: codeInvalidParams, Message: "invalid params: arguments must be an object"})
		}
	}
	handler, ok := s.tools[p.Name]
	if !ok {
		return errorResponse(id, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", p.Name)})
	}
	result, rpcErr := handler(ctx, p.Arguments)
	if rpcErr != nil {
		return errorResponse(id, rpcErr)
	}
	// MCP wraps tool output in a content envelope.
	payload, err := json.Marshal(map[string]any{
		"content": []map[string]any{{"type": "text", "text": mustJSONString(result)}},
	})
	if err != nil {
		return errorResponse(id, &jsonRPCError{Code: codeInternal, Message: fmt.Sprintf("encode tool result: %v", err)})
	}
	return okResponse(id, json.RawMessage(payload))
}

// runTool reports the Phase 4 dependency honestly instead of pretending.
func (s *Server) runTool(_ context.Context, _ json.RawMessage) (any, *jsonRPCError) {
	return nil, &jsonRPCError{Code: codeInternal, Message: "g8s_run requires the Phase 4 worker supervisor; use g8s_submit"}
}

// blastRadiusTool reports the DELTA-07 dependency honestly.
func (s *Server) blastRadiusTool(_ context.Context, _ json.RawMessage) (any, *jsonRPCError) {
	return nil, &jsonRPCError{Code: codeInternal, Message: "g8s_blast_radius requires DELTA-07 LSP analyzer (not yet built)"}
}

// submitArgs mirrors the g8s_submit tool schema.
type submitArgs struct {
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
	Priority       int             `json:"priority"`
	MaxAttempts    int             `json:"max_attempts"`
	Model          string          `json:"model"`
}

func (s *Server) submitTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a submitArgs
	if err := json.Unmarshal(args, &a); err != nil || a.IdempotencyKey == "" || len(a.Payload) == 0 {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_submit requires idempotency_key and payload"}
	}
	task, err := s.cp.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: a.IdempotencyKey,
		Payload:        a.Payload,
		Priority:       a.Priority,
		MaxAttempts:    a.MaxAttempts,
		Model:          a.Model,
	})
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return map[string]any{"task_id": task.TaskID, "state": task.State}, nil
}

func (s *Server) getTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.TaskID == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_get requires task_id"}
	}
	task, err := s.cp.GetTask(ctx, a.TaskID)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	if task == nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown task: %s", a.TaskID)}
	}
	return map[string]any{"task_id": task.TaskID, "state": task.State, "attempts": task.Attempts, "cancel_requested": task.CancelRequested}, nil
}

func (s *Server) receiptIssueTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		AllowedPaths []string `json:"allowed_paths"`
		TTLSeconds   int      `json:"ttl_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "invalid params for g8s_receipt_issue"}
	}
	ttl := time.Duration(a.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 10 * time.Minute // baseline default when caller omits TTL
	}
	rc, err := s.receipts.IssueReceipt("mcp-server", a.AllowedPaths, ttl)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return rc, nil
}

func (s *Server) selfAwarenessTool(ctx context.Context, _ json.RawMessage) (any, *jsonRPCError) {
	infos, err := s.providers.DiscoverAll(ctx)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return map[string]any{"providers": infos}, nil
}

// mustJSONString renders a tool result as compact JSON text content.
func mustJSONString(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(encoded)
}

func okResponse(id json.RawMessage, result any) *jsonRPCResponse {
	encoded, err := json.Marshal(result)
	if err != nil {
		return errorResponse(id, &jsonRPCError{Code: codeInternal, Message: fmt.Sprintf("encode result: %v", err)})
	}
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: encoded}
}

func errorResponse(id json.RawMessage, rpcErr *jsonRPCError) *jsonRPCResponse {
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
}

// Compile-time check that this server implements the DELTA-04 contract.
var _ interface {
	ServeStdio(ctx context.Context) error
	RegisterTools() error
} = (*Server)(nil)
