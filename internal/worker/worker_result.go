package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
)

// agyUsage carries token usage metrics reported by the agy tool wrapper.

type agyUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

// agyResult represents the AGY tool's JSONL result format.
// The AGY tool outputs a stream of JSON objects, with the final one containing
// the result status. We parse the last valid JSON object with a "result" field.
type agyResult struct {
	Result struct {
		Status          string    `json:"status"`
		Error           string    `json:"error,omitempty"`
		Response        string    `json:"response,omitempty"`
		DurationSeconds float64   `json:"duration_seconds,omitempty"`
		Usage           *agyUsage `json:"usage,omitempty"`
	} `json:"result"`
}

// agyStreamErrorTail reports whether the captured stdout stream ends with an
// error_message step_update event without a result event after it (#331):
// the agent hit its ceiling or crashed mid-task, so "exit code 0" from the
// wrapper must not be promoted to a successful result.
func agyStreamErrorTail(stdoutText string) bool {
	lastWasError := false
	for _, line := range strings.Split(stdoutText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var probe struct {
			Event string `json:"event"`
			Step  *struct {
				StepType string `json:"step_type"`
			} `json:"step_update"`
		}
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue
		}
		switch probe.Event {
		case "result":
			lastWasError = false // a result event supersedes any earlier error
		case "step_update":
			if probe.Step != nil && probe.Step.StepType != "" {
				lastWasError = probe.Step.StepType == "error_message"
			}
		}
	}
	return lastWasError
}

// parseAGYResult scans the captured stdout for AGY JSONL result format.
// Returns the last AGY result found, or nil if none.
func parseAGYResult(stdoutText string) *agyResult {
	lines := strings.Split(stdoutText, "\n")
	var lastResult *agyResult
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ar agyResult
		if json.Unmarshal([]byte(line), &ar) == nil && ar.Result.Status != "" {
			lastResult = &ar
		}
	}
	return lastResult
}

// extractAGYUsage scans captured stdout for token usage reported by AGY in
// result or step_update events. When a terminal result event is present, its
// cumulative usage is returned directly; when the stream ends early (e.g. on
// timeout) without a terminal result event, per-step step_update usage records
// are summed so partial runs report accurate cumulative token consumption.
func extractAGYUsage(stdoutText string) *agyUsage {
	lines := strings.Split(stdoutText, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ar agyResult
		if json.Unmarshal([]byte(line), &ar) == nil && ar.Result.Usage != nil {
			return ar.Result.Usage
		}
		var probe struct {
			Usage *agyUsage `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Usage != nil {
			return probe.Usage
		}
	}

	var sum agyUsage
	foundStepUsage := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var probe struct {
			Step *struct {
				Usage *agyUsage `json:"usage"`
			} `json:"step_update"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Step != nil && probe.Step.Usage != nil {
			u := probe.Step.Usage
			sum.InputTokens += u.InputTokens
			sum.OutputTokens += u.OutputTokens
			sum.ThinkingTokens += u.ThinkingTokens
			sum.CacheReadTokens += u.CacheReadTokens
			sum.TotalTokens += u.TotalTokens
			foundStepUsage = true
		}
	}
	if foundStepUsage {
		return &sum
	}
	return nil
}

// WorkerControlPlane is the narrow control-plane surface the supervisor needs.
// *controlplane.Store satisfies it directly.

var providerRefusalPhrases = []string{
	"this request was blocked by",
	"blocked by gemini's filters",
	"occasionally trigger by mistake on safe coding",
	"read more about [our policies here]",
}

func init() {
	for i, phrase := range providerRefusalPhrases {
		providerRefusalPhrases[i] = strings.ToLower(phrase)
	}
}

// providerRefusalDetected reports whether the captured output is a provider
// content-filter refusal rather than task evidence. A refusal is not a
// result: classifying it as succeeded poisons the control plane with
// non-evidence (#344).
func providerRefusalDetected(stdoutText string) bool {
	lower := strings.ToLower(stdoutText)
	for _, phrase := range providerRefusalPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func readWorkerResult(rawResult []byte, stdoutText string, code int) workerResult {
	if envErr := dispatch.ParseWorkerEnvelope([]byte(stdoutText)); envErr != nil {
		var envE *dispatch.WorkerEnvelopeError
		reason := envErr.Error()
		if errors.As(envErr, &envE) && envE.Message != "" {
			reason = envE.Message
		}
		return workerResult{
			OK:      false,
			Status:  "failed",
			Reason:  reason,
			Summary: fmt.Sprintf("child process returned error envelope: %s", reason),
			Usage:   extractAGYUsage(stdoutText),
		}
	}

	// Check for AGY result format (JSONL with result.status field)
	agyRes := parseAGYResult(stdoutText)
	if agyRes != nil {
		if agyRes.Result.Status == "ERROR" {
			usage := agyRes.Result.Usage
			if usage == nil {
				usage = extractAGYUsage(stdoutText)
			}
			res := workerResult{
				OK:      false,
				Status:  "failed",
				Reason:  agyRes.Result.Error,
				Summary: fmt.Sprintf("AGY returned error result: %s", agyRes.Result.Error),
				Usage:   usage,
			}
			if agyRes.Result.DurationSeconds > 0 {
				res.ProviderDurationSeconds = agyRes.Result.DurationSeconds
			}
			return res
		}
		if agyRes.Result.Status == "SUCCESS" {
			// #344: a content-filter refusal is never task evidence, even
			// when the provider stream reports SUCCESS around it. #443: the
			// scan is scoped to the final response — the stream routinely
			// echoes boilerplate (tool output, diffs of our own fixtures;
			// TestReadWorkerResultProviderRefusal embeds the phrase), and a
			// mid-stream echo must not outweigh a completed run.
			if providerRefusalDetected(agyRes.Result.Response) {
				return workerResult{
					OK:      false,
					Status:  "blocked",
					Reason:  "provider content filter refused the prompt (no task evidence produced)",
					Summary: "worker response is a provider refusal, not a result",
				}
			}
			// #383: the model's response text IS the deliverable. A SUCCESS
			// stream with an empty or empty-object response produced no
			// evidence — reject it instead of sealing a hollow completion.
			if degenerateResponse(agyRes.Result.Response) {
				return workerResult{
					OK:      false,
					Status:  "failed",
					Reason:  "worker stream reported SUCCESS without deliverable content (empty response)",
					Summary: "no task evidence produced by the worker response",
				}
			}
			usage := agyRes.Result.Usage
			if usage == nil {
				usage = extractAGYUsage(stdoutText)
			}
			res := workerResult{
				OK:       true,
				Status:   "succeeded",
				Response: agyRes.Result.Response,
				Summary:  "AGY completed successfully",
				Usage:    usage,
			}
			if agyRes.Result.DurationSeconds > 0 {
				res.ProviderDurationSeconds = agyRes.Result.DurationSeconds
			}
			return res
		}
	}

	// #344/#443: no final result event — the stream is the only evidence, so
	// the whole-stream refusal scan still applies there (true prompt-level
	// refusals emit no result event).
	if providerRefusalDetected(stdoutText) {
		return workerResult{
			OK:      false,
			Status:  "blocked",
			Reason:  "provider content filter refused the prompt (no task evidence produced)",
			Summary: "worker response is a provider refusal, not a result",
		}
	}

	if len(rawResult) > 0 {
		var wr workerResult
		if json.Unmarshal(rawResult, &wr) == nil && wr.Status != "" {
			if wr.Usage == nil {
				wr.Usage = extractAGYUsage(stdoutText)
			}
			return wr
		}
	}
	// Fallback 1: Extract fenced JSON from captured stdout
	for _, raw := range fencedJSONPattern.FindAllStringSubmatch(stdoutText, -1) {
		var fenced workerResult
		if json.Unmarshal([]byte(raw[1]), &fenced) == nil && fenced.Status != "" {
			if fenced.Usage == nil {
				fenced.Usage = extractAGYUsage(stdoutText)
			}
			return fenced
		}
	}
	// Fallback 2: Exit code based synthesis for CLI worker compatibility.
	// Guard (#331): an agent stream that terminates in error_message without
	// a result event must be classified as a (retryable) failure even when
	// the wrapper process exited 0 — otherwise ceiling-death runs are
	// silently reported as WORKER_COMPLETED with no deliverable.
	if code == 0 {
		if agyStreamErrorTail(stdoutText) {
			return workerResult{
				OK:      false,
				Status:  "failed",
				Reason:  "agent stream ended in error_message without a result event",
				Summary: "worker stream terminated mid-task before producing a result (likely context/step ceiling)",
				Usage:   extractAGYUsage(stdoutText),
			}
		}
		return workerResult{
			OK:      true,
			Status:  "succeeded",
			Summary: "worker completed execution successfully",
			Usage:   extractAGYUsage(stdoutText),
		}
	}
	// Non-zero exit with an error tail: classify precisely for retry policy.
	if agyStreamErrorTail(stdoutText) {
		return workerResult{
			OK:      false,
			Status:  "failed",
			Reason:  fmt.Sprintf("worker stream ended in error_message (exit code %d)", code),
			Summary: "worker terminated mid-task before producing a result",
			Usage:   extractAGYUsage(stdoutText),
		}
	}
	return workerResult{
		OK:     false,
		Status: "failed",
		Reason: fmt.Sprintf("worker process exited with code %d", code),
		Usage:  extractAGYUsage(stdoutText),
	}
}

// validateResult performs schema and content validation on the worker result
// before finishing the attempt. Returns the validation result and any error calls to record.
func (s *Supervisor) validateResult(
	ctx context.Context,
	taskID, workerID string,
	attempt int,
	wr workerResult,
	resultJSON json.RawMessage,
	rawResult []byte,
	rawOversize bool,
	stdoutText, stderrText string,
) (controlplane.ResultValidation, []controlplane.ErrorCallRecord) {
	validation := controlplane.ResultValidation{
		Valid:         true,
		SchemaErrors:  []string{},
		ContentErrors: []string{},
		ValidatedBy:   workerID,
		Details:       map[string]string{},
	}
	var errorCalls []controlplane.ErrorCallRecord

	schemaError := func(msg string) {
		validation.Valid = false
		validation.SchemaErrors = append(validation.SchemaErrors, msg)
		errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
			Timestamp:  float64(s.clock().UnixNano()) / 1e9,
			WorkerID:   workerID,
			Attempt:    attempt,
			ErrorType:  "schema_error",
			ErrorMsg:   msg,
			StackTrace: "",
		})
	}

	// #383: the worker-authored result file is constrained to the envelope
	// schema — whitelisted keys, boolean `ok`, non-empty string `status`,
	// object-typed `contract_violation`. The wrap-exec adapter's own
	// synthetic envelope (exactly {ok, exit_code}) is trusted infrastructure
	// and exempt; wrapper mode overwrites resultPath after the child exits,
	// so this gate is live for worker-authored files (stdout mode, fake
	// runners, wrapper failure).
	switch {
	case rawOversize:
		schemaError(fmt.Sprintf("worker result file exceeds %d bytes", maxResultFileBytes))
	case len(rawResult) > 0:
		var rawMap map[string]any
		if err := json.Unmarshal(rawResult, &rawMap); err != nil {
			schemaError(fmt.Sprintf("worker result file is not a JSON object: %v", err))
		} else if isWrapperEnvelopeMap(rawMap) {
			// trusted wrap-exec synthetic envelope — not worker-authored
		} else {
			allowed := map[string]bool{
				"ok": true, "status": true, "reason": true, "summary": true,
				"response": true, "contract_violation": true, "usage": true,
				"provider_duration_seconds": true,
			}
			for k := range rawMap {
				if !allowed[k] {
					schemaError(fmt.Sprintf("worker result file has unexpected field %q", k))
				}
			}
			if okVal, present := rawMap["ok"]; !present {
				schemaError(`worker result file missing required boolean field "ok"`)
			} else if _, isBool := okVal.(bool); !isBool {
				schemaError(`worker result file field "ok" is not a boolean`)
			}
			if statusVal, present := rawMap["status"]; !present {
				schemaError(`worker result file missing required string field "status"`)
			} else if statusStr, isStr := statusVal.(string); !isStr || statusStr == "" {
				schemaError(`worker result file field "status" must be a non-empty string`)
			}
			if cv, present := rawMap["contract_violation"]; present {
				if _, isObj := cv.(map[string]any); !isObj {
					schemaError(`worker result file field "contract_violation" must be a JSON object`)
				}
			}
		}
	}

	// Schema validation: check result has required structure
	if len(resultJSON) == 0 {
		validation.Valid = false
		validation.SchemaErrors = append(validation.SchemaErrors, "result is empty")
		errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
			Timestamp:  float64(s.clock().UnixNano()) / 1e9,
			WorkerID:   workerID,
			Attempt:    attempt,
			ErrorType:  "schema_error",
			ErrorMsg:   "result is empty",
			StackTrace: "",
		})
	} else {
		var resultMap map[string]any
		if err := json.Unmarshal(resultJSON, &resultMap); err != nil {
			validation.Valid = false
			validation.SchemaErrors = append(validation.SchemaErrors, fmt.Sprintf("result is not valid JSON: %v", err))
			errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
				Timestamp:  float64(s.clock().UnixNano()) / 1e9,
				WorkerID:   workerID,
				Attempt:    attempt,
				ErrorType:  "schema_error",
				ErrorMsg:   fmt.Sprintf("result is not valid JSON: %v", err),
				StackTrace: "",
			})
		} else {
			// Check for error envelope
			if okVal, ok := resultMap["ok"]; ok {
				if okBool, ok := okVal.(bool); ok && !okBool {
					validation.Valid = false
					validation.ContentErrors = append(validation.ContentErrors, "result.ok is false")
					errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
						Timestamp:  float64(s.clock().UnixNano()) / 1e9,
						WorkerID:   workerID,
						Attempt:    attempt,
						ErrorType:  "content_error",
						ErrorMsg:   "result.ok is false",
						StackTrace: "",
					})
				}
			}
			// Check for AGY error status
			if resultVal, ok := resultMap["result"]; ok {
				if resultMapObj, ok := resultVal.(map[string]any); ok {
					if status, ok := resultMapObj["status"].(string); ok && status == "ERROR" {
						validation.Valid = false
						validation.ContentErrors = append(validation.ContentErrors, "AGY result status is ERROR")
						if errMsg, ok := resultMapObj["error"].(string); ok {
							errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
								Timestamp:  float64(s.clock().UnixNano()) / 1e9,
								WorkerID:   workerID,
								Attempt:    attempt,
								ErrorType:  "content_error",
								ErrorMsg:   fmt.Sprintf("AGY error: %s", errMsg),
								StackTrace: "",
							})
						}
					}
				}
			}
		}
	}

	// Content validation: check workerResult status
	if !wr.OK {
		validation.Valid = false
		validation.ContentErrors = append(validation.ContentErrors, fmt.Sprintf("worker result not OK: %s", wr.Status))
		if wr.Reason != "" {
			errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
				Timestamp:  float64(s.clock().UnixNano()) / 1e9,
				WorkerID:   workerID,
				Attempt:    attempt,
				ErrorType:  "execution_error",
				ErrorMsg:   wr.Reason,
				StackTrace: stderrText,
			})
		}
	}

	// Store validation details
	validation.Details["worker_status"] = wr.Status
	validation.Details["worker_ok"] = fmt.Sprintf("%v", wr.OK)
	if wr.Summary != "" {
		validation.Details["summary"] = wr.Summary
	}

	return validation, errorCalls
}

// validateContract performs contract validation (paths, tools, output size, schema)
// on the task result before finishing the attempt.
func (s *Supervisor) validateContract(
	ctx context.Context,
	taskID, workerID string,
	attempt int,
	resultJSON json.RawMessage,
) (controlplane.ContractValidation, []controlplane.ErrorCallRecord) {
	validation := controlplane.ContractValidation{
		Valid:          true,
		PathViolations: []string{},
		ToolViolations: []string{},
		ValidatedBy:    workerID,
		Details:        map[string]string{},
	}
	var errorCalls []controlplane.ErrorCallRecord

	// Get task to read contract fields
	task, err := s.cp.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return validation, errorCalls
	}

	// Parse contract fields from request JSON
	var requestMap map[string]any
	if err := json.Unmarshal(task.Request, &requestMap); err != nil {
		return validation, errorCalls
	}

	// 1. Validate output size
	maxOutputSize := int64(1024 * 1024) // default 1MB
	if v, ok := requestMap["max_output_size"].(float64); ok {
		maxOutputSize = int64(v)
	}
	resultSize := int64(len(resultJSON))
	if resultSize > maxOutputSize {
		validation.Valid = false
		validation.OutputSizeViolated = true
		validation.Details["output_size_bytes"] = fmt.Sprintf("%d", resultSize)
		validation.Details["max_output_size_bytes"] = fmt.Sprintf("%d", maxOutputSize)
		errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
			Timestamp:  float64(s.clock().UnixNano()) / 1e9,
			WorkerID:   workerID,
			Attempt:    attempt,
			ErrorType:  "contract_violation",
			ErrorMsg:   fmt.Sprintf("output size %d bytes exceeds max %d bytes", resultSize, maxOutputSize),
			StackTrace: "",
		})
	}

	// 2. Validate output schema if provided
	if outputSchemaRaw, ok := requestMap["output_schema"]; ok {
		outcome := validateOutputSchema(outputSchemaRaw, resultJSON)
		if outcome.Evaluated {
			if outcome.Valid {
				validation.Details["schema_validated"] = "true"
			} else {
				validation.Valid = false
				validation.SchemaErrors = append(validation.SchemaErrors, outcome.SchemaErrors...)
				for _, msg := range outcome.SchemaErrors {
					errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
						Timestamp:  float64(s.clock().UnixNano()) / 1e9,
						WorkerID:   workerID,
						Attempt:    attempt,
						ErrorType:  "contract_violation",
						ErrorMsg:   msg,
						StackTrace: "",
					})
				}
			}
		}
	}

	// 3. Path and tool violations would be tracked during execution
	// These are typically detected by the runtime sandbox, not here
	// We store them for visibility

	return validation, errorCalls
}

// buildArgv assembles the worker invocation mirroring the baseline contract:
// prompt file in, structured result file out, explicit scope roots attached.
func (s *Supervisor) buildArgv(req taskRequest, promptPath, resultPath string) []string {
	noSandbox := req.NoSandbox
	switch req.Permission {
	case "workspace_write":
		noSandbox = true
	case "read_only", "automation_read":
		noSandbox = false
	}
	return dispatch.BuildWorkerArgv(dispatch.BuildWorkerArgvOptions{
		Binary:          s.binaryPath,
		PromptFile:      promptPath,
		Model:           req.Model,
		Role:            req.Role,
		Permission:      req.Permission,
		Timeout:         req.Timeout,
		ResultPath:      resultPath,
		AddDirs:         req.AddDirs,
		SkipPermissions: req.SkipPermiss,
		NoSandbox:       noSandbox,
		Effort:          req.Effort,
		ToolPolicy:      req.ToolPolicy,
	})
}

// snapshot removes leftover artifacts, exports the sealed receipt, and
// returns the freshest task view.
func (s *Supervisor) snapshot(ctx context.Context, taskID, runDir string, leftovers ...string) (*controlplane.Task, error) {
	for _, path := range leftovers {
		os.Remove(path)
	}
	s.ExportReceipt(ctx, taskID, runDir)
	return s.cp.GetTask(ctx, taskID)
}
