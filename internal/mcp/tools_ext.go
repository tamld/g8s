package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/charter"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/contextpack"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/fsops"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/search"
)

// ReceiptVerifier is an optional extension of ReceiptIssuer that allows the
// MCP server to verify write receipts before executing g8s_edit/g8s_read/g8s_search.
type ReceiptVerifier interface {
	VerifyReceipt(receiptID string) (*receipt.WriteReceipt, error)
}

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
	resp := map[string]any{
		"ok":                 result.OK,
		"returncode":         result.ReturnCode,
		"harness_returncode": result.HarnessReturnCode,
		"duration_seconds":   result.DurationSeconds,
		"command_preview":    result.CommandPreview,
		"permission":         result.Permission,
		"role":               result.Role,
		"model":              result.Model,
		"worker_binary":      result.AGYBin,
		"add_dirs":           result.AddDirs,
		"stdout":             result.Stdout,
		"stderr":             result.Stderr,
	}
	if a.ReceiptID != "" {
		resp["receipt_id"] = a.ReceiptID
	}
	return resp, nil
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

// ReceiptConsumer is an optional extension of ReceiptVerifier (satisfied by
// *receipt.Manager) that atomically validates and marks a single-use write
// receipt as consumed during MCP mutation requests.
type ReceiptConsumer interface {
	ValidateAndConsume(receiptID string, consumerTaskID string) (*receipt.WriteReceipt, error)
}

// resolveMCPDelegation validates the delegation context and optional receipt_id.
func (s *Server) resolveMCPDelegation(flagReceiptID, flagScopeRoot string, isMutation bool) (string, []string, *jsonRPCError) {
	receiptID, scopeRoot, err := fsops.ResolveDelegationContext(flagReceiptID, flagScopeRoot, isMutation)
	if err != nil {
		return "", nil, blockedStatus("blocked_by_policy", err.Error())
	}
	if isMutation && receiptID == "" {
		return "", nil, blockedStatus("blocked_by_policy", "g8s_edit requires an explicit receipt_id or G8S_RECEIPT_ID environment binding")
	}
	if receiptID != "" && s.receipts != nil {
		if v, ok := s.receipts.(ReceiptVerifier); ok {
			rc, err := v.VerifyReceipt(receiptID)
			if err != nil {
				return "", nil, blockedStatus("blocked_by_policy", fmt.Sprintf("verify receipt %s: %v", receiptID, err))
			}
			if rc.Consumed {
				return "", nil, blockedStatus("blocked_by_policy", fmt.Sprintf("receipt %s already consumed", receiptID))
			}
			return scopeRoot, rc.AllowedPaths, nil
		}
	}
	return scopeRoot, nil, nil
}

func preflightMCPEdit(ctx context.Context, path, scopeRoot string, allowedPaths []string, ifSHA256 string) *jsonRPCError {
	if _, err := fsops.VerifyScope(path, scopeRoot, allowedPaths); err != nil {
		return blockedStatus("blocked_by_policy", err.Error())
	}
	if ifSHA256 != "" {
		readRes, err := fsops.Read(ctx, fsops.ReadOptions{
			Path:         path,
			ScopeRoot:    scopeRoot,
			AllowedPaths: allowedPaths,
			StartLine:    1,
			EndLine:      1,
			MaxBytes:     64,
		})
		if err != nil {
			return &jsonRPCError{Code: codeInternal, Message: err.Error()}
		}
		if !strings.EqualFold(ifSHA256, readRes.SHA256) {
			return &jsonRPCError{Code: codeInternal, Message: fmt.Sprintf("sha256 conflict on %s: expected %s, got %s", path, ifSHA256, readRes.SHA256)}
		}
	}
	return nil
}

func (s *Server) consumeMCPReceipt(flagReceiptID, flagScopeRoot string) *jsonRPCError {
	receiptID, _, err := fsops.ResolveDelegationContext(flagReceiptID, flagScopeRoot, true)
	if err != nil {
		return blockedStatus("blocked_by_policy", err.Error())
	}
	if receiptID != "" && s.receipts != nil {
		if c, ok := s.receipts.(ReceiptConsumer); ok {
			if _, consumeErr := c.ValidateAndConsume(receiptID, "mcp-fs-edit"); consumeErr != nil {
				return blockedStatus("blocked_by_policy", fmt.Sprintf("consume receipt %s: %v", receiptID, consumeErr))
			}
		}
	}
	return nil
}

// searchTool executes native pure-Go repository search (DELTA-22 / DELTA-23).
func (s *Server) searchTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		Pattern      string   `json:"pattern"`
		Roots        []string `json:"roots"`
		FixedStrings bool     `json:"fixed_strings"`
		IgnoreCase   bool     `json:"ignore_case"`
		Globs        []string `json:"globs"`
		FilesOnly    bool     `json:"files_only"`
		MaxResults   int      `json:"max_results"`
		ScopeRoot    string   `json:"scope_root"`
		ReceiptID    string   `json:"receipt_id"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_search params must be an object"}
		}
	}
	if !a.FilesOnly && a.Pattern == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_search requires pattern unless files_only is true"}
	}
	scopeRoot, allowedPaths, rpcErr := s.resolveMCPDelegation(a.ReceiptID, a.ScopeRoot, false)
	if rpcErr != nil {
		return nil, rpcErr
	}
	res, err := search.Execute(ctx, search.Options{
		Pattern:      a.Pattern,
		Roots:        a.Roots,
		FixedStrings: a.FixedStrings,
		IgnoreCase:   a.IgnoreCase,
		Globs:        a.Globs,
		FilesOnly:    a.FilesOnly,
		MaxResults:   a.MaxResults,
		ScopeRoot:    scopeRoot,
		AllowedPaths: allowedPaths,
	})
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return res, nil
}

// readTool executes native pure-Go bounded file reading (DELTA-23).
func (s *Server) readTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		MaxBytes  int    `json:"max_bytes"`
		ScopeRoot string `json:"scope_root"`
		ReceiptID string `json:"receipt_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Path) == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_read requires path"}
	}
	scopeRoot, allowedPaths, rpcErr := s.resolveMCPDelegation(a.ReceiptID, a.ScopeRoot, false)
	if rpcErr != nil {
		return nil, rpcErr
	}
	res, err := fsops.Read(ctx, fsops.ReadOptions{
		Path:         a.Path,
		ScopeRoot:    scopeRoot,
		StartLine:    a.StartLine,
		EndLine:      a.EndLine,
		MaxBytes:     a.MaxBytes,
		AllowedPaths: allowedPaths,
	})
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return res, nil
}

// editTool executes native pure-Go atomic file editing with CAS SHA-256 and receipt enforcement (DELTA-23).
func (s *Server) editTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		IfSHA256   string `json:"if_sha256"`
		ScopeRoot  string `json:"scope_root"`
		ReceiptID  string `json:"receipt_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Path) == "" || a.OldString == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_edit requires path and old_string"}
	}
	scopeRoot, allowedPaths, rpcErr := s.resolveMCPDelegation(a.ReceiptID, a.ScopeRoot, true)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if rpcErr := preflightMCPEdit(ctx, a.Path, scopeRoot, allowedPaths, a.IfSHA256); rpcErr != nil {
		return nil, rpcErr
	}
	if rpcErr := s.consumeMCPReceipt(a.ReceiptID, a.ScopeRoot); rpcErr != nil {
		return nil, rpcErr
	}
	res, err := fsops.Edit(ctx, fsops.EditOptions{
		Path:         a.Path,
		ScopeRoot:    scopeRoot,
		OldString:    a.OldString,
		NewString:    a.NewString,
		ReplaceAll:   a.ReplaceAll,
		IfSHA256:     a.IfSHA256,
		AllowedPaths: allowedPaths,
	})
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return res, nil
}

type selectArgs struct {
	Input    string `json:"input"`
	Selector string `json:"selector"`
}

// selectTool executes native pure-Go JSON dot-path selection (DELTA-23).
func (s *Server) selectTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a selectArgs
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Input) == "" || strings.TrimSpace(a.Selector) == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_select requires input and selector"}
	}
	val, err := fsops.SelectJSON([]byte(a.Input), a.Selector)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("select error: %v", err)}
	}
	return map[string]any{
		"selector": a.Selector,
		"value":    json.RawMessage(val),
	}, nil
}

type llmCallArgs struct {
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	Prompt       string   `json:"prompt"`
	System       string   `json:"system"`
	Effort       string   `json:"effort"`
	MaxTokens    int      `json:"max_tokens"`
	Temperature  *float64 `json:"temperature"`
	Timeout      string   `json:"timeout"`
	ScopeRoot    string   `json:"scope_root"`
	ContextFiles []string `json:"context_files"`
}

// llmCallTool executes a direct synchronous LLM call against configured providers (DELTA-25).
func (s *Server) llmCallTool(ctx context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a llmCallArgs
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Prompt) == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_llm_call requires prompt"}
	}
	cfg, err := config.LoadProvidersConfig()
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: fmt.Sprintf("load providers config: %v", err)}
	}
	var entry *config.ProviderEntry
	if a.Provider != "" {
		p, ok := cfg.FindProvider(a.Provider)
		if !ok {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("provider %q not found", a.Provider)}
		}
		entry = p
	} else {
		p, err := cfg.ResolveProviderForModel(a.Model)
		if err != nil {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: err.Error()}
		}
		entry = p
	}
	if entry == nil || !entry.SupportsDirectExecution() {
		name := "<nil>"
		if entry != nil {
			name = entry.Name
		}
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("provider %s does not support direct execution", name)}
	}
	model := a.Model
	if model == "" {
		model = entry.DefaultModel
	}
	if model == "" && len(entry.Models) > 0 {
		model = entry.Models[0].ID
	}
	if !entry.ServesModel(model) {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("provider %q does not serve model %q", entry.Name, model)}
	}

	callTimeout := 60 * time.Second
	if a.Timeout != "" {
		if dur, parseErr := time.ParseDuration(a.Timeout); parseErr == nil && dur > 0 {
			callTimeout = dur
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	effectivePrompt := a.Prompt
	var hydratedFiles []string
	if len(a.ContextFiles) > 0 {
		_, scopeRoot, dErr := fsops.ResolveDelegationContext("", a.ScopeRoot, false)
		if dErr != nil {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: dErr.Error()}
		}
		var cb strings.Builder
		for _, rawPath := range a.ContextFiles {
			p := strings.TrimSpace(rawPath)
			if p == "" {
				continue
			}
			readPath := p
			if !filepath.IsAbs(p) && scopeRoot != "" && scopeRoot != "." {
				if _, vErr := fsops.VerifyScope(p, scopeRoot, nil); vErr != nil {
					readPath = filepath.Join(scopeRoot, p)
				}
			}
			readRes, rErr := fsops.Read(callCtx, fsops.ReadOptions{
				Path:      readPath,
				ScopeRoot: scopeRoot,
			})
			if rErr != nil {
				return nil, &jsonRPCError{Code: codeInvalidParams, Message: fmt.Sprintf("read context file %q: %v", p, rErr)}
			}
			_, _ = fmt.Fprintf(&cb, "--- FILE: %s (sha256: %s) ---\n%s\n", p, readRes.SHA256, readRes.Content)
			hydratedFiles = append(hydratedFiles, p)
		}
		if cb.Len() > 0 {
			effectivePrompt = cb.String() + effectivePrompt
		}
	}

	hp := provider.NewHTTPProviderFromConfig(*entry)
	opts := provider.ExecuteOptions{
		SystemPrompt: a.System,
		Effort:       a.Effort,
		MaxTokens:    a.MaxTokens,
		Temperature:  a.Temperature,
	}
	start := time.Now()
	res, err := hp.ExecuteWithResult(callCtx, effectivePrompt, model, opts)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: fmt.Sprintf("llm call failed: %v", err)}
	}
	durationMs := res.DurationMs
	if durationMs == 0 {
		durationMs = time.Since(start).Milliseconds()
	}

	out := map[string]any{
		"provider":          entry.Name,
		"protocol":          entry.EffectiveProtocol(),
		"requested_model":   model,
		"resolved_model":    res.ResolvedModel,
		"output":            res.Output,
		"prompt_tokens":     res.PromptTokens,
		"completion_tokens": res.CompletionTokens,
		"total_tokens":      res.TotalTokens,
		"duration_ms":       durationMs,
	}
	if len(hydratedFiles) > 0 {
		out["context_files"] = hydratedFiles
	}
	return out, nil
}

func (s *Server) charterIssueTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		IssuerStratum string   `json:"issuer_stratum"`
		RepoID        string   `json:"repo_id"`
		ScopeRepo     string   `json:"scope_repo"`
		ScopeRoot     string   `json:"scope_root"`
		Objective     string   `json:"objective"`
		AllowedPaths  []string `json:"allowed_paths"`
		BudgetTokens  int      `json:"budget_tokens"`
		DurationSec   int      `json:"duration_sec"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_charter_issue invalid arguments"}
	}
	repoID := strings.TrimSpace(a.RepoID)
	if repoID == "" {
		repoID = strings.TrimSpace(a.ScopeRepo)
	}
	if repoID == "" || strings.TrimSpace(a.Objective) == "" || len(a.AllowedPaths) == 0 {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_charter_issue requires scope_repo (or repo_id), objective, and allowed_paths"}
	}
	store, err := charter.Open("")
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	defer store.Close()
	c, err := store.Issue(charter.IssueRequest{
		IssuerStratum:  charter.Stratum(a.IssuerStratum),
		RepoID:         repoID,
		ScopeRoot:      a.ScopeRoot,
		AllowedPaths:   a.AllowedPaths,
		OutcomeMandate: a.Objective,
		BudgetTokens:   a.BudgetTokens,
		TTL:            time.Duration(a.DurationSec) * time.Second,
	})
	if err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: err.Error()}
	}
	return c, nil
}

func (s *Server) charterVerifyTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		CharterID  string `json:"charter_id"`
		RepoID     string `json:"repo_id"`
		Epoch      int    `json:"epoch"`
		FenceToken string `json:"fence_token"`
		Fence      string `json:"fence"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_charter_verify invalid arguments"}
	}
	fence := strings.TrimSpace(a.FenceToken)
	if fence == "" {
		fence = strings.TrimSpace(a.Fence)
	}
	if strings.TrimSpace(a.CharterID) == "" || a.Epoch <= 0 || fence == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_charter_verify requires charter_id, epoch, and fence_token"}
	}
	store, err := charter.Open("")
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	defer store.Close()
	c, err := store.Verify(a.CharterID, a.RepoID, a.Epoch, fence)
	if err != nil {
		return map[string]any{
			"valid":      false,
			"charter_id": a.CharterID,
			"reason":     err.Error(),
		}, nil
	}
	return map[string]any{
		"valid":   true,
		"charter": c,
	}, nil
}

func (s *Server) portfolioStatusTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		WorkspaceDir string `json:"workspace_dir"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	store, err := charter.Open("")
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	defer store.Close()
	snap, err := store.ProjectPortfolio(a.WorkspaceDir)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return snap, nil
}

// skillsListTool lists digest-pinned skills across F0/F1/F2 containment tiers (DELTA-34).
func (s *Server) skillsListTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		Role    string `json:"role"`
		Tier    string `json:"tier"`
		BankDir string `json:"bank_dir"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_skills_list params must be an object"}
		}
	}
	var dirs []string
	if strings.TrimSpace(a.BankDir) != "" {
		dirs = []string{a.BankDir}
	}
	skills := brief.ListVerifiedSkills(dirs...)
	if strings.TrimSpace(a.Tier) != "" {
		targetTier := strings.ToUpper(strings.TrimSpace(a.Tier))
		var tierFiltered []brief.SkillManifest
		for _, sk := range skills {
			if strings.EqualFold(sk.Tier, targetTier) {
				tierFiltered = append(tierFiltered, sk)
			}
		}
		skills = tierFiltered
	}
	if strings.TrimSpace(a.Role) != "" {
		role, err := harness.GetRole(strings.TrimSpace(a.Role))
		if err != nil {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: err.Error()}
		}
		recSet := make(map[string]bool)
		for _, r := range role.RecommendedSkills {
			clean := r
			if idx := strings.LastIndex(clean, ":"); idx >= 0 {
				clean = clean[idx+1:]
			}
			recSet[clean] = true
			recSet[r] = true
		}
		var filtered []brief.SkillManifest
		for _, sk := range skills {
			if recSet[sk.Name] {
				filtered = append(filtered, sk)
			}
		}
		skills = filtered
	}
	return map[string]any{
		"skills": skills,
		"count":  len(skills),
	}, nil
}

// skillsReadTool reads a skill by name or skill://g8s/ URI with optional expected_digest verification (DELTA-34).
func (s *Server) skillsReadTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		Name           string `json:"name"`
		ExpectedDigest string `json:"expected_digest"`
		BankDir        string `json:"bank_dir"`
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Name) == "" {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_skills_read requires name"}
	}
	var dirs []string
	if strings.TrimSpace(a.BankDir) != "" {
		dirs = []string{a.BankDir}
	}
	res, err := brief.ReadVerifiedSkill(a.Name, a.ExpectedDigest, dirs...)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInvalidParams, Message: err.Error()}
	}
	return res, nil
}

// contextPackTool executes pure-Go 4-tier AST elision, PageRank + BM25F, and Knapsack compression (DELTA-36).
func (s *Server) contextPackTool(_ context.Context, args json.RawMessage) (any, *jsonRPCError) {
	var a struct {
		contextpack.PackRequest
		Root   string `json:"root"`
		Focus  string `json:"focus"`
		Budget int    `json:"budget"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &jsonRPCError{Code: codeInvalidParams, Message: "g8s_context_pack params must be an object"}
		}
	}
	req := a.PackRequest
	if strings.TrimSpace(req.RootDir) == "" && strings.TrimSpace(a.Root) != "" {
		req.RootDir = a.Root
	}
	if strings.TrimSpace(req.FocusQuery) == "" && strings.TrimSpace(a.Focus) != "" {
		req.FocusQuery = a.Focus
	}
	if req.BudgetTokens <= 0 && a.Budget > 0 {
		req.BudgetTokens = a.Budget
	}
	res, err := contextpack.Pack(req)
	if err != nil {
		return nil, &jsonRPCError{Code: codeInternal, Message: err.Error()}
	}
	return res, nil
}
