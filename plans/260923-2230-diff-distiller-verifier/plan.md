---
title: "Plan: Native Pure-Go Diff Distiller & Verifier Subagent Pipeline"
status: "in-progress"
created: "2026-09-23"
author: "Antigravity Assistant"
priority: "P1"
tags: ["diffintel", "verifier", "aic", "orchestration", "pure-go", "supervisor"]
blockedBy: []
blocks: []
supersedes: ["260923-2216-ocr-integration"]
---

# Plan: Native Pure-Go Diff Distiller & Verifier Subagent Pipeline

## 1. Executive Summary & Problem Definition

### 1.1 Background & Root Cause
In `g8s` v0.2.0, DELTA-18 delivered the automated PR review command `g8s orchestrate-aic --pr <num> --intent <text>`. Currently, `orchestrate-aic` operates naively:
1. Fetches raw git diff via `gh pr diff <pr>`.
2. Concatenates the entire diff into a single string (`combinedIntent`).
3. Sends that raw payload to a single worker via `--from-intent`.

### 1.2 The Problem
* **Token Explosion ("Harness Tax")**: A PR touching `go.sum`, `package-lock.json`, or generated code easily exceeds 3,000–10,000 lines. The LLM consumes massive context window on noise.
* **Line Position Drift**: LLMs evaluating long raw diffs frequently hallucinate line numbers, making comments misleading.
* **Why NOT Alibaba `ocr`?**: While `alibaba/open-code-review` proved that deterministic diff slicing works, importing `ocr` introduces a heavy Node.js/npm runtime tax, nested-agent fragility (LLM calling LLM), potential false-alarm filesystem mutations, and violates the [Axiom of Pure Go & Zero-CGO](spec/constitution.md#L19-L23).

### 1.3 The Solution: Native Pure-Go Diff Distiller + Verifier Subagents
We implement the core innovation of hybrid code review **100% in Pure Go** within `g8s`:
1. **`internal/diffintel`**: A zero-CGO unified diff parser, noise pruner, and context slicer that calculates exact line mappings before AI invocation.
2. **Parallel Verifier Dispatch**: Utilizes `g8s`'s existing `FanOut` orchestrator to dispatch sliced review tasks to worker CLI providers (`agy`, `claude`, `gemini`) under `role: verifier` and `permission: read_only`.
3. **Findings Reducer & Gatekeeper**: Synthesizes structured JSON findings into clear PR digests and hooks into the `Supervisor Fix Loop` (DoD validation).

---

## 2. Invariant Rules & Architectural Boundaries

All work in this plan MUST strictly comply with [g8s Constitution](spec/constitution.md) and [Operating Hard Rules](docs/RULES.md):

| Hard Rule | Architectural Requirement & Enforcement |
| :--- | :--- |
| **Rule 1: Pure-Go Invariant (Zero-CGO)** | 100% Go standard library (`strings`, `bufio`, `regexp`, `go/parser`). No third-party CGO or Node.js bindings. Static binary compiles with `CGO_ENABLED=0`. |
| **Rule 2: Single-Use Capability Receipt** | Verifiers run with `permission: read_only`. No write receipts issued. Zero filesystem mutations allowed. |
| **Rule 3: Process Group Isolation** | Worker invocations use `g8s` process harness with `Setpgid: true` on Unix and `CREATE_NEW_PROCESS_GROUP` on Windows. |
| **Rule 4: Zero-Leakage & Data Hygiene** | Intermediate diff chunks and prompt hashes are sanitized; temporary state adheres to POSIX `0600`/`0700`. |
| **Rule 5: Clock Dependency Injection** | Timeouts and lease calculations use injectable `clock func() time.Time`. |
| **Rule 6: Post-Run Mutation Scan** | Post-execution verification via `DetectReadOnlyContractViolations` ensures verifier workers never mutate files. |
| **Rule 8: Self-Describing Executable** | All new flags (`--strict`, `--min-severity`, `--format`) and envelope outputs conform to `cli.Envelope`. |

---

## 3. High-Level Architecture

```
┌────────────────────────────────────────────────────────────────────────┐
│                        BRAIN TIER / CLI USER                           │
│               g8s orchestrate-aic --pr 42 --intent "Security"          │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                    1. DIFF FETCH & NOISE PRUNING                       │
│  • Fetch raw PR diff via `ghDiffFetcher(pr)`                           │
│  • `diffintel.PruneNoise()`: filters lockfiles, minified, generated    │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   2. PURE-GO CONTEXT SLICING                           │
│  • `diffintel.ParseAndSlice()`:                                        │
│      - Calculates deterministic `OldStart`, `NewStart`, `LineCount`    │
│      - Classifies Risk: High (Auth, Concurrency, SQL) vs Normal        │
│      - Emits `[]ReviewChunk` (max 1500 tokens per chunk)               │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│           3. PARALLEL SUBAGENT DISPATCH (internal/orchestrator)        │
│  • Dispatches chunks via `FanOut` to Worker CLI (agy / claude / gemini)│
│  • Contract: Role `verifier`, Profile `read_only`                      │
│  • Output schema: strictly enforces `{"findings": [...]}` JSON         │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   4. REDUCER & SYNTHESIS (`internal/review`)           │
│  • Aggregates JSON findings, deduplicates, and sorts by severity       │
│  • Emits Markdown Summary table + JSON `cli.Envelope`                  │
│  • Gated Verdict: PASS if 0 Critical/Error, else FAIL (Exit Code 1)    │
│  • Ingestible by Supervisor Fix Loop (DoD / RCA verification)          │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 4. Phase Breakdown & PR Roadmap

### Phase 1: Pure-Go Unified Diff Parser & Slicer (`internal/diffintel`)
**PR Title**: `feat(diffintel): pure-go unified diff parser, noise pruner, and context slicer`  
**Deliverables**:
1. `internal/diffintel/model.go`:
   ```go
   type FileDiff struct {
       OldPath   string
       NewPath   string
       IsNew     bool
       IsDeleted bool
       Hunks     []Hunk
   }

   type Hunk struct {
       OldStart  int
       OldLines  int
       NewStart  int
       NewLines  int
       Header    string // e.g. "func Authenticate(ctx context.Context)"
       Body      string
   }

   type ReviewChunk struct {
       ID          string
       FilePath    string
       Hunks       []Hunk
       TokenEstimate int
       RiskLevel   string // "CRITICAL", "HIGH", "NORMAL"
   }
   ```
2. `internal/diffintel/pruner.go`:
   * Rule-based noise filter: skips `go.sum`, `package-lock.json`, `pnpm-lock.yaml`, `*.pb.go`, `*.min.js`, `*.svg`.
3. `internal/diffintel/parser.go`:
   * Standard library unified diff parser (handles standard `git diff` outputs).
   * Exact line mapping logic to prevent position drift.
4. `internal/diffintel/slicer.go`:
   * Slices multi-file diffs into bounded chunks (default cap: 1500 tokens/chunk).
5. **Testing**:
   * Comprehensive table-driven tests in `internal/diffintel/parser_test.go` with real git diff fixtures.
   * Benchmark tests ensuring parsing < 5ms for 5,000-line diffs.

---

### Phase 2: Verifier Contract & Findings Reducer (`internal/review`)
**PR Title**: `feat(review): finding model, verifier contract prompt, and reducer`  
**Deliverables**:
1. `internal/review/model.go`:
   ```go
   type Severity string
   const (
       SeverityCritical Severity = "CRITICAL"
       SeverityHigh     Severity = "HIGH"
       SeverityMedium   Severity = "MEDIUM"
       SeverityLow      Severity = "LOW"
   )

   type Finding struct {
       File       string   `json:"file"`
       Line       int      `json:"line"`
       Severity   Severity `json:"severity"`
       Category   string   `json:"category"` // e.g. "CONCURRENCY", "SECURITY", "CORRECTNESS"
       Message    string   `json:"message"`
       Suggestion string   `json:"suggestion,omitempty"`
   }

   type ReviewReport struct {
       PR          int       `json:"pr,omitempty"`
       Verdict     string    `json:"verdict"` // "PASS", "FAIL"
       Findings    []Finding `json:"findings"`
       TokensSaved int       `json:"tokens_saved"`
       DurationMs  int64     `json:"duration_ms"`
   }
   ```
2. `internal/review/prompt.go`:
   * Enforces zero-drift, line-exact review prompts for worker subagents.
   * Mandates strict JSON-only outputs.
3. `internal/review/reducer.go`:
   * Deduplicates findings from parallel workers.
   * Sorts findings by Severity: `CRITICAL` > `HIGH` > `MEDIUM` > `LOW`.
   * Formats terminal Markdown table and GitHub PR comment payload.
4. **Testing**:
   * Unit tests for JSON extraction and markdown rendering in `internal/review/reducer_test.go`.

---

### Phase 3: Wire into `orchestrate-aic`
**PR Title**: `feat(aic): wire diffintel and parallel verifier into orchestrate-aic`  
**Deliverables**:
1. Update `cmd/g8s/orchestrate_aic.go`:
   * Integrate `diffintel.PruneNoise()` and `diffintel.ParseAndSlice()`.
   * Dispatches sliced chunks to `orchestrator.FanOut` using `role: verifier` and worker provider.
   * Aggregates findings with `review.Reducer`.
   * Adds flags:
     * `--strict`: fail (Exit code 1) on any `HIGH` or `CRITICAL` finding.
     * `--format`: `markdown`, `json`, `compact`.
2. Unit & Integration Tests:
   * Update `cmd/g8s/orchestrate_intent_test.go` with mock `ghDiffFetcher` and mock worker responses.
   * Test zero-findings scenario (Exit 0).
   * Test critical-findings scenario (Exit 1 with detailed diagnostic report).

---

### Phase 4: Supervisor Fix Loop Integration
**PR Title**: `feat(supervisor): pre-merge semantic review gate using diffintel`  
**Deliverables**:
1. Update `internal/supervisor/reviewer.go`:
   * Extend `RealReviewer` to run `diffintel` verification on worker commits prior to granting `VerdictPass`.
   * If critical security or concurrency flaws are identified, fail the review and pipe the finding directly into the `RCA` (Root Cause Analysis) phase for immediate self-correction.
2. End-to-End Regression Tests:
   * Verify supervisor fix loop catches intentional bugs and generates accurate corrective attempts.

---

## 5. Verification Matrix & Quality Gates

| Verification Gate | Command / Target | Pass Criteria |
| :--- | :--- | :--- |
| **Pure Go Static Build** | `CGO_ENABLED=0 go test -count=1 ./...` | 100% pass across all 31+ packages |
| **Race Detector** | `CGO_ENABLED=1 go test -race -count=1 ./internal/diffintel/... ./internal/review/...` | Zero race warnings |
| **Diff Slicing Benchmarks** | `go test -bench=. ./internal/diffintel/...` | < 10ms for 10k-line diff |
| **CLI Contract** | `g8s orchestrate-aic --help` | Flags `--strict`, `--format`, `--model` present & documented |
| **JSON Envelope** | `g8s orchestrate-aic --pr 100 --json` | Validates against `cli.Envelope` schema |

---

## 6. Actionable Next Steps
1. Create branch `feat/diffintel-core`.
2. Implement Phase 1: `internal/diffintel` (Parser, Pruner, Slicer) with unit tests.
3. Open PR 1 and run dual-pass CI.
