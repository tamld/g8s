# CI/CD World-Practice Audit (2025–2026) — F0 Synthesis & Gap Matrix

- **Date:** 2026-11-01
- **Audit Inputs:**
  - **Team A (Testing & Quality):** [`report-A-testing.md`](report-A-testing.md)
  - **Team B (Security, Packaging & Release):** [`report-B-security-release.md`](report-B-security-release.md)
  - **Team C (Live Repo Surface Inventory):** [`report-C-inventory.md`](report-C-inventory.md)
- **Baseline:** `g8s` @ `abdb733` / `c926f0e` (13 GitHub Actions workflows, dual-lane `docs`/`build` CI, SAST triad + gitleaks + govulncheck + native fuzzing + race + 80% coverage floor + ratchet + Windows/Crash-Survival/Golden-Loop E2E + GoReleaser SPDX SBOM + SHA-256 checksums).

---

## 1. Executive Verdict

1. **Release-Path Readiness (`v0.22.0`):** Zero release-blocking security or packaging regressions were discovered on the `v0.22.0` release path. All 5 high-priority items from Wave 1 of the 2026-10-31 audit (`#707` + `#709`: gitleaks full-history gate + allowlist triage, nightly `-race` + bounded fuzz parity, OWASP ASI08 telemetry-poisoning red-tests, OWASP ASI02/04 MCP rogue-tool red-tests, OWASP LLM04/06 model-substitution red-tests, and `testing/quick` property suites) are **LIVE and GREEN on `main`**.
2. **Where `g8s` Leads World Baseline:** Dual-lane path-scoped routing (`ADR-0031`), SAST triad (`gosec` + `Semgrep` + `CodeQL`), OWASP Agentic Top 10 in-tree red-test suites, crash-survival process-group E2E (`Setpgid` / Windows Job Objects), and declarative spec-to-test sync (`ci_spec_code_sync.sh` + `g8s feature verify`).
3. **Where Real Gaps Exist (No Unaudited Debt):** Cross-referencing external 2025–2026 practice (Team A + Team B) against actual repository wiring (Team C) exposes **11 concrete gaps**, partitioned into **Wave 1** (immediate post-`v0.22.0` cut, ~2–3 worker-days) and **Wave 2** (~1 week post-cut, medium/architectural items).

---

## 2. Unified Gap Matrix (World Practice vs. `g8s` Wiring)

| ID | Domain | World-Practice Standard (2025–2026) | Current `g8s` State (`report-C`) | Gap Classification | Target Wave |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **G-T1** | Testing / Merge Hygiene | Pre-queue flake detection (`-count=5` nightly re-run) + automatic quarantine lane (never blanket retry). | **Absent** — Live-proven during Reach S2 push when `TestLiveJevReflexIntegration` fast-fallback flaked `pre_push.sh`. | **P1 Gap** | **Wave 1** |
| **G-T2** | Coverage Hygiene | Hybrid gate: **diff-coverage** (`≥80%` on changed lines via `go-test-coverage`) + global ratchet floor. | **Partial** — Global 80% floor + `.g8s/coverage-baseline` ratchet exist; no diff-coverage check on PR hunks. | **P1 Gap** | **Wave 1** |
| **G-T3** | Test Runner & Sharding | `gotestsum` + JUnit XML output + deterministic package sharding for nightly/CI timing analytics. | **Absent** — Raw `go test` invocations across `ci.yml`, `quality.yml`, and `nightly.yml`. | **P1 Gap** | **Wave 1** |
| **G-S1** | Local↔CI Gate Parity | Local `pre_push.sh` mirrors fast blocking security/doc checks or explicitly documents CI-only steps; DAST/IAST suites wired as named CI steps. | **Partial** — `dast_test.go` / `iast_dast_test.go` run inside `go test ./...` without an isolated CI step; `pre_push.sh` omits `gitleaks` and `govulncheck`. | **P1 Gap** | **Wave 1** |
| **G-G1** | Governance / Drift | Mechanized docs-claims-vs-workflow-wiring inventory script (`tools/ci_surface_inventory.sh`) + closed reflection loop (`D-09 REFLEX-CYCLE`, `#714`). | **Absent** — Caught manually by Team C scout (e.g., `README.md` tree comment vs `quality.yml` consolidation). | **P1 Gap (`#714`)** | **Wave 1** |
| **G-S2** | Supply-Chain Attestation | SLSA Build L3 native GitHub Artifact Attestations (`actions/attest-build-provenance` v4, Sigstore keyless) + SBOM attestation. | **Absent** — `.goreleaser.yaml` ships SHA-256 checksums + SPDX SBOM (`syft`), but no `attest-build-provenance` or `cosign` signing. | **P2 Gap (`#699` item 7)** | **Wave 2** |
| **G-S3** | Workflow Hardening | OpenSSF Scorecard compliance: third-party GitHub Actions pinned by 40-char SHA, job-scoped `permissions: contents: read`, `SECURITY.md`. | **Partial** — Actions use version tags (`@v4`, `@v6`); top-level workflow permissions not uniformly locked down. | **P2 Gap** | **Wave 2** |
| **G-S4** | Secrets Verification | Split-lane secret scanning: `gitleaks` (fast PR/commit gate) + scheduled `TruffleHog` verified-credential scan. | **Partial** — `gitleaks` full-history wired in `quality.yml`; no live-verification scanner in `nightly.yml`. | **P2 Gap** | **Wave 2** |
| **G-S5** | Agentic / MCP CI Evals | MCP tool-manifest hash pinning (`mcp-scan` / in-tree equivalent) + versioned pass-rate-scored golden security evals (`#699` item 6). | **Partial** — Static OWASP red-tests (`ASI02/04/08`, `LLM04/06`) run in CI; live `g8s eval` cadence + versioned fixture pass-rate scoring pending. | **P2 Gap (`#699` item 6)** | **Wave 2** |
| **G-T4** | Deterministic Simulation (DST) | Seed-driven deterministic simulation harness for SQLite WAL queue/claim/CAS-lease/receipt state machine with injected faults. | **Partial** — `testing/quick` property tests cover pure functions (`scope`, `sanitize`, `envelope`); no stateful FSM simulation harness. | **P2 Gap** | **Wave 2** |
| **G-T5** | Mutation Testing | Scoped quarterly/nightly mutation testing on parsers and state machines (`#699` item 9). | **Blocked / Manual** — `zimmski/go-mutesting` panics on Go 1.26 (`D-08` manual quarterly study active); `avito-tech`/`buzyka` forks or `go-gremlins` require a 1-day Go 1.26 spike. | **P2 Spike (`#699` item 9)** | **Wave 2** |

---

## 3. Committed Post-`v0.22.0` Execution Waves (Synced to `#699` & `#714`)

### Wave 1 — Immediate Post-Cut (~2–3 Worker-Days, Target: `2026-11-02 .. 2026-11-04`)

1. **`D-09 REFLEX-CYCLE` (`#714`, Slices `D9-S1..S5`):**
   - `D9-S1`: Wire default worker completion into `RecordModelOutcome` (`model_quality_events.jsonl`) + gate-outcome history ledger.
   - `D9-S2`: `ProcessReflectionReceipt` Draft-2020 schema + Jev judge evidence-ref-or-reject validator.
   - `D9-S3`: Autopilot stage-2 work-packet submitter (`g8s submit` via receipt-scoped worker pipeline).
   - `D9-S4`: `process_prune_ratio` CI gate + 3-empty-cycle tombstone self-delete.
   - `D9-S5`: `tools/ci_surface_inventory.sh` (mechanized Team-C workflow/gate vs. docs claim verifier, release-gated).
2. **Flake Quarantine & Local/CI Parity (`G-T1`, `G-S1`):**
   - Isolate live-network canary tests (`TestLiveJevReflexIntegration`) behind explicit build/env tags or quarantine lane; add nightly `-count=5` flake detector.
   - Wire explicit named DAST/IAST verification step in `quality.yml` and fast `gitleaks` check in `tools/pre_push.sh`.
3. **Diff-Coverage & `gotestsum` (`G-T2`, `G-T3`):**
   - Add diff-coverage (`≥80%` on changed lines) alongside `.g8s/coverage-baseline` ratchet.
   - Wrap CI/Nightly test execution in `gotestsum` with JUnit summary artifacts.
4. **Carry-Over Debt Closures (`#699`):**
   - **AM1 (`busy_timeout`):** **CLOSED AS MEASURED NO-OP** (8 writers × 40 sends = `320/320` persisted, `0 SQLITE_BUSY`, `p50=25ms`, `p95=35ms`, `max=42ms`; `agy` co-signed).
   - **AM2:** Mesh ledger inbox retention/rotation for ACKed messages.
   - **`#698` Re-apply:** Re-apply `parseAGYResult` allocation optimization onto `internal/worker/worker_result.go`.
   - **Audit Item 6:** Hybrid prompt-injection eval cadence (CI deterministic + pre-release live `g8s eval`).

### Wave 2 — Hardening & Supply-Chain Wave (~1 Week Post-Cut, Target: `2026-11-05 .. 2026-11-10`)

1. **SLSA Build L3 Provenance & Workflow Pinning (`G-S2`, `G-S3`, `#699` item 7):**
   - Wire `actions/attest-build-provenance` v4 + SBOM attestation in `.github/workflows/release.yml` (advances `v0.40.0` attestation milestone early at near-zero cost).
   - Pin third-party Actions to full commit SHAs and enforce job-level `permissions: contents: read`.
2. **Seeded DST Harness & Mutation Spike (`G-T4`, `G-T5`, `#699` item 9):**
   - Build seeded deterministic simulation test (`internal/controlplane/dst_sim_test.go`) exercising concurrent claim/lease/receipt/WAL transitions under simulated clock jumps and crashes.
   - Run 1-day Go 1.26 compatibility spike on `go-gremlins` and `buzyka/go-mutesting`.
3. **RT-MESH v1.1 Amendments (`A1–A4`) & Fence-at-Mutation ADR (`#699`):**
   - `A1` cumulative ACK, `A2` flow-window enforcement, `A3` per-peer retry budget, `A4` ping/pong keepalive, and `ADR-0038` (`context_revision` fence-at-mutation enforcement point) — **co-signed by `zcode` + `agy`**.
4. **GoReleaser Distribution Expansion (`Team B` P2):**
   - Evaluate MCP Registry publisher + lifecycle-script-free npm wrapper in `.goreleaser.yaml`.
