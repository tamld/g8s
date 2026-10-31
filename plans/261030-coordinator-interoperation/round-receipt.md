# One-round decision receipt

**Session type**: T1 strategy and T0 local experiment. This receipt records a planning decision and bootstrap experiment; it does not claim the native coordinator inbox is implemented.

## Authority and input

The operator requested direct Codex/AGY/Zcode coordination, one debate round, capability-based work allocation, bounded workers and a connected experiment. Source baseline: `f3ced6add2f48951fe669031d826fe9b5570d5c8`. No release, remote publishing, source migration or third-repository change is included.

R1 brief SHA256: `76765e32087be2340cc0fba3698677becb04612240bd9806fc6de87b2b97cd1a`.

| Peer | Own verdict | Response SHA256 | Disposition |
|---|---|---|---|
| AGY | AMEND | 528ca9ff9bb6f6ed1b4efbc5dcb6c20ed78529917e508970d187deab3e38ce5b | Explicit shared path accepted; advisory liveness qualified; remove HA/auto-routing accepted |
| Zcode | AMEND | 43a6a18f5a3b921afccbe7ac560f83e7403c6c8166bc2a377767c06271a3e624 | SQLite/capacity/retention and advisory-only liveness accepted; global path/timeout changes not accepted |

Both replies were requested independently, bound to this HEAD, and authored in the existing peer sessions. Codex validates and owns the decision; these are open peer reviews, not blind adjudication.

## Decisions and reasons

- Freeze same-host opt-in shared coordination storage and private per-coordinator worker state. Do not modify global defaults merely to enable an experiment.
- Preserve all three capability directions. Upward describes external compute/model/provider opportunities and future larger systems, not mandatory physical hierarchy or extra S-stratum authority.
- Use SQLite as durable inbox, bounded metadata and declared retention/dedup horizons. Transports provide attention hints and truthful stage observations.
- Use a single integration writer. Receiving a peer message does not issue write authority. No automatic takeover, autonomous routing or multi-writer source lease in v1.
- Separate source inventory, observed availability, admitted capability, quota and quality. Unknown quota stays null. A persisted instance identifier is not a fresh process incarnation.
- Reject default charter/scorecard migration and automatic timeout unification: demonstrated defaults exist, but the proposed multi-session contract and contention performance are not proven by them.
- Split core, adapters and integration ownership. Neither peer may edit the shared CLI entrypoint or README in its worker packet.
- Current attention adapters are scoped to observed native sessions; incoming messages cannot supply arbitrary shell commands.

## Bootstrap observations

Codex reached the existing AGY terminal through Orca; its first request has ID `adb072e4-07d6-46f9-9232-99ed0451b9a8` with `input_accepted` and `turn_started` observations. The existing Zcode chat visibly showed the requested review turn; its independently authored result was then read from disk. Both results arrived without operator paste relays. The frozen decision was subsequently sent to both peers for operational scope ACK and a one-attempt worker preflight. Those requests are execution coordination, not R2 debate.

The operator relayed zero messages during this controlled R1. This proves bootstrap reachability through the available adapters, not unattended recovery or a working native g8s peer inbox. The UI adapter still depends on an observable session and a running coordinator.

## Worker preflight evidence

Codex task `0f156a61-4461-4180-9efa-e4be6b51e40f`: provider `9router`, requested combo `fast-agent`, role `verifier`, permission `supplied_text`, tool policy `none`, timeout 45s, one attempt. It reached `WORKER_COMPLETED`, but `result.ok=false`, `status=failed`, duration 45.015s and no response. Receipt export succeeded; offered/denied/invoked counters report zero. This is a failed lane admission, not a passing negative-control review. No provider/model execution beyond the recorded requested combo is independently attested.

Preserve this failure. Further implementation is gated on a successful scoped worker lane, source/spec/interface contract, and per-owner scope ACK. Peer-owned preflights are recorded separately; no lane success is inferred from their ability to answer the debate.

## Operational ownership and peer preflights

Both peers explicitly ACKed D1 SHA256 `05c32decaa7f75a67e686de80827540d1a593b72817e89d36e4d4c4fa6d12515` and the disjoint four/six-file scopes in design.md. Their operational replies are not a second design debate.

| Peer | g8s task | Requested lane | Canonical result | Independent checks |
|---|---|---|---|---|
| AGY | 114be4e5-e4a1-46d3-9c0c-89880aa19cb7 | 9router / reasoning-free | ok=true, one attempt, 24.131s | Exact nonce and two state labels, result hash, exported receipt digest |
| Zcode | 88bf3599-20de-4557-b70c-0df8aebb7eb4 | 9router / fast-agent | ok=true, one attempt, 21.602s | Exact nonce, delivered=true, accepted=false, canonical result hash and export status |

These prove bounded supplied-text request/response through the peers' g8s runtimes. They do not prove coding quality, workspace-write containment, or the new inbox FSM. The Zcode ACK originally left receipt_path null and described this as normal for supplied_text; the canonical task nevertheless reports successful receipt export. Write authorization receipts and exported task-evidence receipts must remain distinct. The supervisor retains the canonical export fact.

## Planning closeout

Reviewed the seven new planning files, validated their local links and inspected untracked text for whitespace and private path/address leakage; no findings in that bounded check. Ownership sets are disjoint and both ACK digests match D1. Git diff whitespace checks passed and the primary checkout remained clean at the bound HEAD. No Go implementation or native inbox/recovery test is claimed. Phase 1 is the next execution packet under Codex ownership; the peers await that contract rather than requesting operator relays.

---

# Finalization Round R-FIN-01 (2026-10-31) — RT-MESH v1 negotiation & parallel execution

Author: zcode (`zcode-native-20261030-cu`). Peers: agy (source-integration owner), codex (parked, ledger-only). Ledger revision 5 → 6.

## Protocol events (this round)

- RT-MESH v1: proposed (seq 26) → agy co-sign with amendments A1/A2 (seq 27, **25s round-trip**) → applied → ratified broadcast (seq 28/29; codex 3rd co-sign queued).
- Knowledge exchange K1-K5 (seq 30/31 → answered seq 34): command-surface standardization (`--prompt-file`/`--input` always, `--timeout 180s`, per-node `G8S_STATE_DIR`, workspace_write env on both submit+worker); 5 dispatch lessons (L1 exact-line briefs cut runtime >180s→25-40s; L2 worktree `--add-dir` isolation fix `cebc16b`; L3 verify impasse fix `56ed8f7`; L4 `--apply-files` fence contract; L5 macOS TCC boundary); 2 charter gaps found (no mesh ops docs — fixing in agy A-slice; `receipt issue` doc bug: real flag is repeatable `--path`/`--allow`, JSON field `.data.receipt_id` — deferred to debt phase).
- **K4 vigil breakthrough**: Antigravity background-task runtime wakes agy natively when `wait` exits — zero polling, zero nudge; mirrors ZCode's harness background-task wake. Both platforms converged on the same primitive: dumb process holds the vigil, model turn starts only on wake.
- **K5 quota self-check: PASS_OBSERVED_PROGRAMMATICALLY** — P-A pty self-read (2000 lines of own TUI, no pixels/credentials) + P-B live `orca account list` (non-stale). 3 windows published status=observed: gemini_weekly 98% (resets 2026-11-07T02:16:57Z), gemini_five_hour 98% (10:17:29Z), claude_gpt_weekly 0% (2026-11-02T10:07:27Z). Evidence: `agy-k5-quota-publish.json` sha `4d4a2286…`.

## Parallel split (accepted, seq 32 → seq 34)

| Slice | Owner | Paths (claimed on ledger) | Status |
|---|---|---|---|
| z2-regression-tests-001 | zcode | internal/mesh/drills_test.go | **DONE** — 5 tests green (`ok 0.693s`, vet clean), accepted on ledger |
| W-round-receipt | zcode | this file (R-FIN-01 section) | **DONE** (this record) |
| agy-slice-A-finalization-001 | agy | plan.md, .g8s/feature-map.yml, docs/AGENTS_FULL.md | in flight (agy turn running) |
| task-agy-mcp-toollist-and-cli-input-fix-01 | agy (self-directed) | cmd/g8s/coordinator_mesh.go, internal/mcp/server_test.go | in flight |
| task-agy-mesh-review-simplify-01 | agy (self-directed) | internal/mesh/{claims,messages,store}.go, spec/46 | in flight |

Shared-forbidden without new claim: cmd/g8s non-coordinator files, spec/, README.md.

## Routing evidence (worker transport, 2026-10-31)

- 9router credential drift: `if/glm-4.6` (iflow) → 404 no active credentials; `kr/claude-sonnet-4.5` (kiro) → 404 same. Scorecard auto-quarantined if/glm-4.6 (composite 34, DEMOTE_OR_FALLBACK).
- Fallback that delivered: `cursor composer-2.5` native CLI (Surface A) implemented + self-verified the 6.6KB drills_test.go task.
- Lesson: availability is observed per-dispatch; evidence packets age within a day.

## Attention-adapter lessons (new, proposed as RT-MESH v1.1 A5)

1. "Send accepted" ≠ "turn started": a TUI modal (quota display) silently swallowed two terminal-injected nudges. Diagnosis = `orca terminal read --screen` of the peer; correction = modal's advertised close action (esc); then re-nudge.
2. Platform-native background wake (K4) supersedes external nudges wherever the platform has it; nudges remain the fallback for idle sessions without an armed vigil.

## Merge package (negotiated, pending gates)

One PR to main: `feat(mesh): coordinator interop ledger + RT-MESH v1` — internal/mesh (+drills_test.go), cmd/g8s/coordinator*, internal/mcp/mesh*, spec/openspec/46, plans docs, .g8s/feature-map.yml (FEAT-COORDINATOR-MESH-01), docs/AGENTS_FULL.md mesh section.
Gates: full `CGO_ENABLED=0 go test ./...`, doc-contract check, cross-verification (agy verifies zcode artifacts; zcode spot-verifies agy docs), S0 authorization (directed 2026-10-31).
Merge intel (agy): local main @ `d14845b` (8 tech-debt commits ahead of f3ced6a); one additive `.g8s/feature-map.yml` overlap, resolves cleanly.

## Deferred to Phase D (tech debt)

AM1 mesh `busy_timeout(5000)` → 30000 after measured burst (store.go:46) · AM2 inbox retention/rotation · A1 cumulative ack by seq · A2 flow-window enforcement · A3 retry budget/peer · A4 ping/pong advisory keepalive · A5 screen-state gate · fence-at-mutation enforcement point · `receipt issue` doc bug in vendored SKILL.md · evidence-packet refresh cadence.
