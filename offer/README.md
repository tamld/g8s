# g8s Offer Bundle — for sibling projects

Pull-based distribution (ADR-0024 distribution model): each project's main
agent decides when to adopt. Nothing is pushed. Findings flow back as
sanitized contribution packets (g8s-supervisor Mode 3).

## What a project gets

1. **A profile** (`profiles/<type>.md`) — trust-boundary seed, lane-bundle
   recommendation, audit dimensions, and cadence tuned to the project's
   nature.
2. **Three generic gate scripts** — copy from `g8s/tools/`:
   `ci_spec_code_sync.sh` (+test), `ci_structure_sync.sh` (+test),
   `ci_link_integrity.sh` (+test). They are project-agnostic: point them at
   a repo root and they enforce spec↔code sync, README structure truth, and
   link integrity.
3. **The supervisor charter** — [`skills/g8s-supervisor`](../skills/g8s-supervisor/SKILL.md)
   v4.3.0: the operating practice for dispatching work through g8s
   (admission-gated, fan-out, evidence-verified, leak-free reporting).
4. **Cross-platform search standard** — [`SEARCH_GUIDE.md`](SEARCH_GUIDE.md):
   why `ripgrep` (`rg`) replaces raw `grep -rn` / `find + xargs` across
   macOS (BSD), Linux (GNU), and Windows, plus the dual-engine (`rg`-first
   with POSIX `grep` fallback) pattern used in portable CI gates.
5. **Native Cross-Platform Agent I/O & MCP Suite (`DELTA-23`)**: `g8s search`, `g8s read`, `g8s edit`, `g8s select` (CLI & MCP `g8s_search`, `g8s_read`, `g8s_edit`, `g8s_select`) with in-process `WriteReceipt` enforcement.
6. **Config-Driven Multi-Protocol LLM Consumer (`DELTA-25`)**: `g8s llm-call` and `g8s_llm_call` MCP tool allowing sibling repos to consume any `providers.json` HTTP gateway (`openai`, `anthropic`, `9router`).
7. **Declarative Feature Map (`.g8s/feature-map.yml`)**: Single Source of Truth linking user stories, SRS requirements, OpenSpec deltas, code paths, invariants, and test suites with automated blast-radius routing and orphan detection.
8. **3-Layer Blind Verification Loop**: Adversarial multi-layer gate defeating reviewer sycophancy, AI evaluation awareness, and test overfitting:
   - *Layer 1 (Holdout Suite)*: Hidden out-of-worktree tests (`$G8S_STATE_DIR/holdout/<feature-id>`) mounted only during verification (`--holdout-dir`).
   - *Layer 2 (Double-Blind Packet)*: Sanitized `BlindReviewPacket` stripping author identity, commit messages, and worker prose to force code-first falsification.
   - *Layer 3 (Canary Mutant)*: Seeded mutant injection (`--canary-mutant`) proving reproduction and test non-vacuity before merge.
9. **6-Module Verification Skills Suite**: Loadable verification engines registered in [`skills/manifest.json`](../skills/manifest.json):
   - [`g8s-blind-verifier`](../skills/g8s-blind-verifier/SKILL.md): Double-blind counterexample and falsification review.
   - [`g8s-red-prove`](../skills/g8s-red-prove/SKILL.md): Mechanical `Red -> Green` reproduction + mutation proof.
   - [`g8s-scenario-matrix`](../skills/g8s-scenario-matrix/SKILL.md): 12-dimension edge matrix generator & holdout suites.
   - [`g8s-ux-visual-gate`](../skills/g8s-ux-visual-gate/SKILL.md): Headless DOM bounding-box geometry, WCAG contrast & media verifier.
   - [`g8s-sec-gate`](../skills/g8s-sec-gate/SKILL.md): Zero-trust SAST + live DAST probe runner for SSRF and trust boundaries.
   - [`g8s-epistemic-audit`](../skills/g8s-epistemic-audit/SKILL.md): Claim decomposition & evidence-tier classifier enforcing the 4 Epistemic Laws.
   - Master reference: [`skills/g8s-supervisor/references/verification-skills-suite.md`](../skills/g8s-supervisor/references/verification-skills-suite.md).

## Onboard in four steps

See [`ONBOARDING.md`](ONBOARDING.md). Short version: pull → scaffold `.g8s/`
from your profile → wire the three gates into CI → run your first brief.

## Contribute back

Running the bundle produces operational evidence: gate catches, PRI scores,
audit findings, escalation patterns. When a finding is a **g8s defect or a
reusable improvement**, ship it back per the charter's Mode 3 (sanitized
contribution packet → PR). That is the learning loop: projects pull tactics,
g8s crystallizes their patterns. See
[`../docs/designs/gate-lane-routing.md`](../docs/designs/gate-lane-routing.md)
for the architecture and its falsification clauses.
