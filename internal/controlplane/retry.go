// Package controlplane implements task queue management and lifecycle state transitions.
// This file implements Wave F1 / ADR-0029 budgeted auto-retry:
// classification (D1 deny-by-default), budget accounting, and Store.ResubmitTask.
//
// Backoff design choice (documented per brief requirement):
// Backoff is enforced via a future "not_before" Unix timestamp stored in request_json
// metadata (completed_at + backoff duration: 5m -> 20m -> 60m).
// ClaimTask and ClaimTaskInSession query tasks with:
//
//	(json_extract(request_json, '$.not_before') IS NULL OR json_extract(request_json, '$.not_before') <= ?)
//
// This requires zero schema migrations, preserves existing table layouts,
// and respects injectable deterministic clocks.
package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

var (
	// ErrLineageCycle is returned when a task lineage contains a cycle.
	ErrLineageCycle = errors.New("lineage cycle detected")
	// ErrRetryBudgetExceeded is returned when the retry budget is exhausted.
	ErrRetryBudgetExceeded = errors.New("retry budget exceeded")
)

// RetryClass classifies whether a task failure is eligible for auto-resubmission.
type RetryClass string

const (
	// RetryClassRetryable indicates transient or infrastructure failures that are safe to retry.
	RetryClassRetryable RetryClass = "RETRYABLE"
	// RetryClassNotRetryable indicates permanent, deterministic, policy/refusal, or unclassified failures.
	RetryClassNotRetryable RetryClass = "NOT_RETRYABLE"

	// Exported aliases matching ADR-0029 convention.
	ClassRetryable    = RetryClassRetryable
	ClassNotRetryable = RetryClassNotRetryable
)

// IsRetryable reports whether the class is RETRYABLE.
func (c RetryClass) IsRetryable() bool {
	return c == RetryClassRetryable
}

// ResubmitOpts configures auto-retry constraints for Store.ResubmitTask.
type ResubmitOpts struct {
	// Enabled is the feature flag (from auto_retry_enabled setting); must be true.
	Enabled bool
	// MaxPerTask is the maximum retries per original task (0..10, default 2).
	MaxPerTask int
	// MaxPerHour is the maximum retries per hour per store/state-dir (0..1000, default 10).
	MaxPerHour int
	// ReceiptID is an optional fresh receipt_id if resubmitting a workspace_write task.
	// Tick jobs must never pass this (D3: receipt issuance is supervisor-only).
	ReceiptID string
}

// Default constants per ADR-0029 D2.
const (
	DefaultAutoRetryMaxPerTask = 2
	DefaultAutoRetryMaxPerHour = 10
)

// RetryBackoff returns the backoff duration based on retry attempt (1-indexed):
// 1st retry: 5m, 2nd retry: 20m, 3rd+ retry: 60m.
func RetryBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 5 * time.Minute
	case 2:
		return 20 * time.Minute
	default:
		return 60 * time.Minute
	}
}

// Non-retryable phrases (checked first per D1 deny-by-default).
var nonRetryablePhrases = []string{
	"receipt violation",
	"receipt bypass",
	"receipt-bypass",
	"invalid receipt",
	"receipt unknown or expired",
	"already consumed",
	"receipt_id mismatch",
	"missing receipt",
	"requires --receipt-id",
	"sanitizer violation",
	"sanitizer-fidelity",
	"sanitizer corruption",
	"sanitizer error",
	"refusal",
	"provider refusal",
	"blocked by gemini's filters",
	"this request was blocked by",
	"occasionally trigger by mistake on safe coding",
	"read more about [our policies here]",
	"content filter",
	"safety policy",
	"e_usage",
	"unknown flag",
	"unknown command",
	"e_invalid",
	"e_denied",
}

// Retryable phrases (only considered if no non-retryable marker matched).
var (
	timeoutPhrases = []string{
		"e_timeout",
		"timeout",
		"timed out",
		"context deadline exceeded",
		"deadline exceeded",
		"execution timed out",
		"lease expired",
	}

	interruptedPhrases = []string{
		"interrupted",
		"signal: interrupt",
		"signal: killed",
		"sigint",
		"sigterm",
		"process interrupted",
	}

	spawnFailurePhrases = []string{
		"spawn-failure",
		"spawn failure",
		"failed to spawn",
		"unable to spawn",
		"exec: executable file not found",
		"fork/exec",
		"cannot run program",
		"unable to start child process",
	}

	providerTransportPhrases = []string{
		"malformed function call",
		"malformed_function_call",
		"provider transport",
		"transport error",
		"connection reset",
		"connection refused",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"internal server error",
		"500 internal server error",
		"502 bad gateway",
		"503 service unavailable",
		"504 gateway timeout",
		"status 500",
		"status 502",
		"status 503",
		"status 504",
		" 500 ",
		" 502 ",
		" 503 ",
		" 504 ",
		"5xx",
		"e_io",
	}
)

// ClassifyRetryable evaluates whether a failed task is eligible for auto-retry
// following ADR-0029 D1 (strict deny-by-default).
//
// RETRYABLE only for:
//   - timeout (task timeout, context deadline, lease expiry recovery)
//   - interrupted (SIGINT, SIGTERM, process interruption)
//   - spawn-failure (worker process fork/exec failure)
//   - provider-transport (malformed function call, 5xx server/gateway errors, transport resets)
//
// NOT_RETRYABLE:
//   - receipt violations
//   - sanitizer violations
//   - refusal verdicts & content filters
//   - E_USAGE, E_INVALID, E_DENIED
//   - successful completions ($.ok = true)
//   - empty or unclassified errors
func ClassifyRetryable(resultJSON string, lastError string) RetryClass {
	trimmedResult := strings.TrimSpace(resultJSON)
	trimmedErr := strings.TrimSpace(lastError)

	if trimmedResult == "" && trimmedErr == "" {
		return RetryClassNotRetryable
	}

	var parsedEnvelope struct {
		OK     *bool  `json:"ok"`
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Cause   string `json:"cause"`
			Hint    string `json:"hint"`
		} `json:"error"`
		Result *struct {
			Status   string `json:"status"`
			Error    string `json:"error"`
			Response string `json:"response"`
		} `json:"result"`
		Reason  string `json:"reason"`
		Summary string `json:"summary"`
	}

	hasJSON := trimmedResult != "" && json.Unmarshal([]byte(trimmedResult), &parsedEnvelope) == nil

	// 1. Never retry if result indicates success
	if hasJSON {
		if parsedEnvelope.OK != nil && *parsedEnvelope.OK {
			return RetryClassNotRetryable
		}
		if strings.EqualFold(parsedEnvelope.Status, "succeeded") {
			return RetryClassNotRetryable
		}
		if parsedEnvelope.Result != nil && strings.EqualFold(parsedEnvelope.Result.Status, "SUCCESS") {
			return RetryClassNotRetryable
		}
	}

	// Build combined text for semantic scanning
	var sb strings.Builder
	sb.WriteString(strings.ToLower(trimmedErr))
	sb.WriteString(" ")
	sb.WriteString(strings.ToLower(trimmedResult))
	if hasJSON && parsedEnvelope.Error != nil {
		sb.WriteString(" ")
		sb.WriteString(strings.ToLower(parsedEnvelope.Error.Code))
		sb.WriteString(" ")
		sb.WriteString(strings.ToLower(parsedEnvelope.Error.Message))
	}
	combined := sb.String()

	// 2. Check NOT_RETRYABLE markers first (deny by default)
	for _, marker := range nonRetryablePhrases {
		if strings.Contains(combined, marker) {
			return RetryClassNotRetryable
		}
	}
	if hasJSON && parsedEnvelope.Error != nil {
		code := strings.ToUpper(parsedEnvelope.Error.Code)
		if code == "E_USAGE" || code == "E_INVALID" || code == "E_DENIED" {
			return RetryClassNotRetryable
		}
	}

	// 3. Check RETRYABLE classes
	// A. Timeout
	if hasJSON && parsedEnvelope.Error != nil && strings.EqualFold(parsedEnvelope.Error.Code, "E_TIMEOUT") {
		return RetryClassRetryable
	}
	for _, phrase := range timeoutPhrases {
		if strings.Contains(combined, phrase) {
			return RetryClassRetryable
		}
	}

	// B. Interrupted
	for _, phrase := range interruptedPhrases {
		if strings.Contains(combined, phrase) {
			return RetryClassRetryable
		}
	}

	// C. Spawn failure
	for _, phrase := range spawnFailurePhrases {
		if strings.Contains(combined, phrase) {
			return RetryClassRetryable
		}
	}

	// D. Provider transport
	for _, phrase := range providerTransportPhrases {
		if strings.Contains(combined, phrase) {
			return RetryClassRetryable
		}
	}

	// 4. Default: unclassified errors are NOT_RETRYABLE
	return RetryClassNotRetryable
}

// In-memory hourly retry tracker per state directory / database path.
type hourlyRetryTracker struct {
	mu      sync.Mutex
	entries map[string][]time.Time
}

var globalRetryTracker = &hourlyRetryTracker{
	entries: make(map[string][]time.Time),
}

// ResetHourlyRetryTracker clears all in-memory hourly retry counters.
// Useful for test isolation.
func ResetHourlyRetryTracker() {
	globalRetryTracker.mu.Lock()
	defer globalRetryTracker.mu.Unlock()
	globalRetryTracker.entries = make(map[string][]time.Time)
}

func (s *Store) checkHourlyRetryLimit(maxPerHour int) error {
	if maxPerHour <= 0 {
		return nil
	}
	globalRetryTracker.mu.Lock()
	defer globalRetryTracker.mu.Unlock()

	now := s.clock()
	cutoff := now.Add(-1 * time.Hour)

	key := s.dbPath
	if key == "" {
		key = "default"
	}

	timestamps := globalRetryTracker.entries[key]
	valid := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	globalRetryTracker.entries[key] = valid

	if len(valid) >= maxPerHour {
		return fmt.Errorf("hourly retry budget exceeded: limit %d per hour reached for %s", maxPerHour, key)
	}
	return nil
}

func (s *Store) recordHourlyRetry() {
	globalRetryTracker.mu.Lock()
	defer globalRetryTracker.mu.Unlock()

	key := s.dbPath
	if key == "" {
		key = "default"
	}
	now := s.clock()
	globalRetryTracker.entries[key] = append(globalRetryTracker.entries[key], now)
}

// ResubmitTask executes an out-of-band auto-retry of a terminal-failed task (ADR-0029).
// It enforces the feature flag, failure classification, per-task and per-hour budgets,
// backoff claim-delay, receipt isolation, and double-fire idempotency.
func (s *Store) ResubmitTask(ctx context.Context, origTaskID string, opts ResubmitOpts) (string, error) {
	if !opts.Enabled {
		return "", errors.New("auto-retry is disabled by configuration (auto_retry_enabled=false)")
	}

	maxPerTask := opts.MaxPerTask
	if maxPerTask <= 0 {
		maxPerTask = DefaultAutoRetryMaxPerTask
	}
	maxPerHour := opts.MaxPerHour
	if maxPerHour <= 0 {
		maxPerHour = DefaultAutoRetryMaxPerHour
	}

	orig, err := s.GetTask(ctx, origTaskID)
	if err != nil {
		return "", fmt.Errorf("load original task %s: %w", origTaskID, err)
	}
	if orig == nil {
		return "", fmt.Errorf("%w: %s", ErrUnknownTask, origTaskID)
	}

	if orig.State != StateFailed {
		return "", fmt.Errorf("cannot resubmit task in state %s: only FAILED tasks can be resubmitted", orig.State)
	}

	resultJSON := ""
	if len(orig.Result) > 0 {
		resultJSON = string(orig.Result)
	}
	lastError := ""
	if orig.LastError != nil {
		lastError = *orig.LastError
	}
	if ClassifyRetryable(resultJSON, lastError) != RetryClassRetryable {
		return "", fmt.Errorf("task %s failure is not retryable (last_error: %q)", origTaskID, lastError)
	}

	var payloadMap map[string]any
	if len(orig.Request) > 0 {
		if err := json.Unmarshal(orig.Request, &payloadMap); err != nil {
			return "", fmt.Errorf("unmarshal original request: %w", err)
		}
	} else {
		payloadMap = make(map[string]any)
	}

	perm := "read_only"
	if p, ok := payloadMap["permission"].(string); ok && strings.TrimSpace(p) != "" {
		perm = strings.ToLower(strings.TrimSpace(p))
	}
	payloadMap["permission"] = perm

	// Trust boundary: drop receipt_id unconditionally from original request.
	// If the task requires workspace_write, a fresh receipt must be provided in opts.ReceiptID.
	delete(payloadMap, "receipt_id")
	if strings.EqualFold(perm, "workspace_write") {
		if opts.ReceiptID == "" {
			return "", errors.New("workspace_write tasks require a fresh receipt_id; auto-retry dropped original receipt")
		}
		// Verify receipt is not already consumed or expired
		if mgr, merr := s.receiptsManager(); merr == nil {
			if rc, verr := mgr.VerifyReceipt(opts.ReceiptID); verr == nil {
				if rc.Consumed {
					return "", fmt.Errorf("workspace_write receipt %s is already consumed", opts.ReceiptID)
				}
			} else {
				var consumedErr *receipt.AlreadyConsumedError
				var expiredErr *receipt.ExpiredError
				if errors.As(verr, &consumedErr) {
					return "", fmt.Errorf("workspace_write receipt %s is already consumed", opts.ReceiptID)
				}
				if errors.As(verr, &expiredErr) {
					return "", fmt.Errorf("workspace_write receipt %s is expired", opts.ReceiptID)
				}
			}
		}
		var consumed int
		err := s.db.QueryRowContext(ctx, "SELECT consumed FROM write_receipts WHERE receipt_id = ?", opts.ReceiptID).Scan(&consumed)
		if err == nil && consumed == 1 {
			return "", fmt.Errorf("workspace_write receipt %s is already consumed", opts.ReceiptID)
		}
		payloadMap["receipt_id"] = opts.ReceiptID
	}

	// 1. Resolve ROOT of lineage (walk parent_task_id to origin) and refuse cycles.
	visited := map[string]bool{orig.TaskID: true}
	curr := orig
	for curr.ParentTaskID != nil && strings.TrimSpace(*curr.ParentTaskID) != "" {
		parentID := strings.TrimSpace(*curr.ParentTaskID)
		if visited[parentID] {
			return "", fmt.Errorf("%w: cycle detected at task %s", ErrLineageCycle, parentID)
		}
		visited[parentID] = true
		parentTask, err := s.GetTask(ctx, parentID)
		if err != nil {
			return "", fmt.Errorf("load parent task %s: %w", parentID, err)
		}
		if parentTask == nil {
			break
		}
		curr = parentTask
	}
	rootTask := curr
	rootTaskID := rootTask.TaskID

	// Query ALL retry descendants of rootTaskID using recursive CTE.
	query := `
WITH RECURSIVE descendants(task_id, parent_task_id, idempotency_key, state, request_json, created_at, depth) AS (
    SELECT task_id, parent_task_id, idempotency_key, state, request_json, created_at, 0
    FROM tasks
    WHERE parent_task_id = ?
    UNION ALL
    SELECT t.task_id, t.parent_task_id, t.idempotency_key, t.state, t.request_json, t.created_at, d.depth + 1
    FROM tasks t
    JOIN descendants d ON t.parent_task_id = d.task_id
    WHERE d.depth < 1000
)
SELECT task_id, idempotency_key, state, request_json, created_at
FROM descendants
ORDER BY created_at ASC;`

	rows, err := s.db.QueryContext(ctx, query, rootTaskID)
	if err != nil {
		return "", fmt.Errorf("query lineage descendants: %w", err)
	}
	defer rows.Close()

	type childInfo struct {
		taskID         string
		idempotencyKey string
		state          string
	}
	var retryChildren []childInfo
	seenTasks := make(map[string]bool)
	for rows.Next() {
		var c childInfo
		var reqJSON string
		var createdAt float64
		if err := rows.Scan(&c.taskID, &c.idempotencyKey, &c.state, &reqJSON, &createdAt); err != nil {
			return "", fmt.Errorf("scan descendant task: %w", err)
		}
		if seenTasks[c.taskID] {
			continue
		}
		seenTasks[c.taskID] = true
		retryChildren = append(retryChildren, c)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate descendant tasks: %w", err)
	}

	// Check for idempotent double-fire: if latest retry descendant is not FAILED, return it.
	for i := len(retryChildren) - 1; i >= 0; i-- {
		if retryChildren[i].state != StateFailed {
			return retryChildren[i].taskID, nil
		}
	}

	if len(retryChildren) >= maxPerTask {
		return "", fmt.Errorf("%w: task %s lineage has already used %d retries (max %d)",
			ErrRetryBudgetExceeded, rootTaskID, len(retryChildren), maxPerTask)
	}

	if err := s.checkHourlyRetryLimit(maxPerHour); err != nil {
		return "", err
	}

	// Calculate backoff: completed_at + backoff (5m -> 20m -> 60m).
	// Base is completed_at when present and positive; otherwise NOW (never stale updated_at).
	attemptCount := len(retryChildren) + 1
	backoff := RetryBackoff(attemptCount)
	now := float64(s.clock().UnixNano()) / 1e9
	baseTime := now
	if orig.CompletedAt != nil && *orig.CompletedAt > 0 {
		baseTime = *orig.CompletedAt
	}
	notBefore := baseTime + backoff.Seconds()

	payloadMap["retry_of"] = origTaskID
	payloadMap["retry_attempt"] = attemptCount
	payloadMap["not_before"] = notBefore

	// Preserve prompt if redacted
	if p, ok := payloadMap["prompt"].(string); !ok || strings.TrimSpace(p) == "" {
		if h, ok := payloadMap["prompt_hash"].(string); ok && h != "" {
			payloadMap["prompt"] = fmt.Sprintf("[redacted-prompt-hash:%s]", h)
		}
	}

	newPayloadBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return "", fmt.Errorf("marshal resubmit payload: %w", err)
	}

	role := "collector"
	if r, ok := payloadMap["role"].(string); ok && r != "" {
		role = r
	}
	model := "gemini-3.8-flash-high"
	if m, ok := payloadMap["model"].(string); ok && m != "" {
		model = m
	}
	timeout := ""
	if t, ok := payloadMap["timeout"].(string); ok {
		timeout = t
	}
	var addDirs []string
	if dirs, ok := payloadMap["add_dirs"].([]any); ok {
		for _, d := range dirs {
			if ds, ok := d.(string); ok {
				addDirs = append(addDirs, ds)
			}
		}
	}
	if len(addDirs) == 0 {
		addDirs = []string{"."}
	}

	skipPermissions := false
	if sp, ok := payloadMap["skip_permissions"].(bool); ok {
		skipPermissions = sp
	}
	noSandbox := false
	if ns, ok := payloadMap["no_sandbox"].(bool); ok {
		noSandbox = ns
	}

	newReq := SubmitTaskRequest{
		IdempotencyKey:  fmt.Sprintf("%s#r%d", origTaskID, attemptCount),
		Priority:        orig.Priority,
		MaxAttempts:     orig.MaxAttempts,
		ParentTaskID:    &origTaskID,
		Payload:         newPayloadBytes,
		Role:            role,
		Permission:      perm,
		Model:           model,
		Timeout:         timeout,
		AddDirs:         addDirs,
		SkipPermissions: skipPermissions,
		NoSandbox:       noSandbox,
		OrchestratorID:  orig.OrchestratorID,
		WorktreeID:      orig.WorktreeID,
		WorkerName:      orig.WorkerName,
		Iter:            orig.Iter,
		SessionID:       orig.SessionID,
	}

	if ap, ok := payloadMap["allowed_paths"].([]any); ok {
		for _, p := range ap {
			if ps, ok := p.(string); ok {
				newReq.AllowedPaths = append(newReq.AllowedPaths, ps)
			}
		}
	}
	if at, ok := payloadMap["allowed_tools"].([]any); ok {
		for _, t := range at {
			if ts, ok := t.(string); ok {
				newReq.AllowedTools = append(newReq.AllowedTools, ts)
			}
		}
	}
	if mos, ok := payloadMap["max_output_size"].(float64); ok {
		newReq.MaxOutputSize = int64(mos)
	}
	if osJSON, ok := payloadMap["output_schema"]; ok {
		if osBytes, err := json.Marshal(osJSON); err == nil {
			newReq.OutputSchema = osBytes
		}
	}

	newTask, err := s.SubmitTask(ctx, newReq)
	if err != nil {
		return "", fmt.Errorf("submit retry task: %w", err)
	}

	if !newTask.Deduplicated {
		s.recordHourlyRetry()
	}

	return newTask.TaskID, nil
}
