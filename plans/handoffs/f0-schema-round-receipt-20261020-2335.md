---
handoff-version: 1
generated: 2026-10-20T16:35:00Z
generator: ak:handoff@2.1.0
focus: "Phase 06b & Wave B — Information Firewall, BriefSchema/RoundReceiptSchema, Worktree-Targeted g8s verify, and Portable Ground Truth"
workspace: /Users/tamld/Documents/github/g8s
branch: main
head: 3c4675944094cf20eed6e6791e3f6acca2989c83
---

# HANDOFF: Phase 06b & Wave B — Information Firewall, BriefSchema/RoundReceiptSchema, Worktree-Targeted g8s verify, and Portable Ground Truth

## Mission and current status
Focus: "Phase 06b & Wave B — Information Firewall, BriefSchema/RoundReceiptSchema, Worktree-Targeted g8s verify, and Portable Ground Truth".
Done:
- Completed and squash-merged **Wave A** of Epic `#620` (`#621`, `#622`, `#623`, `#624`, `#631`) in PR `tamld/g8s#634` at commit `3c4675944094cf20eed6e6791e3f6acca2989c83` (18/18 GitHub Actions checks green, Wave A replay `5/10 BLOCK`, `2/10 FLAG`, `p50 = 45.98 ms`).
- Conducted a post-merge Socratic Root Cause Analysis (`/deep-thinking`) identifying why Supervisor (`F0`) context drifts (> 6.2 MB of raw test/CI logs ingested in `F0` due to two defects in `g8s verify` and direct `gh pr checks --watch` usage) and how `F0` vs. `F1` trust must be structured.
- Updated `docs/PRD.md`, `docs/SRS.md`, `docs/DOD_DOR.md`, `docs/decisions/0033-f0-verified-delegation.md` (ADR-0033), `plans/261020-f0-verified-delegation/plan.md`, and created `plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md`.

Remaining:
- Execute (`ck:cook`) **Slice 06b-1** (`internal/verifier/**`, `cmd/g8s/verify*.go`, `cmd/g8s/watch*.go`) and **Slice 06b-2** (`tools/f0/**`, `.g8s/f0.yml`) via F1 workers (`g8s submit` + `WriteReceipt`), then proceed to Wave B (`#625`–`#630`).

Urgency: high (next session starts with a clean context window and must preserve the Information Firewall from turn 1).

## Scope and guardrails
Workspace: /Users/tamld/Documents/github/g8s
In scope:
- `plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md`
- `plans/261020-f0-verified-delegation/plan.md`
- `docs/PRD.md`, `docs/SRS.md`, `docs/DOD_DOR.md`, `docs/decisions/0033-f0-verified-delegation.md`
- Slice 06b-1: `internal/verifier/**`, `cmd/g8s/verify*.go`, `cmd/g8s/watch*.go`
- Slice 06b-2: `tools/f0/**`, `.g8s/f0.yml`
Out of scope:
- Modifying `internal/worker/**` in the same PR as `cmd/g8s/**` (forbidden by DEBT-34 layer-ownership gate).
- Installing `tools/f0/hooks/agy-hooks.json` into the operator's personal `~/.gemini/` root without explicit synchronous PiC approval (`T4` boundary).
Constraints:
- **Zero-Log F0 (Information Firewall)**: The successor `F0` supervisor MUST NOT run raw `go test -json ./...` or `gh pr checks --watch` directly in its own context window. Use F1 workers (`g8s submit` + `g8s worker --once`), `g8s verify --task <id>`, and `g8s watch --pr <N> --json`.
- **Signal-Proportional Sizing (No Fixed Line Truncation)**: Never truncate `RoundReceipt` diagnostics to an arbitrary line count (e.g., 30 lines). Strip 100% of mechanical noise (passing tests, polling spinners) and preserve 100% of failure diagnostics and `D1..Dn` DoD mappings (`Size = f(|DoD| + |Failures| + |Boundaries|)`).
Safety boundaries:
- Enforce `1 issue/slice = 1 worker = 1 receipt`.
- Isolate supervisor session state via `export G8S_STATE_DIR="$HOME/.local/state/g8s-sess-<id>"` and `export AGY_MCP_ALLOW_WORKSPACE_WRITE=1`.

## Current state
Branch: main
HEAD: 3c4675944094cf20eed6e6791e3f6acca2989c83
Working tree: dirty (intentional documentation and plan updates ready to be committed on a feature branch)
Changed files:
- `M docs/DOD_DOR.md`
- `M docs/PRD.md`
- `M docs/SRS.md`
- `M docs/decisions/0033-f0-verified-delegation.md`
- `M plans/261020-f0-verified-delegation/plan.md`
Untracked files:
- `?? plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md`
- `?? plans/handoffs/f0-schema-round-receipt-20261020-2335.md`
Intentional local modifications: yes

## Decisions and rationale
| Decision | Rationale | Alternative rejected | Reference |
| --- | --- | --- | --- |
| **D10: Information Firewall (`Zero-Log F0`)** | `F0` context drifted in Wave A because > 6.2 MB of raw `go test -json` and `gh pr checks --watch` logs flooded `F0`'s control plane, causing compaction loss and Role Collapse. Raw logs must stay on disk (`$G8S_STATE_DIR/logs/`) and inside disposable `F1` contexts. | Letting `F0` run `f0 run -- go test ./...` directly in its own stdout stream. | `docs/decisions/0033-f0-verified-delegation.md` (§Implementation Audit & Post-Wave-A Architectural Refinement) |
| **D11: Signal-Proportional Sizing (`Lossless Signal, Zero Noise`)** | Receipt size must be `f(\|DoD Items\| + \|Distinct Failures\| + \|Changed Boundaries\|)` — stripping 100% of passing-test/polling noise while keeping 100% of failure diagnostics and `D1..Dn` mappings. | Fixed 30-line digest cap (rejected because it causes Decision Blindness when a round has multiple failures or many DoD items). | `docs/SRS.md` (`FR-8`), `docs/DOD_DOR.md` (§4.1) |
| **D12: Free Iteration Topology, Strict Boundary Schemas (`BriefSchema` ↔ `RoundReceiptSchema`)** | Code cannot hardcode how many rounds (`1..N`) `F0` and `F1` iterate, but Go code strictly enforces `BriefSchema` (`D1..Dn` downward) and `RoundReceiptSchema` (`execution_facts` + `dod_matrix[D1..Dn]` + `failure_diagnostics` upward) at each round boundary and commitment gate. | Hardcoding a fixed round sequence in Go code or relying on unstructured prose summaries from workers. | `plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md` (§1–§2) |
| **D13: Fix Defects `L3` & `L4` in `g8s verify` and `L2` in `g8s watch`** | `cmd/g8s/verify.go:133` ran checks on the unmodified main checkout instead of the F1 worktree (`res.Deliverable.Dir`), and `internal/verifier/verifier.go:597` discarded `stdout` (`_`) where `go test` prints failure details. Fixing `L3`/`L4` makes `g8s verify` the native engine for `RoundReceiptSchema`. | Building a second external test runner outside `g8s verify`. | `cmd/g8s/verify.go:133-147`, `internal/verifier/verifier.go:597`, `cmd/g8s/watch.go:268` |
| **D14: Portable Ground Truth (`tree_sha` + `f0:bundle`) & `normalizeExtra`** | Local `.git/f0/ledger.jsonl` is not pushed to GitHub. Embedding a hashed `<!-- f0:bundle: ... -->` in `<!-- f0:status -->` + matching `HEAD^{tree}` (`tree_sha`) enables remote CI runners and fresh clones to verify `G1`, `G2`, `G3` (against git blob hashes), and `G6` without local ledger files. | Relying exclusively on `.git/f0/ledger.jsonl` (fails on fresh clones/CI) or commit SHA alone (invalidated by `git commit --amend`). | `plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md` (§2.2) |

## Work performed
- Executed and merged Wave A of Epic `#620` (`PR #634` -> `3c4675944094cf20eed6e6791e3f6acca2989c83`), closing `#621`, `#622`, `#623`, `#624`, and `#631`.
- Rebuilt `./bin/g8s` (`make local`) and verified `./bin/g8s doctor --json` (`HEALTHY`, `trace_id: 01a1578a-b536-7e63-a794-d104d3dc1f72`).
- Synced `skills/g8s-supervisor/` to `~/.agents/skills/g8s-supervisor/`.
- Inspected `cmd/g8s/verify.go`, `internal/verifier/verifier.go`, `cmd/g8s/deliver.go`, and `cmd/g8s/watch.go` to ground the Socratic RCA (`L2`, `L3`, `L4`).
- Updated `docs/PRD.md`, `docs/SRS.md`, `docs/DOD_DOR.md`, `docs/decisions/0033-f0-verified-delegation.md`, `plans/261020-f0-verified-delegation/plan.md`, and wrote `plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md`.
- 0 redactions applied.

## Verification
| Check | Command | Outcome | When |
| --- | --- | --- | --- |
| PR `#634` GitHub Actions CI (18 checks / 10 workflows) | `gh run list --commit d6093e6fb3269ac7ebf05f68773dde33d5014617` | PASS (`CI` `39367924386`, `Quality` `39367924251`, `Windows E2E` `39367924143`) | 2026-10-20T04:20Z |
| Wave A `f0 gate check --pr 634` | `/tmp/f0 gate check --repo . --action merge --pr 634` | PASS (`G0`, `G1` `ev-1ac3c8d9`, `G2`, `G3`, `G6` all `PASS`) | 2026-10-20T04:20Z |
| Post-merge binary rebuild & doctor | `make local && ./bin/g8s doctor --json` | PASS (`overall_status: HEALTHY`) | 2026-10-20T04:21Z |
| Ledger integrity check | `/tmp/f0 ledger verify --repo .` | PASS (`64 entries verified`) | 2026-10-20T04:22Z |

Not run:
- Phase 06b unit tests (`Slice 06b-1` and `Slice 06b-2`): scheduled for execution in the new clean-context session via `ck:cook`.

## Open risks and blockers
- Type: risk. Owner: F0 Supervisor. Impact: `docs/PRD.md`, `docs/SRS.md`, `docs/DOD_DOR.md`, `docs/decisions/0033-f0-verified-delegation.md`, and `plans/261020-f0-verified-delegation/*` are currently uncommitted on `main` in the working tree so the successor session can checkout a feature branch (`git checkout -b feat/f0-phase-06b-information-firewall`) and include them cleanly.
- Type: risk. Owner: F0 Supervisor. Impact: DEBT-34 layer-ownership gate forbids mixing `cmd/g8s` with `internal/worker` in the same PR; keep `Slice 06b-1` scoped to `internal/verifier/**` + `cmd/g8s/{verify,watch}*.go`.

## Exact next actions
1. **First safe step** — Read `plans/handoffs/f0-schema-round-receipt-20261020-2335.md` and `plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md`, then verify `git status --short` on `/Users/tamld/Documents/github/g8s`.
2. Create and switch to branch `feat/f0-phase-06b-information-firewall` (`git checkout -b feat/f0-phase-06b-information-firewall`) and commit the updated spec/plan/ADR/handoff files (`docs/PRD.md`, `docs/SRS.md`, `docs/DOD_DOR.md`, `docs/decisions/0033-f0-verified-delegation.md`, `plans/261020-f0-verified-delegation/*`, `plans/handoffs/f0-schema-round-receipt-20261020-2335.md`).
3. Isolate supervisor state (`export G8S_STATE_DIR="$HOME/.local/state/g8s-sess-06b" AGY_MCP_ALLOW_WORKSPACE_WRITE=1`) and dispatch **Slice 06b-1** via `g8s receipt issue` + `g8s submit` + `g8s worker --once` to fix `L3` (`cmd/g8s/verify.go` worktree target), `L4` (`internal/verifier/verifier.go` `stdout` capture, vacuous guard, `$G8S_STATE_DIR/logs/verify-<task-id>.log`, and `RoundReceiptSchema` `FailureDiagnostics` + `DoDMatrix`), and `L2` (`cmd/g8s/watch.go` `--repo` flag).
4. Verify Slice 06b-1 with `./bin/g8s verify --task <id> --json`, deliver via `./bin/g8s deliver <id> --prune-worktree --json`, and rebuild `./bin/g8s` (`make local`).
5. Dispatch **Slice 06b-2** (`tools/f0/**`, `.g8s/f0.yml`) via F1 worker to add `normalizeExtra`, `TreeSHA`, clean `PASS`-only `f0 status --report-block` with `f0:bundle`, `--mode auto|local|remote`, declarative `.g8s/f0.yml` parsers (`pytest`, `vitest`), and narrowed `G6` verdict-only claim matching; verify using the newly rebuilt `./bin/g8s verify --task <id> --json` (confirming 0 raw test logs dumped into F0!).
6. Open PR, watch CI using `./bin/g8s watch --pr <N> --json` (0 polling spam in F0!), run `f0 gate check --pr <N>`, and merge when 100% green.

## Source pointers
- `plans/261020-f0-verified-delegation/phase-06b-portable-ground-truth.md`
- `plans/261020-f0-verified-delegation/plan.md`
- `docs/decisions/0033-f0-verified-delegation.md`
- `docs/PRD.md`
- `docs/SRS.md`
- `docs/DOD_DOR.md`
- `cmd/g8s/verify.go`
- `internal/verifier/verifier.go`
- `cmd/g8s/watch.go`
- `tools/f0/ledger.go`
- `tools/f0/gate.go`
- `tools/f0/status.go`
- `tools/f0/claims.go`
