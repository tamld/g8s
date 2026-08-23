# g8s MVP → OSS Release (DELTA-02 onward)

## Objective

Advance g8s from the current state (DELTA-02 receipt engine applied on feature branch) through the full MVP gate defined by `docs/REFACTORING_PLAN.md` Phases 1–5, with CI workflows in place, so the project is publication-ready as open source under MIT. Actual publishing (push/public) happens only after the owner's final look at the finished MVP.

## Original Request

Owner (2026-08-24, Vietnamese, intent preserved): "Em tự chạy, thiết kế trong goal. Đây là experiment của anh: anh thiết kế sẵn bộ khung và em tự chạy, anh sẽ chờ kết quả cuối cùng khi em đạt goal." Prior same-day directives: execute per self-defined goal with evidence logs; conditional permission to publish as OSS under MIT once MVP achieved; permission to set up CI/CD + cross-platform release build testing via the public repo; never harm/delete files outside project scope.

## Intake Summary

- Input shape: `existing_plan`
- Audience: repo owner (tamld), who reviews only the final result
- Authority: `approved` (full delegated authority inside CWD; publish step gated on owner approval)
- Proof type: `metric` (plus `test`)
- Completion proof: full-suite dual-pass gate green with >= 140 tests, GoReleaser snapshot artifacts for linux/darwin/windows on amd64/arm64, and a final audit mapping every receipt back to the refactor plan phases
- Goal oracle: see below
- Likely misfire: declaring victory after receipt engine alone; pushing/publishing to GitHub before the MVP gate; gold-plating control plane beyond its OpenSpec delta
- Blind spots considered: remote `origin` already exists (visibility unverified); no GoReleaser config yet; host has no Go toolchain so every gate runs in Docker `golang:1.25`; AGENTS.md exit-criteria wording (`CGO_ENABLED=0 -race`) is unsatisfiable — dual-pass gate substitutes (documented D4 in M1 sprint log)
- Existing plan facts: Phase 1 harness DONE; DELTA-02 receipt DONE on branch `feat/m1-receipt-engine` (3 commits, unmerged); Phase 2 = control plane WAL queue + CAS leases + worker supervisor; Phase 3 = providers + MCP server; Phase 4 = kardianos service + cobra CLI; Phase 5 = parity >= 140 tests + GoReleaser

## Goal Oracle

The oracle for this goal is:

`In Docker golang:1.25: CGO_ENABLED=0 go vet ./... && go test ./... is fully green AND CGO_ENABLED=1 go test -race ./... is green with zero reports AND total collected test count >= 140 AND goreleaser release --snapshot --clean succeeds producing cross-platform binaries`

The PM must keep comparing task receipts to this oracle. Planning, discovery, a passing tiny slice, or a clean-looking board is not enough. The goal finishes only when a final Judge/PM audit maps receipts and verification back to this oracle and records `full_outcome_complete: true`.

## Goal Kind

`existing_plan`

## Current Tranche

Continuous execution: merge M1 into main, stand up CI, port the control plane (Phase 2), then providers/MCP (Phase 3), service + CLI (Phase 4), parity + GoReleaser (Phase 5), reviewing only at phase boundaries, until the oracle passes end to end. Publishing itself stays out of the tranche and becomes a single blocked-on-owner terminal card once everything else is proven.

## Non-Negotiable Constraints

- Constitution axioms: Zero-CGO (`modernc.org/sqlite` only), injectable clock for time-dependent logic, receipts single-use TTL <= 3600s path-scoped, containment (0600/0700, process groups).
- Spec-first: no code before the corresponding OpenSpec delta is verified/updated.
- All gates run in Docker `golang:1.25` with named cache volumes (`g8s-gomod`, `g8s-gocache`); host stays toolchain-free.
- Never commit secrets; security scan before every commit; 100% English commits/docs/comments.
- Never touch files outside the project scope/folder (owner hard constraint).
- No push / no repo visibility changes without explicit owner approval at the MVP gate.

## Stop Rule

Stop only when a final audit proves the full original outcome is complete (oracle green end to end, receipts mapped to plan phases) — or when the only remaining action is owner-gated publishing, recorded as a blocked terminal card with `waiting_for_user_approval: true`.

## Slice Sizing

Safe means bounded, explicit, verified, and reversible. It does not mean tiny. Control plane lands as 2–3 vertical packages (queue, leases, supervisor), each with its own tests and race gate, not one mega-commit. Repeated same-shape work (e.g., provider ports) goes into one package reviewed as a whole.

If an exact human approval phrase is the only remaining blocker and no safe local work remains, ask once and stop. Preserve the exact phrase in the blocked receipt as `required_reply`, set `waiting_for_user_approval: true`, set `goal.status: blocked`, and set `active_task: null`.

## Board Health

The PM owns board health:

```bash
node /Users/tamld/.claude/skills/goal-prep/scripts/check-goal-state.mjs docs/goals/g8s-mvp-oss
```

## Canonical Board

Machine truth lives at `docs/goals/g8s-mvp-oss/state.yaml`. If this charter and `state.yaml` disagree, `state.yaml` wins.

## Run Command

```text
/goal Follow docs/goals/g8s-mvp-oss/goal.md.
```

## PM Loop

On every `/goal` continuation:

1. Read this charter, and follow the GoalBuddy execution contract (`references/goal-execution.md` in the goal-prep skill) when available.
2. Read `state.yaml`.
3. Re-check intake facts and the oracle.
4. Work only on the active board task.
5. Assign Scout, Judge, Worker, or PM according to the task (in this harness the PM executes roles directly, optionally delegating heavy exploration to subagents).
6. Write a compact task receipt; update the board.
7. Continue to the next largest safe package unless blocked; review at phase/risk/final boundaries only.
8. Finish only with an audit receipt recording `full_outcome_complete: true`.
