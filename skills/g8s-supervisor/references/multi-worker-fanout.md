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
     T1 (F1 → F2 Dialectical Fan-Out): F1 middle managers spawn cross-family F2 subagents (REA / Proponent / Skeptic / Synthesizer) via g8s llm-call; depth-2 telemetry recorded in $G8S_STATE_DIR/f2_telemetry/<task_id>.jsonl
     T2 (Jev System-1 Sensor + F0 Cross-Examination): Run g8s reflex triage on F1 blueprints; F0 debates F1 on architecture-critical claims
     T3 (Perimeter Verification & Commitment): Run g8s verify --task <id> --json (checks DoDMatrix, f2_summary, anti-theater net-delta guards #644, and ratified lesson injection) before merging
```

### Horizontal vs. Deep-Vertical Tournament Modes

| Dimension | Mode A: Broad Horizontal (`Đánh diện rộng`) | Mode B: Deep Vertical (`Đánh diện chuyên sâu`) |
|---|---|---|
| **Topology** | `1 F0 → m F1 (1 per repo/subsystem) → n F2` | `1 F0 → k F1 (Domain Leads: UX, Security, Deploy) → n F2` |
| **Target Scope** | Multiple sibling repos or large disjoint packages | Single target repository (`--add-dir <repo>`) with deep domain slices |
| **F2 Roles** | Concept Harvester, Feasibility Auditor, Red-Team Skeptic, Blueprint Synthesizer | REA Researcher, Red-Team Skeptic, Code Implementer, Acceptance Verifier |
| **F0 Context Firewall** | F0 reads only sealed F1 handoffs + `RoundReceiptSchema` (`f2_summary`) | F0 orchestrates multi-round debate/verification via `BriefSchema` ↔ `RoundReceiptSchema` without reading raw logs |

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
