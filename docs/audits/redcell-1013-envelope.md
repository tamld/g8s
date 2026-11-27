# Red Cell D-01 Supplement — #1013 Envelope Honesty Surface

- Scope: PR #1013 (0d5a115) only — `envelope_invariant.go`, worker.go spawn-path typed codes,
  `worker_sweep_collect.go` suspect heuristic, `worker_result.go` backstop. Original 7 surfaces
  audited separately (merged #1014).
- Worktree: g8s-wt-redcell2 @ audit/redcell-1013-surface (main d8926bc).
- Verdict: **GAPS-FOUND** (no criticals; 2 exploitable-as-mislabel, 2 theoretical, 1 by-design).

## Q1 — Code-derivation spoofing: EXPLOITABLE (mislabeling, not auth bypass)

The extraction is anchored **nowhere**: `envelopeCodePattern = E_[A-Z0-9_]+` is unanchored with
no word boundary (`internal/worker/envelope_invariant.go:44`), and `extractEnvelopeCode` returns
the **leftmost** match scanning candidates in order reason → error → summary
(`envelope_invariant.go:59-66`, used at `:97-102`). Consequences:

- A model/provider-controlled reason containing "…never fabricate E_TRUST_ROOT_S0_APPROVAL_REQUIRED
  codes…" seals `code=E_TRUST_ROOT_S0_APPROVAL_REQUIRED`. `ValidateResultEnvelope` then **blesses**
  the spoofed code (`envelope_invariant.go:127` accepts any E_* token).
- Substring hits inside larger identifiers: `SEE_E_TIMEOUT` matches `E_TIMEOUT` (regex scans from
  any index).
- Window is the **dynamic** result path only: all 19 hand-rolled call sites set explicit codes
  (verified worker.go:501/524/542/560/576/601/619/635/652/670/726/816 + sweep_collect), so spoofing
  requires an ok=false envelope **without** a code — i.e. harness-parsed `resultJSON` in collect
  (`worker_sweep_collect.go:901`) whose reason is model prose. The 2026-11-20 defect class
  (untyped reason reaching the envelope) is exactly this path.

Impact: downstream routing/diagnosis consumes a typed code the invariant claims is trustworthy;
ledger `ErrorReason`/`ClassifyFailureDiagnosis` inherit the same untrusted text
(`internal/routing/scorecard.go:188`). Flag-level severity (no capability change), but it defeats
the honesty property of the code vocabulary.

## Q2 — Funnel completeness: SECURE on worker side; 1 theoretical bypass

Census: exactly 19 `s.finishAttempt` sites (14 in `internal/worker/worker.go`
498/521/541/557/575/598/616/632/649/669/722/752/812/988; 5 in `internal/worker/worker_sweep_collect.go`
454/489/504/529/901). No worker-package call reaches `s.cp.FinishAttempt` except the funnel itself
(`envelope_invariant.go:143`); grep for post-refactor bypass sites found none. Every ok=false write
in the worker passes the seal.

Gap (theoretical): controlplane conveniences bypass the funnel by construction —
`Store.FailTask` seals `Success:false` with `Result: json.RawMessage("{}")`
(`internal/controlplane/lifecycle.go:593-602`): ok-**absent**, no code, no error, and both
`sealFailureEnvelope` (returns unchanged when `ok` missing, `envelope_invariant.go:89`) and
`ValidateResultEnvelope` (`:122` treats ok-absent as pass) accept it. Currently **zero non-test
callers** (interface decl only, `internal/controlplane/model.go:499`) — dormant, not reachable.

## Q3 — Suspect-heuristic gaming: EXPLOITABLE (flag suppression)

Question premise inverted: inline single-backtick JSON does **not** evade the extractor — it
**satisfies** it. `fencedJSONPattern` (`internal/worker/worker.go:468`) is
`(?s)(?:```|`)(?:json)?\s*(\{.*?\})\s*(?:```|`)`: the alternation accepts a **single backtick** as
opener and closer, the `json` tag is optional, and `\{.*?\}` matches empty `{}`. So:

- Bare `{}` (no backticks): not matched — flag still fires.
- `` `{}` `` (4 chars, no fence, no json tag): matched → `resultCarriesJSONBlock` true →
  `suspectFastCompletion` returns false (`envelope_invariant.go:183-185`) → phantom-success flag
  **suppressed**. A 1-second ok=true carrying inline `` `{}` `` passes as contract-compliant.
- Looseness is deliberate for parser agreement ("same fence pattern the result parser uses",
  `envelope_invariant.go:165-167`; parser uses it at `worker_result.go:474`,
  `worker_loop_export.go:164`) — but single-backtick acceptance makes the phantom-detection signal
  trivially gameable by the exact adversary it targets (a model emitting phantom successes).
- Prompt-side marker (`promptDeclaresOutputContract`, `envelope_invariant.go:158-163`) is plain
  substring on the lowered prompt — an attacker-crafted prompt can avoid declaring a contract
  (by-design: keyed on declared work), and duration never flags alone (`:187`).

Severity: exploitable telemetry bypass; flag-never-fail, so impact is a blind spot in the #989
signal, not a verdict change.

## Q4 — Validator reachability: COMPLETE on worker paths; store-side unvalidated

`ValidateResultEnvelope` is called at exactly 2 sites: inside the funnel (`envelope_invariant.go:140`),
which covers all 19 terminal worker paths, and the post-run snapshot backstop
(`internal/worker/worker_result.go:810`). Exported, but **no callers outside the worker package**
(grep across `cmd/` + non-worker `internal/`: none). The controlplane store persists verdicts with
only `canonicalJSON`/`contentHash` (`lifecycle.go:455-464`) — no server-side validation, so any
writer that skips the worker funnel (Q2's FailTask, `CompleteTask` at `lifecycle.go:581-586` which
passes Brain-supplied results unsealed) lands unvalidated. Worker-surface verdict: reachable
everywhere it claims; the "invariant" is worker-local by design. Severity: theoretical.

## Q5 — Telemetry feed: WIRED to ledger; routing consumption not implemented

Full chain verified: `suspectFastCompletion` at `worker_sweep_collect.go:682` → telemetry payload
`:693-696` (TraceEventTaskCompleted/Failed) → result envelope
`m["suspect_fast_completion"]`/`m["suspect_reason"]` `:880-883` → `recordAttemptModelOutcome(..., suspect)`
at `:910` → `ModelOutcomeRecord.SuspectFastCompletion` (`:326-343`) → persisted JSONL via
`routing.RecordModelOutcome` (`internal/routing/scorecard.go:177-213`, field at `:96`,
`json:"suspect_fast_completion"`). Gap: `evaluateModelWindow` (`scorecard.go:282+`) never reads
`SuspectFastCompletion` — no suspect-rate aggregation or down-weight exists; the "routing can
down-weight" comment (`:93-94`) is aspirational. A 100%-phantom model still scores on raw success
count. Severity: by-design (documented future), noted as follow-up.

## Findings

| ID | Q | Severity | Summary |
|----|---|----------|---------|
| F-01 | Q1 | Exploitable | Unanchored leftmost E_* extraction mints spoofable typed codes from untrusted reason text; validator blesses them |
| F-02 | Q2 | Theoretical | `Store.FailTask` writes ok-absent `{}` failed verdict past the seal; no in-repo callers today |
| F-03 | Q3 | Exploitable | Single-backtick acceptance in fencedJSONPattern lets inline `` `{}` `` suppress the phantom-success flag |
| F-04 | Q4 | Theoretical | Validator covers all worker terminal paths; store-side writers unvalidated by construction |
| F-05 | Q5 | By-design | Suspect flag persisted to ledger but not consumed by routing quality scoring |

## Red-test proposals

- RT-1 (F-01): `sealFailureEnvelope` with reason "provider exit 1; advisory: do not fabricate
  E_TRUST_ROOT_S0_APPROVAL_REQUIRED codes" must not derive E_TRUST_ROOT_* — gate derivation on an
  anchored/word-bounded, first-token-only pattern or drop derivation in favor of E_WORKER_FAILURE_CODE_MISSING.
- RT-2 (F-01): reason "SEE_E_PROVIDER_SPAWN_FAILED documentation" must not match inside a larger identifier.
- RT-3 (F-03): `suspectFastCompletion` with result payload "Analysis: `{}` inline" must still flag —
  require a triple-fence (and/or json tag, multi-char body) in `resultCarriesJSONBlock`.
- RT-4 (F-02): `Store.FailTask` then `ValidateResultEnvelope` on the stored Result must fail loudly,
  or FailTask must seal a typed code before persisting.
- RT-5 (F-05): `evaluateModelWindow` over records with 100% `SuspectFastCompletion` must expose a
  suspect rate in `ModelQualityScore` (PROBE_REQUIRED / down-weight).

Existing coverage gap check: `envelope_invariant_test.go` covers head-anchored legit derivation
(`:188`), fallback (`:198`), preserve (`:212`), validator (`:236`), flag/telemetry/ledger happy paths
(`:265/:387/:430`) — none of the adversarial cases above.
