---
handoff-version: 1
generated: 2026-11-01T00:00:00Z
generator: zcode-manual
focus: "Multi-coordinator mesh (RT-MESH v1) merged to main; reach layer campaign opened (release-gated v0.22.0); CI/CD depth wave merged; ADR-0037 co-sign + reach S1-S4 implementation pending"
workspace: github.com/tamld/g8s
branch: main
head: 5eed521 (verify latest — nightly/A6/bot squashes may have advanced it)
---

# HANDOFF: Mesh interop shipped → reach layer campaign in flight

- **Session type**: T2 (execution) — handing off to a fresh T2 session
- **Why handoff**: source session context saturated after the mesh + reach + CI-depth campaigns; all state externalized (memory + ledger + GitHub); next phase is a clean execution campaign.

## Mission and current status

**DONE (all merged to main, full battery green at each merge):**
- **PR #700** — multi-coordinator mesh: `internal/mesh` (SQLite WAL ledger, 10 ops via CLI+MCP: register/quota-publish/send/inbox/ack/claim/release/accept/snapshot/wait), typed idempotent messages (UNIQUE message_id + digest echo), CAS claims (TTL + context-revision fence), wait long-poll, drills R1-R4 + 5 regression tests. RT-MESH v1 protocol ratified (agy co-sign; codex 3rd co-sign queued).
- **PR #701** — nightly full-battery workflow + gofumpt tree parity.
- **PR #702** — A6: whitespace-only layer-gate exemption (agy co-sign with guard clause: exemption requires a REAL diff; synthetic CHANGED_FILES never exempt).
- **PR #703** — roadmap v0.22.0 row (EN+VI).
- **PR #707** — CI/CD depth wave: gitleaks wired into Quality Gate; nightly parity (-race internal/ CGO=1, bounded fuzz 10s/target, honest comment); red-test suites (telemetry-poisoning ASI08, model-substitution LLM04/06, MCP rogue-tool ASI02/04 — each names its OWASP class); property-based tests (stdlib testing/quick: scope/sanitize/envelope); wave plan. agy cross-review APPROVE 4/4 with mechanical verification.
- **Bot PRs** — #691 (Sentinel, silent-swallow+perms 700) and #692 (Bolt, RuneStart scan) merged after review; #698 closed superseded (parseAGYResult moved to worker_result.go — re-apply optimization queued).

**Reach campaign (S0 ratified, RELEASE-GATED: v0.22.0 does NOT cut before reach S1-S4 land):**
- Contract: **issue #704** (spec-in-issue: design, 4 security invariants, 5 slices, 5 acceptance criteria, falsification notes).
- Roadmap row v0.22.0: status "In Progress — release gated on reach S1-S4" (PR #705 merged).
- **ADR-0037 draft: PR #706 PROPOSED** (docs/decisions/0037-reach-layer.md on branch docs/adr-0037-reach) — awaiting agy co-sign.
- Kickoff sent to agy (ledger seq 49, 51): proposed split = zcode S1 (internal/reach registry/chain/health + CLI, channels web+github, fault-injection tests) + S4 (research-gate machine-artifact upgrade); agy S2/S3 (health ledger reorder + Context Broker WorldSnippets + vault promotion) + ADR co-sign + per-slice verify.
- zcode CLAIMED S1: `reach-s1-core-001` @ ledger rev 8 (paths: internal/reach/{registry,chain,health}.go + tests, cmd/g8s/reach.go).

**In-flight at handoff:**
- Main battery post-#707: CodeQL, Quality, CI, Windows E2E were in-flight (Version Sync + Crash-Survival already success). Verify: `gh run list --repo tamld/g8s --branch main --limit 6`.
- agy ADR-0037 verdict: NOT yet arrived. Check: ledger inbox drain (see entry ritual) + agy TUI screen.

## Entry ritual for the new session (exact order)

1. Read memory index (`MEMORY.md`) — especially `g8s-reach-campaign-20261031.md`, `g8s-mesh-experiment-20261030.md`, `feedback-ci-resolution-peer-review.md`.
2. Drain mesh ledger: `P=$P; $P/g8s-experiment coordinator mesh --mesh-dir $P/shared-ledger --op inbox --input <(printf '{"node_id":"zcode","incarnation":"zcode-native-20261030-cu"}') --json` — ack each after processing (ack needs: node_id, incarnation, message_id; only recipient can ack).
3. If the experiment binary dies with exit=137/empty output: `codesign -s - -f $P/g8s-experiment` (agy rebuilds strip the ad-hoc signature; lesson in memory).
4. Check main battery: `gh run list --repo tamld/g8s --branch main --limit 6`.
5. Read agy's ADR verdict → APPROVE/AMEND → dispatch reach S1 worker (cursor lane, brief pattern in memory + briefs under P/briefs/).

## Exact next actions

1. **agy ADR-0037 verdict** → if APPROVE: merge PR #706, dispatch reach S1 worker (brief: registry/chain/health + `g8s reach read` for web+github channels + fault-injection fallback tests; cursor lane; receipt-scoped; per-commit layer-clean). If AMEND: apply amendments, re-request co-sign.
2. **agy S2/S3 claims** — coordinate file-set overlap with S1 before dispatch (P3 per-commit check).
3. **S4** research-gate machine-artifact upgrade — zcode slice after S1.
4. **v0.22.0 cut** — ONLY after S1-S4 accepted + full battery + agy verify (release gate in roadmap).
5. **Remaining #699 debts** (post-reach): AM1 busy_timeout measurement (burst drill first, per D1 constraint), AM2 inbox retention, A1-A4 amendments (RT-MESH v1.1 propose→co-sign), fence-at-mutation ADR, #698 parseAGYResult optimization re-apply onto worker_result.go, items 6/7/9 per audit verdicts.

## Scope and guardrails

- ChatGPT/Codex app = NO-TOUCH zone (quota allocated elsewhere; codex participates ledger-only until S0 announces availability).
- 9router routes credential-dead (iflow/kiro/gc 404, scorecard quarantined) — worker lane = cursor composer-2.5 native CLI (proven ×6) or agy pool (gemini weekly 98%, lean).
- Every write: g8s worker + scoped receipt + receipt-scoped paths only; per-commit layer-clean (DEBT-34: no W+C mix; A6 exempts whitespace-only with REAL diff).
- Merges: ONLY via green-gate precondition (poll total>0 ∧ pending==0 ∧ fail==0). Three violations on record (seq 48 self-report) — do not add a fourth.
- gofumpt binary: ~/go/bin/gofumpt (NOT on interactive PATH — resolve before trusting "0 dirty").
- Mesh ops: ack messages after processing (unacked re-surface in wait loops); read payload BEFORE acking.
- Secrets/credentials: never in receipts/ledger/logs/ContextPacket (two-layer separation); reach layer read-only invariant absolute.

## Source pointers

- Memory (platform): `MEMORY.md` index → `g8s-reach-campaign-20261031.md`, `g8s-mesh-experiment-20261030.md`, `feedback-ci-resolution-peer-review.md`, `g8s-session-ops-gotchas.md`, `g8s-dispatch-mechanics-v012.md`.
- Experiment runtime: `$P/` (binary `g8s-experiment`, ledger `shared-ledger/`, briefs `briefs/`, worktree `portable-worktree` @ branch codex/coordinator-mesh-experiment).
- Ledger trail: seq 1-54 (native 3-platform exchange, zero human relay in steady state).
- Round receipts: `portable-worktree/plans/261030-coordinator-interoperation/round-receipt.md` (D1 + R-FIN-01), `realtime-protocol.md` (RT-MESH v1, ratified).
- GitHub: #700-#703 merged, #706 open (ADR), #704/#699 open (contracts/trackers).
- agy channel: TUI handle term_5f1fdb61-9463-4cc5-ba2a-944bc2cf9d50 (screen-state gate before send — read tail first; ledger is the primary channel).
