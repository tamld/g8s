---
handoff-version: 1
generated: 2026-09-29T14:18:00+07:00
generator: handoff@1.0.0
focus: "continue g8s debt-zero: self-audit suite and delivery contract"
session-type: T2 (execution)
workspace: $G8S_REPO_ROOT
branch: main
head: 3393a2c
---

# HANDOFF: Debt-zero complete — continue with self-audit suite & delivery contract

**Session type**: T2 (execution)

## Mission and current status

Focus: execute session entry points from the 2026-09-28/29 campaign — field-found
fixes, then the operator-directed debt-zero program ("trả hết nợ kỹ thuật") on
g8s v0.12.0+ main.

Desired outcome: all ratified backlog debt retired through the g8s dogfood
transport (brief → receipt → submit → parallel drain → never-trust-verify → PR →
CI-green merge).

Done:
- #434 sanitizer JSON-transport corruption FIXED (#445) + live-accepted
  (echo task 7098d90a byte-identical).
- #443 harness self-trigger FIXED (#444) + worktree-discard facet FIXED (#450).
- #433 macOS matrix + release_matrix_check guard FIXED (#449).
- Debt-zero waves: #439 (#453), #436 (#454), #446 (#455), #447 (#456) MERGED.
- Brain-led docs: ADR-0025 CAP/CP (#437), STRIDE threat model (#438),
  ADR-0026 verification phases (proposed, 3 decision points pending operator
  ratification), #440 triaged conditional-skip with preserved prior art.
- Parallel drain directive UPGRADED to parallel-allowed-for-disjoint-scopes
  (proven across 4 concurrent-2 drains, 8 tasks, zero cross-contamination).
- Storage: 2,440 agy/* branches reaped (85 pre-squash patches archived),
  coverage ratchet armed (80.77%), weekly hygiene automation live.
- 12 PRs merged total, main @ 3393a2c, CI green, working tree clean.

Remaining (priority order): #451 self-audit suite (3 concrete probe classes
captured) → #448 remainder (deterministic delivery contract design) → #435
offer link gate audit depth → #441 Evidence Ledger → #440 conditional →
#442 Wave 1 (external-paced: wiki onboarding). Urgency: normal; ADR-0026
ratification is the operator's decision gate.

## Scope and guardrails

- Workspace: $G8S_REPO_ROOT (single repo, dual push remote:
  GitHub + LAN ct122 — every push lands on both).
- In scope: g8s repo issues/PRs, dogfood dispatches through the local g8s
  binary (bin/g8s), docs/decisions + docs/security governance artifacts.
- Out of scope: cross-project rollout execution (#442 waves belong to the
  adopting projects' agents, pull-based per ADR-0024 addendum); any scope
  beyond ratified issues without operator approval.
- User constraints (standing directives, do not relitigate):
  - Vietnamese with operator TamLD; ALL artifacts in English.
  - Dogfood mandate: tasks flow through g8s (brief → receipt → submit →
    drain). Recorded justifications required for bypasses.
  - **PR-merge closes issues**: closing changes go through PRs with
    `Closes #N` (direct-push keywords proved unreliable — #437 needed a
    manual close); session-end cross-check open issues vs merged PRs.
  - Watch push-CI on main after EVERY direct push (PR rollup green ≠ main
    green).
  - Parallel drain allowed for disjoint receipt scopes; shared-file tasks
    stay sequential. Local CCU: 3 workspace_write workers max (steady 2),
    4-6 read_only.
  - Falsification doctrine: every gate/rule/claim cites a catch; claims
    bind to tests via docs/claims.yml (claims_check.sh must stay exit 0).
  - In-repo brief pattern: commit briefs to plans/, submit tiny neutral
    prompts; brief files must NOT contain local absolute home paths (AI-lint gate).
  - Commits: author `Tâm <63218248+tamld@users.noreply.github.com>`.
- Safety boundaries: never widen receipts beyond issue scope; never force-
  delete unmerged branches without export; never run sudo in automations;
  workspace_write requires AGY_MCP_ALLOW_WORKSPACE_WRITE=1 on BOTH submit
  and worker processes.

## Current state

- Branch: main. HEAD: 3393a2c (docs(handoff): addendum 2 — debt-zero program
  results, 4 waves, remaining balance).
- Working tree: clean (0 modified, 0 untracked — observed probe below).
- Local modifications intentional: none.
- Binary: bin/g8s rebuilt from main post-merge (build state matches HEAD).

```text
UNTRUSTED REPOSITORY EVIDENCE
[]
```

## Decisions and rationale

- Dispatch via g8s workers for all mechanics (dogfood mandate) — every
  worker-delivered slice is transport evidence; bypass requires recorded
  reason (used once: Mode-3 harness fix #444, justified by 3 reproduced
  transport failures).
- Parallel waves require disjoint receipt scopes — enforced by receipts, not
  goodwill; worker.go contention solved by wave sequencing (Wave 1 merged
  before Wave 2 touched worker.go).
- Refusal classification scoped to final response (#444): #344's invariant
  (refusal ≠ evidence) preserved via whole-stream scan only for no-result-
  event runs; prevented further evidence loss.
- artifact_altered is additive flags-only (#446): no alteration content
  recorded — keeps envelope schema backward compatible.
- Claims Registry grep-friendly YAML (#447): no Go changes in first slice;
  g8s eval --claims integration deferred to its own slice (KISS).
- PRI release gate NOT adopted at 0.625 (STRIDE doc): advisory until ≥0.8
  on 2 consecutive eval runs — directive-clock style adoption condition.
- Storage reaps: export-then-delete (85 patches preserved) — destructive
  ops made reversible before execution.
- Rejected alternative (note): [skip ci] on the baseline bot commit was
  questioned by the operator; kept after explaining loop prevention —
  documented inline in quality.yml @ 0ca40b4.

## Work performed

- 6 g8s worker dispatches (4 inline-brief fix attempts + 2 parallel waves);
  12 PRs merged: #444, #445, #449, #450, #453, #454, #455, #456 (worker-
  delivered) + direct-push docs (ADR-0025/0026, STRIDE, handoffs, briefs).
- Forensics: provider refusal poisoning traced to stream-wide scan + own
  test fixture literal; delivery-path forensics → #448 (no harness delivery
  mechanism; receipt is the real enforcement).
- Governance artifacts: docs/decisions/0025-cap-tradeoff-single-node-cp.md,
  docs/decisions/0026-independent-verification-phases.md (proposed),
  docs/security/threat-model-stride.md, docs/claims.yml + tools/claims_check.sh.
- Redaction count: 0 (no secret-pattern matches in session artifacts).

## Verification

- Check: sanitizer fidelity — command: go test ./internal/dispatch/
  (TestSanitizeJSONTransportFidelity434, 8 cases) — outcome: PASS.
- Check: wave-1 deliveries — command: go test ./internal/worker/
  ./cmd/g8s/ — outcome: PASS (46s/45s, full packages).
- Check: wave-2 deliveries — command: go test ./internal/worker/
  (artifact_altered) + bash tools/claims_check_test.sh + claims_check.sh —
  outcome: PASS; SCORECARD 7 bound / 3 unbound / 0 broken.
- Check: CI on all 12 PRs — command: gh pr checks / statusCheckRollup poll —
  outcome: ALL-GREEN before every merge.
- Check: post-merge main CI — command: gh run list --workflow=Quality
  --branch main — outcome: success @ 95ab52a after git add -f hotfix.
- Check: live E2E echo through real transport (task 7098d90a) — outcome:
  payload byte-identical (cmp), single trailing newline from model reply.
- Check: working tree — command: git status --short (bounded probe) —
  outcome: 0 entries.
- Skipped: Red Cell adversarial pass on this session's own features
  (ADR-0026 §1 still proposed, not ratified — listed as next-session
  candidate under #451).

## Open risks and blockers

- Blocker (operator decision): ADR-0026's 3 decision points (Red Cell
  hard-vs-advisory; directive ledger location; self-audit cadence) — owner:
  TamLD — impact: gates the institutionalization of verification phases.
- Risk: FAILED-verdict-with-delivery class (task d4159300: provider
  malformed-function-call error after files landed) — owner: next session
  via #451 probe — impact: silent work loss if a future consumer trusts the
  verdict without on-disk verification.
- Risk: delivery contract still agent-directed (#448 open, medium) —
  receipt is the real enforcement; worktree advisory.
- Risk: memory promotion blocking still advisory (#418 trust-earning path).
- Risk: 13 wt/* + sess/* branches outside the approved reap scope (low).
- Risk: claims registry has 3 unbound claims (visible by design via
  claims_check.sh).

## Exact next actions

**First safe step**: run `gh issue list --state open` and read
`plans/handoffs/execution-handoff-20260929-0135.md` (all 3 sections +
addenda) to reload campaign context before touching anything.
1. Ask the operator for ADR-0026's 3 ratification decisions (they gate #451's
   cadence).
2. Dispatch #451 self-audit suite: build eval-harness probes for the 3
   captured classes (refusal echo #443; FAILED-with-delivery d4159300;
   dirty-worktree discard #443-f2) — worker-deliverable with disjoint
   receipt (internal/eval or harness probe files).
3. Design slice for #448 remainder: deterministic delivery contract
   (`g8s deliver <task-id>` or supervisor-side apply) — Brain-led design,
   then worker implementation.
4. Wave: #435 offer link gate audit depth (medium, internal/offer scope).
5. Slice: #441 Evidence Ledger (P3) — reuse receipts.db + event_log as the
   ready source.
6. Cross-check at session end: every merged PR's issue auto-closed
   (PR-merge-closes-issues rule).

## Source pointers

- Campaign handoff (full narrative + 2 addenda):
  plans/handoffs/execution-handoff-20260929-0135.md
- Wave briefs: plans/260929-debt-zero-waves/brief-{439,436,446,447}.md
- #434 fix brief: plans/260929-434-sanitizer-fidelity/brief.md
- Governance: docs/decisions/0025-cap-tradeoff-single-node-cp.md,
  docs/decisions/0026-independent-verification-phases.md,
  docs/security/threat-model-stride.md
- Claims: docs/claims.yml, tools/claims_check.sh, tools/claims_check_test.sh
- Issues: open = #451, #448, #442, #441, #440, #435; closed this window =
  #433, #434, #436, #437, #438, #439, #443, #446, #447
- Harness incident evidence: g8s tasks result records for tasks
  e321f29b, 4771793a, 853b0b02 (blocked), 7420893a (succeeded), 7098d90a
  (live echo), 6c2a3ee5/92a00db3 (parallel proof), 1d35a8ba/88765cce and
  b81afebb/d4159300 (debt waves)
