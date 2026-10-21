# Multi-Worker Fan-Out Playbook (Mode 2 extension)

> One issue/task → N workers with different roles. Read `mode-2-supervisor-worker.md`
> and `hard-boundary.md` first. The supervisor decomposes; workers never see
> each other's output.

## Decomposition law

A task is fan-out-eligible when it splits into **independent read-only
packets** with disjoint roots and per-packet oracles. If two packets' outputs
must be combined to know whether either is correct, they are NOT independent —
sequence them instead (hunt → verify), or do the join at supervisor level with
each packet still independently checkable.

Every packet gets the full minimum packet from SKILL.md. A fan-out plan file
(`--plan plan.json`) records, per packet: objective, role, root, timeout,
oracle, evidence sink. The plan is the DoR — one missing field blocks ALL
dispatches in the plan.

## Role matrix

| Role | Use for | Output shape | Permission |
|---|---|---|---|
| `collector` | deterministic file/metadata inventory | list/table, zero judgment | `read_only` |
| `scout` | bounded pattern/reference hunt with citations | report + file:line | `read_only` |
| `summarizer` | redacted digest of an already-captured packet | prose digest ≤ N lines | `read_only` |
| `verifier` | declared check against a declared oracle | pass/fail + evidence | `read_only` |
| `mcp-mapper` | API/tool surface inventory | table | `read_only` |
| `test-runner` | test execution | structured result | separate authorization; workspace_write only with receipt + worktree isolation |

Role discipline: the role names the OUTPUT CONTRACT, not the model. A scout
that returns un-cited impressions failed its role, even if the text is useful.

## Canonical shapes

```text
1. FAN-OUT INVENTORY (N collectors, disjoint subtrees)
     issue: "map the module"  →  collector(A/) collector(B/) collector(C/)
     join: supervisor merges tables; each table independently checkable

2. HUNT-VERIFY (scout → verifier, sequenced, same or overlapping root)
     issue: "find + confirm leak"  →  scout(hunt) → verifier(check scout's citations)
     join: verifier's verdict is the acceptance; scout text is evidence only

3. DIGEST-JOIN (collect first, then summarize redacted packets)
     issue: "digest the evidence"  →  collector(raw) → supervisor redacts → summarizer(packet)
     join: summarizer sees only redacted input; never raw captures

4. CONCURRENT DRAIN (same role, queue of tasks)
     g8s worker --once=false --concurrency N   (requires git checkout; per-attempt worktrees)
     join: queue semantics; per-task receipts; N>1 hard-gated on isolation

5. MANIFEST-LOCKED MULTI-PHASE PIPELINE (creative / multimodal / multi-component builds)
     Phase 0 (F0 exclusive): freeze SSoT contract (facts + continuity/interface manifest) in trust_boundaries
     Phase 1..K (parallel F1 builders): disjoint WriteReceipts (e.g. data/script vs assets/audio vs scenes/ui)
     Phase K+1 (independent F1 verifier, Author != Reviewer): multi-gate check (Gate A content/spec, Gate B visual/structural, Gate C runtime/output)
     join: supervisor verifies all gate stamps + SHA-256 chain before HITL acceptance

6. T0..T3 MULTI-AGENT TOURNAMENT (Broad Horizontal vs. Deep Vertical F0 → F1 → F2)
     T0 (F0 Scope & Brief Freeze): Issue disjoint WriteReceipts + G8S_ADD_DIRS multi-root ceilings (#643)
     T1 (F1 → F2 Dialectical Fan-Out): F1 middle managers spawn cross-family F2 subagents (REA / Proponent / Skeptic / Synthesizer) via g8s llm-call; depth-2 observational telemetry recorded in $G8S_STATE_DIR/f2_telemetry/<task_id>.jsonl
     T2 (Blind Independent Review → Jev Triage → Adversarial Comparison): Reviewer evaluates requirements and code independently BEFORE seeing F0's verdict or Jev's heuristic score; disagreements are resolved by counterexamples or executable tests, never by model concession
     T3 (Perimeter Verification & Drill-Down Audit): Run g8s verify --task <id> --json (checks DoDMatrix, f2_summary observational digest + telemetry_sha256, anti-theater net-delta guards #644, and signal-matched ratified lesson injection); F0 drills down into primary evidence (RawLogPath, worktree diff, source files) whenever conflicts or anomalies arise
```

### Horizontal vs. Deep-Vertical Tournament Modes

| Dimension | Mode A: Broad Horizontal (`Đánh diện rộng`) | Mode B: Deep Vertical (`Đánh diện chuyên sâu`) |
|---|---|---|
| **Topology** | `1 F0 → m F1 (1 per repo/subsystem) → n F2` | `1 F0 → k F1 (Domain Leads: UX, Security, Deploy) → n F2` |
| **Target Scope** | Multiple sibling repos or large disjoint packages | Single target repository (`--add-dir <repo>`) with deep domain slices |
| **F2 Roles** | Concept Harvester, Feasibility Auditor, Red-Team Skeptic, Blueprint Synthesizer | REA Researcher, Red-Team Skeptic, Code Implementer, Acceptance Verifier |
| **F0 Context & Drill-Down** | F0 reads sealed F1 handoffs + `RoundReceiptSchema` (`f2_summary`) by default; drills down to raw source/logs on conflict | F0 orchestrates multi-round debate/verification via `BriefSchema` ↔ `RoundReceiptSchema`, drilling down to primary evidence whenever contradictions appear |

### Methodological & Epistemic Guardrails (GPT 6.1 Sol Ultra Audit)

1. **Blind Independent Review (Anti-Sycophancy)**: Never feed F0's preferred conclusion or Jev's heuristic score into an F1/F2 reviewer's initial prompt. First collect an independent evaluation from requirements + code; only then compare competing arguments. A model conceding (`"F0 and Jev are right"`) is not proof — only a counterexample or executable test arbitrates.
2. **Jev Calibration & Decision Delta Tracking**: Scores like `breach_prob = 0.05` from `g8s reflex triage` are uncalibrated model heuristics, not validated incident probabilities. Track: (a) which decision Jev changed, (b) whether the changed decision reduced escaped defects/rework, and (c) whether net cost decreased.
3. **Default Summary, Mandatory Drill-Down on Conflict**: F0 reads `RoundReceiptSchema` by default to preserve context budget, but F0 is NEVER forbidden from inspecting primary source files, worktree diffs, `RawLogPath`, or `$G8S_STATE_DIR/f2_telemetry/<task_id>.jsonl` when contradictions or high-risk changes occur.
4. **Git-Tracked Wiki vs. Scoped AgentMemory & Conditional Re-open**: Architectural knowledge and decision rationale belong in Git-tracked Wiki/ADRs; `AgentMemory` (`vault.db` / `memory.db`) holds scoped operational heuristics with lifecycle decay. Deferred proposals use `deferred_with_reopen_criteria` rather than `"rejected permanently"`.
5. **Controlled Empirical Comparison**: Evaluate multi-tier (`F0 → F1 → F2`) orchestration against a simpler baseline under equal budget by measuring: accepted deliverables, escaped defects, rework time, F0 context consumption, and post-interruption recovery.

Shapes 1–3 and 5 are supervisor-side N submits (default). Shape 4 uses the worker
loop's own bounded pool (#394) — one process, N isolated attempts.

## Join protocol

1. Wait for every packet's terminal receipt; a missing/late packet is an
   EVIDENCE_GAP for that packet — never filled by another worker's output.
2. Check each packet against ITS OWN oracle before any cross-packet reasoning.
3. Join at supervisor level: merge inventories, sequence hunt→verify verdicts,
   digest redacted packets. Conflicts between packets are a RETEST signal, not
   a negotiation.
4. Record per-packet verdicts (`reuse` / `avoid` / `retest`) in the campaign
   ledger; the joined artifact cites every packet's task/receipt ID.

## Budgets and stop conditions

- Default N ≤ 4 concurrent packets; more requires disjoint roots AND a written
  reason in the plan file.
- Wall-clock budget per packet (timeout flag); a packet that exceeds it is
  cancelled via `g8s cancel`, not re-prompted with a bigger timeout.
- Fan-out stops (no re-dispatch) after one full round of retests; persistent
  gaps escalate to the operator with the sanitized evidence bundle.
- Any scope drift in ANY packet freezes the whole fan-out for a zero-change
  audit before anything else runs.

## Live dispatch mechanics (proven 2026-09-27, dogfood)

- `workspace_write` submits fail at the HARNESS unless BOTH are true:
  1. `--receipt-id <id>` is attached to `g8s submit` itself (issuing a
     receipt is not enough — it must ride the submission);
  2. the worker environment sets `AGY_MCP_ALLOW_WORKSPACE_WRITE=1`
     (delegated writes are off by default even with a valid receipt).
- `read_only` dogfood probes need none of the above — default to them and
  only escalate to workspace_write when the oracle demands file changes.
- Concurrent-dispatch lane discipline (D6): one writer per ref. While a
  worker owns a branch/PR, the supervisor must not push to the same ref —
  split lanes (worker: CI fix on branch A; supervisor: unrelated branch B).
- The worker's FINAL MESSAGE is evidence, not truth: verify with your own
  zero-change audit (`git status`, receipt cross-check, CI page) before
  accepting.
- **Workers fix, supervisors watch** (proven 2026-09-27): never put a
  "poll CI / watch for N minutes" loop inside a worker packet — the agent
  goes idle mid-wait and the result envelope reports failure even when the
  substantive work (the fix + push) succeeded. Worker packet = bounded
  mechanical action ending in a push; the WATCH belongs to the supervisor's
  background task or to CI itself. Read the receipt/reason before verdicts:
  `ok:false` may still contain a completed push.
