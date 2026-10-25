---
title: "Round 3 Platform Empowerment: Multi-Root F1->F2 Delegation, Depth-2 Telemetry, Perimeter Anti-Theater & Doctor Capability Matrix"
description: "Implementation plan for Issues #643, #644, and #645 empowering both Broad Horizontal and Deep Vertical F0->F1->F2 campaigns without F0 context dilution."
status: completed
priority: P1
effort: 4h
branch: feat/r3-platform-empowerment-643-645
tags: [adr-0032, adr-0033, fsops, telemetry, verifier, doctor, pathutil]
created: 2026-10-21
---

# Plan: Round 3 Platform Empowerment (`#643`, `#644`, `#645`)

> **Session type**: T2 (execution)

## 1. Reflection & Antithesis (`Phản tư & Phản đề`)

### 1.1. Phản tư (Critique of Round 1 & Round 2)
1. **Self-Shackling on Multi-Root Scope (`G8S_SCOPE_ROOT` vs `G8S_ADD_DIRS`)**:
   - In Round 1, F1 workers isolated inside `g8s` worktrees needed to inspect sibling repositories (`aegis`, `defense-in-depth`, `homelab-proxmox`, `adopter-utility-01`, `hash-checker`) while writing sealed reports back into `g8s` (`plans/handoffs/r1-ingest-*.md`).
   - Because `fsops.ResolveDelegationContext` treated `G8S_SCOPE_ROOT` as a single path string and ignored `G8S_ADD_DIRS`, F1 workers had to run `env -u G8S_SCOPE_ROOT ./bin/g8s llm-call --scope-root <sibling_repo>` as a workaround.
2. **Dark `F1 -> F2` Depth-2 Telemetry (`ADR-0032` §4 Gap)**:
   - When F1 workers spawned 20 F2 subagents via `./bin/g8s llm-call`, F0 could only verify F2 invocations by manually scanning F1 markdown prose.
   - However, dumping raw F2 prompts/outputs into F0's receipt would violate `ADR-0033` (F0 Information Firewall) and dilute F0's context window during large horizontal or deep vertical campaigns.
3. **Why Hardcoding Tournament Topology in Go Is an Anti-Pattern (`Law 3`)**:
   - Hardcoding `"every task must spawn 4 F2 subagents"` in Go would turn `g8s` into a rigid bureaucratic cage (`gọng kiềm`). Topology belongs in the `g8s-supervisor` skill (`BriefSchema`), while Go provides the **empowering primitives** (multi-root scope + compact depth-2 telemetry digest + perimeter verification).

### 1.2. Phản đề (Two Campaign Modes Enabled by `#643`–`#645`)
- **Mode A — Broad Horizontal Campaign (`Đánh diện rộng`)**: `1 F0 -> m F1 (Repo/Module Leads) -> n F2 (Collectors/Auditors)` across multiple repositories or subsystems with zero F0 context dilution.
- **Mode B — Deep Vertical Campaign (`Đánh diện chuyên sâu` — Next Experiment on `adopter-utility-01`)**: `1 F0 -> k F1 (Domain Leads: UX, Security, Deployment) -> n F2 (REA Researcher, Red-Team Skeptic, Implementer, Verifier)` iterating deeply on a single repository (`adopter-utility-01`) with high-trust `WriteReceipt` isolation, perimeter anti-theater verification, and compact `f2_summary` proof.

---

## 2. Implementation Phases (`ck:cook`)

### Phase 1: Issue `#643` — Multi-Root Scope Delegation & `ADR-0032` `F1->F2` Depth-2 Telemetry Digest
- **Files**:
  - `cmd/g8s/fsops.go`, `cmd/g8s/search.go`, `cmd/g8s/llm_call.go`
  - `internal/fsops/fsops.go`, `internal/fsops/fsops_test.go`
  - `internal/worker/worker.go`
  - `internal/verifier/verifier.go`, `internal/verifier/verifier_test.go`
- **Deliverables**:
  - **D1.1**: Unblock read-only operations (`g8s read`, `g8s search`, `g8s llm-call --context-files`) when `G8S_RECEIPT_ID` is inherited in the environment unless `--receipt-id` is explicitly passed on the CLI.
  - **D1.2**: Support comma-separated ceiling roots in `G8S_SCOPE_ROOT` and `G8S_ADD_DIRS` inside `fsops.ResolveDelegationContext` (mutations require matching a root in `G8S_SCOPE_ROOT`; read-only operations accept any root in `G8S_SCOPE_ROOT` or `G8S_ADD_DIRS`). Export `G8S_ADD_DIRS` in `internal/worker/worker.go`.
  - **D1.3**: When `g8s llm-call` executes while `G8S_TASK_ID` is set in the environment, append a compact JSONL record (`task_id`, `trace_id`, `provider`, `model`, `duration_ms`, `input_tokens`, `output_tokens`, `total_tokens`, `context_files_count`, `applied_files_count`, `timestamp`) to `$G8S_STATE_DIR/f2_telemetry/<task_id>.jsonl`.
  - **D1.4**: In `internal/verifier/verifier.go`, aggregate `$G8S_STATE_DIR/f2_telemetry/<task_id>.jsonl` into `Verdict.F2Summary` (`*F2TelemetrySummary`: `total_calls`, `models_used`, `total_tokens`, `total_duration_ms`, `telemetry_sha256`) so F0 gets an observational telemetry digest of F2 fan-out (with SHA-256 audit link to the raw JSONL) without context bloat.

### Phase 2: Issue `#644` — Perimeter Anti-Theater Net-Delta Guards & Point-of-Failure Lesson Injection
- **Files**:
  - `internal/verifier/diff_guard.go`, `internal/verifier/diff_guard_test.go`
  - `internal/verifier/verifier.go`, `internal/verifier/verifier_test.go`
- **Deliverables**:
  - **D2.1**: Extend `EvaluateNetDelta` in `internal/verifier/diff_guard.go` with advisory checks:
    - `TIER0-THEATER-PLACEHOLDER` for added lines containing hollow scaffolding markers (`FILL IN HERE`, `<Empty>`, `[Insert Here]`, `panic("TODO`, `panic("not implemented`) while ignoring owned `TODO(#123)` comments.
    - `TIER0-TRIVIAL-ASSERT` in `*_test.go` files for tautological assertions (`assert.True(t, true)`, `require.True(t, true)`, `assert.Nil(t, nil)`, `if true == true`).
  - **D2.2**: In `internal/verifier/verifier.go`, when a verification run produces `OutcomeFail` or non-empty `Advisories`, query ratified lessons from the lessons ledger (`docs/lessons/ledger.jsonl` or `WithLessonsLedgerPath`) matching both the verifier class AND the specific failure/advisory signals (`extractFailureSignals`), attaching `InjectedLessons []InjectedLessonHint` (`id`, `author_class`, `matched_signal`, `wrong_approach`, `correct_approach`) to `Verdict`.

### Phase 3: Issue `#645` — Non-Breaking `capability_status` Matrix in `g8s doctor`, `.portable` State Resolution & Playbook
- **Files**:
  - `internal/doctor/doctor.go`, `internal/doctor/doctor_test.go`, `cmd/g8s/doctor.go`
  - `internal/pathutil/pathutil.go`, `internal/pathutil/pathutil_test.go`
  - `skills/g8s-supervisor/references/multi-worker-fanout.md`
- **Deliverables**:
  - **D3.1**: Add `CapabilityStatus` (`FULL` | `PARTIAL` | `MINIMAL`) and `Capabilities []CapabilityEntry` (with `verification_state: "probed" | "configured_unprobed" | "absent"`) to `doctor.DoctorReport` without changing `OverallStatus: "HEALTHY"` when core checks pass.
  - **D3.2**: Update `pathutil.DefaultStateDir()` and `pathutil.StateDirForOS()` to honor `G8S_PORTABLE=1` / `PORTABLE_MODE=1` (or a `.portable` marker file next to the executable) after `G8S_STATE_DIR` and before OS/XDG defaults.
  - **D3.3**: Document the `T0..T3` Horizontal vs. Deep-Vertical Multi-Agent Tournament Playbook in `skills/g8s-supervisor/references/multi-worker-fanout.md` and sync to `~/.agents/skills/g8s-supervisor/references/multi-worker-fanout.md`.
