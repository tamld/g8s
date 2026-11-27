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

// envelopeCodeHeadPattern matches a typed E_* code only at the HEAD of a
// failure string, optionally after leading whitespace (redcell D-01 F8.1):
// the harness convention places the code at the start of the reason
// ("E_PROVIDER_SPAWN_FAILED: exec failed"). The previous unanchored leftmost
// scan let model-controlled prose mint spoofed codes from any embedded E_*
// fragment ("…do not fabricate E_TRUST_ROOT_S0_APPROVAL_REQUIRED codes…"
// sealed E_TRUST_ROOT_*), which the validator then blessed.
var envelopeCodeHeadPattern = regexp.MustCompile(`^\s*E_[A-Z0-9_]+`)

// envelopeCodeFullPattern requires the WHOLE string to be one typed E_* token
// (redcell D-01 F8.1 companion): a code field like "garbage E_SPOOFED_CODE
// tail" must not validate as a typed code.
var envelopeCodeFullPattern = regexp.MustCompile(`^E_[A-Z0-9_]+$`)

// fallbackFailureCode is the NEUTRAL code attached by sealFailureEnvelope
// when the reason does not START with a typed E_* token (#988; anchored
// derivation per redcell D-01 F8.1 — embedded fragments never derive codes).
const fallbackFailureCode = "E_WORKER_FAILURE_CODE_MISSING"

// envelopeInvariantViolationCode is reported by validateResultEnvelope when a
// terminal ok=false envelope still lacks the typed {code, error} pair after
// sealing — which must be impossible; seeing it means the envelope skipped
// (*Supervisor).finishAttempt.
const envelopeInvariantViolationCode = "E_ENVELOPE_INVARIANT_VIOLATED"

// extractEnvelopeCode pulls the typed E_* code out of the candidate failure
// strings. HEAD-ANCHORED (redcell D-01 F8.1): a candidate only contributes a
// code when the string STARTS with the E_* token — an embedded fragment
// mid-sentence or inside a larger identifier ("SEE_E_TIMEOUT …") never
// derives a code, so the caller falls back to the neutral
// fallbackFailureCode instead of sealing a spoofed vocabulary token.
func extractEnvelopeCode(candidates ...string) string {
	for _, c := range candidates {
		if m := envelopeCodeHeadPattern.FindString(c); m != "" {
			return strings.TrimSpace(m)
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
		// redcell D-01 F8.1: derivation fills ONLY the fallback path and
		// derives ONLY from the reason's head-anchored E_* token — reason,
		// summary and stdout are model/provider-controlled prose on the
		// dynamic result path, so any other candidate would mint a spoofable
		// vocabulary token. Everything else seals the neutral fallback code.
		code := extractEnvelopeCode(reason)
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
	// redcell D-01 F8.1 companion: the code FIELD itself must be one full
	// typed token — an embedded E_* fragment inside larger prose ("garbage
	// E_SPOOFED_CODE tail") must not validate as a typed code.
	if !envelopeCodeFullPattern.MatchString(code) || strings.TrimSpace(detail) == "" {
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
// JSON block the harness itself could extract: the same fence pattern the
// result parser uses (so "contract satisfied" and "result parseable" agree),
// with a MINIMAL-CONTENT floor (redcell D-01 F8.2) — the extracted body must
// parse as a JSON object carrying at least one key. The bare pattern accepted
// a single backtick + optional json tag + an empty `{}` body, so a 1-second
// phantom result carrying inline `{}` satisfied the check and suppressed the
// #989 phantom-success flag; content-free blocks no longer count.
func resultCarriesJSONBlock(resultPayload string) bool {
	for _, m := range fencedJSONPattern.FindAllStringSubmatch(resultPayload, -1) {
		var obj map[string]any
		if json.Unmarshal([]byte(m[1]), &obj) == nil && len(obj) >= 1 {
			return true
		}
	}
	return false
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
