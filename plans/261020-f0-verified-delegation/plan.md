---
title: "F0 Verified Delegation — evidence-gated autonomy instead of synchronous HITL"
issues: [620, 621, 622, 623, 624, 625, 626, 627, 628, 629, 630, 631]
status: in_progress
priority: P1
created: 2026-10-20
blockedBy: []
relatedPlans: [261017-differential-governance-hitl]
epistemic_status: hypothesis
---

# Plan: F0 Verified Delegation (epic #620)

- **Session type**: T1 (strategy + implementation)
- **Design**: approved brainstorm B′ (2026-10-19), decisions D1–D9, codified in ADR-0033 (Phase 1)
- **Motivating evidence**: 2026-10-18 corrections ledger C1–C10. All 10 defects came from F0, none from F1.

## 1. Axioms
1. **Freedom on the road, toll at the booth.** Zero hard gates while F0 explores, reasons, experiments or dispatches F1. Gates fire only at *commitment points*: merge, close issue, publish, and the final report to PiC. Cost scales with the number of claims, not with the amount of work.
2. **Tools are eyes, not handcuffs.** The gate *fetches ground truth itself* (CI API, file counts, deliver records). F0 never has to transcribe evidence by hand. `f0 status` is always available.
3. **Non-blocking whispers in-flight.** Lessons arrive as one-line `jit_whisper`s (reuse #594 / `internal/fsops`), capped by `K_max`.
4. **Hard gates are deterministic.** Jev and any LLM auditor are advisory sensors (Sensor ≠ Decider). They are never the sole blocker.
5. **Forge-agnostic and provider-agnostic.** Local evidence is the source of truth; forge CI is an adapter (`github` | `none`). Missing Jev or provider ⇒ `UNAUDITED` stamp, never deadlock.
6. **Not a product feature (D3).** Everything lives in `tools/f0/` (a separate Go `main`, excluded from goreleaser) plus the `g8s-supervisor` skill. It reuses `internal/telemetry` (OverrideReceipt), `internal/fsops` whisper, `internal/reflex` (Jev triage) and `tools/merger.sh`.

## 2. DRY: existing assets reused
| Asset | Origin | Reused for |
|---|---|---|
| `tools/merger.sh` (autonomy gate, lane gate, verifier gate, green checks) | #516 | T2 merge path calls `f0 gate check` instead of re-implementing checks |
| `internal/telemetry.RecordOverrideReceipt`, rule weights | #596 | G4/G2 overrides and waivers; error-budget debits |
| `fsops` `jit_whisper` + `K_max` | #594 | in-flight lesson whispers to F0 |
| `internal/reflex` `TriageRequest`/`TriageVerdict` | ADR-0020 | Jev L1 tier suggestion |
| `g8s eval` probe suites | #254/#601 | T3 deterministic independent verifier |
| `tools/claims_check.sh` pattern | #447 | G6 claim-citation lint style |

## 3. Phases
| Phase | Wave | Issue | Title | AllowedPaths (F1 receipt) | Depends |
|---|---|---|---|---|---|
| [01](phase-01-adr.md) | A | #621 | ADR-0033 | `docs/decisions/0033-f0-verified-delegation.md`, `skills/g8s-supervisor/SKILL.md` | — |
| [02](phase-02-evidence-ledger.md) | A | #622 | Evidence ledger (per-repo + global, anchored) + `f0 run` + G5 | `tools/f0/**` | 01 |
| [03](phase-03-mandate.md) | A | #623 | Campaign mandate (ledger-sourced) | `tools/f0/main.go`, `tools/f0/mandate*.go` | 02 |
| [04](phase-04-gate-core.md) | A | #624 | Gate G0/G1/G2/G3/G6 + status/waive/manual/anchor/audit + merger Gate e | `tools/f0/{main,gate,forge,status,waive,manual,anchor,audit,config,claims}*.go`, `tools/merger.sh`, `tools/merger_test.sh` | 02, 03 |
| [04b](phase-04b-agent-hooks.md) | A | #624 | Agent-level intercept: PreToolUse + Stop hooks | `tools/f0/{main,hook}*.go`, `tools/f0/hooks/**`, `tools/f0/testdata/hooks/**` | 04 |
| [05](phase-05-skill-update.md) | A | #631 | Supervisor skill: tier table + lessons + toolkit | `skills/g8s-supervisor/**` | 01–04b |
| [06](phase-06-wave-a-replay.md) | A | #620 | Replay C1–C10 and measure | `tools/f0/testdata/replay/**`, `tools/f0/replay_test.go` | 02–04b |
| [07](phase-07-wave-b.md) | B | #625 #626 #630 #627 #628 #629 | G4, G7, auditor+digest+Jev, T2 executor (PiC stop), T3 verifier, breaker | per sub-phase | Wave A measured |

Wave B is planned at outline level in phase-07. It is re-planned in detail after the Phase 06 measurement (YAGNI).

### Red-team review (2026-10-20, independent subagent)
3 critical + 9 major + 6 minor findings, all accepted and folded into the phases:
- C1: merger.sh is not F0's merge path ⇒ new Phase 04b (agent hooks) + `f0 audit`.
- C2: ledger truncatable/escapable ⇒ fixed per-repo/global paths + external anchors.
- C3: mandate not enforced and editable ⇒ G0 + ledger-sourced mandate.
- M1–M9 and m1–m6: see the "Revised after red-team" notes in each phase file.

## 4. Execution protocol (cook `--auto`, per PiC)
- Every code change goes F0 → F1: `g8s submit` with a path-scoped receipt, one worker per phase slice, F0 verifies the diff, then `g8s deliver --prune-worktree`. F0-manual authoring is limited to `plans/**`.
- Never `kill` a concurrent drain (lesson from #617).
- Test gate: `CGO_ENABLED=0 go test ./tools/f0/...` passes 100%, `bash tools/merger_test.sh` passes, and the pre-push hook passes.
- Review gate: an independent review subagent checks each phase diff against its success criteria.
- Merge: only when all CI checks are completed and successful, with run IDs cited.
- **PiC stops**: (1) installing hooks into the operator's customization root (T4 config); (2) Phase 07 T2 executor credential/Keychain design.

## 5. Definition of Done (Wave A)
- [ ] ADR-0033 merged
- [ ] `f0 run`, `f0 ledger verify` (with anchors), `f0 mandate`, `f0 gate check`, `f0 status`, `f0 audit` work offline (`ci: none`)
- [ ] `f0 hook pre-tool` denies a merge whose gate FAILs and denies bypass flags. `f0 hook stop` forces a continue on uncited status claims.
- [ ] merger.sh Gate e refuses on gate FAIL
- [ ] Replay suite records **actual** results. Hypothesis: hard BLOCK = C1, C2, C6, C9, C10 (5); FLAG = C5, C8 (2); not covered = C3, C4, C7 (Wave B)
- [ ] Skill updated and synced to `~/.agents/skills/g8s-supervisor`

## 6. Risks
| Risk | Mitigation |
|---|---|
| Gate friction strangles F0 (axiom 1) | Gates only at commitment points; hooks allow all unclassified commands; G6 is subject+verdict only; measure added time in Phase 06 |
| Single-user machine: F0 can bypass tooling (edit hooks.json, spoof `go` on PATH, self-issue mandate) | Hooks deny shell edits to hooks/ledger; `f0 audit` + anchors detect after the fact; documented as accepted limitations in ADR-0033 |
| Hook crash blocks all work | Fail-safe: deny only merge/T4 on hook error; allow everything else |
| Heuristic false positives (G4, G5, G6) | Overrides with reason, debited and visible in digest; prose corpus tests for G6 |
| Scope creep into product | No changes under `cmd/g8s` or `internal/` in Wave A |

