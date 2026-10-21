# Round 2 F0 ↔ F1 Cross-Family Adversarial Debate & Jev Triage Verdict

> **Session type**: T1 (strategy)

- **Date**: 2026-10-21
- **Presiding Brain (F0)**: Antigravity Supervisor (`F0`)
- **System-1 Pre-Debate Sensor**: `Jev` (`./bin/g8s reflex triage`, live TypeSafe classifier, `Sensor ≠ Decider`)
- **Round-2 F1 Debate Counterparts (`9router` Cross-Family Council)**:
  1. `F1-Aegis` $\rightarrow$ `cx/gpt-5.6-sol` (`--effort high`)
  2. `F1-Defense-in-Depth` $\rightarrow$ `reasoning-high` (`nvidia/nemotron-3-ultra-550b-a55b`)
  3. `F1-Homelab-Proxmox` $\rightarrow$ `cx/gpt-6-sol` (`--effort high`)
  4. `F1-TuneFlow` $\rightarrow$ `deep-coding` (`nvidia/nemotron-3-ultra-550b-a55b`)
  5. `F1-Hash-Checker` $\rightarrow$ `cx/gpt-5.6-terra` (`--effort medium`)

---

## 1. Jev System-1 Pre-Debate Triage Scores (`./bin/g8s reflex triage`)

Before opening the floor to debate, `Jev` scored the blast radius and breach probability of each Round-1 blueprint (`source: "jev"`, `is_fallback: false`):

| Candidate Blueprint | Target Files in `g8s` | `Jev` Risk Score | `Jev` Breach Prob | Latency | Sensor Interpretation |
|---|---|:---:|:---:|:---:|---|
| **TuneFlow** (Tiered Doctor + Portable Mode) | `internal/doctor/doctor.go`, `internal/pathutil/pathutil.go` | **1.02** | **0.05** | 1,374 ms | **Lowest risk**; isolated diagnostic & path resolution surface |
| **Hash-Checker** (Manifest + Batch Verify) | `internal/fsops/manifest.go`, `cmd/g8s/manifest.go` | **1.59** | **0.05** | 1,044 ms | **Low risk**; read-only SHA-256 verification surface |
| **Defense-in-Depth** (Net-Delta Guard + Lesson Hint) | `internal/verifier/diff_guard.go`, `internal/verifier/verifier.go` | **1.67** | **0.05** | 409 ms | **Low risk**; extends existing post-run verifier |
| **Homelab-Proxmox** (Approval Tokens + Circuit Breaker) | `internal/approval/service.go`, `internal/worker/circuit.go` | **2.36** | **0.13** | 1,930 ms | **Highest breach prob (`0.13`)**; touches P0 `internal/worker/*` and auth surface |
| **Aegis** (TheaterGuard + Receipt HMAC Chaining) | `internal/verifier/theater.go`, `internal/receipt/receipt.go` | **2.38** | **0.08** | 1,135 ms | **Highest risk score (`2.38`)**; touches P0 trust boundary `internal/receipt/*` |

---

## 2. Transcript of F0 Challenges vs. F1 Rebuttals & Concessions

### 2.1. `F0` vs. `F1-Homelab-Proxmox` (`cx/gpt-6-sol`)
- **F0 Challenge**:
  1. `g8s` already has `internal/receipt` (`WriteReceipt` with single-use CAS, TTL, and path scope). Building `internal/approval` duplicates `WriteReceipt`.
  2. `g8s` tasks already have `--max-attempts` and `--idempotency-key` in SQLite. Why build an in-memory sliding-window breaker in `internal/worker/circuit.go` when `g8s worker --once` restarts processes?
- **F1 (`cx/gpt-6-sol`) Rebuttal & Concession**:
  - **Blueprint 1 (`internal/approval`)**: **Full Concession.** *"internal/receipt already provides operator-issued, single-use, TTL-bound, path-scoped authority. Do not build `internal/approval`."*
  - **Blueprint 2 (`internal/worker/circuit.go`)**: **Conceded Proposed Design.** *"An in-memory breaker is wrong: restart clears it, and separate workers do not share its state. Final Round-2 position: Build neither blueprint now."*
- **Round-2 Outcome**: **ELIMINATED (0 LOC bloat added to Core Go; keep Bucket B/C Dual-Plane Secrets lessons only).**

---

### 2.2. `F0` vs. `F1-Aegis` (`cx/gpt-5.6-sol`)
- **F0 Challenge**:
  1. Why create a new `internal/verifier/theater.go` that re-reads whole files from disk when `internal/verifier/diff_guard.go` (`EvaluateNetDelta`) and `tools/f0/appeasement.go` already exist?
  2. Why add HMAC chaining to short-lived (600s TTL) `WriteReceipt`s in SQLite WAL (`Jev risk=2.38`, P0 boundary)?
- **F1 (`cx/gpt-5.6-sol`) Rebuttal & Concession**:
  - **Conceded `theater.go`**: *"F0 right. New `internal/verifier/theater.go` duplicates scanning, adds whole-file I/O, and conflicts with Net-Delta semantics. Merge high-confidence stub patterns into `EvaluateNetDelta`."*
  - **Conceded Receipt HMAC Chaining**: *"F0 and Jev right. Reject HMAC chaining for ephemeral `WriteReceipt`... Local process able to alter receipt DB may likely access local HMAC key too."*
- **Round-2 Outcome**: **MERGED WITH `Defense-in-Depth` INTO `EvaluateNetDelta` (`internal/verifier/diff_guard.go`); `theater.go` AND `receipt HMAC` ELIMINATED.**

---

### 2.3. `F0` vs. `F1-Defense-in-Depth` (`reasoning-high` / `nemotron-3-ultra-550b`)
- **F0 Challenge**:
  1. Won't flagging `TODO` in `EvaluateNetDelta` punish legitimate owned TODOs (`// TODO(#639): ...`) and become a bureaucratic shackle?
  2. Doesn't `lookupJITWhisper` in `internal/fsops/fsops.go` already inject rules on `Read`/`Edit`? Why inject lessons again in `g8s verify`?
- **F1 (`reasoning-high`) Rebuttal & Defense**:
  - **Conceded Bare `TODO`**: Excluded `TODO(#...)` and prose comments; narrowed regex strictly to **hollow placeholders** (`FILL IN HERE`, `<Empty>`, `[Insert Here]`, `panic("TODO")`, `panic("not implemented")`) and **trivial fake test assertions** (`assert.True(t, true)`).
  - **Defended Post-Failure Lesson Injection**: Proved `JIT Whisper` (proactive spatial file hint during `Read/Edit` from `vault.db`) vs. `Verify Injection` (reactive ratified `wrongApproach` $\rightarrow$ `correctApproach` lesson attached to `RoundReceipt` *only when a verifier check fails*) serve different audiences and timings.
- **Round-2 Outcome**: **APPROVED FOR `v0.19.0` IMPLEMENTATION (Narrowed ~45 LOC in `internal/verifier/diff_guard.go` + `verifier.go`).**

---

### 2.4. `F0` vs. `F1-TuneFlow` (`deep-coding` / `nemotron-3-ultra-550b`)
- **F0 Challenge**:
  1. Changing `g8s doctor` `status` from `"HEALTHY"` to `"DEGRADED"` when optional CLIs (`ollama`, `gemini`) are absent will break existing scripts/CI gates that check `g8s doctor --select status == "HEALTHY"`.
  2. Why add `PORTABLE_MODE=1` / `.portable` when `G8S_STATE_DIR` already exists?
- **F1 (`deep-coding`) Rebuttal & Defense**:
  - **Conceded Backward-Compat Trap**: Kept `status` (`OverallStatus = CoreStatus`) strictly `"HEALTHY"` whenever core checks pass! Added non-breaking `capability_status` (`FULL`/`PARTIAL`/`MINIMAL`) and `capabilities[]` array alongside existing fields.
  - **Defended `.portable` Marker**: Proved `G8S_STATE_DIR` requires knowing an explicit path, whereas a `.portable` marker file next to the `g8s` executable resolves state relative to `filepath.Dir(os.Executable())` with zero env vars (`G8S_STATE_DIR` > `.portable` > `XDG`).
- **Round-2 Outcome**: **APPROVED FOR `v0.19.0` IMPLEMENTATION (Non-breaking `capabilities[]` in `g8s doctor` + `.portable` fallback in `internal/pathutil`).**

---

### 2.5. `F0` vs. `F1-Hash-Checker` (`cx/gpt-5.6-terra`)
- **F0 Challenge**:
  1. Inside a git repository, `git status --porcelain` and `git diff` already report modified, missing, and untracked files in `<10ms`. Why reinvent a SHA-256 tree walker for worktrees?
  2. Why propose two separate commands (`g8s manifest` and `g8s batch`) and two separate packages?
- **F1 (`cx/gpt-5.6-terra`) Rebuttal & Consolidation**:
  - **Conceded Worktree Redundancy**: Agreed `git status --porcelain` wins for git worktrees; dropped `g8s manifest create/verify` for worktrees.
  - **Conceded `manifest` vs `batch` Duplication**: Merged both into a single **Supply-Chain Artifact Verifier** (`g8s verify files --in checks.json --root <dir>`, SHA-256 only, zero MD5/SHA-1) specifically for **non-Git release archives, extracted bundles, and supply-chain verification in `v0.20.0` ([Issue #639](https://github.com/tamld/g8s/issues/639))**.
- **Round-2 Outcome**: **CONSOLIDATED & ASSIGNED TO `v0.20.0` EPIC ([Issue #639](https://github.com/tamld/g8s/issues/639)).**
