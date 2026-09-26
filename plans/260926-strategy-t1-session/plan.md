# T1 Strategy Session Ledger — 2026-09-26

**Session type**: T1 (strategy) — per ADR-0022 (Proposed)
**Input**: `plans/handoffs/strategic-handoff-20260926-1030.md` + ADR-0021 + issues #383/#379/#385 + repo state
**Jev triage**: `grant_receipt`, risk 0.01, breach 0.02, confidence 0.95, source=jev, trace `01a0dd42-f736-7f0b-b53c-23ed39830e97` (before first mutation)
**Output**: ADR-0022 draft (Proposed) + this ledger. Zero code changes. Zero commits (operator reviews first).

---

## DECIDE table (disposition of all 9 handoff topics)

| # | Topic | Disposition | Campaign order | Rationale (one line) |
|---|-------|-------------|----------------|----------------------|
| 1 | ADR-0022 Session Protocol | **RATIFY → draft written** (Proposed) | — (done here) | Codifies the only insight that makes the rest repeatable |
| 6 | Session-scoped state | **RATIFY → T2 P0** | 1st | HIGH risk R1; blocks #5; evidence accumulating in repo right now |
| 5 | Concurrent dispatch | **DEFER → T2 after #6** | 2nd | Hard dependency: concurrency without state isolation multiplies D6 |
| 2 | Context Broker | **DEFER → T2 design-first** | 3rd | Dependency root for L2-Jev and L4; ADR-0021 §8.2 sketch suffices as design |
| 8 | #379 BLOCKED semantics | **DECIDEd: options 1+2** | 4th (independent) | Deterministic core + boundary probe; option 3 killed |
| 9 | #383 Injection detection | **DECIDEd: schema first, content via L3** | 5th (independent) | Issue's fix plan is concrete; content-level reuses shipped L3 gate |
| 3 | L2 Brief quality gate | **DEFER (split)** | 6th | Deterministic DoR floor now; Jev-judged quality only after Context Broker |
| 4 | L4 Skill routing | **DEFER** | 7th | Advisory v1 only; routing value is operator-local, not portable |
| 7 | #258 Phase B (A2A) | **DEFER → operator product decision** | parallel | Public protocol commitment = product-identity call, not Brain's alone |

**Strategic insight (the finding of this session)**: the handoff ordered topics by
*impact*; the dependency graph orders them differently — **6→5** is hard (state
isolation before concurrency), **2→(3,4)** likewise (Context Broker before
Jev-judged gates). #379 and #383 are independent fillers for parallel capacity.

---

## Per-topic analysis (EXPLORE → CHALLENGE)

### 1. ADR-0022: Strategic Session Protocol — RATIFY (draft `docs/decisions/0022-strategic-session-protocol.md`)

- **Evidence** (`observed`): prior campaign produced 0 strategy artifacts in 40+ PRs; fresh-context session produced 9 topics + this ledger; `plans/handoffs/` has exactly 1 handoff (pattern is new — structural argument, not recurrence).
- **Strongest objection**: process-for-process meta-work; ADR-0021 itself warns about gate theater. A protocol ADR can become ritual that no one follows by session 3.
- **Counter + falsification**: cost is 2 markdown files; the ADR carries its own kill clause — if the next campaign misses nothing without the markers, kill it. Recurrence check at next handoff: ≥2 handoffs in `plans/handoffs/` = pattern confirmed.
- **Composition insight**: T1-DECIDE → T2-TRIAGED makes the two FSMs one lifecycle (closes ADR-0021 §1a's loop without new machinery).

### 6. Session-scoped state — RATIFY as next campaign P0

- **Evidence** (`observed`): risk table R1 = HIGH; live in repo today — 4 `/tmp` worktrees from `g8s orchestrate --blind-converge` runs (`blind/1-*`, `blind/2-*` per run, hex-suffixed) left as orphan worktrees + dangling branch names in the shared namespace; 6 `agy/sup-*` worker branch pairs accumulating; handoff branch count (12) vs actual (34) — sessions keep adding refs and no one reaps them mid-campaign.
- **Mechanism**: `blind-converge` and supervisor sub-tasks write branch refs into the shared `$GIT_DIR` even when worktrees are isolated — worktree isolation is necessary but not sufficient; the ref namespace itself needs session scoping or explicit reaping.
- **Options**: (a) `session_id` column + partial indexes on state DB; (b) repo-lock (flock on `.g8s/session.lock`) serializing supervisors per checkout; (c) worktree-per-supervisor as default dispatch posture.
- **Challenge**: (b) alone serializes away the very concurrency #5 wants; (a) alone leaves git refs shared. Recommendation: **(c) default + (a) namespace** — (c) kills D6 at the root (separate checkouts → separate ref namespaces), (a) keeps the shared daemon DB correct when (c) is opted out; keep (b) as a cheap belt-and-suspenders guard.
- **Interaction with R6** (user_version conflict, receipt v3 vs controlplane v10): same DB architecture bucket but distinct concern — R6 is schema versioning, this is namespace isolation. Sequence R6 first only if its fix (per-package version tables) reshapes the schema this topic builds on; otherwise parallel.

### 5. Concurrent dispatch — DEFER behind #6

- **Evidence** (`derived`): controlplane already has CAS leases + idempotency keys → the queue is concurrency-safe; the unsolved part is git-level and state-level contention, which is exactly topic #6's scope.
- **Strongest objection**: "just raise the lease pool and ship — the DB is safe." Rebuttal: safe DB + unsafe worktree/refs = D6 at scale, with N workers hitting the same checkout instead of 2 sessions.
- **Design sketch recorded for T2**: `--concurrency` flag on RunLoop, bounded goroutine pool, per-worker worktree via existing worktree machinery, heartbeat already per-session (#363 lineage). No new primitives needed — the slice is wiring, not invention.

### 2. Context Broker (`internal/context`) — DEFER → design-first T2 slice

- **Evidence** (`observed`): ADR-0021 §8.2 already sketches `ContextPacket` from Vault + Telemetry + SOM state; handoff lists it as insight #2; live Jev today triages on summary+files only (`observed` in this session's own triage call — reason field shows policy reasoning, not situational awareness).
- **Design constraints to carry into T2** (from Constitution + this session):
  - **Fail-open**: broker failure must degrade to today's minimal TriageRequest, never block the System-1 fast path (a broker outage must not turn the reflex into a wall).
  - **Bounded budget**: enrichment is top-N capped (token + latency); Jev latency measured at ~425ms live this session — enrichment must stay within the same order.
  - **Backward compat**: `TriageRequest v2` must keep existing CLI flags byte-compatible (Constitution Axiom 5: self-describing executable).
- **Strongest objection**: "Jev already grants/kills correctly (15+ live calls, 0 breaches) — the broker is speculative richness." Rebuttal: correct on *current* request shapes; L2/L4 questions ("is this brief dispatchable *here*?", "does this task match that skill *given recent failures*?") are undefined without situational context — the broker is the enabler, not a nicety. Honest uncertainty: the payoff is unproven until L2/L4 exist; that is why it is DEFER-with-design, not P0.

### 8. #379 BLOCKED semantics — DECIDEd: Option 1 (semantic classes) + Option 2 (gate probe); kill Option 3

- **Evidence** (`observed` from issue): live refusals rarely contain the literal "BLOCKED"; substring scoring would misreport.
- **Option 1** (semantic regex classes: refusal verbs, policy mentions, empty-for-adversarial → BLOCKED): deterministic, tunable, cheap, fits the harness's determinism culture. **Option 2** (run probes through the real harness gate, not a live model): deterministic, CI-safe, tests the boundary that actually ships. They compose: gate probe = the always-green CI mode; semantic classes = the live-mode scorer.
- **Option 3 (LLM-judge) KILLED**: cost + judge bias + nondeterminism in a harness whose entire value is reproducible scoring. Recorded rationale per DECIDE rule.
- **DoD addition** (`derived`): semantic classes belong in a table-driven classification function with golden tests — same style as the deterministic classifier in reflex (ADR-0020 calibration fix #329).

### 9. #383 Worker output injection detection — DECIDEd: schema validation first; content-level via L3 extension

- **Evidence** (`observed` from issue): `parseAGYResult` checks `status` only; `result_validation` column exists unused; L3 post-run gate already blocks hallucination at source (v0.11.0).
- **Split the issue's two concerns**: (a) *structural* validation — enforce a result envelope schema before `result_json` write, mark FAILED on violation (issue's fix plan, concrete, no design debt); (b) *content-level* injection — stdout carrying escalation/instruction payloads. (b) should NOT be a new parser: extend the shipped L3 Jev gate with injection pattern families (receipt-issuance requests in output, privilege-escalation imperatives, credential probes) — reuse the sensor + policy plumbing instead of a parallel regex engine.
- **Strongest objection**: schema validation is trivially bypassed by a compromised worker that returns well-formed JSON. Rebuttal: it was never the point — it raises the cost of *accidental* poisoning (the common case); deliberate compromise is the receipt/jail layer's problem, not the parser's. Defense in depth, each layer for its own threat.

### 3. L2 Brief quality gate — DEFER, split in two

- **Deterministic floor (T2-ready now)**: DoR preflight checks that need no Jev — goal present, scope files listed, DoD present, receipt path when `workspace_write`. Mechanical, testable, enforce-code.
- **Jev-judged quality (blocked on #2)**: "is this brief well-formed *for this situation*?" is a context question; shipping it before the Context Broker means shipping a second context-blind gate — the exact anti-pattern ADR-0021 §8 warns against.
- **Challenge**: risk of blocking dispatch on subjective quality → mitigation is the split itself (floor blocks; Jev advises).

### 4. L4 Skill routing — DEFER, advisory v1

- **Evidence**: skill bank is operator-local (`~/.agents/skills/`), not part of the repo — routing value depends on the operator's inventory; enforcement would couple g8s binaries to a machine-local directory.
- **v1 shape**: deterministic keyword/manifest match surfaced as a *suggestion* in the brief envelope; Jev scoring of task-to-skill match only after Context Broker; enforcement later, only if suggestion data shows routing errors matter.
- **Challenge**: "score task-to-skill match" without execution history is a cold-start classifier — its scores would be fiction. Sequencing after telemetry distillation (already shipped, closed-loop) is what makes it honest.

### 7. #258 Phase B: A2A Agent Card + Agent Protocol — DEFER to operator decision

- **Evidence** (`prior`, needs operator confirmation): A2A Agent Card is a well-known discovery document (`/.well-known/`-style) exposing agent capabilities; `g8s serve` already has bearer-auth HTTP API — the technical surface is small.
- **Why T1 cannot decide alone**: publishing a protocol surface is a *product identity* commitment (external orchestrators become a supported consumer class) — it expands the compatibility burden beyond single-tenant homelab scope. This is roadmap-intent, which ADR-0021 reserves for the Operator.
- **Question for operator**: is g8s discoverable infrastructure (yes → Phase B in v0.12.0) or a private harness (no → keep A2A on the roadmap's back burner)? Roadmap v0.12.0 currently lists "A2A surface" — the anchor needs the decision either way.

---

## Repo-state findings (EXPLORE byproducts, for next T2's awareness — not cleaned per handoff directive)

| Finding | Class (ADR-0021 §3) | Note |
|---------|--------------------|------|
| 4 orphan `/tmp` worktrees (`g8s-blind-*`, from `--blind-converge` test runs) + 8 `blind/*` branches | F (garbage) | Benign; OS purges `/tmp`; `cleanup --force --scratch` reaps in next T2 |
| 6 `agy/sup-*` branch pairs in shared namespace | D (contention) evidence | Session-scoped refs (#6) should reap or namespace these |
| Local branch drift: handoff said 12, actual 34 | F | Same reaping slice |

## Verification (this session's E2E, per ADR-0021 §4 adapted to T1)

- [x] All 9 handoff topics EXPLOREd with evidence tags and CHALLENGEd with strongest objections.
- [x] Jev triage before mutation (recorded above, `grant_receipt`).
- [x] DECIDE table complete: every topic RATIFYed, DEFERed, or KILLed with rationale.
- [x] Draft ADR-0022 written as **Proposed** (never self-ratified).
- [x] Zero code mutations, zero commits, zero garbage created.
- [ ] Operator ratifies ADR-0022 + campaign order (pending — the gate this session cannot pass for itself).

## Next session (T2) first safe step

1. Operator reviews this ledger + ADR-0022 → ratify/amend/reject.
2. On ratification: T2 campaign opens with **topic #6 (session-scoped state)** as P0, #5 second; issues #379/#383 follow their DECIDEd options as independent fillers; doc-contract gate extension for the session-type marker rides along with the first docs-touching PR.

---

## RATIFICATION (2026-09-26, operator approved in session)

ADR-0022 → **Accepted**. Campaign issues opened. The CHALLENGE round below was
self-directed after the initial DECIDE table; revisions supersede where noted.

### Self-reflection revisions (supersede DECIDE table details)

- **S1 isolation mechanism revised**: static "worktree default ON" → **flock-gated
  conditional isolation** — orchestrate attempts non-blocking `flock` on
  `.g8s/session.lock`; acquired → run in-place holding the lock (zero overhead,
  the common case); contended → auto-promote to a pool worktree. flock is
  kernel-released on process death (no stale locks) and arbitrates atomically
  (no TOCTOU). Rationale: the repo currently carries 4 orphan `/tmp` worktrees
  from g8s itself; default-ON multiplies the mechanism that produces the garbage.
- **S2 schema correction**: "partial index `WHERE session_id=?`" is wrong
  (SQLite partial indexes need literals). Correct: composite index
  `(session_id, status)` + partial index on `is_active=1` only. Requires a
  **sessions registry table** (heartbeat, status) as the truth source for
  cleanup — session_id alone cannot distinguish live from zombie.
- **S2 broker fail-open kept with conditions**: safe only because the L2 split
  keeps the blocking floor deterministic (broker-judged quality is advisory);
  `broker_failure` must be a telemetry event (degraded mode must be visible).
- **Composition warning**: these decisions interlock (D5 depends on the L2
  split; sessions registry is S1 scope). Changing one requires re-checking the
  chain.

### Sharing topology (operator-endorsed answer to "share result/memory/decision")

One pattern, three gates — the filesystem write-receipt applied to knowledge:

| Stream | Local (session, untrusted) | Gate | Shared | Provenance = undo path |
|--------|---------------------------|------|--------|------------------------|
| Result | raw stdout, in-flight rows | **ingestion** (#383 schema + L3) | `result_json` append-only facts | session_id stamp |
| Memory | session scratch memory | **promotion** (verify + Jev) — *does not exist today* (`grep promote internal/memory` = 0) | vault entries | session_id + source task |
| Decision | draft ADR, local FSM | **ratification** (operator) | ADRs (git, immutable) + `supervisor_decisions` | ADR number / decision id |

Principle: **facts share-on-write (after ingestion gate), judgments
share-on-promotion, decisions share-on-ratification.** What is NOT shared:
in-flight FSM state (read-only to others), locks/leases (exclusive), scratch
worktrees (private), live cross-session decision sync (multi-tenant territory,
Non-Goals).

**New finding (supersedes campaign order in lane B)**: Context Broker without a
memory promotion gate **amplifies poisoning** — broker reads vault → injects
into TriageRequest → every downstream Jev decision inherits the poison.
Therefore: provenance stamps ride with S1 (same tables), and a new slice
**S3a (Memory promotion gate, ADR-0023 candidate)** must precede the Context
Broker (**S3b**).

### Final campaign order (ratified)

```
S0 hygiene reap (cleanup, no code) → S1 session state + PROVENANCE (P0) →
S2 concurrent dispatch (hard-coupled: --concurrency>1 hard-errors without S1)
Lane B: S3a memory gate (ADR-0023) → S3b Context Broker → S5 L2 floor + L4 advisory
Lane C: #379 (semantics: option 1+2, option 3 killed) + #383 (schema first,
        content-level = L3 extension) — independent fillers
Docs: doc-contract gate session-type marker (ADR-0022 §6) rides first docs PR
```

R6 (user_version conflict) stays a separate small PR before S1's migration.

### Session artifacts shipped

- ADR-0022: `docs/decisions/0022-strategic-session-protocol.md` — **Accepted**
- Campaign issues: opened on GitHub (see issue list in PR body, T2 session)
- This ledger: `plans/260926-strategy-t1-session/plan.md`

---

## ADDENDUM — Memory design closure (3-round brainstorm, 2026-09-26 evening)

Operator-driven brainstorm on memory system state, executed per `ck:brainstorm`
with `prove` gates. Outcome folded into **ADR-0023 (Proposed)** +
issue #395 body update:

- **Round 1 (states)**: two-level state model — entry FSM (7 states) + system
  states (COLD/WARM/SATURATED/DEGRADED/QUARANTINE); label schema where labels
  ARE the allocation policy (kind/trust/salience/scope/provenance);
  STM/LTM mapped onto existing DELTA-21 tiers (no fourth tier built).
- **Round 2 (transport/protocol/Jev)**: four flows (capture/promote/inject/
  doctrine), three existing transports + one new Gate API; MemoryEntry v1
  borrows the receipt envelope pattern; Jev at promotion gate + revocation
  trigger only, read path Jev-free.
- **Round 3 (termination — operator's question: "có tính hồi quy không?")**:
  found two real unbounded loops in the round-2 design (resurrection of
  revoked payloads via re-capture; trust laundering/echo chamber). Closed by
  guards G1–G6 (payload-hash tombstones, out-of-sample corroboration,
  naked-payload gating, meta-entry ban, DAG trust path, injection budget).
  Fixed points: doctrine / revoked+tombstone / archived.
- **Live-red captured**: `InjectPreflightContext` emitted 80,455 chars vs 4K
  budget — G6 false in shipped code today. Red evidence + contract table
  (C1–C10) in ADR-0023 Red Test Proof.

Operator's execution order for this stream: **red test all claims → plan →
issues + docs → implement per ALDC**. Issues (#395 updated) and docs (ADR-0023)
done; plan + implementation follow via ALDC when Lane B opens.
