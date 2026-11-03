# g8s Product Inventory: Everything a Visitor Needs to Install, Use, and Evaluate

> **Document ID**: `docs/research/29-g8s-product-inventory.md`  
> **Role**: Analyst (Repo-Grounded Inventory & Site Surface Architecture)  
> **Scope**: Comprehensive product inventory for the upcoming g8s product landing site  
> **Repository Baseline**: Verified against `v0.22.0`, Go 1.26.10, Zero-CGO pure-Go harness  

---

## 1. IDENTITY

### One-Sentence Product Definition
**`g8s` (The Gatekeepers) is a single-binary, zero-trust process execution and capability harness (<25MB static pure-Go binary, Zero-CGO) that protects developer workstations and CI runners by isolating autonomous AI agent CLI workers into private Git worktrees, bounding filesystem mutations with single-use write receipts, and capturing verifiable execution evidence.**

*Market Analogy*: *"k8s orchestrates your compute containers; g8s orchestrates your AI subagents."* ([README.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/README.md#L8))

### Target Audience & The Market Problem
- **Target Audience**: AI software engineers, platform architects, and engineering teams deploying autonomous coding agent loops (`claude`, `codex`, `agy`, `gemini`, local LLMs) on local workstations, shared homelab servers, or CI/CD pipelines.
- **The Market Problem Solved**: Today's coding agents execute with ambient shell authority (`bash`/`exec`), directly mutating code on developer machines. Without containment, teams face catastrophic risks: accidental file deletions, out-of-scope edits outside target modules, orphan zombie background processes, infinite token/spend loops, environment credential leaks (`G8S_API_KEY`, private tokens, LAN IPs), and zero verifiable audit trails.
- **The Zero-Trust Architectural Solution**:
  - **Symmetric Governance**: Decouples the high-reasoning strategic orchestrator ("Brain" / `F0`) from bounded execution workers ("Muscle" / `F1`).
  - **Zero Ambient Authority**: Workers cannot touch the filesystem without an active, path-scoped, time-limited capability receipt (`AllowedPaths`).
  - **Fail-Closed Sandboxing**: Execution is constrained to isolated Git worktrees and OS process groups (`Setpgid`), terminating cleanly on cancel or timeout.
  - **Information Firewall**: Filters megabytes of noisy test logs into compact, verifiable receipts (`RoundReceiptSchema`), protecting orchestrator context quotas.

---

## 2. INSTALL PATHS (Exact Commands & Source Citations)

Every install command below is verified against and quoted verbatim from source files in this repository:

### 1. Standalone Shell Script (macOS & Linux)
- **Target**: macOS (Apple Silicon `arm64` & Intel `amd64`) and Linux (`amd64` / `arm64`).
- **Command**:
  ```bash
  curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
  ```
- **Source Citation**: [scripts/install.sh](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/scripts/install.sh#L6)
- **Mechanism**: Auto-detects OS and architecture, downloads release archive from GitHub Releases, enforces fail-closed SHA-256 verification against `checksums.txt`, unpacks to `${G8S_INSTALL_DIR:-$HOME/.local/bin}` (or `/usr/local/bin` if root), and runs `g8s version`.
- **Environment Overrides**: `G8S_VERSION=v0.22.0`, `G8S_INSTALL_DIR=/custom/bin`.

### 2. Standalone PowerShell Script (Windows)
- **Target**: Windows 10/11 / Windows Server 2019/2022 (`amd64` / `arm64`).
- **Command**:
  ```powershell
  irm https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.ps1 | iex
  ```
- **Source Citation**: [scripts/install.ps1](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/scripts/install.ps1#L6)
- **Mechanism**: Resolves latest release via GitHub API, downloads `g8s_<ver>_windows_<arch>.zip`, verifies SHA-256 against `checksums.txt` fail-closed, extracts `g8s.exe` to `$LOCALAPPDATA\Programs\g8s\bin`, and registers the directory in User `PATH`.

### 3. Homebrew Tap (macOS & Linux)
- **Target**: macOS and Linux via Homebrew.
- **Commands**:
  ```bash
  brew tap tamld/homebrew-tap
  brew install g8s
  ```
- **Source Citation**: [docs/quickstart.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/quickstart.md#L15-L16), [.goreleaser.yaml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.goreleaser.yaml#L131-L142)
- **Release Status**: Declaratively registered in `.goreleaser.yaml:131-142` (`tamld/homebrew-tap`); release SOP tracked in `docs/RELEASE_SOP.md:60` (`tamld/homebrew-tap/g8s.rb`).

### 4. Go Toolchain (`go install`)
- **Target**: Any system with Go 1.26.10+ installed.
- **Command**:
  ```bash
  go install github.com/tamld/g8s/cmd/g8s@latest
  ```
- **Source Citation**: [README.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/README.md#L101), [docs/quickstart.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/quickstart.md#L38)

### 5. Build from Source (`make local`)
- **Target**: Developer environments with `git` and `go`.
- **Commands**:
  ```bash
  git clone https://github.com/tamld/g8s.git
  cd g8s
  make local
  ./bin/g8s doctor --json
  ```
- **Source Citation**: [README.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/README.md#L107-L110), [Makefile](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/Makefile#L7-L8)
- **Mechanism**: Builds pure-Go static binary with Zero-CGO, embedding immutable git commit SHA, build timestamp, and `BuildSource=local-dev` ldflags into `./bin/g8s`.

### 6. Direct GitHub Releases Pre-Compiled Archives
- **Target**: Offline or hermetic deployment environments.
- **URL**: `https://github.com/tamld/g8s/releases`
- **Source Citation**: [.goreleaser.yaml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.goreleaser.yaml#L89-L99), [docs/quickstart.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/quickstart.md#L21-L33)
- **Published Artifacts**:
  - `g8s_<ver>_darwin_all.tar.gz` (macOS Apple Silicon & Intel Universal binary)
  - `g8s_<ver>_linux_amd64.tar.gz` (Linux x86_64)
  - `g8s_<ver>_linux_arm64.tar.gz` (Linux ARM64 / aarch64)
  - `g8s_<ver>_windows_amd64.zip` (Windows x64)
- **Integrity Verification**: `sha256sum -c checksums.txt` ([.goreleaser.yaml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.goreleaser.yaml#L99))

### 7. Linux System Package Managers
- **Target**: Debian/Ubuntu (`.deb`), RHEL/Fedora/CentOS (`.rpm`), Alpine Linux (`.apk`).
- **Commands**:
  ```bash
  sudo dpkg -i g8s_*.deb        # Debian / Ubuntu
  sudo rpm -i g8s_*.rpm         # RedHat / Fedora
  apk add --allow-untrusted g8s_*.apk  # Alpine
  ```
- **Source Citation**: [.goreleaser.yaml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.goreleaser.yaml#L118-L130) (NFPM package generator)

### 8. Windows Package Managers (Winget & Chocolatey)
- **Winget**:
  ```powershell
  winget install TamLD.g8s
  ```
  *Source Citation*: [packaging/winget/manifest.yaml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/packaging/winget/manifest.yaml#L1)
- **Chocolatey**:
  ```powershell
  choco install g8s
  ```
  *Source Citation*: [packaging/chocolatey/g8s.nuspec](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/packaging/chocolatey/g8s.nuspec#L4)

### 9. In-Binary Self-Update
- **Command**:
  ```bash
  g8s update
  g8s update --check
  ```
- **Source Citation**: [cmd/g8s/lifecycle.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/lifecycle.go), [spec/openspec/40-lifecycle-uninstall-update-spec.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/spec/openspec/40-lifecycle-uninstall-update-spec.md#L20-L25)
- **Mechanism**: Queries GitHub Releases API, checks current version against latest tag, downloads archive, verifies SHA-256 fail-closed, and performs atomic binary swap on disk.

### 10. Container Images Status (`ghcr.io/tamld/g8s`)
- **Status**: Documented gap / roadmap item ([plans/261101-cicd-world-practice-audit/report-B-security-release.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/plans/261101-cicd-world-practice-audit/report-B-security-release.md#L68) F14). No active OCI container images are currently built or pushed to GHCR.

---

## 3. 60-SECOND QUICKSTART (Copy-Paste Block)

The shortest real path from install to verified execution using standard CLI flags as they exist today. Safe for any repository (uses `read_only` permission and requires zero API keys beyond local environment defaults).

```bash
# 1. Health check: verify SQLite WAL, git worktree engine, and local worker CLIs (<1s)
g8s doctor

# 2. Inspect registered zero-trust role contracts and permission boundaries
g8s roles
g8s permissions

# 3. Enqueue a safe read-only reconnaissance task into the durable SQLite WAL queue
g8s submit \
  --idempotency-key "quickstart-01" \
  --role collector \
  --permission read_only \
  --prompt "Scan repository documentation and list top-level markdown files"

# 4. Confirm the task is safely queued with CAS lease tracking
g8s tasks

# 5. Process the queued task with a single bounded worker attempt
g8s worker --once
```

### What the Visitor Sees (Actual CLI Output)

`g8s submit` produces a structured, machine-verifiable JSON task envelope:
```json
{
  "v": 1,
  "kind": "task",
  "cmd": "submit",
  "data": {
    "task_id": "015ea318-b977-4aed-85cf-4b0cb9c4cce1",
    "idempotency_key": "quickstart-01",
    "schema_version": "agy.task.v1",
    "state": "QUEUED",
    "priority": 0,
    "request": {
      "actor": "operator",
      "effort": "medium",
      "permission": "read_only",
      "prompt": "Scan repository documentation and list top-level markdown files",
      "role": "collector",
      "timeout": "90s"
    },
    "attempts": 0,
    "max_attempts": 1
  }
}
```

`g8s tasks` displays all active and queued items:
```json
{
  "v": 1,
  "kind": "tasks",
  "cmd": "tasks",
  "data": [
    {
      "task_id": "015ea318-b977-4aed-85cf-4b0cb9c4cce1",
      "state": "QUEUED",
      "role": "collector",
      "permission": "read_only"
    }
  ]
}
```

When `g8s worker --once` runs, the harness claims the task atomically via SQLite WAL CAS lease, spawns an isolated private Git worktree, runs the prompt inside the OS sandbox, outputs `queue drained`, and logs the outcome to the durable evidence lake.

---

## 4. FEATURE TABLE (Top Capabilities Grounded in Repository Proof)

| Feature Name | Concrete Benefit for Users & Teams | Proof Artifact in Repo |
| :--- | :--- | :--- |
| **Zero-Trust Containment Tiers (`F0/F1/F2`)** | Enforces physical sandboxing: Brain (`F0`) holds exclusive commit and architecture authority; Worker (`F1`) executes in private worktrees; Subagent (`F2`) is strictly bounded by F1's receipt boundary. | [docs/decisions/0032-containment-levels.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/decisions/0032-containment-levels.md), [internal/charter/charter_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/charter/charter_test.go), [spec/constitution.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/spec/constitution.md#L11-L14) |
| **Single-Use Write Receipts** | Eliminates ambient shell write access; workers can mutate files only when presented with a cryptographically tracked, time-limited ($\le 3600s$), path-scoped (`AllowedPaths`) token consumed atomically. | [internal/receipt/receipt.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/receipt/receipt.go), [internal/receipt/receipt_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/receipt/receipt_test.go), [schemas/receipt-v1.schema.json](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/schemas/receipt-v1.schema.json) |
| **Risk-Tiered ALDC Gate Lanes (`0 / D / R / F / S`)** | Routes tasks to appropriate verification lanes—from fast documentation updates (Lane 0) to security-critical code changes (Lane S) requiring dual-blind verification and grey-box fuzzing. | [.g8s/lane-bundles.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.g8s/lane-bundles.yml), [internal/lane/lane_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/lane/lane_test.go), [docs/decisions/0024-gate-lane-routing.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/decisions/0024-gate-lane-routing.md), Issue [#514](https://github.com/tamld/g8s/issues/514) |
| **Cross-Repository Fleet Hygiene** | Prevents dirty drift across multi-repo workspaces with `g8s fleet status`, verifying uncommitted changes, unpushed branches, and detached HEADs across sister repositories before allowing execution. | [cmd/g8s/fleet.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/fleet.go), [cmd/g8s/fleet_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/fleet_test.go), [spec/openspec/32-fleet-hygiene-spec.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/spec/openspec/32-fleet-hygiene-spec.md) (`DELTA-32`) |
| **Multi-Coordinator Interop Mesh (`RT-MESH v1`)** | Connects equal-peer F0 coordinators (Claude, Codex, Antigravity) through a shared SQLite WAL ledger with CAS claims, revision fences, and typed idempotent messages (`UNIQUE message_id`). | [spec/openspec/46-peer-coordinator-ledger-spec.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/spec/openspec/46-peer-coordinator-ledger-spec.md), [cmd/g8s/coordinator_mesh.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/coordinator_mesh.go), PRs [#700](https://github.com/tamld/g8s/pull/700), [#701](https://github.com/tamld/g8s/pull/701), [#702](https://github.com/tamld/g8s/pull/702), Issue [#699](https://github.com/tamld/g8s/issues/699) |
| **Platform Reach Layer & Fallback Chains** | Acquires live research and web docs through ordered fallback chains (`g8s reach read`) with persistent health tracking, path ownership ledgers, and quarantine circuit-breakers. | [docs/decisions/0037-reach-layer.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/decisions/0037-reach-layer.md), [cmd/g8s/reach.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/reach.go), [cmd/g8s/reach_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/reach_test.go), [internal/reach/chain_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/reach/chain_test.go), Issue [#704](https://github.com/tamld/g8s/issues/704) |
| **System-1 Reflex Triage (`Jev`)** | Non-autoregressive fast triage enforcing "Sensor ≠ Decider" before file mutation, validating context packets and immediately halting unauthorized sandbox escape attempts. | [cmd/g8s/reflex.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/reflex.go), [internal/reflex/jev.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/reflex/jev.go), [internal/reflex/jev_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/reflex/jev_test.go), [internal/reflex/triage_v2_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/reflex/triage_v2_test.go) |
| **Universal LLM Consumer & `9router` Gateway** | Direct synchronous CLI execution (`g8s llm-call`) and worker queuing across OpenAI, Anthropic, and 9router with adaptive effort timeouts (`45s`/`90s`/`240s`) and live model scorecard. | [docs/decisions/0034-config-driven-llm-consumer-9router.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/decisions/0034-config-driven-llm-consumer-9router.md), [cmd/g8s/llm_call.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/llm_call.go), [cmd/g8s/llm_call_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/llm_call_test.go), [.g8s/agent-models.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.g8s/agent-models.yml) (`DELTA-25`) |
| **Zero-CGO Cross-Platform Parity** | 100% pure Go compilation with `CGO_ENABLED=0` and modernc SQLite; guarantees byte-for-byte behavior and process tree kill safety across macOS, Linux, and Windows. | [.github/workflows/windows-e2e.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.github/workflows/windows-e2e.yml), [cmd/g8s/autopilot_probe_windows.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/autopilot_probe_windows.go), [cmd/g8s/autopilot_probe_unix.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/autopilot_probe_unix.go), [Makefile](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/Makefile#L32-L35) |
| **Supply Chain Attestation & SPDX SBOM** | Hardens distribution with signed releases, automated SPDX 2.3 JSON SBOM generation via Syft, and fail-closed SHA-256 checks mapped directly to OWASP Agentic Top 10 (2025). | [.goreleaser.yaml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.goreleaser.yaml#L69-L74), [docs/security/owasp-agentic-slsa-sbom.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/security/owasp-agentic-slsa-sbom.md), [scripts/install.sh](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/scripts/install.sh#L104-L121), Issue [#639](https://github.com/tamld/g8s/issues/639) |
| **6-Stage AI QA Pipeline & Seeded Mutant Canary** | Replaces unverified model claims with mechanical proofs: holdout suite isolation (`E_HOLDOUT_CONTAMINATION`), seeded mutant canary runs (`--canary-mutant`), and epistemic claim auditing. | [internal/verifier/qapipeline.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/verifier/qapipeline.go), [internal/verifier/qapipeline_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/verifier/qapipeline_test.go), [cmd/g8s/qa_report.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/qa_report.go), [cmd/g8s/verify.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/verify.go) |
| **Native Pure-Go Agent I/O & ContextPack** | Eliminates shell dependencies (`rg`, `sed`, `awk`, `jq`) with pure-Go regex search, line-slicing reader, atomic CAS editor, dot-path selector, and 4-tier AST context compressor. | [cmd/g8s/search.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/search.go), [cmd/g8s/fsops.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/fsops.go), [cmd/g8s/context_pack.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/cmd/g8s/context_pack.go), [internal/contextpack/contextpack_test.go](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/internal/contextpack/contextpack_test.go), [offer/SEARCH_GUIDE.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/offer/SEARCH_GUIDE.md) |

---

## 5. DOCS SURFACE + GAPS

### What Exists Today
1. **Core Governance & Protocol**: [AGENTS.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/AGENTS.md), [docs/AGENTS_FULL.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/AGENTS_FULL.md), [spec/constitution.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/spec/constitution.md) (6 Invariant Laws).
2. **Architecture & Decisions**: [docs/ARCHITECTURE.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/ARCHITECTURE.md) and 37 formal ADRs in `docs/decisions/` (`0001` through `0037`).
3. **Product & Engineering Specs**: [docs/PRD.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/PRD.md), [docs/SRS.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/SRS.md), [docs/DOD_DOR.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/DOD_DOR.md), and OpenSpec Deltas (`DELTA-01` through `DELTA-47`).
4. **User Guides**: [docs/quickstart.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/quickstart.md), [docs/user-guide/cli-reference.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/user-guide/cli-reference.md), [docs/user-guide/mcp-tools.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/user-guide/mcp-tools.md), [docs/user-guide/receipt-workflow.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/user-guide/receipt-workflow.md), [docs/user-guide/providers.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/user-guide/providers.md).
5. **Security & Assurance**: [docs/security/owasp-agentic-slsa-sbom.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/security/owasp-agentic-slsa-sbom.md), [docs/security/threat-model-stride.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/security/threat-model-stride.md).
6. **IDE Integrations**: Dedicated configs for [Claude Desktop](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/integrations/claude-desktop.md), [Cursor](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/integrations/cursor.md), [Windsurf](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/integrations/windsurf.md), and [Antigravity](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/docs/integrations/antigravity.md).
7. **Offer Bundle**: Pull-based adoption for sister repos ([offer/README.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/offer/README.md), [offer/SEARCH_GUIDE.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/offer/SEARCH_GUIDE.md)).

### Honest Gap List (What Is Missing for a Commercial Product Site)
1. **Interactive Installation Selector**: Visitors need a tabbed GUI selector that instantly reveals the 1-click command for their exact OS/tool combination (macOS/Homebrew, Linux/Apt, Windows/PowerShell, or Go).
2. **Visual "How It Works" Narrative**: The repository explains `F0/F1/F2` and `S0..S3` in dense text tables; the product site requires interactive, visual comparison cards contrasting ambient shell vulnerability vs zero-trust write receipts.
3. **Simulated Terminal / asciinema Demo**: No live interactive playground or video demonstration of a task enqueueing, claiming, worktree sandboxing, and outputting evidence.
4. **Auto-Generated CLI Reference Explorer**: `docs/user-guide/cli-reference.md` is manually maintained markdown; it lags behind the 45+ actual CLI subcommands (`reach`, `coordinator`, `scorecard`, `f0` are omitted in some guides).
5. **Interactive 1-Click MCP Config Generator**: Needs a dropdown where a user selects Claude Desktop, Cursor, or VS Code and copies a pre-populated JSON snippet with automatically resolved binary paths.
6. **FAQ & Objection Handling**: No public-facing answers to natural enterprise questions ("Why not standard Docker?", "How does g8s compare to LangChain/CrewAI?", "What is SQLite WAL latency?").
7. **Missing Container Image (`ghcr.io/tamld/g8s`)**: Documented as an audit finding ([plans/261101-cicd-world-practice-audit/report-B-security-release.md](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/plans/261101-cicd-world-practice-audit/report-B-security-release.md#L68)) but not yet built in CI.

---

## 6. PROOF (Verifiable Evidence Grounded in Repo)

- **Continuous Integration Gates**:
  - [.github/workflows/ci.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.github/workflows/ci.yml): Zero-CGO verification on Ubuntu & macOS (`go vet`, `go test -count=1`, `go test -race`).
  - [.github/workflows/windows-e2e.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.github/workflows/windows-e2e.yml): Native Windows Server 2022 runner running PowerShell e2e, process group kill tests, and path resolution.
  - [.github/workflows/quality.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.github/workflows/quality.yml): Complete SAST triad (`golangci-lint`, `gosec`, `semgrep`, `CodeQL`) and quantitative coverage gate.
  - [.github/workflows/dogfood.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.github/workflows/dogfood.yml): Weekly automated self-audit running `make dogfood` roundtrip with automated GitHub issue creation on failure.
  - [.github/workflows/release.yml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.github/workflows/release.yml): Tag-triggered GoReleaser pipeline generating cross-platform archives, SHA-256 checksums, and SPDX SBOMs.
- **Strict Code Coverage Baseline & Ratchet**:
  - Tracked in [.g8s/coverage-baseline](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.g8s/coverage-baseline): **`82.50%`**.
  - CI Quality Gate enforces aggregate coverage $\ge 80\%$ across all internal packages.
  - Ratchet gate ([tools/ci_coverage_ratchet.sh](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/tools/ci_coverage_ratchet.sh), ADR-0024 Gate G4) fails if coverage drops $>0.25\%$ below baseline.
- **Supply Chain Security & SPDX Provenance**:
  - Every release builds with immutable ldflags stamping `Version`, `Commit`, and `BuildTime`.
  - Full SPDX 2.3 JSON SBOM generated per archive via Syft ([.goreleaser.yaml](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/.goreleaser.yaml#L69-L74)).
  - Standalone installers enforce fail-closed checksum checks ([scripts/install.sh](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/scripts/install.sh#L104-L121), [scripts/install.ps1](file:///Users/tamld/.local/state/g8s-fxv5/worktrees/wt-772f98ad/scripts/install.ps1#L50-L62)).
- **The Living Dogfood Story ("g8s builds g8s")**:
  - `g8s` is developed entirely using `g8s` workers: all features, bugfixes, and refactors are submitted via `g8s submit`, executed inside private worktrees under write receipts, and verified with `g8s verify`.
  - Historical commit evidence:
    - Commit `df2b7fc`: Coordinator interop ledger + RT-MESH v1 (`#700`, `#701`, `#702`).
    - Commit `a455449`: External CLI agent adapters & dogfooding fixes (`#672`).
    - Commit `6518b58`: Complete 4-pillar AppSec battery (`#598`, `#600`, `#601`, `#602`, `#603`).
    - Commit `5406740`: Critical doctor checks, evidence retention & `make local` workflow (`#476`, `#483`).
  - Active runtime proof: This very inventory report is being executed by an F1 subagent isolated inside g8s worktree `wt-772f98ad` bound to receipt `a4b3abaa-786f-4dcc-8dfa-a16be1795548`.

---

## 7. SITE CONTENT MAP (Inventory Item → Product Site Section)

| Inventory Item | Target Site Section | Key Component / Visual Element | User Goal Addressed |
| :--- | :--- | :--- | :--- |
| **I1: Identity & Value** | **Hero & Subhead** | Headline, value proposition, badges (License, Platforms, Go, Coverage 82.5%), primary CTA ("Quickstart 60s"), secondary CTA ("Star on GitHub"). | Instantly understand what g8s is, who it is for, and why ambient shell authority is dangerous. |
| **I1: Problem Statement** | **The Danger of Ambient Shell** | Visual 2-column interactive before/after comparison card: Ambient Shell Risks vs. Zero-Trust g8s Harness. | Convince developer leads of the security and reliability risks of ungoverned agent execution. |
| **I2: Install Paths** | **Install Matrix** | Interactive tabbed selector (macOS Brew/Curl, Linux Deb/Rpm/Curl, Windows PowerShell/Winget, Go Install, Build from Source). | Install g8s in seconds on any operating system without hunting for terminal commands. |
| **I3: Quickstart** | **60-Second Quickstart** | Step-by-step interactive code walk with copy buttons, collapsible JSON outputs, and runnable read-only sample task. | Experience first successful task submission and safe sandboxed execution within 60 seconds. |
| **I4: Feature Surface** | **Core Capabilities Grid** | 6-card interactive grid (Zero-Trust Tiers, Write Receipts, ALDC Gate Lanes, Multi-Coordinator Mesh, System-1 Reflex, Native Agent I/O). | Explore deep architectural capabilities with links to code and ADRs. |
| **I4: Architecture** | **How It Works / 5-Step Lifecycle** | Interactive SVG diagram showing the 5 stages: Submit & Gate → CAS Lease → Contain & Hydrate → Verify & Canary → Deliver. | Understand the exact execution flow from high-tier Brain (F0) to isolated Worker (F1). |
| **I5: Integrations** | **IDE & MCP Integration Hub** | Dropdown selector (Claude Desktop, Cursor, Windsurf, VS Code) generating copyable `g8s mcp` configuration JSON blocks. | Seamlessly plug g8s into any existing AI IDE or coding tool in under a minute. |
| **I5: Documentation** | **Docs & Reference Portal** | Searchable navigation leading to User Guide, Complete CLI Reference, 37 Architecture Decisions (ADRs), and Offer Bundle. | Deepen knowledge and implement enterprise-grade agent orchestration patterns. |
| **I6: Proof & Trust** | **Security & Enterprise Proof** | Verifiable badges: 82.50% test coverage ratchet, SPDX 2.3 SBOM, OWASP Agentic Top 10 matrix, and "Built with g8s" dogfood stats. | Establish enterprise credibility and trust for deploying g8s across engineering fleets. |
| **I5 Gaps: FAQ** | **Enterprise FAQ & Objections** | Accordion covering "Why not standard Docker?", "Model & provider costs?", "Performance overhead of SQLite WAL?", "Supported platforms?". | Overcome friction, answer technical objections, and clarify deployment boundaries. |
