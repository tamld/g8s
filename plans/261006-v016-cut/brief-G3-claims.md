# Task: G3 — bind the effort wave's quantitative claims to evidence
# (v0.16 gate G3, issue #548 standard)

Repo: g8s @ main. The v0.16.0 release notes will carry cost-per-class
numbers; the claims registry must already bind every machinery claim
to a test or a demo command BEFORE the tag. claims_check FAILS
proof-less claims — that gate is the deliverable.

Read first (in order):

- docs/claims.yml — the registry format (bound claims with
  `test:`/`eval:` bindings at top level; `unbound:` entries with
  `demo:` commands; every claim carries a `source:` path)
- tools/claims_check.sh — what the checker enforces (test names must
  match `func <Name>(` in *_test.go; source paths must exist)
- The wave's real test names: grep `func Test` in
  internal/routing/effort_test.go, internal/lane/effortclass_test.go,
  internal/lane/effortsignal_test.go, internal/ladder/*_test.go,
  cmd/g8s/submit_effort_test.go, cmd/g8s/submit_route_test.go
- .g8s/effort-classes.yml + .g8s/agent-models.yml — what the
  registries promise

## Required additions (docs/claims.yml ONLY)

Add six bound/unbound claims following the existing entry style
(exact `test:` names verified by grepping the tree — never invent a
name; prefer a real test you have confirmed exists over a prettier
hypothetical one):

1. claim-effort-submit-knob — submit validates --effort against the
   7-level ladder at parse time, default medium. (demo:
   `bin/g8s submit --help`)
2. claim-effort-class-registry — write-scope class registry maps
   paths to effort defaults, first match by priority wins,
   unregistered fails open to medium. (bind to the lane
   effortclass table test)
3. claim-effort-signal-demotion — declared signals (blast radius +
   LOC estimate) demote the path-class effort per matrix v1.1.
   (bind to the lane effortsignal test)
4. claim-effort-baked-realignment — baked-name model ids realign to
   the matching variant at submit and mismatched (model, effort)
   combos are never queued (issue #568). (bind to
   TestSubmitEffort_RealignmentConsumesWinningLevel or the exact
   current name in cmd/g8s/submit_effort_test.go)
5. claim-effort-token-telemetry — worker usage capture records
   effort class/level with token counts for per-class aggregation.
   (bind to the worker/telemetry test from #558 — find it)
6. claim-ladder-gauges-cli — ladder gauges report pass-rate,
   escalation-rate and HITL metrics per class. (demo:
   `bin/g8s ladder gauges`)

Each entry: one-sentence claim text, evidence block, source path
(the SSoT doc or code file that owns the statement). Keep the YAML
grep/sed-friendly (no anchors, no multi-line strings).

## Delivery protocol

Scratch worktree. Write ONLY to:

- docs/claims.yml

Do NOT run git commit. Do NOT touch code, tests, or other docs.

## Constraints

- `bash tools/claims_check.sh` must pass (run it, include the
  SCORECARD line in the report).
- If a claim cannot find a real binding, say so in the report
  instead of binding to a nonexistent test — an honest gap beats a
  fabricated citation (that is the whole point of this gate).

## Report

- The six entries as added; claims_check output; any claim you
  could NOT bind and why.
