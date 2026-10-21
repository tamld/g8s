# Round 1 Concept Harvesting & Multi-Agent Debate: `hash-checker` -> `g8s`

> **Session type**: T1 (strategy)

- **Author**: F1 Middle Manager (High-Reasoning Coordination Lead)
- **Session ID**: `r1-hash-checker`
- **Task ID**: `9d5c7651-ab04-4e4a-86e4-5b9b054c5ccc`
- **Write Receipt ID**: `db071ecc-23c0-4336-80f7-0e460d29d4f9`
- **Scope Target**: `plans/handoffs/r1-ingest-hash-checker.md`
- **Sibling Target**: `/Users/tamld/Documents/github/hash-checker` (Rust 1.88+, `egui`, `clap`)
- **Host Target**: `/Users/tamld/Documents/github/g8s` (Pure-Go, Zero-CGO, Single-Binary Agent Execution & LLM Call Platform)
- **Date**: 2026-10-21

---

## 1. Executive Summary & Core Thesis for `hash-checker` -> `g8s`

### 1.1. The Strategic Mandate from F0 (Brain)
`g8s` is a Pure-Go, Zero-CGO, single-binary **Agent Execution & LLM Call Platform** operating across three unified surfaces (`CLI + MCP + HTTP`).

**Core Axiom**: `g8s` builds an empowering foundation for LLM calls and multi-agent execution — **NOT a bureaucratic shackle ("không phải gọng kiềm siết chết khả năng của Agent")**. Security, integrity, and containment live strictly at the perimeter:
- Mathematical boundary validation (`WriteReceipt` with single-use, TTL, path-scoped leases).
- Physical file-system isolation (`git worktree` isolation per worker).
- Deterministic acceptance verifiers (`.g8s/verifier-classes.yml`, pure-Go AST/file check gates).

Inside that perimeter, agents must have **maximum cognitive and tool autonomy** to reason, experiment, search, read, edit, spawn subagents, and execute LLM calls without artificial friction, arbitrary approval bottlenecks, or procedural micromanagement.

### 1.2. Sibling Repository Relationship & Ingestion Thesis
`hash-checker` is an active sibling repository that previously onboarded the `g8s` Offer Bundle (`utility` profile, Issue #80, commit `ec0db62`). It demonstrates how an external, non-Go project adopts `g8s` declarative governance (`.g8s/lane-bundles.yml`, `.g8s/verifier-classes.yml`, `tools/ci_*.sh`).

However, as a standalone project, `hash-checker` accumulated both **high-value mathematical mechanisms** and **bureaucratic/language-specific ballast**:
1. **The Gold (Mechanism to Adopt)**:
   - Quad-category verification reporting (`matched`, `mismatched`, `missing`, `extra`), which provides exact mathematical provenance for file trees and worktree mutations.
   - Declarative batch verification with structured JSON input/output and deterministic exit codes.
   - Algorithmic collision detection (rejecting mismatched digest prefixes before hashing).
   - Golden-master regression comparator mathematics (verifying performance deltas against a tolerance threshold).
2. **The Rust/Ballast (Bureaucracy & Bloat to Reject)**:
   - "Autonomy Level" human approval checkpoints (`cursor_role_review&feedback.md`) that treat agents as untrusted toddlers requiring permission to touch files.
   - Container-first build/test bottlenecks that mandate Docker for everyday operations.
   - Python scripting in the critical CI gate path.
   - Desktop GUI frameworks (`egui`, `eframe`, GTK4 dialogs, `rfd`).

**The Thesis**: We strip away the prose, GUI, container locks, and human checkpoint bureaucracy. We transplant the **pure mathematical verification engine** into `g8s` as Pure-Go, Zero-CGO primitives (`internal/fsops/manifest.go`, `internal/fsops/hash.go`, `internal/batch/verify.go`), empowering agents with instantaneous, machine-verifiable integrity checks for worktrees, deliverables, and LLM call outputs.

---

## 2. F2 Team Roster & Debate Log (Evidence of `F1 -> n F2` Orchestration)

To rigorously evaluate `hash-checker`, F1 orchestrated a team of **4 diverse F2 workers** combining native subagents and heterogeneous `9router` models across different cognitive personas.

### 2.1. F2 Team Roster

| Worker ID | Role & Persona | Platform / Model | Trace / Conversation ID | Files Inspected / Context | Latency / Tokens | Verdict Stance |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **F2-1 (Inspector)** | Research Specialist | Native Subagent (`research`) | Conv `2cd12f4e-60e3-4a59-bada-504bdf2af94a` | 14+ files in `hash-checker` (`README`, `.g8s/`, `Makefile`, `rust/`, `scripts/`, `specs/`, `docker/`) | ~95s (multi-turn read/eval) | Exhaustive taxonomy of mechanisms & bloat |
| **F2-2 (Harvester)** | Concept Harvester (Expansive) | `9router` / `cx/gpt-6-luna` (effort: low) | Trace `01a15eb0-9a1f-7a7f-a52f-27e149960177` | `README.md`, `cursor_role_review&feedback.md`, `scripts/check_performance_regression.py` | 5,949 ms<br>6,290 in / 345 out | Proposed 5 candidate mechanisms for adoption |
| **F2-3 (Skeptic)** | Devil's Advocate (Falsifier) | `9router` / `reasoning-high` (`nvidia/nemotron-3-ultra-550b-a55b`) | Trace `01a15eb0-f262-70ee-a07b-b3dc2fba75c8` | `README.md`, `cursor_role_review&feedback.md`, `scripts/check_performance_regression.py` | 28,661 ms<br>8,820 in / 1,845 out | Tested all concepts against g8s 3 core laws; 4 rejections |
| **F2-4 (Architect)** | Pure-Go Systems Architect | `9router` / `cx/gpt-6-luna` (effort: low) | Trace `01a15eb4-af82-7ee4-9d25-778a737eb942` | `rust/hash-checker/src/manifest.rs`, `rust/hash-checker/src/batch.rs` | 17,508 ms<br>1,003 in / 1,613 out | Produced production Pure-Go blueprints, types & tests |

---

### 2.2. The Multi-Agent Debate Log

```
               [F2-Harvester: cx/gpt-6-luna]
             "Let's adopt container-first builds,
              autonomy checkpoints & batch reports!"
                          │
                          ▼
            [F2-Skeptic: reasoning-high]
    "HALT! Containers violate Law 1 (Zero-CGO Pure Go).
     Checkpoints violate Law 3 (empower, don't shackle).
     Python perf scripts violate Law 1. REJECT AT F1!"
                          │
                          ▼
             [F1 Middle Manager Adjudication]
     "Skeptic is correct on perimeter vs bureaucracy.
      Strip containers, strip Python, strip checkpoints.
      Adopt quad-category manifest & batch engine into Go stdlib."
                          │
                          ▼
               [F2-Architect: cx/gpt-6-luna]
      "Pure-Go Zero-CGO Blueprints created:
       internal/fsops/manifest.go + cmd/g8s/batch.go"
```

#### Debate Clash 1: Container-First Make Targets vs. Host-Native Pure-Go
- **F2-Harvester**: Argued that `hash-checker`'s container-first `Makefile` (`make rust-test`, `make rust-build` running inside Docker containers) provides guaranteed reproducibility across developer environments and should be adopted by `g8s`.
- **F2-Skeptic (nemotron-550b)**: Falsified this as a direct violation of **Law 1 ("Pure-Go Zero-CGO single binary; no container dependencies required for core ops")**. If an agent or operator cannot run `go test ./...` and `go build ./cmd/g8s` directly on macOS/Linux without a Docker daemon running, `g8s` loses its core platform agility.
- **F1 Adjudication**: **REJECT container-first from g8s core**. `g8s` core development must remain 100% native host Go. Container recipes belong exclusively in `offer/` as optional sibling references for teams that require isolated CI runners.

#### Debate Clash 2: Agent Autonomy Levels vs. Zero-Trust Perimeter
- **F2-Harvester**: Pointed to `cursor_role_review&feedback.md` and recommended introducing formal "Autonomy Levels" (Level 1: Read/Lint, Level 2: Checkpoint Approval for changes >5 files, Level 3: Prohibited).
- **F2-Skeptic (nemotron-550b)**: Denounced this as a direct violation of **Law 3 ("Empower LLM calls/agents rather than shackling them")** and F0's strategic axiom. Level 2 checkpoints introduce human bottlenecks that destroy autonomous multi-agent pipelines. Arbitrary file count limits (>5 files) are bureaucratic theater.
- **F1 Adjudication**: **REJECT AT F1 with extreme prejudice**. In `g8s`, safety is enforced by **unforgeable mathematical perimeters**:
  - `WriteReceipt.AllowedPaths` blocks writes outside the declared task scope in-process.
  - Ephemeral `git worktree` isolation prevents unreviewed contamination of the repository.
  - Acceptance verifiers (`.g8s/verifier-classes.yml`) mathematically validate the output.
  Inside this perimeter, the agent has 100% autonomy. We never shackle agents with human checkpoint delays for procedural tasks.

#### Debate Clash 3: Python Performance Regression Scripts
- **F2-Inspector & Harvester**: Highlighted `scripts/check_performance_regression.py` as an excellent pattern for preventing CI throughput regressions using baseline golden master JSON files and a percentage tolerance flag (`--tolerance 0.10`).
- **F2-Skeptic (nemotron-550b)**: Agreed that regression gating is vital, but flagged that putting Python scripts in `g8s` CI violates **Law 1 ("No Python scripts in critical path")**.
- **F1 Adjudication**: **Adopt the mathematical model; Reject the Python runtime**. `g8s` will implement performance regression comparison natively in pure Go (`internal/verifier/perf.go` or `g8s perf compare`), operating directly on `go test -bench` JSON output with zero third-party interpreters.

#### Debate Clash 4: Quad-Category Manifest Verification
- **F2-Inspector & F2-Skeptic & F2-Architect**: All converged on `rust/hash-checker/src/manifest.rs` and `batch.rs`. Unlike standard tools that only report "OK/FAIL", `hash-checker` partitions verification into 4 explicit sets:
  1. `matched`: File exists and digest matches manifest.
  2. `mismatched`: File exists but digest differs (returns expected vs actual).
  3. `missing`: Path in manifest but missing on disk.
  4. `extra`: File exists on disk within scope but was untracked in manifest.
- **F1 Adjudication**: **UNANIMOUS ADOPTION into Bucket A**. This schema provides exact mathematical evidence for F0 when verifying worker worktrees.

---

## 3. Round-1 Candidate Classification Matrix (3-Bucket Routing)

Following `g8s` architecture governance, all harvested candidates are categorized into one of three distinct architectural destinations:

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                    CANDIDATE HARVESTING ROUTING MATRIX                       │
├────────────────────────┬─────────────────────────────┬───────────────────────┤
│  Bucket A: Core Go     │  Bucket B: Offer Bundle     │  Bucket C: Operator   │
│  Engine & Platform     │  & Declarative Governance   │  Skill (~/.agents/)   │
│  (Pure Go, Zero-CGO)   │  (Sibling Offerings)        │  (Untracked Policy)   │
├────────────────────────┼─────────────────────────────┼───────────────────────┤
│ • Quad-Category        │ • Sibling Verifier Gating   │ • Release & Attest    │
│   Manifest Engine      │   Suite (ci_*.sh tests)     │   Automation Skill    │
│ • Declarative Batch    │ • Headless Snapshot Harness │ • Baseline Benchmark  │
│   Verification Core    │   (UI/TUI projects)         │   Harvester Skill     │
│ • Pure-Go Performance  │ • Cross-Platform Artifact   │                       │
│   Regression Verifier  │   Attestation Pattern       │                       │
└────────────────────────┴─────────────────────────────┴───────────────────────┘
```

### 3.1. Bucket A — Core Go Engine & LLM Platform (`cmd/g8s`, `internal/*`)
*Directly strengthens g8s execution, verifier evidence, and agent tooling. Must be Pure-Go, Zero-CGO.*

1. **Candidate A1: Quad-Category Directory Manifest Engine (`internal/fsops/manifest.go`, `cmd/g8s/manifest.go`)**
   - **Mechanism**: Pure-Go directory walker with 1MB chunked streaming hashing (`crypto/sha256`, `crypto/md5`, `crypto/sha1`). Generates canonical, sorted JSON manifests. Verifies disk state against manifest and outputs exact partition: `matched`, `mismatched`, `missing`, and `extra`.
   - **Agent Empowerment**: Agents can take an instantaneous snapshot of their worktree before running mutations and verify exact diffs post-mutation, eliminating hallucinations and untracked file leaks.
   - **Interfaces**: CLI (`g8s manifest create`, `g8s manifest verify`), MCP (`g8s_manifest_create`, `g8s_manifest_verify`), and HTTP (`/api/v1/fs/manifest`).

2. **Candidate A2: Declarative Batch Verification Core (`internal/batch/verify.go`, `cmd/g8s/batch.go`)**
   - **Mechanism**: Ingests structured JSON/CSV files specifying path, algorithm, and expected digest. Executes parallel streaming verification bounded by `runtime.GOMAXPROCS(0)`. Returns structured JSON report with deterministic exit codes (0 = match, 1 = mismatch, 2 = operational/syntax error).
   - **Agent Empowerment**: Enables multi-file integrity assertions in a single tool call rather than issuing dozens of individual shell hash checks.
   - **Interfaces**: CLI (`g8s batch verify`), MCP (`g8s_batch_verify`).

3. **Candidate A3: Pure-Go Performance Regression Verifier (`internal/verifier/perf.go`)**
   - **Mechanism**: Mathematical comparator that loads a golden-master benchmark JSON and compares it against current `go test -bench -json` execution. Flags regressions that exceed a configurable tolerance (`--tolerance 0.05` / 5%).
   - **Agent Empowerment**: Plugs directly into `g8s verify --task <id>` as an automated machine verifier, preventing performance degradation in queue leases and file operations without requiring Python.

---

### 3.2. Bucket B — Offer Bundle & Declarative Governance (`offer/`, `.g8s/*.yml`, `tools/ci_*.sh`)
*Repo-agnostic assets distributed to sibling repositories via the g8s Offer Bundle.*

1. **Candidate B1: Sibling Self-Test Suite for CI Gates (`offer/tools/ci_*_test.sh`)**
   - **Mechanism**: `hash-checker` contains test scripts for the offer CI gates: `ci_link_integrity_test.sh`, `ci_structure_sync_test.sh`, and `ci_spec_code_sync_test.sh`.
   - **Destination**: Re-incorporate these validation tests into `g8s/offer/` to ensure that updates to offer scripts are tested prior to sibling rollout.

2. **Candidate B2: Headless Smoke-Test & Snapshot Automation Pattern (`offer/patterns/headless-smoke.md`)**
   - **Mechanism**: The pattern in `docker/gui-automation.Dockerfile` + `xvfb` for rendering and snapshotting headless interfaces.
   - **Destination**: Package as a reference guide in `offer/patterns/` for sibling repos building TUI or GUI applications (e.g., terminal dashboards, desktop tools) without polluting `g8s` core.

3. **Candidate B3: Attestation & Checksum Distribution Spec (`offer/specs/release-attestation.md`)**
   - **Mechanism**: Manifest-driven SHA256SUMS and GitHub Build Attestation workflows (`dist-workspace.toml`).
   - **Destination**: Standardized release contract for sibling repositories adopting `g8s` release pipelines.

---

### 3.3. Bucket C — Local Untracked Operator Skill (`~/.agents/skills/`)
*Local workflow knowledge for Antigravity/Cursor operators; untracked by public git.*

1. **Candidate C1: `operator-perf-regression` Skill (`~/.agents/skills/operator-perf-regression/`)**
   - **Purpose**: Automates capturing benchmark golden masters (`go test -bench` or `cargo bench`), storing baselines under `~/.local/state/benchmarks/`, and running historical regression analysis across git revisions.

2. **Candidate C2: `operator-dist-release` Skill (`~/.agents/skills/operator-dist-release/`)**
   - **Purpose**: Orchestrates release tagging, dry-run packaging checks, and multi-platform attestation verification using local operator keys without committing secrets.

---

## 4. Explicitly Rejected Ideas (`REJECTED AT F1`)

The F1 Middle Manager, in alignment with F2-Skeptic, **explicitly rejects** four concepts from `hash-checker`:

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                        REJECTED AT F1 (BUREAUCRACY & BLOAT)                  │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ Rejected Idea                        │ Reason for Rejection                  │
├──────────────────────────────────────┼───────────────────────────────────────┤
│ 1. Autonomy Level Approval           │ Violates Law 3 (Empower, don't        │
│    Checkpoints (>5 files, CI, deps)  │ shackle). Imposes human bottlenecks.  │
│ 2. Container-First Make Targets      │ Violates Law 1 (Pure-Go, Zero-CGO).   │
│    (make rust-test requires Docker)  │ Mandatory Docker cripples agility.    │
│ 3. Python-Based Telemetry & Perf     │ Violates Law 1. Core verification     │
│    Scripts (check_regression.py)     │ must be single-binary Go stdlib.      │
│ 4. Desktop GUI Framework             │ Out-of-Scope Bloat. g8s is CLI,       │
│    (egui, eframe, GTK4 dialogs, rfd) │ MCP, and HTTP for agents, not humans. │
└──────────────────────────────────────┴───────────────────────────────────────┘
```

### Rejection 1: Multi-Tier Autonomy & Human Checkpoint Bottlenecks (`cursor_role_review&feedback.md`)
- **What hash-checker does**: Categorizes agent actions into Level 1 (Full Autonomy), Level 2 (Checkpoint Approval required for changes to CI, dependencies, or >5 files), and Level 3 (Prohibited).
- **Technical Justification for Rejection**:
  - Direct violation of **Law 3** and F0's strategic philosophy: *"g8s builds an empowering foundation for LLM calls and multi-agent execution — NOT a bureaucratic shackle."*
  - Arbitrary metrics like ">5 files" incentivize agents to create large monolithic files or fail to complete refactorings.
  - In `g8s`, safety is architectural, not procedural. A worker operates in an isolated `git worktree` under a path-scoped `WriteReceipt`. If the worker violates its receipt, the write is rejected in-process. If the worker produces invalid code, the acceptance verifier fails. Freezing execution to ask a human "Can I edit this 6th file?" turns an autonomous platform into an interactive nuisance.

### Rejection 2: Container-First Make Pipeline (`scripts/docker-rust-*.sh`, `Makefile`)
- **What hash-checker does**: Requires Docker containers for standard development tasks (`make rust-test`, `make rust-build`, `make ci-linux-local`).
- **Technical Justification for Rejection**:
  - Direct violation of **Law 1 ("Pure-Go Zero-CGO single binary")**.
  - `g8s` compiles in <2 seconds with standard `go build`. Forcing a Docker daemon, volume mounts, and container startup overhead adds 15–30 seconds of latency to every cycle and prevents execution in constrained environments (nested virtualization, lightweight worker nodes).
  - Native host toolchains must always be the first-class path for `g8s`.

### Rejection 3: Desktop GUI Stack (`egui`, `eframe`, GTK4 dialogs, `rfd`)
- **What hash-checker does**: Implements an immediate-mode 60fps GUI (`rust/hash-checker-gui/src/main.rs`, 2,671 lines) with Slate themes, virtual framebuffers, and OS file dialog wrappers.
- **Technical Justification for Rejection**:
  - `g8s` is a headless agent execution engine and LLM call platform.
  - Agents communicate via machine-readable JSON envelopes over `stdio` (CLI/MCP) and UNIX domain sockets or TCP (HTTP). Desktop graphical components add massive C/OS library dependencies and have zero utility for AI agents.

### Rejection 4: Python-Based Regression & Telemetry Scripts (`scripts/*.py`)
- **What hash-checker does**: Implements performance regression analysis and telemetry parsing in Python 3 scripts (`check_performance_regression.py`, `analyze_telemetry.py`), requiring `pip install` and Python virtual environments.
- **Technical Justification for Rejection**:
  - Violates **Law 1** and the single-binary zero-dependency deployment promise.
  - Python scripts introduce environment drift (Python 3.10 vs 3.12, missing modules, virtualenv activation). All CI and verifier logic in `g8s` must compile into the static `g8s` binary or run as POSIX shell scripts using standard UNIX utilities.

---

## 5. Top 2 Implementation Blueprints for Round 2 Review

### Blueprint 1: Pure-Go Quad-Category Manifest Engine

#### File Locations in `g8s`
- `internal/fsops/hash.go`: Streaming chunked hashing primitive.
- `internal/fsops/manifest.go`: Core manifest data structures, generation, serialization, and quad-category verification.
- `cmd/g8s/manifest.go`: CLI command registration (`g8s manifest create`, `g8s manifest verify`).
- `internal/mcp/manifest.go`: MCP tool registration (`g8s_manifest_create`, `g8s_manifest_verify`).

#### Core Go Struct Definitions
```go
package fsops

import "time"

type Algorithm string

const (
	AlgorithmSHA256 Algorithm = "sha256"
	AlgorithmSHA1   Algorithm = "sha1"
	AlgorithmMD5    Algorithm = "md5"
)

// Manifest represents a portable cryptographic snapshot of a file tree.
type Manifest struct {
	Version     int            `json:"version"`      // Schema version (1)
	Algorithm   Algorithm      `json:"algorithm"`    // Hashing algorithm used
	CreatedAt   time.Time      `json:"created_at"`   // RFC3339 timestamp
	Root        string         `json:"root"`         // Normalized relative root
	Files       []ManifestFile `json:"files"`        // Lexicographically sorted entries
}

type ManifestFile struct {
	Path   string `json:"path"`   // POSIX slash-separated relative path
	Digest string `json:"digest"` // Hexadecimal digest string
	Size   int64  `json:"size"`   // File size in bytes
}

// VerifyResult partitions the verification of a file tree into 4 mutually exclusive sets.
type VerifyResult struct {
	Matched    []string         `json:"matched"`    // Paths with matching digests
	Mismatched []MismatchRecord `json:"mismatched"` // Paths where disk digest != manifest
	Missing    []string         `json:"missing"`    // In manifest, but missing on disk
	Extra      []string         `json:"extra"`      // On disk within root, but omitted from manifest
}

type MismatchRecord struct {
	Path     string `json:"path"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

func (vr *VerifyResult) HasFailures() bool {
	return len(vr.Mismatched) > 0 || len(vr.Missing) > 0 || len(vr.Extra) > 0
}
```

#### CLI Contract
```bash
# Generate manifest for worktree or directory
g8s manifest create --root <dir> --out <manifest.json> [--algorithm sha256|sha1|md5] [--json]

# Verify directory against existing manifest
g8s manifest verify --root <dir> --manifest <manifest.json> [--allow-extra] [--json]
```
- **Exit Codes**:
  - `0`: All files matched cleanly (no mismatches, missing, or unallowed extra files).
  - `1`: Verification discrepancy detected (mismatched digest, missing file, or extra file).
  - `2`: Operational error (invalid JSON, path traversal attempt, read error).

#### MCP Tool Schema Sketch
- `g8s_manifest_create`: Arguments `{ "root": string, "algorithm"?: "sha256"|"sha1"|"md5", "output_path": string }`.
- `g8s_manifest_verify`: Arguments `{ "root": string, "manifest_path": string, "allow_extra"?: boolean }`.
  - Returns `{ "matched_count": int, "mismatched": [...], "missing": [...], "extra": [...] }`.

#### Deterministic Go Test Cases
- `TestManifest_CreateAndVerify_CleanMatch`: Populates temporary directory with deterministic files, writes manifest, verifies, and asserts `len(Matched) == N` and `!vr.HasFailures()`.
- `TestManifest_PartitionCategories`: Mutates the directory: modifies file A (expect `Mismatched`), removes file B (expect `Missing`), adds file C (expect `Extra`). Asserts all 4 arrays match expected entries exactly.
- `TestManifest_PathTraversalRejection`: Verifies that paths containing `..` or leading slashes are rejected with `ErrInvalidPath`.
- `TestManifest_DeterministicSerialization`: Generates manifest twice on the same directory; asserts identical byte-for-byte JSON output.

---

### Blueprint 2: Declarative Batch Verification Engine

#### File Locations in `g8s`
- `internal/batch/verify.go`: Concurrent verification worker pool, parsing, and aggregation.
- `cmd/g8s/batch.go`: CLI entrypoint (`g8s batch verify`).
- `internal/mcp/batch.go`: MCP tool registration (`g8s_batch_verify`).

#### Core Go Struct Definitions
```go
package batch

import (
	"context"
	"github.com/tamld/g8s/internal/fsops"
)

type BatchInput struct {
	Version int          `json:"version"` // Schema version (1)
	Checks  []BatchCheck `json:"checks"`  // List of files to verify
}

type BatchCheck struct {
	ID        string          `json:"id"`                  // Unique check identifier
	Path      string          `json:"path"`                // File path relative to scope root
	Algorithm fsops.Algorithm `json:"algorithm,omitempty"` // Algorithm (defaults to sha256 or inferred)
	Expected  string          `json:"expected"`            // Expected hex digest
}

type BatchReport struct {
	Summary BatchSummary      `json:"summary"`
	Results []BatchItemResult `json:"results"` // Preserves exact order of BatchInput.Checks
}

type BatchSummary struct {
	Total      int `json:"total"`
	Matched    int `json:"matched"`
