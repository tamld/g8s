---
title: "Integration Plan: Alibaba Open Code Review (ocr) Engine for g8s"
status: "superseded"
created: "2026-09-23"
supersededBy: "plans/260923-2230-diff-distiller-verifier/plan.md"
author: "Antigravity Pair Programmer"
priority: "P2"
tags: ["review-engine", "ocr", "aic", "orchestration", "supervisor"]
blockedBy: []
blocks: []
---

# Integration Plan: Alibaba Open Code Review (`ocr`) for g8s

## 1. Executive Summary & Problem Statement

### 1.1 Context
In `g8s` v0.2.0, DELTA-18 delivered automated PR review via `g8s orchestrate-aic --pr <num> --intent <text>`. Currently, `orchestrate-aic` operates naively by fetching the PR diff via `gh pr diff <pr>` and appending the raw diff directly into a combined prompt (`combinedIntent`) dispatched to general-purpose LLM workers via `--from-intent`.

### 1.2 The Problem
1. **The "Harness Tax" & Token Explosion**: Dumps of large git diffs (hundreds/thousands of lines) into generic LLM prompts consume massive token context windows and incur high API costs.
2. **Line Position Drift**: Small/fast models (Gemini Flash, Claude Haiku) frequently hallucinate or miscalculate line offsets when parsing raw unified diffs in natural language.
3. **Low Precision / High False Positive Rate**: General LLMs produce verbose, generic stylistic comments rather than detecting critical enterprise bugs (NPE, concurrency races, resource leaks, SQLi).

### 1.3 The Solution: Alibaba `ocr` Integration
Integrate **Alibaba Open Code Review (`ocr`)** as a specialized **Deterministic Review Engine** within `g8s`. 
`ocr` combines a deterministic AST & diff parsing pipeline with targeted semantic checks, consuming **~1/9th the tokens** of general agents while guaranteeing zero line-position drift and high precision.

---

## 2. Invariant Rules & Architectural Boundaries

Any implementation MUST adhere to the [g8s Constitution](spec/constitution.md) and [Operating Hard Rules](docs/RULES.md):

| Rule | Enforcement Strategy for `ocr` Integration |
| :--- | :--- |
| **Rule 1: Pure-Go (Zero-CGO)** | **NO CGO, NO embedded Node.js runtime**. `ocr` is invoked strictly as an external CLI binary (`exec.LookPath("ocr")`). `g8s` compiles static binaries with `CGO_ENABLED=0`. |
| **Rule 3: Process Group Isolation** | `ocr` executions are isolated in a dedicated OS Process Group (`Setpgid: true` on Unix, `CREATE_NEW_PROCESS_GROUP` on Windows) to prevent zombie processes on timeouts. |
| **Rule 4: Zero-Leakage & Data Hygiene** | Temporary diff files passed to `ocr` reside strictly in `state_dir/scratch/` with POSIX `0600`/`0700` permissions and are immediately purged post-run. |
| **Rule 5: Clock Dependency Injection** | All timeout calculations and TTL handling use an injectable `clock func() time.Time`. |
| **Rule 6: Post-Run Mutation Scan** | `ocr` runs strictly under `read_only` permission profile. Any unauthorized file writes trigger exit code 3 (`READ_ONLY_CONTRACT_EXIT`). |
| **Rule 8: Self-Describing Executable** | All new flags (`--engine`), enums, and JSON outputs must be discoverable via `--help` and `--json`. |

---

## 3. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                 Brain Tier / Developer CLI                  │
│             g8s orchestrate-aic --pr 42 --engine auto       │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                       cmd/g8s/                              │
│   1. Fetch diff via ghDiffFetcher seam                      │
│   2. Resolve Engine (ocr available? -> ocr : fallback llm)  │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│             internal/review/ (New Package)                  │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ ReviewEngine Interface                                │  │
│  │  - ReviewDiff(ctx, diff, opts) (*ReviewReport, error) │  │
│  └───────────────────────────┬───────────────────────────┘  │
│                              │                              │
│  ┌───────────────────────────▼───────────────────────────┐  │
│  │ OCRAdapter (Worker Process Group Runner)              │  │
│  │  - Spawns: `ocr review --format json`                 │  │
│  │  - Sandboxed Process Group + Context Timeout          │  │
│  │  - Parses JSON output -> []ReviewFinding              │  │
│  └───────────────────────────────────────────────────────┘  │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│             Evidence Lake & Structured Output               │
│  - Formatted Markdown Summary to stdout / PR Comment        │
│  - JSON Envelope (cli.Envelope) with findings               │
│  - Ingestible by Supervisor Fix Loop (DoD / RCA verification)│
└─────────────────────────────────────────────────────────────┘
```

---

## 4. Phased PR Implementation Roadmap

We break the delivery into **2 cleanly decoupled PRs** to ensure atomic reviewability, zero regressions, and strict test coverage.

### Phase 1: Core Engine & OCR Adapter (PR 1)
**Branch**: `feat/review-engine-core`  
**PR Title**: `feat(review): introduce ReviewEngine contract and Alibaba ocr process adapter`  
**Target Milestone**: v0.2.1 / v0.3.0  

#### Scope of PR 1:
1. **Create `internal/review/model.go`**:
   * Data structures:
     ```go
     type Severity string
     const (
         SeverityCritical Severity = "CRITICAL"
         SeverityError    Severity = "ERROR"
         SeverityWarning  Severity = "WARNING"
         SeverityInfo     Severity = "INFO"
     )

     type ReviewFinding struct {
         File        string   `json:"file"`
         StartLine   int      `json:"start_line"`
         EndLine     int      `json:"end_line"`
         RuleID      string   `json:"rule_id"`
         Severity    Severity `json:"severity"`
         Message     string   `json:"message"`
         Suggestion  string   `json:"suggestion,omitempty"`
         Category    string   `json:"category,omitempty"` // security, concurrency, correctness
     }

     type ReviewReport struct {
         Engine      string          `json:"engine"`
         Findings    []ReviewFinding `json:"findings"`
         TotalIssues int             `json:"total_issues"`
         DurationMs  int64           `json:"duration_ms"`
         RawOutput   string          `json:"raw_output,omitempty"`
     }
     ```
2. **Create `internal/review/engine.go`**:
   * Interface declaration:
     ```go
     type ReviewEngine interface {
         Name() string
         IsAvailable() bool
         ReviewDiff(ctx context.Context, diffContent string, opts ReviewOptions) (*ReviewReport, error)
     }
     ```
3. **Implement `internal/review/ocr_adapter.go`**:
   * Binary resolution via `exec.LookPath("ocr")` (with override via `G8S_OCR_BIN`).
   * Spawns subprocess with `syscall.SysProcAttr{Setpgid: true}` on Unix / `CREATE_NEW_PROCESS_GROUP` on Windows.
   * Handles stdout JSON parsing into `ReviewReport`.
   * Test seam for subprocess execution (`execCommandContext` func var) to allow 100% deterministic mock testing.
4. **Register in `internal/provider/catalog.go`**:
   * Add `ocr` to provider catalog with install hint: `npm install -g @alibaba-group/open-code-review` / `brew install open-code-review`.
5. **Unit Tests (`internal/review/ocr_adapter_test.go`)**:
   * Test successful parsing of `ocr` JSON fixtures.
   * Test error states: exit code != 0, unparseable JSON, context timeout (SIGTERM to process group).
   * Test binary not found (graceful error / `IsAvailable() == false`).

---

### Phase 2: CLI Integration & Orchestration Hook (PR 2)
**Branch**: `feat/orchestrate-aic-ocr`  
**PR Title**: `feat(aic): add --engine flag and ocr review engine to orchestrate-aic`  
**Target Milestone**: v0.2.1 / v0.3.0  

#### Scope of PR 2:
1. **Update `cmd/g8s/orchestrate_aic.go`**:
   * Add flag: `--engine` (enum: `auto`, `ocr`, `llm`, default: `auto`).
   * Engine Resolution Logic:
     ```go
     // If engine == "auto":
     //   check if ocr.IsAvailable() -> use "ocr", else fallback to "llm".
     // If engine == "ocr" and not available -> return exit code 2 with install hint.
     ```
   * Fast-path execution for `ocr`:
     * Directly passes fetched PR diff to `review.ReviewDiff()`.
     * Formats review findings into clean human-readable table / Markdown digest, or JSON envelope if `--json` is supplied.
     * Computes PR status: `PASSED` if 0 critical/error findings; `FAILED` if blocking bugs found.
2. **Supervisor Integration Hook**:
   * Expose findings for the Supervisor Fix Loop (`internal/supervisor/reviewer.go`):
     * When reviewer checks a newly written fix, if `ocr` is enabled, run `ReviewDiff` against unstaged changes.
     * If high-severity bugs (NPE, Race) are found, automatically fail review and generate RCA context without spending LLM tokens.
3. **Tests (`cmd/g8s/orchestrate_intent_test.go`)**:
   * Mock `ghDiffFetcher` and `reviewEngine` stub.
   * Test `--engine ocr` with clean diff -> Exit 0.
   * Test `--engine ocr` with bug findings -> Exit 0 with structured findings envelope.
   * Test `--engine ocr` when `ocr` missing -> Proper error message and exit code.
   * Test `--engine auto` fallback behavior.
4. **Documentation**:
   * Update `docs/user-guide/cli-reference.md`.
   * Update `README.md` (mention `ocr` hybrid review engine in DELTA-18 / AIC section).

---

## 5. Risk Assessment & Mitigations

| Risk | Impact | Likelihood | Mitigation |
| :--- | :--- | :--- | :--- |
| **User does not have `ocr` installed** | High | High | Default to `--engine auto`. If `ocr` is not in `$PATH`, seamlessly fall back to existing LLM prompt review. Display clear `InstallHint`. |
| **`ocr` CLI output schema changes in newer versions** | Medium | Medium | Implement defensive JSON deserialization with fallback to line-by-line regex scanning if root JSON structure differs. |
| **Large PR diffs exceeding pipe buffer** | Medium | Low | Use temporary scratch files with strict `0600` permissions instead of stdin if diff size > 512KB. Ensure immediate cleanup. |
| **Long-running `ocr` process hangs** | Medium | Low | Hard process group context timeout (default 120s, configurable via `--timeout`). Subprocess killed cleanly via `pgid`. |

---

## 6. Definition of Done (DoD) & Verification Matrix

- [ ] `CGO_ENABLED=0 go test -count=1 ./...` passes on macOS, Linux, and Windows.
- [ ] `CGO_ENABLED=1 go test -race ./internal/review/...` runs with 0 race detector warnings.
- [ ] CLI command `g8s orchestrate-aic --help` displays `--engine` options (`auto`, `ocr`, `llm`).
- [ ] `g8s orchestrate-aic --json` outputs standard `cli.Envelope` matching schema.
- [ ] `g8s providers recommend` displays Alibaba `ocr` with correct install commands.
- [ ] Zero dynamic runtime dependencies added to Go module (`go.mod` remains clean, no Node bindings).
