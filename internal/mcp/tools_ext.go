package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/harness"
)

// defaultProtocolVersion answers initialize calls that omit a version.
const defaultProtocolVersion = "2025-06-18"

// negotiateProtocolVersion echoes the client-requested protocol version
// verbatim (DELTA-04 Amendment A §4.1) or falls back to the default.
func negotiateProtocolVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil || p.ProtocolVersion == "" {
		return defaultProtocolVersion
	}
	return p.ProtocolVersion
}

// SyncDispatchRequest carries one guarded synchronous dispatch call.
type SyncDispatchRequest struct {
	Prompt          string
	Role            string
	Permission      string
	Timeout         string
	AddDirs         []string
	SkipPermissions bool
	NoSandbox       bool
	ReceiptID       string
}

// SyncDispatcher executes one bounded synchronous worker dispatch.
type SyncDispatcher interface {
	Dispatch(ctx context.Context, req SyncDispatchRequest) (dispatch.Result, error)
}

// BinaryProber resolves the worker CLI binary before any dispatch runs so a
// missing installation surfaces as setup_required instead of exec noise.
type BinaryProber interface {
	Resolve(explicit string) (string, error)
}

// NewSyncDispatcher returns the production dispatcher backed by internal/dispatch.
func NewSyncDispatcher() SyncDispatcher {
	return syncDispatcherFunc(func(ctx context.Context, req SyncDispatchRequest) (dispatch.Result, error) {
		return dispatch.Run(dispatch.RunOptions{
			Prompt:          req.Prompt,
			Role:            req.Role,
			Permission:      req.Permission,
			Timeout:         req.Timeout,
			AddDirs:         req.AddDirs,
			SkipPermissions: req.SkipPermissions,
			NoSandbox:       req.NoSandbox,
			ReceiptID:       req.ReceiptID,
		})
	})
}

type syncDispatcherFunc func(ctx context.Context, req SyncDispatchRequest) (dispatch.Result, error)

func (f syncDispatcherFunc) Dispatch(ctx context.Context, req SyncDispatchRequest) (dispatch.Result, error) {
	return f(ctx, req)
}

// defaultBinaryProber uses the host resolver chain from internal/dispatch.
var defaultBinaryProber BinaryProber = binaryProberFunc(func(explicit string) (string, error) {
	return dispatch.ResolveBinary(explicit, dispatch.ResolveOptions{})
})

type binaryProberFunc func(explicit string) (string, error)

func (f binaryProberFunc) Resolve(explicit string) (string, error) { return f(explicit) }

// blockedStatus builds a machine-readable policy guard error (Amendment A §4.4).
func blockedStatus(status, message string) *jsonRPCError {
	return &jsonRPCError{
		Code:    codeInvalidParams,
		Message: message,
		Data:    map[string]any{"status": status, "reason": message},
	}
}

// dispatchTool runs the guard chain, then one bounded synchronous dispatch.
func (s *Server) dispatchTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		Prompt          string   `json:"prompt"`
		Role            string   `json:"role"`
		Permission      string   `json:"permission"`
		Timeout         string   `json:"timeout"`
		AddDirs         []string `json:"add_dirs"`
		SkipPermissions bool     `json:"skip_permissions"`
		NoSandbox       bool     `json:"no_sandbox"`
		ReceiptID       string   `json:"receipt_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Prompt == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_dispatch requires prompt"}
	}
	if a.Permission == "" {
		a.Permission = "read_only"
	}
	profile, err := harness.GetPermission(a.Permission)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: err.Error()}
	}
	if profile.MutationAllowed {
		return nil, blockedStatus("blocked_by_policy",
			fmt.Sprintf("permission %s is disabled on the MCP surface (requires delegated write receipt)", a.Permission))
	}
	if a.SkipPermissions && !profile.SkipPermissionsAllowed {
		return nil, blockedStatus("blocked_by_harness",
			fmt.Sprintf("skip-permissions is not allowed for permission %s", a.Permission))
	}
	if a.NoSandbox {
		return nil, blockedStatus("blocked_by_sandbox_policy",
			"no_sandbox is rejected on the MCP surface; the OS sandbox always stays enabled")
	}
	if len(a.AddDirs) == 0 {
		return nil, blockedStatus("blocked_missing_add_dirs",
			"g8s_dispatch requires explicit add_dirs so workers never gain unscoped filesystem access")
	}
	if _, proberErr := s.prober.Resolve(""); proberErr != nil {
		return nil, &jsonRPCError{
			Code:    codeInternal,
			Message: fmt.Sprintf("worker binary not found: %v", proberErr),
			Data: map[string]any{
				"status":     "setup_required",
				"reason":     "worker binary not found",
				"setup_hint": "install the agy CLI (npm i -g agy) or point AGY_BIN at the binary",
			},
		}
	}
	result, runErr := s.dispatch.Dispatch(ctx, SyncDispatchRequest{
		Prompt:          a.Prompt,
		Role:            a.Role,
		Permission:      a.Permission,
		Timeout:         a.Timeout,
		AddDirs:         a.AddDirs,
		SkipPermissions: a.SkipPermissions,
		NoSandbox:       a.NoSandbox,
		ReceiptID:       a.ReceiptID,
	})
	if runErr != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: runErr.Error()}
	}
	if result.ContractViolation != nil || result.HarnessReturnCode == dispatch.ReadOnlyContractExit {
		return nil, &jsonRPCError{
			Code:    codeInternal,
			Message: "read-only contract violation detected in worker output",
			Data: map[string]any{
				"status":             "contract_violation",
				"harness_returncode": result.HarnessReturnCode,
				"contract_violation": result.ContractViolation,
			},
		}
	}
	return map[string]any{
		"ok":                 result.OK,
		"returncode":         result.ReturnCode,
		"harness_returncode": result.HarnessReturnCode,
		"duration_seconds":   result.DurationSeconds,
		"command_preview":    result.CommandPreview,
		"permission":         result.Permission,
		"stdout":             result.Stdout,
		"stderr":             result.Stderr,
	}, nil
}

// sanitizeRequestView strips prompt material from a stored request payload
// before it crosses the MCP boundary (Amendment A §4.2).
func sanitizeRequestView(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return json.RawMessage("null")
	}
	delete(obj, "prompt")
	encoded, err := json.Marshal(obj)
	if err != nil {
		return json.RawMessage("null")
	}
	return encoded
}

// taskView projects a durable task into its sanitized MCP wire shape.
func taskView(task *controlplane.Task) map[string]any {
	view := map[string]any{
		"task_id":          task.TaskID,
		"state":            task.State,
		"priority":         task.Priority,
		"attempts":         task.Attempts,
		"max_attempts":     task.MaxAttempts,
		"cancel_requested": task.CancelRequested,
		"created_at":       task.CreatedAt,
		"updated_at":       task.UpdatedAt,
		"request":          sanitizeRequestView(task.Request),
	}
	if task.ParentTaskID != nil {
		view["parent_task_id"] = *task.ParentTaskID
	}
	if task.LastError != nil {
		view["last_error"] = *task.LastError
	}
	return view
}

// listTasksTool lists durable tasks filtered by state with sanitized views.
func (s *Server) listTasksTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		State string `json:"state"`
		Limit int    `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_list_tasks params must be an object"}
		}
	}
	filter := controlplane.TaskFilter{Limit: a.Limit}
	if a.State != "" {
		filter.State = &a.State
	}
	tasks, err := s.cp.ListTasks(ctx, filter)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	views := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, taskView(task))
	}
	return map[string]any{"tasks": views, "count": len(views)}, nil
}

// cancelTaskTool requests cooperative cancellation with an audit reason.
func (s *Server) cancelTaskTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		TaskID string `json:"task_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.TaskID == "" || strings.TrimSpace(a.Reason) == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_cancel_task requires task_id and reason"}
	}
	if err := s.cp.CancelTask(ctx, a.TaskID, a.Reason); err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	task, err := s.cp.GetTask(ctx, a.TaskID)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	if task == nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown task: %s", a.TaskID)}
	}
	return map[string]any{"task_id": task.TaskID, "state": task.State}, nil
}

// listRolesTool enumerates registered role profiles.
func (s *Server) listRolesTool(_ context.Context, _ json.RawMessage) (any, *jsonRPCError) {
	profiles := make([]harness.RoleProfile, 0, len(harness.RoleNames()))
	for _, name := range harness.RoleNames() {
		role, err := harness.GetRole(name)
		if err != nil {
			return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
		}
		profiles = append(profiles, role)
	}
	return map[string]any{"roles": profiles}, nil
}

// listPermissionsTool enumerates permission profiles with MCP enablement
// metadata (Amendment A §4.3).
func (s *Server) listPermissionsTool(_ context.Context, _ json.RawMessage) (any, *jsonRPCError) {
	entries := make([]map[string]any, 0, len(harness.PermissionNames()))
	for _, name := range harness.PermissionNames() {
		profile, err := harness.GetPermission(name)
		if err != nil {
			return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
		}
		entry := map[string]any{
			"name":                     profile.Name,
			"description":              profile.Description,
			"mutation_allowed":         profile.MutationAllowed,
			"skip_permissions_allowed": profile.SkipPermissionsAllowed,
			"max_prompt_chars":         profile.MaxPromptChars,
			"mcp_enabled":              true,
		}
		if profile.MutationAllowed {
			entry["mcp_enabled"] = false
			entry["disabled_reason"] = fmt.Sprintf(
				"%s requires an explicit Brain write receipt which cannot be carried through MCP tool arguments", name)
		}
		entries = append(entries, entry)
	}
	return map[string]any{"permissions": entries}, nil
}
