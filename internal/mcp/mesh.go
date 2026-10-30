package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/mesh"
)

type coordinatorMeshArgs struct {
	MeshDir   string          `json:"mesh_dir"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
}

var validMeshOperations = map[string]bool{
	"register":      true,
	"quota-publish": true,
	"send":          true,
	"inbox":         true,
	"ack":           true,
	"claim":         true,
	"release":       true,
	"accept":        true,
	"snapshot":      true,
	"wait":          true,
}

// coordinatorMeshTool executes coordinator mesh operations against a SQLite mesh ledger.
func (s *Server) coordinatorMeshTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	// Worker context guard: reject invalid env worker context G8S_TIER=F1|F2.
	// No shared control mutation through an F1 tool.
	tier := strings.ToUpper(strings.TrimSpace(os.Getenv("G8S_TIER")))
	if tier == "F1" || tier == "F2" {
		return nil, blockedStatus("blocked_by_policy", fmt.Sprintf("coordinator mesh access denied for tier %s: no shared control mutation through worker tier", tier))
	}

	if len(bytes.TrimSpace(args)) == 0 {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "missing arguments"}
	}

	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()

	var a coordinatorMeshArgs
	if err := dec.Decode(&a); err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("invalid arguments: %v", err)}
	}

	// Reject trailing bytes
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "invalid arguments: trailing bytes after JSON object"}
	}

	// Reject missing or empty mesh_dir
	if strings.TrimSpace(a.MeshDir) == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "missing required field: mesh_dir"}
	}

	// Reject missing or invalid operation enum
	if strings.TrimSpace(a.Operation) == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "missing required field: operation"}
	}
	if !validMeshOperations[a.Operation] {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("invalid operation: %q", a.Operation)}
	}

	// Reject missing or non-object input
	if len(bytes.TrimSpace(a.Input)) == 0 || string(bytes.TrimSpace(a.Input)) == "null" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "missing required field: input"}
	}
	var inputObj map[string]any
	if err := json.Unmarshal(a.Input, &inputObj); err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("invalid input: must be a JSON object: %v", err)}
	}

	// Open store with explicit dir, defer Close, execute with ctx
	store, err := mesh.Open(a.MeshDir)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: fmt.Sprintf("open mesh store: %v", err)}
	}
	defer store.Close()

	res, err := store.Execute(ctx, a.Operation, a.Input)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}

	// Return result as json.RawMessage (real object, not double-encoded JSON)
	return json.RawMessage(res), nil
}
