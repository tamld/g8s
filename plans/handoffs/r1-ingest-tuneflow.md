# Round 1 Concept Harvesting & Multi-Agent Debate Report: `tuneflow` → `g8s`

> **Session type**: T1 (strategy)

**Author**: F1 Middle Manager (High-Reasoning Supervisor Delegate)  
**Assigned Worktree**: `/Users/tamld/.local/state/g8s-sess-r1-tuneflow/worktrees/wt-882762eb`  
**WriteReceipt ID**: `1db079e8-f14c-4603-9208-95bbcd29eb56` (Target: `plans/handoffs/r1-ingest-tuneflow.md`)  
**Source Repository**: `/Users/tamld/Documents/github/tuneflow` (`v2.5.0`, Sibling Repo, Onboarded Wave 4)  
**Target Platform**: `/Users/tamld/Documents/github/g8s` (Pure-Go, Zero-CGO, Single-Binary Agent Execution Platform)  
**Date**: 2026-10-21  

---

## 1. Executive Summary & Core Thesis for `tuneflow` → `g8s`

### 1.1 The Sibling Relationship
`tuneflow` is a specialized, filial-technology media appliance and streaming downloader designed for senior citizens ("SilverMelody" WCAG 2.2 AAA UX) and homelab self-hosters. Formally onboarded into the `g8s` ecosystem in Wave 4 (2026-10-14) under the `utility` profile, `tuneflow` operates in an asymmetric edge-compute topology (M4 APFS local compute, `pnpm` CAS store, Proxmox ZFS CT122 dual-remote backup) and runs `g8s` offer gates (`tools/ci_spec_code_sync.sh`, `tools/ci_structure_sync.sh`, `tools/ci_link_integrity.sh`).

### 1.2 The Core Thesis: Empowerment vs. Bureaucratic Shackles
F0's strategic philosophy dictates:
> *`g8s` builds an empowering foundation for LLM calls and multi-agent execution — **NOT a bureaucratic shackle ("không phải gọng kiềm siết chết khả năng của Agent")**. Security and containment live at the perimeter (`WriteReceipt`, worktree isolation, deterministic verifiers), while inside that perimeter agents have maximum cognitive and tool autonomy.*

When inspecting `tuneflow` through this lens, our F1/F2 analysis identified two sharply distinct categories of engineering solutions:
1. **External Sidecar Coping Mechanisms (Rejected at F1)**: Solutions devised to survive Node.js/Python/FFmpeg/yt-dlp child-process instability, zombie orphan processes on Windows, and executable file-locking (`taskkill /T /F`, 85MB runtime SHA-256 hash loops, `.old` rename swaps). Because `g8s` was vaccinated against this chaos at birth by being a **Pure-Go, Zero-CGO, statically compiled single binary**, adopting these mechanisms into the core Go engine would introduce bureaucratic ceremony and bloat for problems `g8s` does not have.
2. **Pure Ergonomic & Operational Mechanisms (Adopted at F1)**: Elegant, zero-dependency patterns that expand agent predictability and autonomy without adding red tape:
   - **Tiered Health Classification & Capability Matrix**: Distinguishing platform-critical integrity (SQLite WAL, WriteReceipt sandbox) from optional provider availability (`9router`, Ollama, Ripgrep), preventing false-alarm platform degradation.
   - **Zero-Footprint Portable Storage Engine**: A deterministic `.portable` / `PORTABLE_MODE=1` resolution pattern enabling instant, zero-install execution on ephemeral containers, air-gapped nodes, and USB drives without polluting host directories.
   - **Multi-Surface Contract Parity**: Static AST verification guaranteeing 100% schema alignment across CLI, MCP, and HTTP surfaces without runtime overhead.

---

## 2. F2 Team Roster & Debate Log (Evidence of `F1 → n F2` Orchestration)

To rigorously evaluate `tuneflow` without single-agent confirmation bias, F1 orchestrated four heterogeneous F2 workers combining platform-native codebase research with `9router` multi-model specialist calls.

### 2.1 F2 Team Roster

| Worker ID / Trace ID | Agent / Model | Role | Scope & Files Inspected | Latency / Tokens | Key Output |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`3d49e4b2-ffcc-4c5a-97f7-6ae64c1d43ef`** | Gemini 3.8 Flash (Native Subagent) | **F2-Explorer & Codebase Researcher** | `tuneflow/README.md`, `AGENTS.md`, `manifest.json`, `.g8s/`, `packages/contract/`, `src/desktop/connection_fsm.js`, `src/security/binary_guard.js`, `tools/claims_check.sh`, `g8s/tools/` | ~198s<br>~120k tokens | Full comparative baseline: stack discipline, Tauri desktop IPC, OpenAPI route parity, Web Audio DSP, and offer bundle conformance. |
| **`01a15eb0-ca7e-7e01-91d7-2fb332c4de78`** | `cx/gpt-6-luna` (via `9router`) | **F2-Harvester** | `tuneflow/README.md`, `src/engine/process_manager.js`, `src/engine/doctor.js`, `src/security/binary_guard.js` | 6,864 ms<br>9,626 tokens (9,225 in / 401 out) | Extracted 5 candidate mechanisms: process supervision, fail-closed sidecars, portable storage, system doctor, and bounded autonomy. |
| **`01a15eb0-fea3-77c0-a805-df7953d96d15`** | `reasoning-high` (`nemotron-3-ultra-550b`) | **F2-Skeptic (Devil's Advocate)** | `tuneflow/README.md`, `src/engine/process_manager.js`, `src/engine/doctor.js`, `src/security/binary_guard.js` | 50,811 ms<br>14,007 tokens (12,099 in / 1,908 out) | Ruthless adversarial falsification against g8s axioms: rejected 3/5 ideas as external-sidecar antibodies; endorsed portable storage and tiered doctor. |
| **`01a15eb2-1f9d-7afc-9b2c-1da3ac546936`** | `deep-coding` (`nemotron-3-ultra-550b`) | **F2-Architect** | `g8s/internal/doctor/doctor.go`, `g8s/internal/pathutil/pathutil.go` | 29,961 ms<br>15,910 tokens (13,862 in / 2,048 out) | Synthesized concrete Go structs, evaluation algorithms, and test suites for Tiered Health and Portable Storage blueprints. |

---

### 2.2 Summary of the Multi-Agent Debate & F1 Adjudications

#### Clash 1: Centralized Process Supervision & Anti-Orphan Tree Killing (`process_manager.js`)
* **F2-Harvester**: Proposed importing `ProcessManager`'s active PID tracking map, abort-signal hooks, and cross-platform tree kill (`taskkill /T /F` vs. POSIX `-pid`) into `g8s` to supervise agent tasks and child processes.
* **F2-Skeptic**: **Strongly Objected**. Argued that `tuneflow`'s `ProcessManager` exists specifically because it spawns external Node/Python/FFmpeg sidecars. In `g8s`, concurrency is native Go routines. Furthermore, `g8s` already implements POSIX `Setpgid` + `kill(-pgid)` and Windows Job Objects (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` in `internal/worker/proc_windows.go`), which is kernel-enforced and strictly superior to userland PID tracking.
* **F1 Middle Manager Adjudication**: **REJECTED FROM CORE ENGINE**. Prior art check confirms `g8s` already possesses superior kernel-level containment via Windows Job Objects (#415 PR-2) and POSIX process groups. Adding an in-memory `Map<pid, entry>` is redundant paperwork that restricts agent throughput without adding security.

#### Clash 2: Fail-Closed Sidecar Integrity & Atomic Binary Swap (`binary_guard.js`)
* **F2-Harvester**: Recommended importing `verifyBinaryIntegrity` (SHA-256 verification against `SHA256SUMS.json`) and `atomicSwapExecutable` (`.old` rename pattern on Windows) to secure external tools.
* **F2-Skeptic**: **Strongly Objected**. Pointed out that reading 85MB binaries into RAM on spawn is CGO-adjacent bloat. `g8s` is a single static binary whose integrity is guaranteed at build time via Go module checksums (`go.sum`), Cosign signatures, and SLSA provenance. The OS loader handles verification, not userland hashing loops.
* **F1 Middle Manager Adjudication**: **REJECTED FROM CORE ENGINE**. Statically compiled Go binaries have no need for runtime sidecar hash loops. However, for sibling repositories adopting the `offer/` bundle, lightweight static file checksum checks belong in declarative CI gates (Bucket B), not in the core `g8s` binary.

#### Clash 3: Tiered Health Classification & Capability Matrix (`doctor.js`)
* **F2-Harvester**: Praised `tuneflow`'s doctor for reporting structured health across runtime, storage, database, sidecars, and processes via unified CLI and HTTP.
* **F2-Skeptic**: Concurred on the data model, noting that while `g8s` already has `g8s doctor`, it currently conflates core engine health with optional tool/provider availability. If `9router` is offline or `ripgrep` is missing, `g8s` risks reporting an overall `FAIL` even though its core SQLite and write-receipt engines are 100% operational.
* **F2-Architect**: Delivered concrete Go structs (`HealthTier`: `core` vs `capability`; `DoctorReport.Capabilities`; `CapabilityStatus`: `available`, `degraded`, `unavailable`).
* **F1 Middle Manager Adjudication**: **ADOPTED FOR CORE ENGINE (Bucket A)**. This is a pure mechanism that eliminates false-alarm degradation and empowers agents to make intelligent fallback decisions.

#### Clash 4: Zero-Footprint Portable Storage Mode (`.portable` & `PORTABLE_MODE`)
* **F2-Harvester**: Highlighted `resolveStoragePaths` which checks for `.portable` or `PORTABLE_MODE=1` before falling back to XDG or AppData.
* **F2-Skeptic**: **Endorsed**. Minimal Go implementation (~30 lines), zero dependencies, and immediately empowers agents running in disposable CI runners, isolated docker mounts, or USB flash drives.
* **F1 Middle Manager Adjudication**: **ADOPTED FOR CORE ENGINE (Bucket A)**. Direct addition to `internal/pathutil/pathutil.go`.

#### Clash 5: Client-Server Contract Parity Verification (`packages/contract/`)
* **F2-Explorer**: Discovered `tuneflow/packages/contract/index.js`, which statically verifies that 100% of declared backend routes exist in OpenAPI and that every client call across Rust, Java, and JS targets a contracted route.
* **F2-Skeptic**: Warned against introducing Node.js scripts into `g8s`.
* **F1 Middle Manager Adjudication**: **ADOPTED FOR OFFER BUNDLE (Bucket B)**. We will not import Node scripts into `g8s`, but we will extract the *verification pattern* into a Pure-Go / Shell CI gate (`tools/ci_contract_sync.sh` or `g8s verify --contract`) to prevent drift across `g8s` CLI, MCP tools, and HTTP endpoints.

---

## 3. Round-1 Candidate Classification Matrix (3-Bucket Routing)

```text
┌──────────────────────────────────────────────────────────────────────────────────┐
│                             3-BUCKET ROUTING MATRIX                              │
├───────────────────────┬──────────────────────────┬───────────────────────────────┤
│       Bucket A        │         Bucket B         │           Bucket C            │
│    Core Go Engine     │       Offer Bundle       │        Local Operator         │
│  (`cmd/`, `internal/`)│(`offer/`, `.g8s/`, `ci_`)│   (`~/.agents/skills/`)       │
├───────────────────────┼──────────────────────────┼───────────────────────────────┤
│ 1. Tiered Health &    │ 1. Multi-Surface Contract│ 1. Air-Gapped USB /           │
│    Capability Matrix  │    Parity Gate           │    Homelab Media Bridge       │
│ 2. Portable Storage   │ 2. Declarative Claims    │ 2. Audio/DSP Diagnostic       │
│    Engine             │    Registry Gate         │    Skill                      │
│ 3. Connection FSM     │                          │                               │
│    Latency Tiers      │                          │                               │
└───────────────────────┴──────────────────────────┴───────────────────────────────┘
```

### Bucket A — Core Go Engine & LLM Platform (`cmd/g8s`, `internal/*`)
*Empowering mechanisms that live inside the static binary, require zero dependencies, and maximize agent execution throughput.*
1. **Tiered Health Classification & Capability Matrix (`internal/doctor/doctor.go`, `cmd/g8s/doctor.go`)**:
   - Classifies checks into `TierCore` (database, write-receipt engine, sandbox isolation, storage writability) and `TierCapability` (`9router` connectivity, Ollama daemon, ripgrep acceleration).
   - Core failure yields `UNHEALTHY`; capability gaps yield `DEGRADED` with explicit remediation hints. Agents reading `g8s doctor --json` know exactly what features are available without crashing.
2. **Zero-Footprint Portable Storage Engine (`internal/pathutil/pathutil.go`)**:
   - Respects `PORTABLE_MODE=1` or presence of `.portable` file in the binary's directory or working directory.
   - Redirects all state, evidence, database, and logs to `./data` or `./.g8s-state`, enabling completely air-gapped and container-isolated execution.
3. **Upstream Provider Connection FSM with Degraded Latency Tiers (`internal/llm/` or `internal/provider/handle.go`)**:
   - Inspired by `tuneflow`'s `ConnectionFSM` (`SPEC-0013`). Classifies upstream LLM provider endpoints as `CONNECTED` or `DEGRADED` (when latency $>1500$ms or token rate throttled). Allows `g8s` to automatically downgrade effort class or route to fallback models before catastrophic timeouts occur.

### Bucket B — Offer Bundle & Declarative Governance (`offer/`, `.g8s/*.yml`, `tools/ci_*.sh`)
*Repo-agnostic verification tools and schemas distributed to sibling repositories.*
1. **Multi-Surface Contract Parity Gate (`tools/ci_contract_sync.sh`)**:
   - Inspired by `tuneflow/packages/contract/`. Scans OpenAPI / JSON schemas and ensures every exposed HTTP route and MCP tool has matching implementation handlers and test coverage.
2. **Declarative Claims Registry Gate (`tools/ci_claims_check.sh` & `docs/claims.yml`)**:
   - Adapted from `tuneflow/tools/claims_check.sh`. Validates that every architectural claim made in documentation strictly maps to an existing `evidence.test` and `evidence.source` file on disk.

### Bucket C — Local Untracked Operator Skill (`~/.agents/skills/`)
*Local operator skills providing specialized workflows without polluting the repository.*
1. **Air-Gapped Media & USB Bridge (`skills/airgap-storage/`)**:
   - Operational skill for formatting, FAT32 metadata sanitization, and automated file-copying to physical storage devices for air-gapped homelab deployments.
2. **Audio/DSP & Senior Accessibility Evaluation (`skills/accessibility-audit/`)**:
   - Automated WCAG 2.2 AAA contrast and touch-target audit recipes.

---

## 4. Explicitly Rejected Ideas (`REJECTED AT F1`)

F1 and F2-Skeptic rigorously falsified and rejected the following candidates from entering `g8s`:

### Rejection 1: Centralized Child Process Lifecycle Manager (`src/engine/process_manager.js`)
- **What it is**: An in-memory tracking map (`Map<pid, entry>`), timeout timer scheduler, and abort signal coordinator for child processes.
- **Why Rejected**:
  1. *Go Native Parity*: In Go, concurrency and lifecycle are handled natively via `context.Context` and goroutines. Spawning child processes in `g8s` is already bounded by `exec.CommandContext(ctx, ...)`.
  2. *Containment Redundancy*: `g8s` already implements OS-level containment: POSIX `Setpgid` with `syscall.Kill(-pid, sig)` in `internal/worker/proc_posix.go` and Windows Job Objects (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) in `internal/worker/proc_windows.go`. Windows Job Objects terminate detached grandchild processes that `tuneflow`'s `taskkill /T /F` cannot catch.
  3. *Cognitive Bureaucracy*: Adding an internal process registry adds state locking and synchronization overhead without providing any capability benefit to LLM agents.

### Rejection 2: Fail-Closed Runtime Sidecar Hashing & Memoization (`src/security/binary_guard.js`)
- **What it is**: Reading bundled sidecar executables into memory on execution, hashing them via SHA-256, and comparing them against `SHA256SUMS.json`.
- **Why Rejected**:
  1. *Violates Pure-Go Axiom*: `g8s` is a single static binary. It does not ship external python scripts or dynamic sidecars.
  2. *Performance Penalty*: Reading 50MB–100MB binaries from disk into memory on startup causes severe I/O thrashing on cold starts.
  3. *Supply Chain Theater*: For static Go binaries, cryptographic integrity belongs at build time (Go module checksum database, Sigstore Cosign, SLSA Level 3 provenance), enforced by the OS kernel loader, not userland runtime checks.

### Rejection 3: In-Place Executable Renaming Swap (`atomicSwapExecutable`)
- **What it is**: Renaming `current.exe -> current.exe.old` and moving `new.exe -> current.exe` to bypass Windows in-use file write locks.
- **Why Rejected**:
  1. *Container & Daemon Standard*: `g8s` in production runs as a container image or a system service (`g8s service install|start`). Binary replacement is handled by package managers (`scoop`, `brew`, `apt`) or container orchestrators.
  2. *Fragile Userland State*: Leaving `.old` files littered on the filesystem creates cleanup debt and risks loading stale binaries.

---

## 5. Top 1–2 Implementation Blueprints for Round 2 Review

### Blueprint 1: Tiered Health Classification & Capability Matrix in `g8s doctor`

#### File Targets
- `internal/doctor/doctor.go`
- `cmd/g8s/doctor.go`
- `internal/doctor/doctor_tiered_test.go` (new test)

#### Architectural Go Struct Enhancements
```go
package doctor

// HealthTier classifies whether a check is critical to core execution or optional capability.
type HealthTier string

const (
	TierCore       HealthTier = "core"       // Platform-critical: SQLite WAL, sandbox, write-receipt engine
	TierCapability HealthTier = "capability" // Optional: 9router reachability, Ollama, Ripgrep turbo
)

// CapabilityStatus defines the operational readiness of a specific feature.
type CapabilityStatus string

const (
	CapabilityAvailable   CapabilityStatus = "available"
	CapabilityDegraded    CapabilityStatus = "degraded"
	CapabilityUnavailable CapabilityStatus = "unavailable"
)

// CapabilityEntry maps a feature to its status, reason, and remediation.
type CapabilityEntry struct {
	Name        string           `json:"name"`
	Status      CapabilityStatus `json:"status"`
	Reason      string           `json:"reason,omitempty"`
	Remediation string           `json:"remediation,omitempty"`
}

// Enhanced DoctorReport incorporating tiered health and capability matrix.
type DoctorReport struct {
	OverallStatus    string             `json:"overall_status"`    // HEALTHY, DEGRADED, UNHEALTHY
	CoreStatus       string             `json:"core_status"`       // HEALTHY, UNHEALTHY
	CapabilityStatus string             `json:"capability_status"` // FULL, PARTIAL, MINIMAL
	Platform         string             `json:"platform"`
	GoRuntime        string             `json:"go_runtime"`
	ZeroCGO          bool               `json:"zero_cgo"`
	Scope            string             `json:"scope,omitempty"`
	ConfigDir        string             `json:"config_dir,omitempty"`
	DataDir          string             `json:"data_dir,omitempty"`
	DatabasePath     string             `json:"database_path,omitempty"`
	Checks           []DiagnosticResult `json:"checks"`
	Capabilities     []CapabilityEntry  `json:"capabilities"`
	AppliedFixes     []string           `json:"applied_fixes,omitempty"`
}
```

#### Evaluation Logic Contract
```go
// EvaluateReport separates core health from capability degradation.
func (d *Doctor) EvaluateReport(report *DoctorReport) {
	coreFail := false
	for _, c := range report.Checks {
		if c.Tier == TierCore && c.Status == "FAIL" {
			coreFail = true
			break
		}
	}

	if coreFail {
		report.CoreStatus = "UNHEALTHY"
		report.OverallStatus = "UNHEALTHY"
	} else {
		report.CoreStatus = "HEALTHY"
		// Determine capability status
		capUnavail, capDeg := 0, 0
		for _, cap := range report.Capabilities {
			if cap.Status == CapabilityUnavailable {
				capUnavail++
			} else if cap.Status == CapabilityDegraded {
				capDeg++
			}
		}

		if capUnavail == 0 && capDeg == 0 {
			report.CapabilityStatus = "FULL"
			report.OverallStatus = "HEALTHY"
		} else if capUnavail == 0 {
			report.CapabilityStatus = "PARTIAL"
			report.OverallStatus = "DEGRADED"
		} else {
			report.CapabilityStatus = "MINIMAL"
			report.OverallStatus = "DEGRADED"
		}
	}
}
```

#### CLI / MCP Contract Sketch
- CLI invocation: `g8s doctor --json`
- Machine-readable JSON output:
```json
{
  "overall_status": "DEGRADED",
  "core_status": "HEALTHY",
  "capability_status": "PARTIAL",
  "capabilities": [
    { "name": "sqlite_wal", "status": "available" },
    { "name": "write_receipt_sandbox", "status": "available" },
    { "name": "9router", "status": "degraded", "reason": "latency > 1500ms" },
    { "name": "ollama", "status": "unavailable", "remediation": "install ollama" }
  ]
}
```

#### Deterministic Verification Test
```go
func TestDoctor_TieredCapabilityDegradation(t *testing.T) {
	doc := New()
	report := &DoctorReport{
		Checks: []DiagnosticResult{
			{Name: "Database", Status: "OK", Tier: TierCore},
			{Name: "WriteReceipt", Status: "OK", Tier: TierCore},
			{Name: "Ollama", Status: "FAIL", Tier: TierCapability, Capability: "ollama"},
		},
		Capabilities: []CapabilityEntry{
			{Name: "ollama", Status: CapabilityUnavailable, Reason: "not running"},
		},
	}
	doc.EvaluateReport(report)

	if report.CoreStatus != "HEALTHY" {
		t.Fatalf("expected CoreStatus HEALTHY, got %s", report.CoreStatus)
	}
	if report.OverallStatus != "DEGRADED" {
		t.Fatalf("expected OverallStatus DEGRADED, got %s", report.OverallStatus)
	}
}
```

---

### Blueprint 2: Zero-Footprint Portable Storage Engine

#### File Targets
- `internal/pathutil/pathutil.go`
- `internal/pathutil/pathutil_portable_test.go` (new test)

#### Architectural Go Modifications
Modify `DefaultStateDir` and `DataDirForOS` to inspect the portable execution markers:
```go
// IsPortableMode reports whether portable execution is activated.
func IsPortableMode() bool {
	if os.Getenv("PORTABLE_MODE") == "1" || os.Getenv("G8S_PORTABLE") == "1" {
		return true
