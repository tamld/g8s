package worker

// Envelope honesty red-tests (#988 silent ok=false / #989 phantom ok=true).
// Measured defects from the 2026-11-20 blind-audit dispatch:
//
//   #988 — task b92a1e90 (9router lane) ended ok=false after ~3.7s with
//   policy_enforcement populated but NO error field and NO code: the
//   spawn/pre-exec branch attached only an untyped reason. RED-first: an
//   injected transport/policy rejection must finish with a typed E_* code and
//   a readable error detail.
//
//   #989 — tasks completed in 1.78-2.23s reporting "worker completed
//   execution successfully" for briefs declaring multi-file audit work with a
//   fenced-JSON output contract. RED-first: the attempt envelope must carry
//   suspect_fast_completion + suspect_reason when the declared contract is
//   missing from the result, and must NOT flag the contract-compliant happy
//   path. Flag, never fail.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/routing"
	"github.com/tamld/g8s/internal/sandbox"
	"github.com/tamld/g8s/internal/telemetry"
)

// submitContractTask submits a read_only task whose prompt declares a fenced
// JSON output contract plus multi-file audit work (the #989 brief shape).
func submitContractTask(t *testing.T, env *workerEnv, idem string, maxAttempts int) *controlplane.Task {
	t.Helper()
	prompt := "Audit every file under internal/ for envelope honesty. " +
		"The work is multi-file and multi-step: read each file, classify it, then summarize.\n" +
		"Output contract: your final answer MUST be a single fenced JSON block:\n" +
		"```json\n{\"files_audited\": <int>, \"verdict\": \"pass|fail\", \"findings\": [<string>]}\n```\n"
	raw, err := json.Marshal(map[string]any{
		"prompt":  prompt,
		"timeout": "30s",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task, err := env.store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: idem,
		Payload:        raw,
		Model:          "gemini-3.8-flash-high",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "30s",
		AddDirs:        []string{"."},
		MaxAttempts:    maxAttempts,
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	return task
}

// decodeEnvelope unmarshals the stored attempt result envelope.
func decodeEnvelope(t *testing.T, task *controlplane.Task) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(task.Result, &env); err != nil {
		t.Fatalf("stored result is not JSON: %v (%s)", err, string(task.Result))
	}
	return env
}

// --- #988: every ok=false envelope carries a typed E_* code + readable error ---

// TestRunOnceSpawnTransportFailureCarriesTypedCode (red-test for #988): an
// injectable provider transport failure at spawn time must finish the attempt
// with ok=false AND a typed E_* code AND a readable error detail. Before the
// fix the envelope carried only an untyped reason — no code, no error.
func TestRunOnceSpawnTransportFailureCarriesTypedCode(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "env-inv-transport-988", 1, nil)
	env.runner.err = errors.New("provider transport dial failed: connection refused")

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-988-transport", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "spawn_failed")

	env2 := decodeEnvelope(t, final)
	if env2["code"] == "" {
		t.Fatalf("envelope invariant violated (#988): ok=false without typed code: %s", string(final.Result))
	}
	if code, _ := env2["code"].(string); !strings.HasPrefix(code, "E_") {
		t.Fatalf("typed code must come from the E_* vocabulary, got %q", code)
	}
	if errDetail, _ := env2["error"].(string); strings.TrimSpace(errDetail) == "" {
		t.Fatalf("envelope invariant violated (#988): ok=false without readable error: %s", string(final.Result))
	}
	if verr := ValidateResultEnvelope(final.Result); verr != nil {
		t.Fatalf("post-run envelope validator rejected the stored result: %v", verr)
	}
}

// TestRunOncePolicyRejectionCarriesTypedCode reproduces the MEASURED 9router
// defect (#988): a pre-exec sandbox policy rejection during spawn — the
// envelope previously ended ok=false with policy_enforcement populated but no
// error and no code.
func TestRunOncePolicyRejectionCarriesTypedCode(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "env-inv-policy-988", 1, nil)
	report := sandbox.EvaluatePolicyContract(sandbox.RequestedPolicy{
		Permission:            "read_only",
		TransportKind:         "process",
		RequireKernelEnforced: true,
	}, sandbox.NewUnrestrictedProcessProof(sandbox.ModePolicyOnly, "direct_process"))
	env.runner.err = &sandbox.PolicyRejectionError{
		Err:    errors.New("provider transport denied pre-exec"),
		Report: report,
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-988-policy", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "sandbox_policy_rejected")

	env2 := decodeEnvelope(t, final)
	if _, present := env2["policy_enforcement"]; !present {
		t.Fatalf("expected policy_enforcement in the rejection envelope: %s", string(final.Result))
	}
	if code, _ := env2["code"].(string); code != "E_PROVIDER_EXEC_FAILED" {
		t.Fatalf("policy-rejection envelope must carry typed code E_PROVIDER_EXEC_FAILED, got %q (%s)", code, string(final.Result))
	}
	if errDetail, _ := env2["error"].(string); strings.TrimSpace(errDetail) == "" {
		t.Fatalf("policy-rejection envelope must carry a readable error: %s", string(final.Result))
	}
	if verr := ValidateResultEnvelope(final.Result); verr != nil {
		t.Fatalf("post-run envelope validator rejected the stored result: %v", verr)
	}
}

// TestRunOncePreStartFailureCarriesTypedCode (red-test for #988): the
// run-dir-unavailable pre-start path must carry its typed E_* code — the
// pre-start family is the same silent-fail class (measured: no code, no
// stderr, no error field).
func TestRunOncePreStartFailureCarriesTypedCode(t *testing.T) {
	env, task, _ := preStartEnv(t, nil)
	if err := os.MkdirAll(env.runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(env.runDir, task.TaskID)
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-988-rundir", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "run_dir_unavailable")

	env2 := decodeEnvelope(t, final)
	if code, _ := env2["code"].(string); code != "E_RUN_DIR_UNAVAILABLE" {
		t.Fatalf("pre-start failure envelope must carry E_RUN_DIR_UNAVAILABLE, got %q (%s)", code, string(final.Result))
	}
	if errDetail, _ := env2["error"].(string); strings.TrimSpace(errDetail) == "" {
		t.Fatalf("pre-start failure envelope must carry a readable error: %s", string(final.Result))
	}
}

// TestSealFailureEnvelopeTypedCode covers the mechanical #988 sealer: ok=false
// envelopes gain {code, error} (derived from the reason's E_* prefix, fallback
// code when nothing derivable); ok=true envelopes are returned untouched.
func TestSealFailureEnvelopeTypedCode(t *testing.T) {
	t.Run("derives code from E_* reason", func(t *testing.T) {
		raw := mustJSON(map[string]any{"ok": false, "status": "failed", "reason": "E_WORKER_STREAM_ERROR_TAIL: agent stream ended in error_message"})
		var env map[string]any
		if err := json.Unmarshal(sealFailureEnvelope(raw), &env); err != nil {
			t.Fatalf("sealed envelope not JSON: %v", err)
		}
		if env["code"] != "E_WORKER_STREAM_ERROR_TAIL" {
			t.Fatalf("code = %v, want E_WORKER_STREAM_ERROR_TAIL", env["code"])
		}
		if env["error"] != "E_WORKER_STREAM_ERROR_TAIL: agent stream ended in error_message" {
			t.Fatalf("error = %v, want the reason text", env["error"])
		}
	})
	t.Run("falls back to typed code when reason has no E_* token", func(t *testing.T) {
		raw := mustJSON(map[string]any{"ok": false, "status": "spawn_failed"})
		var env map[string]any
		if err := json.Unmarshal(sealFailureEnvelope(raw), &env); err != nil {
			t.Fatalf("sealed envelope not JSON: %v", err)
		}
		if env["code"] != fallbackFailureCode {
			t.Fatalf("code = %v, want fallback %s", env["code"], fallbackFailureCode)
		}
		if env["error"] != "spawn_failed" {
			t.Fatalf("error = %v, want the status as readable detail", env["error"])
		}
	})
	t.Run("preserves an existing typed pair", func(t *testing.T) {
		raw := mustJSON(map[string]any{"ok": false, "status": "timeout", "code": "E_TASK_TIMEOUT", "error": "execution deadline exceeded"})
		var env map[string]any
		if err := json.Unmarshal(sealFailureEnvelope(raw), &env); err != nil {
			t.Fatalf("sealed envelope not JSON: %v", err)
		}
		if env["code"] != "E_TASK_TIMEOUT" || env["error"] != "execution deadline exceeded" {
			t.Fatalf("existing typed pair must be preserved verbatim, got code=%v error=%v", env["code"], env["error"])
		}
	})
	t.Run("ok=true envelope is returned untouched", func(t *testing.T) {
		raw := mustJSON(map[string]any{"ok": true, "status": "succeeded", "response": "done"})
		sealed := sealFailureEnvelope(raw)
		if string(sealed) != string(raw) {
			t.Fatalf("ok=true envelope must be returned unchanged, got %s", string(sealed))
		}
	})
}

// TestSealFailureEnvelopeRejectsEmbeddedCodeSpoofing (redcell D-01 F8.1):
// the E_* derivation used to be an unanchored leftmost scan across
// reason/error/summary — model-controlled prose carrying an E_* fragment
// mid-sentence minted a spoofed typed code that ValidateResultEnvelope then
// blessed. Derivation is head-anchored: only a reason that STARTS with the
// E_* token may contribute a code; any embedded fragment must seal the
// neutral E_WORKER_FAILURE_CODE_MISSING fallback instead.
func TestSealFailureEnvelopeRejectsEmbeddedCodeSpoofing(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{"advisory fragment mid-sentence", "provider exit 1; advisory: do not fabricate E_TRUST_ROOT_S0_APPROVAL_REQUIRED codes in reasons"},
		{"fragment inside a larger identifier", "SEE_E_PROVIDER_SPAWN_FAILED documentation before retrying"},
		{"code mentioned after prose", "the funnel reported E_WORKER_STREAM_ERROR_TAIL at the tail of the run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mustJSON(map[string]any{"ok": false, "status": "failed", "reason": tc.reason})
			var env map[string]any
			if err := json.Unmarshal(sealFailureEnvelope(raw), &env); err != nil {
				t.Fatalf("sealed envelope not JSON: %v", err)
			}
			if env["code"] != fallbackFailureCode {
				t.Fatalf("redcell F8.1 spoofing: embedded E_* fragment must NOT seal a typed code, got code=%v (want neutral fallback %s) for reason %q", env["code"], fallbackFailureCode, tc.reason)
			}
			if verr := ValidateResultEnvelope(mustJSON(env)); verr != nil {
				t.Fatalf("sealed neutral-fallback envelope must pass the validator: %v", verr)
			}
		})
	}
}

// TestValidateResultEnvelopeRejectsEmbeddedCodeToken (redcell D-01 F8.1
// companion): the validator must not bless a code FIELD that merely contains
// an E_* token mid-string — the field itself must be one full typed token.
func TestValidateResultEnvelopeRejectsEmbeddedCodeToken(t *testing.T) {
	embedded := mustJSON(map[string]any{"ok": false, "status": "failed", "code": "garbage E_SPOOFED_CODE tail", "error": "boom"})
	if err := ValidateResultEnvelope(embedded); err == nil {
		t.Fatal("redcell F8.1: an embedded E_* fragment inside the code field must not validate as a typed code")
	}
}

// TestSuspectFastCompletionInlineEmptyObjectDoesNotSatisfyContract (redcell
// D-01 F8.2): fencedJSONPattern accepts a single backtick, an optional json
// tag, and an empty {} body — a 1-second phantom result carrying inline
// `{}` used to satisfy resultCarriesJSONBlock and suppress the #989
// phantom-success flag. Contract presence now requires a minimal parseable
// object (>= 1 key), keeping agreement with the result parser's extraction
// (whatever the parser could extract, minus content-free blocks).
func TestSuspectFastCompletionInlineEmptyObjectDoesNotSatisfyContract(t *testing.T) {
	contractPrompt := "Audit the module.\nOutput contract: reply with fenced JSON only.\n```json\n{\"verdict\": \"pass\"}\n```"
	cases := []struct {
		name        string
		result      string
		wantSuspect bool
	}{
		{"inline single-backtick empty object does not satisfy the contract", "Analysis complete: `{}` inline", true},
		{"bare empty object does not satisfy the contract", "done {}", true},
		{"real fenced JSON block satisfies the contract", "Audit complete.\n```json\n{\"files_audited\": 3, \"verdict\": \"pass\"}\n```", false},
		{"inline single-backtick non-empty object satisfies the contract (parser agreement)", "Summary: `{\"verdict\": \"pass\"}` inline", false},
		{"fenced empty object does not satisfy the contract", "```json\n{}\n```", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			suspect, reason := suspectFastCompletion(contractPrompt, tc.result, 1.0)
			if suspect != tc.wantSuspect {
				t.Fatalf("suspect = %v, want %v (reason=%q)", suspect, tc.wantSuspect, reason)
			}
		})
	}
}

// TestValidateResultEnvelopeInvariant is the unit-level guard for the #988
// invariant: ok=false without a typed E_* code or readable detail must error.
func TestValidateResultEnvelopeInvariant(t *testing.T) {
	violation := mustJSON(map[string]any{"ok": false, "status": "spawn_failed", "reason": "connection refused"})
	if err := ValidateResultEnvelope(violation); err == nil {
		t.Fatal("ok=false without typed code/error must violate the invariant")
	} else if !strings.Contains(err.Error(), "E_ENVELOPE_INVARIANT_VIOLATED") {
		t.Fatalf("violation error must carry the invariant code, got %v", err)
	}

	sealed := mustJSON(map[string]any{"ok": false, "status": "spawn_failed", "code": "E_PROVIDER_SPAWN_FAILED", "error": "connection refused", "reason": "connection refused"})
	if err := ValidateResultEnvelope(sealed); err != nil {
		t.Fatalf("sealed envelope must pass the invariant: %v", err)
	}

	okTrue := mustJSON(map[string]any{"ok": true, "status": "succeeded"})
	if err := ValidateResultEnvelope(okTrue); err != nil {
		t.Fatalf("ok=true envelope must pass: %v", err)
	}

	if err := ValidateResultEnvelope(json.RawMessage(`not json`)); err != nil {
		t.Fatalf("non-envelope payload is not this invariant's problem: %v", err)
	}
}

// --- #989: phantom ok=true suspect flag (flag, never fail) ---

// TestRunOnceSuspectFastCompletionFlagged (red-test for #989): a contract-
// declaring brief whose "successful" result carries no JSON block and
// completes implausibly fast must flag suspect_fast_completion in the attempt
// envelope — and must still finish WORKER_COMPLETED (flag, never fail).
func TestRunOnceSuspectFastCompletionFlagged(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitContractTask(t, env, "suspect-989-flag", 1)
	// The phantom shape from the blind audit: exit 0, synthetic-looking
	// success, no deliverable, no stdout evidence.
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded","summary":"worker completed execution successfully"}`, 5*time.Millisecond)
		return child
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-989-suspect", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	if final.TaskID != task.TaskID {
		t.Fatalf("final task mismatch: got %s, want %s", final.TaskID, task.TaskID)
	}
	if final.State != controlplane.StateWorkerCompleted {
		t.Fatalf("suspect flag must never fail the attempt (flag, never fail), state = %q", final.State)
	}

	env2 := decodeEnvelope(t, final)
	if env2["ok"] != true {
		t.Fatalf("expected the phantom ok=true to be preserved (flag, never fail), got %v", env2["ok"])
	}
	if env2["suspect_fast_completion"] != true {
		t.Fatalf("envelope invariant violated (#989): contract-declared brief finished ok=true without the contract block and was NOT flagged suspect: %s", string(final.Result))
	}
	reason, _ := env2["suspect_reason"].(string)
	if !strings.Contains(reason, "output_contract_missing") {
		t.Fatalf("suspect_reason must name the missing output contract, got %q", reason)
	}
	if !strings.Contains(reason, "implausibly fast") {
		t.Fatalf("suspect_reason must qualify the implausible duration for a sub-threshold run, got %q", reason)
	}
}

// TestRunOnceContractPresentNotSuspect guards the happy path (#989): a result
// that carries the declared fenced JSON contract block is never flagged, no
// matter how fast it ran.
func TestRunOnceContractPresentNotSuspect(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitContractTask(t, env, "suspect-989-clean", 1)
	deliverable := "Audit complete.\n```json\n{\"files_audited\": 3, \"verdict\": \"pass\", \"findings\": []}\n```\n"
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded","response":"`+jsonEscape(deliverable)+`"}`, 5*time.Millisecond)
		return child
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-989-clean", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	if final.TaskID != task.TaskID {
		t.Fatalf("final task mismatch: got %s, want %s", final.TaskID, task.TaskID)
	}
	env2 := decodeEnvelope(t, final)
	if flagged, present := env2["suspect_fast_completion"]; present && flagged == true {
		t.Fatalf("false positive (#989): contract-compliant result must NOT be flagged: %s", string(final.Result))
	}
	if reason, present := env2["suspect_reason"]; present && reason != "" {
		t.Fatalf("false positive (#989): suspect_reason set on the happy path: %v", reason)
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return strings.TrimPrefix(strings.TrimSuffix(string(b), `"`), `"`)
}

// TestSuspectFastCompletionHeuristic pins the documented heuristic (#989):
// keyed on DECLARED WORK (the brief's output contract), duration only ever
// qualifies the reason — it can never flag on its own.
func TestSuspectFastCompletionHeuristic(t *testing.T) {
	contractPrompt := "do the audit\n```json\n{\"verdict\":\"pass\"}\n```"
	phrasePrompt := "Output contract: reply with fenced JSON only."
	blockResult := "```json\n{\"verdict\": \"pass\"}\n```"

	cases := []struct {
		name        string
		prompt      string
		result      string
		duration    float64
		wantSuspect bool
		wantFast    bool // implausibly-fast qualifier present
	}{
		{"contract declared + block missing + fast", contractPrompt, "worker completed execution successfully", 1.8, true, true},
		{"contract declared + block missing + slow", contractPrompt, "worker completed execution successfully", 300, true, false},
		{"contract declared + block present (happy path)", contractPrompt, blockResult, 1.0, false, false},
		{"output contract phrase + block missing", phrasePrompt, "all done", 1.0, true, true},
		{"no declared contract is never flagged", "inventory the module", "", 0.5, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			suspect, reason := suspectFastCompletion(tc.prompt, tc.result, tc.duration)
			if suspect != tc.wantSuspect {
				t.Fatalf("suspect = %v, want %v (reason=%q)", suspect, tc.wantSuspect, reason)
			}
			if !suspect {
				return
			}
			if !strings.Contains(reason, "output_contract_missing") {
				t.Fatalf("suspect_reason must name the missing output contract, got %q", reason)
			}
			if tc.wantFast != strings.Contains(reason, "implausibly fast") {
				t.Fatalf("implausibly-fast qualifier mismatch for duration %.2fs: %q", tc.duration, reason)
			}
		})
	}
}

// TestSuspectFlagFeedsWorkerCompletionTelemetry (#989): the suspect flag must
// land in the worker-completion telemetry event payload (the D-09 event
// stream) so downstream supervisors can query it.
func TestSuspectFlagFeedsWorkerCompletionTelemetry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	t.Setenv("G8S_TELEMETRY", "1")
	t.Setenv("G8S_TELEMETRY_DB", dbPath)

	env := newWorkerEnv(t, nil)
	task := submitContractTask(t, env, "suspect-989-telemetry", 1)
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded","summary":"worker completed execution successfully"}`, 5*time.Millisecond)
		return child
	}
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-989-tel", LeaseSeconds: 60}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open telemetry ledger: %v", err)
	}
	defer raw.Close()
	var payloadRaw []byte
	if err := raw.QueryRow(
		`SELECT payload FROM telemetry_events WHERE task_id = ? AND event_type = ?`,
		task.TaskID, string(telemetry.TraceEventTaskCompleted),
	).Scan(&payloadRaw); err != nil {
		t.Fatalf("query task_completed event: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload["suspect_fast_completion"] != true {
		t.Fatalf("worker-completion telemetry payload must carry suspect_fast_completion=true (#989), got %s", string(payloadRaw))
	}
	if reason, _ := payload["suspect_reason"].(string); !strings.Contains(reason, "output_contract_missing") {
		t.Fatalf("telemetry payload must carry the suspect_reason, got %q", reason)
	}
}

// TestSuspectFlagFeedsModelOutcomeLedger (#989): the routing/model-quality
// history ledger (written on every worker completion) must carry the suspect
// flag so phantom-heavy models can be down-weighted.
func TestSuspectFlagFeedsModelOutcomeLedger(t *testing.T) {
	env := newWorkerEnv(t, nil)
	tmpDir := filepath.Dir(env.runDir)
	ledgerPath := filepath.Join(tmpDir, "model_quality_events.jsonl")
	t.Setenv("G8S_MODEL_SCORECARD_PATH", "")
	t.Setenv("G8S_STATE_DIR", "")

	task := submitContractTask(t, env, "suspect-989-ledger", 1)
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded","summary":"worker completed execution successfully"}`, 5*time.Millisecond)
		return child
	}
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-989-ledger", LeaseSeconds: 60}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read model outcome ledger: %v", err)
	}
	var found bool
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec routing.ModelOutcomeRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode ledger record: %v (%s)", err, line)
		}
		if rec.TaskID == task.TaskID {
			found = true
			if !rec.SuspectFastCompletion {
				t.Fatalf("ledger record must carry suspect_fast_completion=true (#989): %s", line)
			}
			if !rec.Success {
				t.Fatalf("suspect flag must never flip the verdict (flag, never fail): %s", line)
			}
		}
	}
	if !found {
		t.Fatalf("expected a model outcome record for %s in ledger: %s", task.TaskID, string(data))
	}
}
