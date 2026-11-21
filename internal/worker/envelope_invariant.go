package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/tamld/g8s/internal/controlplane"
)

// Envelope honesty invariants (#988 / #989).
//
// #988 — every attempt result envelope that reports ok=false must carry a
// typed E_* failure code and a readable detail. The 2026-11-20 blind-audit
// measured a 9router-lane attempt that ended ok=false after ~3.7s with
// policy_enforcement populated but NO error field and NO code: the pre-exec
// transport error reached the envelope only as an untyped `reason`, and two
// downstream consumers could not route on it. The invariant is enforced two
// ways:
//
//  1. explicitly — every hand-rolled ok=false envelope in RunOnce/collect now
//     sets "code" (from the E_* vocabulary) and "error" (readable detail); and
//  2. mechanically — FinishAttempt traffic is funneled through
//     (*Supervisor).finishAttempt, which seals any ok=false envelope that is
//     still missing the typed pair (derive the code from the reason's E_*
//     prefix; fall back to E_WORKER_FAILURE_CODE_MISSING), then validates and
//     leaves a stderr breadcrumb if the invariant somehow still fails.
//
// #989 — a success envelope may be a phantom: the blind audit measured tasks
// reporting ok=true ("worker completed execution successfully") in under 2.3s
// for briefs declaring multi-file audit work. suspectFastCompletion flags
// those conservatively (flag, never fail), keyed on DECLARED WORK, not
// absolute time: the heuristic fires only when the prompt itself declares an
// output contract (a fenced ```json requirement marker, an explicit "output
// contract" requirement, or a "fenced JSON" deliverable requirement) and the
// result carries no JSON block the harness could extract. Duration only ever
// qualifies the reason text (implausibly fast for the declared work); it can
// never flag on its own, so a fast but contract-compliant result — the happy
// path — is never flagged.

// envelopeCodePattern matches the typed E_* error-code vocabulary.
var envelopeCodePattern = regexp.MustCompile(`E_[A-Z0-9_]+`)

// fallbackFailureCode is attached by sealFailureEnvelope when neither the
// envelope nor its reason carries any E_* token (#988).
const fallbackFailureCode = "E_WORKER_FAILURE_CODE_MISSING"

// envelopeInvariantViolationCode is reported by validateResultEnvelope when a
// terminal ok=false envelope still lacks the typed {code, error} pair after
// sealing — which must be impossible; seeing it means the envelope skipped
// (*Supervisor).finishAttempt.
const envelopeInvariantViolationCode = "E_ENVELOPE_INVARIANT_VIOLATED"

// extractEnvelopeCode pulls the first typed E_* token out of the candidate
// failure strings (reason first — the harness convention places the code at
// the head of the reason).
func extractEnvelopeCode(candidates ...string) string {
	for _, c := range candidates {
		if m := envelopeCodePattern.FindString(c); m != "" {
			return m
		}
	}
	return ""
}

// envelopeString reads a string field defensively (a malformed envelope may
// carry a non-string under code/error/reason).
func envelopeString(env map[string]any, key string) string {
	if v, ok := env[key].(string); ok {
		return v
	}
	return ""
}

// sealFailureEnvelope enforces the #988 invariant on one attempt result
// envelope: ok=false ⇒ typed "code" (E_*) + readable "error" present.
// Existing typed fields are preserved verbatim; missing fields are derived
// (code from the reason's E_* prefix, error from reason/summary/status) and
// only synthesized when nothing derivable exists. ok=true envelopes are
// returned unchanged. Non-JSON or non-object payloads are returned unchanged
// (FinishAttempt rejects those upstream).
func sealFailureEnvelope(resultJSON json.RawMessage) json.RawMessage {
	var env map[string]any
	if err := json.Unmarshal(resultJSON, &env); err != nil || env == nil {
		return resultJSON
	}
	if ok, present := env["ok"].(bool); !present || ok {
		return resultJSON
	}
	reason := envelopeString(env, "reason")
	errDetail := envelopeString(env, "error")
	summary := envelopeString(env, "summary")
	status := envelopeString(env, "status")

	if envelopeString(env, "code") == "" {
		code := extractEnvelopeCode(reason, errDetail, summary)
		if code == "" {
			code = fallbackFailureCode
		}
		env["code"] = code
	}
	if strings.TrimSpace(errDetail) == "" {
		detail := firstNonEmpty(reason, summary, status)
		if detail == "" {
			detail = "worker reported ok=false without a readable detail"
		}
		env["error"] = detail
	}
	return mustJSON(env)
}

// ValidateResultEnvelope is the post-run envelope-invariant validator (#988):
// it returns an error for any terminal ok=false envelope that lacks a typed
// E_* code or a readable error detail.
func ValidateResultEnvelope(resultJSON json.RawMessage) error {
	var env map[string]any
	if err := json.Unmarshal(resultJSON, &env); err != nil || env == nil {
		return nil // non-envelope payloads are FinishAttempt's problem, not this invariant
	}
	if ok, present := env["ok"].(bool); !present || ok {
		return nil
	}
	code := envelopeString(env, "code")
	detail := firstNonEmpty(envelopeString(env, "error"), envelopeString(env, "reason"))
	if envelopeCodePattern.FindString(code) == "" || strings.TrimSpace(detail) == "" {
		return fmt.Errorf("%s: ok=false envelope missing typed failure code or detail (code=%q, error=%q, reason=%q, status=%q)",
			envelopeInvariantViolationCode, code, envelopeString(env, "error"), envelopeString(env, "reason"), envelopeString(env, "status"))
	}
	return nil
}

// finishAttempt is the single funnel every worker-side attempt finish goes
// through (#988): it seals the ok=false envelope invariant before the control
// plane persists the verdict, and validates post-seal — a violation after
// sealing is a bug in this package, so it leaves a loud stderr breadcrumb.
func (s *Supervisor) finishAttempt(taskID, workerID, leaseToken string, params controlplane.FinishAttemptParams) (*controlplane.Task, error) {
	params.Result = sealFailureEnvelope(params.Result)
	if verr := ValidateResultEnvelope(params.Result); verr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: result envelope invariant violated for %s: %v\n", taskID, verr)
	}
	return s.cp.FinishAttempt(taskID, workerID, leaseToken, params)
}

// suspectFastCompletionSeconds is the duration floor below which a completed
// attempt whose declared work includes an output contract is annotated as
// implausibly fast (#989). It only ever qualifies the suspect_reason text —
// duration alone never flags (the measured phantom completions ran 1.78-2.23s;
// real multi-file audit work runs minutes).
const suspectFastCompletionSeconds = 10.0

// promptDeclaresOutputContract reports whether the brief declares a structured
// JSON deliverable (#989): a fenced ```json requirement marker, an explicit
// "output contract" requirement, or a "fenced JSON" deliverable requirement.
// Deliberately narrow — flagging is keyed on the brief's own declared work, so
// a generic prompt can never produce a suspect flag.
func promptDeclaresOutputContract(prompt string) bool {
	lower := strings.ToLower(prompt)
	return strings.Contains(lower, "```json") ||
		strings.Contains(lower, "output contract") ||
		strings.Contains(lower, "fenced json")
}

// resultCarriesJSONBlock reports whether the result payload contains a fenced
// JSON block the harness itself could extract (same fence pattern the result
// parser uses, so "contract satisfied" and "result parseable" agree).
func resultCarriesJSONBlock(resultPayload string) bool {
	return fencedJSONPattern.MatchString(resultPayload)
}

// suspectFastCompletion evaluates the #989 phantom-success heuristic.
// Conservative by construction: flag only when the prompt declared an output
// contract and the result carries no JSON block; duration_seconds below
// suspectFastCompletionSeconds is appended to the reason as an implausibility
// qualifier, never an independent trigger. Flag, never fail — the attempt
// verdict is left untouched so downstream supervisors can apply their own
// policy to the signal.
func suspectFastCompletion(prompt, resultPayload string, durationSeconds float64) (bool, string) {
	if !promptDeclaresOutputContract(prompt) {
		return false, ""
	}
	if resultCarriesJSONBlock(resultPayload) {
		return false, ""
	}
	reason := "output_contract_missing: prompt declares a fenced JSON output contract but the result carries no extractable JSON block (phantom-success suspect, #989)"
	if durationSeconds >= 0 && durationSeconds < suspectFastCompletionSeconds {
		reason += fmt.Sprintf("; duration_seconds=%.2f is implausibly fast for the declared work", durationSeconds)
	}
	return true, reason
}
