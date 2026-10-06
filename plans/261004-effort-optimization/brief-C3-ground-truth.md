# Task: C3 — ladder diagnosis dispatch + baked-name model/effort alignment (issue #550, ground-truth fixes)

Repo: g8s @ main. Two defects surfaced by the FIRST real ladder
traversal and a real provider refusal (dogfood ground truth, tasks
bb2957f8 + the bb2957f8 advance attempt). Read first:
cmd/g8s/ladder.go (the advance path around the SubmitTask call) +
cmd/g8s/submit.go (model/effort resolution) +
internal/controlplane/lifecycle.go:169 (payload.Prompt validation —
prompts are REDACTED in stored request_json: prompt_hash +
prompt_redacted:true; lineage CANNOT replay them) +
internal/ladder/classifier.go (staleness markers).

## Delivery protocol

Scratch worktree. Write ONLY to:

- cmd/g8s/ladder.go (diagnosis-prompt generation + prompt-file for
  re-dos + the Timeout bug) + ladder_test.go (extend)
- cmd/g8s/submit.go (baked-name alignment) + submit_effort_test.go
  (extend)
- internal/ladder/classifier.go (one marker addition) +
  classifier_test.go (extend)

Do NOT run git commit. Do NOT touch internal/controlplane,
internal/routing, internal/worker.

## Required implementation

1. **Diagnosis dispatch fix (ladder.go)** — ground truth: `ladder
   advance` on a real task fails E_DENIED "request.prompt is required"
   because the stored request_json is prompt-redacted:
   - Rung 0 (diagnosis): the dispatch does NOT replay the original
     prompt. Generate a diagnosis prompt from available evidence
     (task id, class, last error, result summary) — the diagnosis
     worker's job is to classify the failure shape and verify class
     checks, read_only, low effort. Deterministic template, table-
     tested.
   - Rungs 1..5 (escalation re-dos): the original prompt is NOT
     recoverable (redacted by design) — require `--prompt-file` /
     `--prompt` on the advance command; typed usage error naming the
     flag when a re-do rung is requested without one. Never synthesize
     or guess a prompt.
   - REAL BUG visible in the same call: `Timeout: lin.latestTask.
     RequestHash` passes a content hash as the execution window. Carry
     the original task's timeout from the payload (`timeout` key) with
     the record's value as fallback.
2. **Baked-name model/effort alignment (submit.go)** — ground truth:
   agy hard-refuses a suffix/flag conflict ("--model gemini-3.8-flash-
   low conflicts with --effort=medium" — real task failure). When the
   effective model matches the baked-name pattern
   (`-<level>` suffix where level ∈ config.EffortLadder, for a
   provider whose catalog style is baked-name) AND differs from
   effort_applied: rewrite the model to the `-{effort_applied}` variant
   of the same base name, keep the requested model in the payload as
   `model_requested`, set `model_realigned=true`, and record the realign
   in effort_applied semantics (no silent failure — the payload shows
   both). Plain pass-through when they already agree or the model is
   not baked-name.
3. **Staleness marker (classifier.go)**: add the real agy refusal text
   ("conflicts with --effort" / "invalid model selection") to the
   provider-rejection markers so this failure class tags
   `catalog-stale(model, level)` — it is exactly the
   catalog-lied-about-support signature (DoD 6).

## Constraints

- stdlib; `go test ./cmd/g8s/ ./internal/ladder/` green;
  `go build ./...` green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Red-first tests: diagnosis prompt generation (evidence embedded,
  deterministic), re-do without prompt-file → typed error, Timeout
  carries the payload timeout (not the hash), alignment matrix
  (agree/pass-through, disagree/rewrite+record, non-baked
  pass-through), staleness marker hit.

## Report

The generated diagnosis prompt template, the advance flag surface,
the alignment rule and payload keys, and the test matrix with pass
counts.
