# Versioning & Release Strategy

## 1. Semantic Versioning 2.0.0 Standard

`g8s` strictly adheres to [Semantic Versioning 2.0.0](https://semver.org/):

$$\mathbf{v\text{MAJOR}.\text{MINOR}.\text{PATCH}[-\text{PRERELEASE}]}$$

* **MAJOR (`v1.0.0`)**: Incompatible API or CLI breaking changes (e.g. modifying the JSON-RPC MCP schema, altering receipt cryptographic signatures, or breaking SQLite schema compatibility without migrations).
* **MINOR (`v0.2.0`)**: Backwards-compatible new features (e.g. adding a new worker provider like Ollama/Claude, adding new CLI subcommands, adding new MCP tools).
* **PATCH (`v0.1.1`)**: Backwards-compatible bug fixes, security hardening, or performance optimizations.
* **PRERELEASE (`v0.1.0-alpha.1`, `v0.1.0-beta.1`, `v0.1.0-rc.1`)**: Pre-release builds for internal testing and parity validation before stable tags.

---

## 2. Release Cadence & Phases

```
v0.14.0 (2026-10-03) ──► v0.15.0 (2026-10-05) ──► v0.16.0 (2026-10-07) ──► v0.17.0 (2026-10-16) ──► v0.18.0 (2026-10-21, Current) ──► v1.0.0 (Production Stable)
```

| Version Milestone | Target Scope | Stability Level |
| :--- | :--- | :--- |
| **`v0.1.0-alpha`** | Milestone 1 (Foundation): Core Harness, SQLite WAL ControlPlane, Write Receipt Engine. | Experimental (Private repo) |
| **`v0.1.0-beta`** | Milestone 2 (Capabilities): Stdio MCP server, Pluggable Providers (AGY, Claude, Gemini). | Staging & Testing |
| **`v0.1.0`** | Milestone 3 (OS Daemon & Packaging): Multi-OS Service (launchd/systemd/windows), GoReleaser. | Public Open-Source Launch |
| **`v0.14.0`** | Feature wave release (2026-10-03). | Delivered |
| **`v0.15.0`** | Integrity hardening release (2026-10-05). | Delivered |
| **`v0.16.0`** | Self-measuring effort telemetry release (2026-10-07): effort manifest + adapter + `submit --effort`, class registry + signal demotion, token telemetry, quality ladder + gauges, baked-name realignment (#568), ladder hardening (#570). | Delivered |
| **`v0.17.0`** | Native Cross-Platform Agent I/O & Cross-Repo Offer Rollout release (2026-10-16): `DELTA-23` pure-Go `search`/`read`/`edit`/`--select` (#591, #593), F0/F1/F2 recursive containment (#587), Offer Bundle Wave 1–4 rollout & cross-repo delivery (#442, #569), DLP & SQLite DSN hardening (#574, #586, #590), OpenSpec `DELTA-01..23` & 50/50 bound claims. | Delivered |
| **`v0.18.0`** | F0 Verified Delegation & Symmetric Zero-Trust Control Plane ([ADR-0033](decisions/0033-f0-verified-delegation.md), Epic #620, #621–#630, 2026-10-21): append-only hash-chained evidence ledger (`f0 run`, `f0 status`), signed operator mandates (`T0`–`T4`), merge/commit gates `G0`–`G7` (`f0 gate check`), agent hooks (`f0 hook pre-tool|stop`), portable ground-truth bundles, gate-appeasement detection (G4), issue-closure gate (G7), error-budget circuit breaker, and lossless verify firewall. | Delivered (Current) |
| **`v1.0.0`** | 100% Test Parity, Formal Security Audit, Stable MCP & CLI Interface guarantee. | Production GA |

---

## 3. Git Branching Strategy

* **`main`**: The canonical production-ready branch. Every commit must pass all CI tests on macOS, Linux, and Windows.
* **`feat/<feature-name>`**: Feature branches branched from `main`. Merged via PR only after DoD signoff.
* **`fix/<bug-name>`**: Bugfix branches for resolving specific issues.
* **`release/vX.Y.Z`**: Release preparation branch for tagging, changelog generation, and pre-release smoke testing.

---

## 4. Git Tagging & Changelog Convention

* Tags must follow the format: `vX.Y.Z` (e.g. `v0.1.0`).
* Commit messages must follow **Conventional Commits**:
  * `feat(scope): ...` $\rightarrow$ Minor bump
  * `fix(scope): ...` $\rightarrow$ Patch bump
  * `docs(scope): ...` $\rightarrow$ Documentation only
  * `test(scope): ...` $\rightarrow$ Test suite updates
  * `refactor(scope): ...` $\rightarrow$ Code refactoring without behavior changes
  * `feat(scope)!: ...` or `BREAKING CHANGE:` $\rightarrow$ Major bump
