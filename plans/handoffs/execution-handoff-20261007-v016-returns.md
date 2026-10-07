---
handoff-version: 1
generated: 2026-10-07T10:30:00+07:00
focus: "RETURNS — Session A (cut v0.16.0) closed PASSED. The strategy session that spawned A/B reads THIS to ingest results, decide the F-recursion probe, and dispose the storage question."
workspace: github.com/tamld/g8s
branch: main
head: 6ab22b6
tag: v0.16.0
---

# RETURNS HANDOFF: Session A → strategy session

**Session type**: T1 (strategy — ROUTING doc, ADR-0022 §6 marker; read THIS to ingest Session
A's returns. Session B writes its own returns handoff — this doc records
only what Session A observed of the factory, not B's results).

## Mission recap (the one line)

Prove the factory runs two heterogeneous projects with zero context
interleaving and zero goal drift — "test passed" defined structurally:
v0.16.0 tagged + Returns Ledger cited + patrol green.

## Session A verdict: PASSED (artifact-by-artifact)

- **Tag**: `v0.16.0` @ `0dea3b9`, pushed; release commit `6ab22b6`
  (latest_release metadata chore).
- **CI 5/5 on main**: CI / Quality / Crash-Survival E2E / Windows E2E /
  Version Sync Check — all success.
- **GoReleaser**: Release success, **11 assets** live on the GitHub
  release.
- **Binary smoke**: fresh build at the release commit prints
  `g8s version 0.16.0`.
- **G1–G4** all landed: 4 permanent red-cell suites (`redtest_*` in
  lane/routing/ladder/cmd), residue fixes (`ef4b103`), claims registry
  +6 bound entries with 0 broken (`d865e84`), user docs
  `docs/user-guide/effort.md` + roadmap flip (`d30605c`).

## The headline finding: #568 (closed with evidence)

The release's first live dispatch exposed a **four-link shadowing
chain** around baked-name (model, effort) resolution — family-id vs
variant-id catalog, style-less manifest models shadowing the catalog
verdict, the manifest's 3.7 entry shadowing the 3.8 default, and
provider-less submits refusing the question. Each fix uncovered the
next; the supervisor verdict REJECTED the worker's effort-coercion
design in favor of the ratified C3 model-realignment contract, keeping
per-class differentiation (the pre-push gate caught the C3 test
conflict before it shipped). Two product-level fixes rode along:
the `g8s init` scaffold was recreating the bug for every fresh user
(`928c181`), and lane glob matching was Windows-broken exactly as
red-cell R1-1 predicted (`d35b72c`, caught by the Windows E2E oracle
during the release candidate).

## Cost-per-class (shipped in the CHANGELOG "Measured" section)

Six live tasks, every number citing its task id (#570 carries the
table): low tier 79K–304K tokens with ZERO thinking tokens (2/2);
high tier 377K–947K with 45–64K thinking. The low tier ran on the
realigned flash-low variant — the #568 fix is what made the
measurement possible at all.

## Factory evidence (the stress test, Session A side)

- ~17 worker dispatches through g8s in one day: staggered spawns,
  parallel drains (2 workers, disjoint receipts), zero cross-slice
  contamination (specific files only, `git add` by path — no `add -A`).
- Two-project isolation held: Session B observed alive and dispatching
  from its own state dir (`g8s-sess-aegis`); it never touched g8s main.
  B's results are B's handoff to write.
- The factory caught its own defects: the blocker was found by USING
  the factory, and the red-cell wave (worker-built suites, supervisor
  verdicts) hardened 7 of 13 findings before the tag.

## What v0.17 inherits (all on #570)

R3-2/4/5/8 (ladder lineage design review), R4-1/2 (CLI wiring +
envelope consistency), effort-class registry glob rework
(`docs/**` never matches nested paths — classes fail open today, the
SIGNAL model is the operative demotion path), telemetry opt-in
make-default decision, Windows-oracle coverage for new glob code.

## Decisions the strategy session owes (not urgent, but queued)

1. **F-recursion clerk probe (read_only)** — the plan sequenced it
   AFTER the tag; the tag exists, so it is unblocked
   (plans/261006-two-project-ops/brainstorm.md addendum).
2. **Storage disposition** — the volume hit 100% mid-session
   (recovered to ~3Gi). Items needing an owner decision:
   `opencode.db` 6.7G, `wiki-semantic-models` 2.7G (Pillar 1 — hands
   off by default), `.gemini/antigravity-cli` 1.8G,
   `.codex/archived_sessions` 399M. The 5-week-old `.agentkit` backup
   was COMPRESSED (3.1G→602M tar.gz, data intact, not deleted).
3. **Tuneflow onboarding (wave 4)** — SCORECARD row exists, per the
   deploy handoff's "after both sessions pass".
4. **Patrol round** — next automation-87f06732 run (Mondays 09:30)
   audits the released state; verify it reads v0.16.0 cleanly.

## Memory pointers (already folded)

`g8s-v016-cut-lessons` (4-link chain, worker/telemetry/storage
gotchas, the pre-tag state machine for release.sh). Session A context
is saturated — this doc plus memory is the full bridge.
