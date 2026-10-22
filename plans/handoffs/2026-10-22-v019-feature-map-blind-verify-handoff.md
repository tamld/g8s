# Session Handoff: Pre-`v0.19.0` Feature Map, 3-Layer Blind Verification Loop & Verification Skills Suite

- **Date**: 2026-10-22
- **Session type**: T2 (execution)
- **Target Release**: `v0.19.0` (BLOCKED on Issues `#648` – `#652`)
- **Governance Tier**: `F0 -> F1 -> F2` Zero-Trust Capability Delegation (`ADR-0032`, `ADR-0033`)

---

## 1. Executive Summary & Pre-`v0.19.0` Release Gate

> **RELEASE GATE DIRECTIVE**: Do **NOT** tag or cut `v0.19.0` until GitHub Issues **`#648`, `#649`, `#650`, `#651`, and `#652`** are implemented via `F0 -> F1 -> F2`, mechanically verified, and merged into `main`.

During the `TuneFlow` Deep Vertical dogfooding campaign and our subsequent multi-agent architectural audit (`F1-Arch-Auditor`, `F1-Skill-Verifier-Researcher`, and `GPT-6.1 Sol Ultra` via `9router`), we uncovered:
1. Two concrete verification defects in `g8s` (`EvaluateDoDMatrix` trusting worker-authored `ReportedDoD` strings without requiring `checksOK == true`, and `f0 verify-t3` leaving `ProviderRunner`/`EvalProbeRunner` unwired `nil` in CLI invocations).
2. Fragmentation of feature traceability across 6 disjoint artifacts (`manifest.json`, `docs/PRD.md`, `docs/SRS.md`, `spec/openspec/*.md`, `.g8s/verifier-classes.yml`, and telemetry), causing context rot and cross-feature regressions when workers modify shared packages.
3. Susceptibility to **AI Evaluation Awareness**, **Test Overfitting**, and **Reviewer Sycophancy** when F1/F2 workers can read the full test suite and when verifiers read persuasive worker self-summaries (`worker.Summary`, `worker.ReportedDoD`).

To close these gaps permanently before tagging `v0.19.0`, we have opened 5 tracking issues defining the **Declarative End-to-End Feature Map (`.g8s/feature-map.yml`)**, the **3-Layer Anonymous / Blind Verification Loop**, the **6-Module `g8s` Verification Skills Suite**, and **TuneFlow Deep Vertical Phase 2 Dogfooding**.

---

## 2. Newly Opened GitHub Issues (Pre-`v0.19.0` Blockers)

Execute these issues in the following dependency order:

| Order | Issue | Title | Target Files / Scope | URL |
| :---: | :---: | :--- | :--- | :--- |
| **1** | **#648** | `feat(verifier): fix EvaluateDoDMatrix self-report trust hole & wire CLI runner in f0 verify-t3` | `internal/verifier/verifier.go`, `cmd/g8s/verify.go`, `tools/f0/verifier_t3.go` | https://github.com/tamld/g8s/issues/648 |
| **2** | **#649** | `feat(feature-map): declarative end-to-end Feature Map (.g8s/feature-map.yml) & blast-radius routing for F0-F1-F2` | `.g8s/feature-map.yml`, `internal/featuremap/`, `cmd/g8s/feature.go`, `tools/ci_feature_map_sync.sh` | https://github.com/tamld/g8s/issues/649 |
| **3** | **#650** | `feat(verify-blind): 3-layer Anonymous / Blind Verification Loop (Out-of-Worktree Holdout, Sanitized Double-Blind Packet & Seeded Mutant Canary)` | `internal/verifier/blind_packet.go`, `internal/verifier/canary.go`, `internal/verifier/verifier.go`, `cmd/g8s/verify.go` | https://github.com/tamld/g8s/issues/650 |
| **4** | **#651** | `feat(skills): package g8s Verification Skills Suite & offer bundle integration` | `skills/`, `skills/manifest.json`, `.g8s/verifier-classes.yml`, `offer/` | https://github.com/tamld/g8s/issues/651 |
| **5** | **#652** | `feat(dogfood): apply g8s v0.19.0 Feature Map, Blind Verification Loop & Verification Skills to TuneFlow Deep Vertical Phase 2` | `TuneFlow` worktree (`.g8s/feature-map.yml`, holdout suites, blind review), `offer/SCORECARD.md` | https://github.com/tamld/g8s/issues/652 |

> **Parallelism Note**: Issues **#648**, **#649**, and **#651** touch disjoint file scopes and can be dispatched in parallel to isolated F1 workers in Wave 1. Issue **#650** builds on `#648`/`#649` in Wave 2, and Issue **#652** validates the full stack in Wave 3.

---

## 3. Key Audit Findings & Architectural Blueprint

### 3.1. The 2 Verifier Vulnerabilities (`#648`)
1. **Finding 1 — `EvaluateDoDMatrix` Worker Self-Report Trust Hole**:
   - **Location**: `cmd/g8s/verify.go:140-152` and `internal/verifier/verifier.go:1412-1437`.
   - **Defect**: `EvaluateDoDMatrix(items, target.ReportedDoD, checksOK)` accepts worker-authored `target.ReportedDoD` entries whenever `strings.EqualFold(r.Status, "MET") && strings.TrimSpace(r.Evidence) != ""` **without checking `checksOK == true`** or mechanically validating the evidence artifact.
   - **Fix**: Require `checksOK == true` as a mandatory precondition for `MET` in `EvaluateDoDMatrix`, and verify cited evidence mechanically (file/hash existence or check output match).
2. **Finding 2 — Unwired CLI Runner in `f0 verify-t3`**:
   - **Location**: `tools/f0/verifier_t3.go:207-268` (`verifyT3Command`).
   - **Defect**: `opts.ProviderRunner` and `opts.EvalProbeRunner` are left `nil` outside unit tests, causing CLI invocations of `f0 verify-t3` to hit `!verifierRan` (`tools/f0/verifier_t3.go:125`) and deterministically return `VETO_ESCALATE` (`independent T3 verifier and eval probes did not execute`).
   - **Fix**: Wire the live config-driven `internal/llmcall` (`9router` / provider runner) into `opts.ProviderRunner` and wire `g8s eval run` probe execution into `opts.EvalProbeRunner`.

### 3.2. Declarative `.g8s/feature-map.yml` Schema & Blast-Radius Routing (`#649`)
Unifies `manifest.json`, `docs/PRD.md`, `docs/SRS.md`, `spec/openspec/*.md`, `.g8s/verifier-classes.yml`, and test suites into a single machine-checkable DAG:
- **Node Schema**:
  `feature_id -> persona_story (PRD) -> srs_reqs (SRS) -> openspec_delta -> code_paths -> invariants -> visible_tests -> holdout_tests -> verifier_class -> blast_radius (depends_on / direct_consumers)`
- **How F0 Uses It to Prevent Context Rot & Cross-Feature Regressions**:
  1. **Scoped Write Receipts**: When F0 dispatches an F1 worker for `feature_id`, `WriteReceipt.AllowedPaths` is automatically derived/validated from `code_paths` + `visible_tests` (strictly excluding `holdout_tests`).
  2. **Transitive Consumer Verification**: `g8s verify --feature <id>` executes both the target feature's tests and the test suites of all `blast_radius.direct_consumers`, catching breaking changes across feature boundaries immediately.
  3. **Orphan Detection**: `tools/ci_feature_map_sync.sh` / `g8s feature verify --json` fails CI if production code paths or spec requirements are orphaned.

### 3.3. 3-Layer Anonymous / Blind Verification Loop (`#650`)
Designed to defeat AI Evaluation Awareness, Test Overfitting, and Reviewer Sycophancy:
1. **Layer 1 — Out-of-Worktree Holdout Suite (`--holdout-dir`)**:
   - Hidden acceptance tests reside in `$G8S_STATE_DIR/holdout/<feature-id>/`, outside F1/F2's worktree and blocked by `G8S_SCOPE_ROOT` (`g8s read` and `g8s search` cannot access them during worker execution).
   - `g8s verify --holdout-dir` mounts and executes the holdout suite only after the worker completes.
2. **Layer 2 — Sanitized Double-Blind Review Packet (`internal/verifier/blind_packet.go`)**:
   - Strips `worker.Summary`, `worker.ReportedDoD`, author/model/provider identity, commit message prose, and F0/Jev opinions.
   - Passes **only** `SpecContract + Invariants + RawGitDiff + MechanicalTestOutput` to the independent Verifier F1/T3.
3. **Layer 3 — Seeded Mutant / Canary Check (`internal/verifier/canary.go`)**:
   - Injects a synthetic regression into an ephemeral copy of the modified target and verifies that the holdout suite + Blind Verifier reject it (`CanaryCaught == true`). If the mutant survives, `g8s verify` emits `VETO_CANARY_SURVIVED`.

### 3.4. 6 Verification Skills Mapping (`#651`)
Distilled from `~/.agents/skills/` (`brainstorm/references/ultra-verifier-mode.md`, `code-review/references/adversarial-review.md`, `prove/SKILL.md`, `ck-scenario/SKILL.md`, `wiki-verifier`) and SOTA verification gates into `skills/` and `offer/`:
1. **`g8s-blind-verifier`**: Double-blind counterexample & falsification review operating on `BlindReviewPacket` (`adversarial-review.md`).
2. **`g8s-red-prove`**: Mechanical `Red -> Green` reproduction + property/mutation proof (`prove/SKILL.md`).
3. **`g8s-scenario-matrix`**: 12-dimension edge-case, concurrency, and failure-injection generator for visible + holdout suites (`ck-scenario/SKILL.md`).
4. **`g8s-ux-visual-gate`**: Headless browser DOM bounding-box (`getBoundingClientRect`), computed style/contrast (`getComputedStyle`), zero console error, and media playback contract verifier.
5. **`g8s-sec-gate`**: Zero-trust SAST + live DAST probe runner enforcing SSRF, path traversal, CSP/XSS, and trust-boundary invariants (`.g8s/trust-boundaries.yml`).
6. **`g8s-epistemic-audit`**: Claim decomposition and evidence-tier classifier (`MECHANICAL_VERIFIED` vs `OBSERVATIONAL_TELEMETRY` vs `UNVERIFIED_PROSE`, from `ultra-verifier-mode.md` & `wiki-verifier`).

### 3.5. Epistemic Invariants (`GPT-6.1 Sol Ultra` Audit Synthesis)
All `v0.19.0` verification components must enforce these four epistemic laws:
1. **Telemetry is Observational Unless Cryptographically/Mechanically Bound**: Worker telemetry (`F1 -> F2` depth digests, self-reported DoD) is observational metadata, never proof of correctness. Only deterministic exit codes, content hashes, and out-of-worktree holdout outcomes carry verification authority.
2. **Jev (`SystemOne`) is a Routing & Triage Sensor, Measured by Counterfactual Decision Delta**: `Jev` assists L1 triage and L3 anomaly flagging (`Sensor != Decider`), and its value is measured by counterfactual decision delta (how often its signal changes routing or catches defects missed by static heuristics), never as a replacement for mechanical verification.
3. **F0 Retains Raw Evidence Drill-Down on Conflict**: While F0 consumes `RoundReceiptSchema` by default (`ADR-0033` cognitive firewall), whenever mechanical checks, holdout tests, or blind verifiers conflict—or when a canary survives—F0 MUST drill down into raw diffs and mechanical logs rather than trusting summary fields.
4. **Memory Partitioning (`LLM Wiki` vs `AgentMemory`)**: Git-tracked `LLM Wiki` (`wiki.py`) + OpenSpec/ADRs serve as the normative architectural SSoT, whereas `AgentMemory` (`.g8s/` runtime state / lessons store) is strictly scoped operational memory that cannot override spec invariants.

---

## 4. Current State of `TuneFlow` Deep Vertical Dogfooding

- **Active Worktree Path**: `$HOME/.local/state/g8s-sess-tf-ux-qa/worktrees/wt-56415a1a`
- **Current HEAD Commit**: `1f9355e` (`feat(macos,stream,logs): 512KB chunked range streaming, local desktop auto-admin, and operational logger`)
- **Completed Dogfooding Rounds (Rounds 1–4)**:
  1. **Round 1 — Core Resilience & Security**: Elderly Queue Recovery (`public/app.js`), SSRF private-IP blocking & bounded LRU cache (`src/services/`), and `tuneflow doctor` CLI health diagnostics.
  2. **Round 2 — Full-Stack API/DOM & Security CI Gates**: Added `scripts/ci-sast-gate.js` and `scripts/ci-dast-gate.js` verifying zero-trust input handling, traversal rejection, and live HTTP security headers.
  3. **Round 3 — Single Focus Frame UX & Headless Chrome UX-QA Gate**: Redesigned UI into a Single Focus Frame for elderly usability and enforced DOM geometry/contrast invariants via Headless Chrome (`scripts/ci-ux-qa-gate.js`).
  4. **Round 4 — macOS Native `WKWebView`/`AVFoundation` Streaming & Operational Logger (`1f9355e`)**:
     - Fixed `WKWebView`/`AVFoundation` byte-range starvation by streaming `ffmpeg` MP3 transcodes in 512KB `206 Partial Content` chunks.
     - Enabled `TUNEFLOW_DESKTOP_LOCAL=1` loopback auto-admin for the native macOS desktop app wrapper.
     - Added structured operational logging (`src/utils/logger.js`) with rotation and diagnostic tracing.
- **Live Installed macOS App & Logs**:
  - App Bundle: `$HOME/Applications/TuneFlow.app`
  - Operational Log File: `$HOME/Library/Application Support/TuneFlow/logs/tuneflow.log`

---

## 5. Next Session Bootstrap Commands (Zero-Context Cold-Start for F0)

Copy-paste the following sequence at the start of the next F0 Supervisor session to orient, isolate state, and dispatch Wave 1 workers (`#648`, `#649`, `#651`):

```bash
# 1. Orient & verify repo health (Lesson #6: Rebuild First)
cd $HOME/Documents/github/g8s
~/.local/bin/wiki.py orient
make local
./bin/g8s doctor --json

# 2. Inspect the 5 pre-v0.19.0 blocker issues & handoff
gh issue list --limit 10
./bin/g8s read plans/handoffs/2026-10-22-v019-feature-map-blind-verify-handoff.md --lines 1:160

# 3. Initialize isolated F0 session state directory
export G8S_STATE_DIR="$HOME/.local/state/g8s-sess-v019-blind-verify"
mkdir -p "$G8S_STATE_DIR"

# 4. Run L1 pre-mutation triage before dispatching Wave 1 F1 workers (#648, #649, #651)
./bin/g8s reflex triage \
  --summary "Wave 1: Fix EvaluateDoDMatrix trust hole & wire verify-t3 (#648), add .g8s/feature-map.yml (#649), and package 6 verification skills (#651)" \
  --files "internal/verifier/verifier.go,cmd/g8s/verify.go,tools/f0/verifier_t3.go,.g8s/feature-map.yml,internal/featuremap/featuremap.go,skills/manifest.json"

# 5. Dispatch Wave 1 via F0 -> F1 (1 issue = 1 worker = 1 disjoint WriteReceipt)
# After Wave 1 merges, dispatch Wave 2 (#650 3-Layer Blind Verification Loop) and Wave 3 (#652 TuneFlow Phase 2).
```
