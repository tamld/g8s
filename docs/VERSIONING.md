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
v0.1.0-alpha (Current) ──► v0.1.0-beta (Parity Complete) ──► v0.1.0 (Public Launch) ──► v1.0.0 (Production Stable)
```

| Version Milestone | Target Scope | Stability Level |
| :--- | :--- | :--- |
| **`v0.1.0-alpha`** | Milestone 1 (Foundation): Core Harness, SQLite WAL ControlPlane, Write Receipt Engine. | Experimental (Private repo) |
| **`v0.1.0-beta`** | Milestone 2 (Capabilities): Stdio MCP server, Pluggable Providers (AGY, Claude, Gemini). | Staging & Testing |
| **`v0.1.0`** | Milestone 3 (OS Daemon & Packaging): Multi-OS Service (launchd/systemd/windows), GoReleaser. | Public Open-Source Launch |
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
