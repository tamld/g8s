# Round 1 Concept Harvesting & Multi-Agent Debate: `aegis` -> `g8s`

> **Session type**: T1 (strategy)

**Document ID**: `plans/handoffs/r1-ingest-aegis.md`  
**Author**: F1 High-Reasoning Middle Manager (`3c95c1d2-d52f-45f9-a126-e31407452f77`)  
**Authorized WriteReceipt**: `ed4a3f75-77fb-4efc-88a2-41caa46a925b`  
**Target Sibling Repository**: `$HOME/Documents/github/aegis`  
**Host Platform**: `$HOME/Documents/github/g8s`  
**Date**: 2026-10-21  

---

## 1. Executive Summary & Core Thesis for `aegis` -> `g8s`

### 1.1 Sibling Domain Analysis: What is `aegis`?
`aegis` (formerly AAOS — *Antigravity Autonomous Agent Operating System*, package `@tamld/aegis`) is a local-first governance middleware framework built in TypeScript/Node.js for autonomous AI coding agents. Born out of prolonged operational battles with agentic hallucinations, hollow compliance theater, and regression loops, `aegis` embodies the principle: *"Your AI writes the code. Aegis makes sure it doesn't burn the house down."*

Its architecture spans:
1. **Perimeter Isolation**: Worktree-per-ticket lifecycle via `aegis ticket claim`.
2. **Proportionate Governance**: A 7-state finite state machine (`INIT → PLANNING → EXECUTING → VERIFYING → REFLECTING → FINALIZING → DONE`) parameterized by risk tiers (`EXPRESS`, `STANDARD`, `FORTRESS`).
3. **Empirical Anti-Theater**: Hard-coded machine guards detecting hollow scaffolding (`TODO: Implementation`, ghost files, empty test runs).
4. **Case Law Memory**: Offline SQLite lesson storage with HMAC-SHA256 append-only chaining and `lessons_blocked_repeat` metric tracking.
5. **Machine-Readable Governance**: Executable negative constraints (`docs/specs/DND.md`) with explicit banishment of unread prose essays (`DND-101`, `DND-102`).

### 1.2 Strategic Philosophy Alignment with F0 (Brain)
`g8s` is a Pure-Go, Zero-CGO, single-binary **Agent Execution & LLM Call Platform** (`CLI + MCP + HTTP`).

**The Core Axiom**: `g8s` builds an empowering foundation for LLM calls and multi-agent execution — **NOT a bureaucratic shackle ("không phải gọng kiềm siết chết khả năng của Agent")**. Security and containment live at the perimeter (`WriteReceipt`, worktree isolation, deterministic acceptance verifiers), while inside that perimeter agents must enjoy maximum cognitive and tool autonomy.

### 1.3 The F1 Core Thesis
While `aegis` contains brilliant operational insights, its current manifestation is heavily encumbered by **bureaucratic ritual, state explosion, and language-specific sprawl** (Node.js runtime, 400+ line workflow FSMs, and AST import-graph blast radius calculators). If imported blindly, these mechanisms would strangle agent cognitive velocity and violate g8s's constitutional laws.

Therefore, our **Round-1 harvesting filter** separates:
- **Procedural Micromanagement (REJECTED)**: Forcing agents through rigid 7-state workflows, blocking mid-task discovery with brittle plan-vs-diff checkers, and turning memory into a punitive blocker gate.
- **High-Leverage Perimeter Mechanics (ADOPTED)**: Stateless, deterministic, Pure-Go perimeter verifiers:
  1. **Empirical Anti-Theater & Ghost/Stub Verification**: Catching fake implementations, unexecuted test suites, and empty receipts at the boundary before accepting worker deliverables.
  2. **Tamper-Evident HMAC-SHA256 Chained Audit Trails**: Pure-Go cryptographic chaining for receipts and execution events, guaranteeing crash resilience and tamper detection with zero external databases.
  3. **Dynamic Ceremony Scaling**: Replacing rigid FSM tiers with lightweight effort-class and lane routing that frees simple agent calls from unnecessary verification overhead.

---

## 2. F2 Team Roster & Debate Log (Evidence of `F1 -> n F2` Orchestration)

To rigorously dissect `aegis` without personal bias, F1 assembled and orchestrated a heterogeneous team of **4 F2 workers** combining platform-native subagents and multi-model `9router` specialists.

### 2.1 F2 Specialist Roster

| Worker ID / Trace ID | Agent Role | Model / Engine | Scope & Files Inspected | Latency / Tokens | Primary Deliverable / Stance |
|---|---|---|---|---|---|
| **F2-Inspector** (`1b12a613-d4c8-4be3-a715-d6cfa9735aef`) | Codebase & Architecture Researcher | Platform Native (`Gemini Flash` inherit) | `aegis` (`README.md`, `MANIFEST.yml`, `.g8s/`, `ops/ag-cli/`, `knowledge/`, `docs/specs/`) vs `g8s` (`internal/`, `cmd/`) | 95 steps / ~120k tokens context | Comprehensive architectural briefing; extracted 7 candidate mechanisms with line-level file citations. |
| **F2-Harvester** (`01a15eb0-dac8-723e-866d-1df326724483`) | Concept Harvester | `cx/gpt-6-luna` (`9router`, low effort) | `README.md`, `docs/specs/FSM.md`, `DOD.yaml`, `DND.md`, `DOR.yaml` | 8,625 ms / 4,212 in, 367 out | Proposed 4 primary mechanisms: ticket worktrees, risk-priced FSM, plan-bounded diffs, and Case Law memory. |
| **F2-Skeptic** (`01a15eb1-1d26-78dd-8c41-a87b37c5053e`) | Devil's Advocate / Skeptic | `reasoning-high` (`nvidia/nemotron-3-ultra-550b-a55b`) | `README.md`, `docs/specs/FSM.md`, `DOD.yaml`, `DND.md`, `DOR.yaml` | 21,774 ms / 6,859 in, 2,048 out | Aggressive falsification against g8s Core Laws. Issued "kill shot" on 7-state FSM; critiqued plan-diff as discovery killer; supported pure-Go HMAC. |
| **F2-Architect** (`01a15eb1-a391-7b0c-8b86-9c25d8fff0a9`) | Systems Architect | `deep-coding` (`nvidia/nemotron-3-ultra-550b-a55b`) | `docs/specs/DND.md`, `DOD.yaml`, `ops/ag-cli/services/zero-theater-guard.ts` | 28,732 ms / 5,984 in, 2,048 out | Designed pure-Go package layout, interfaces, and unit tests for `internal/verifier/theater.go`. |

---

### 2.2 Summary of the F2 Multi-Agent Debate & F1 Adjudication

The debate centered on four core tensions between maximizing agent capability and preventing catastrophic defects.

```
       [F2-Harvester]                                  [F2-Skeptic]
  "Adopt 7-state FSM &                           "Kill shot: FSM is a
   Plan-diff constraints to                       bureaucratic straitjacket.
   prevent agent drift!"                          Diff ⊆ plan kills discovery!"
               \                                       /
                \                                     /
                 ▼                                   ▼
              ┌─────────────────────────────────────────┐
              │          F1 Middle Manager              │
              │             ADJUDICATION                │
              │  - Reject rigid FSM & plan diff blocks  │
              │  - Adopt perimeter Anti-Theater guard   │
              │  - Adopt Pure-Go HMAC audit chaining    │
              └─────────────────────────────────────────┘
```

#### Clash 1: 7-State Workflow FSM & Risk-Priced Tiers (`FSM.md`)
- **F2-Harvester's Argument**: Unconstrained LLMs flounder when given open-ended tasks. Aegis's 7-state FSM (`INIT → PLANNING → EXECUTING → VERIFYING → REFLECTING → FINALIZING → DONE`) with risk-priced paths (`EXPRESS`, `STANDARD`, `FORTRESS`) provides clear guardrails and prevents unverified merges.
- **F2-Skeptic's Rebuttal**: *"Kill shot."* Forcing agents through 4 to 7 ceremonial stops with static AST blast-radius pricing is the textbook definition of a bureaucratic straitjacket ("gọng kiềm siết chết khả năng của Agent"). It adds massive state machine bloat (400+ LOC in TypeScript) and generates friction on every step. Furthermore, `g8s` already handles task lifecycles through its linear worker queue and verification pipeline.
- **F1 Adjudication (REJECTED)**: F1 sides fully with F2-Skeptic. `g8s` must never shackle agents with intermediate ceremonial state transitions. Inside the perimeter, the agent should explore, execute, and verify without asking permission at every micro-phase. Dynamic scaling should be handled purely at dispatch time via effort classes (`low/medium/high`) and ALDC lanes (`0/D/R/F/S`), requiring zero intermediate FSM overhead.

#### Clash 2: Plan-Bounded Mutations (`PlanDiffVerifier`: `diff ⊆ plan markers`)
- **F2-Harvester's Argument**: LLMs suffer from "hallucinated completion" and "scope drift" (touching unrelated files). Aegis's `PlanDiffVerifier` parses `[MODIFY|NEW|DELETE]` markers from the implementation plan and mechanically verifies that the git diff does not exceed the declared plan.
- **F2-Skeptic's Rebuttal**: `diff ⊆ plan` destroys agent discovery. In complex engineering tasks, an agent often discovers mid-implementation that an unmentioned helper or configuration must be touched. If the harness hard-blocks on `UNPLANNED_CHANGE`, the agent is paralyzed and forced into a tedious re-planning loop.
- **F1 Adjudication (REDIRECTED / SCOPED TO WRITE RECEIPT)**: Hard-blocking on an implementation plan prose file is Goodhart-bait. Instead, `g8s` already enforces the true hard boundary at the perimeter via `WriteReceipt.AllowedPaths`. However, F2-Harvester correctly identified a real failure mode: workers claiming success while omitting key promised files. Therefore, we redirect this concept into an **advisory check** and **perimeter completeness check** inside `g8s verify`, never an in-flight execution blocker.

#### Clash 3: Case Law Memory & "Blocked Repeat" Gates
- **F2-Harvester's Argument**: Aegis's Case Law memory captures failures and blocks repeated mistakes, tracking efficacy via `lessons_blocked_repeat.jsonl`.
- **F2-Skeptic's Rebuttal**: Blocking an agent simply because a past lesson matches the current context penalizes valid retries and novel approaches. Furthermore, maintaining SQLite/pgvector stores for lessons adds binary and operational weight.
- **F1 Adjudication (REDIRECTED TO RETRIEVAL / TELEMETRY)**: Memory must **empower**, never **punish**. We firmly reject making memory a blocking gate. However, two underlying mechanisms are exceptional:
  1. *Telemetry Metric*: Emitting a metric event when past lessons were consulted before a verification pass.
  2. *Context Injection*: Using Jev / System-1 Reflex to inject top-matching lessons as informational context hints during prompt synthesis.

#### Clash 4: Anti-Theater & Ghost Code Detection (`ZeroTheaterGuard`)
- **F2-Harvester & F2-Architect Consensus**: LLMs frequently produce "SUCCEEDED theater" (passing empty test runs, committing `TODO: Implementation` stubs, or staging ghost files that don't exist). Aegis catches this mechanically.
- **F2-Skeptic's Assessment**: Anti-theater operates strictly at the perimeter boundary. It does not dictate how the agent reasons inside; it merely checks the deliverable before issuing an acceptance verdict. It is 100% compliant with Law 1 (pure Go, zero-CGO, fast string/stat checks) and Law 3 (empowering perimeter check).
- **F1 Adjudication (ADOPTED)**: Unanimously approved for implementation blueprint in Round 2.

---

## 3. Round-1 Candidate Classification Matrix (3-Bucket Routing)

To ensure clean architectural separation and adhere to g8s's Two-Layer Separation Rule, all harvested candidates are categorized into three distinct operational buckets:

```
┌────────────────────────────────────────────────────────────────────────┐
│                      3-BUCKET ROUTING MATRIX                           │
├────────────────────────────────────────────────────────────────────────┤
│ Bucket A: Core Go Engine & Platform (cmd/g8s, internal/*)              │
│   → Pure-Go, Zero-CGO, single-binary perimeter mechanisms              │
├────────────────────────────────────────────────────────────────────────┤
│ Bucket B: Offer Bundle & Declarative Governance (offer/, .g8s/*.yml)   │
│   → Repo-agnostic CI scripts, cross-repo registries, sibling adapters  │
├────────────────────────────────────────────────────────────────────────┤
│ Bucket C: Local Untracked Operator Skills (~/.agents/skills/)          │
│   → Operator policies, prompt synthesis, private network configs       │
└────────────────────────────────────────────────────────────────────────┘
```

| ID | Candidate Mechanism | Aegis SSoT Citation | Target Bucket | Rationale & Mechanical Scope in `g8s` |
|---|---|---|---|---|
| **M-01** | **Empirical Anti-Theater Guard** | `ops/ag-cli/services/zero-theater-guard.ts` | **Bucket A** (`internal/verifier/`) | Pure-Go verification gate in `g8s verify`: detects `TODO` stubs, `NotImplementedError`, ghost files, and 0-test evidence runs before accepting worker tasks. |
| **M-02** | **Tamper-Evident HMAC Chaining** | `ops/ag-cli/services/memory/memory-hmac.ts` | **Bucket A** (`internal/receipt/`, `internal/telemetry/`) | Pure-Go cryptographic chaining (`crypto/hmac` + `crypto/sha256`) for WriteReceipts and execution logs to ensure crash resilience and tamper detection. |
| **M-03** | **Dynamic Ceremony Scaling** | `ops/ag-cli/core/governance-tier.ts:90` | **Bucket A** (`cmd/g8s/submit.go`) | Scales verification rigors and timeout budgets according to declared effort class and ALDC lane without requiring an FSM state machine. |
| **M-04** | **TypeScript Structure-Sync Gate** | `.g8s/lane-bundles.yml:12` (unadopted note) | **Bucket B** (`offer/gates/`, `tools/`) | Aegis could not adopt `ci_structure_sync.sh` because the renderer was Go-specific (`go list`). Porting a generic/TS-aware renderer unblocks sibling adoption. |
| **M-05** | **Declarative DND (Do-Not-Do) Registry** | `docs/specs/DND.md` | **Bucket B** (`.g8s/dnd-rules.yml`) | Declarative schema for negative repo-level constraints enforced across CI gates (`ci_link_integrity.sh`, `gitleaks`, root-cleanliness). |
| **M-06** | **`lessons_blocked_repeat` Telemetry** | `ops/ag-cli/services/memory/blocked-repeat.ts` | **Bucket B** (`offer/telemetry/`) | Standardized schema for measuring the ROI of memory when a gate catches a defect matching an existing lesson. |
| **M-07** | **Evidence-Anchored Prompt Scaffolder** | `ops/ag-cli/core/context-router.ts:287` | **Bucket C** (`~/.agents/skills/`) | Operator skill pre-flight: scans user prompts for concrete anchors (ticket IDs, commit SHAs, file paths); downscales to investigative triage if anchors are missing. |
| **M-08** | **Mercenary Dogfood Return Protocols** | `records/evidence/m2-g8s-dogfood.md` | **Bucket C** (`~/.agents/skills/g8s-supervisor/`) | Formalized supervisory playbooks for executing mercenary sessions in sibling repositories and returning defects/lessons to g8s. |

---

## 4. Explicitly Rejected Ideas (`REJECTED AT F1`)

In strict accordance with F0's strategic directive, we actively protect `g8s` from cognitive shackles and architectural bloat. The following mechanisms from `aegis` are **firmly rejected** from entering the `g8s` core engine:

### Rejection 1: The 7-State / 14-State Workflow FSM & Risk-Priced Tiers
- **Aegis Source**: [`docs/specs/FSM.md`](../aegis/docs/specs/FSM.md), [`ops/ag-cli/core/workflow.ts`](../aegis/ops/ag-cli/core/workflow.ts)
- **Concrete Technical Reason for Rejection**:
  1. *Destroys Cognitive Velocity*: Forcing an AI agent through sequential state transitions (`INIT → PLANNING → EXECUTING → VERIFYING → REFLECTING → FINALIZING → DONE`) introduces massive latency, token churn, and repetitive handoffs.
  2. *AST Blast-Radius Fragility*: Calculating blast radius by parsing TypeScript AST import trees (`code-review-graph` / TypeScript compiler API) is language-specific, slow, and cannot be compiled into a lean, zero-CGO Go binary.
  3. *Redundant*: `g8s` already handles task dispatch, CAS leasing, and result verification via clean, stateless CLI/MCP primitives (`g8s submit`, `g8s worker`, `g8s verify`). Adding an FSM layer violates Law 1 and Law 3.

### Rejection 2: Memory-Enforced Blocker Gates ("Lesson Existed -> Gate Blocks Repeat")
- **Aegis Source**: [`ops/ag-cli/services/zero-theater-guard.ts:147-160`](../aegis/ops/ag-cli/services/zero-theater-guard.ts#L147-L160)
- **Concrete Technical Reason for Rejection**:
  1. *Perverse Penalty on Adaptation*: Blocking an agent's submission because a vector or text search matched a historical lesson assumes the agent is making the identical mistake. In reality, agents frequently employ novel patterns that contain similar keywords or touch the same subsystem.
  2. *External DB Dependency*: Querying PostgreSQL/pgvector or maintaining large SQLite FTS indices in the critical verification path violates the zero-dependency philosophy.
  3. *Philosophical Misalignment*: Memory should **inform and guide** (via context injection), never **act as a blunt censor**.

### Rejection 3: Hard-Blocking Plan-Diff Verifier (`diff ⊆ plan`)
- **Aegis Source**: [`ops/ag-cli/services/plan-diff-verifier.ts:28-42`](../aegis/ops/ag-cli/services/plan-diff-verifier.ts#L28-L42)
- **Concrete Technical Reason for Rejection**:
  1. *Kills Autonomous Discovery*: Engineering requires iteration. An agent discovering a necessary refactor or bugfix mid-implementation should not be failed because the file was not declared in the upfront markdown plan.
  2. *Goodhart's Law Bait*: Agents learn to game `diff ⊆ plan` by declaring wildcards or over-broad file lists in their plans, turning the plan into meaningless compliance prose.
  3. *Existing Primitive is Superior*: `g8s`'s `WriteReceipt.AllowedPaths` already sets an unbypassable filesystem boundary at the OS/tool level. Verifying against markdown prose adds paper bureaucracy without security value.

---

## 5. Top 1–2 Implementation Blueprints for Round 2 Review

### Blueprint 1: Empirical Anti-Theater & Ghost/Stub Verifier at Perimeter

#### 5.1 Motivation & Mechanics
Ensures that worker deliverables represent genuine, executed engineering work rather than hollow LLM compliance theater. Runs during `g8s verify --task <id>` (or standalone `g8s verify --theater-check`).

#### 5.2 Go Struct & Interface Contracts (`internal/verifier/theater.go`)

```go
package verifier

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TheaterViolationCode represents deterministic anti-theater violation categories.
type TheaterViolationCode string

const (
	CodeGhostFile      TheaterViolationCode = "GHOST_FILE"       // Staged in git index but missing on disk
	CodeStubCode       TheaterViolationCode = "STUB_CODE"        // Contains TODO/NotImplemented placeholders
	CodeEmptyTestRun   TheaterViolationCode = "EMPTY_TEST_RUN"   // Evidence shows 0 executed tests
	CodeMissingTestRun TheaterViolationCode = "MISSING_TEST_RUN" // Code modified without test execution
)

// TheaterViolation describes a detected compliance failure.
type TheaterViolation struct {
	Code    TheaterViolationCode `json:"code"`
	Path    string               `json:"path"`
	Message string               `json:"message"`
}

// TheaterCheckResult contains the aggregate outcome of the anti-theater check.
type TheaterCheckResult struct {
	Passed     bool               `json:"passed"`
	Violations []TheaterViolation `json:"violations,omitempty"`
}

// TheaterGuard inspects a worktree before receipt sealing.
type TheaterGuard struct {
	RepoRoot     string
	StubMarkers  []string
	RequireTests bool
}

// NewTheaterGuard instantiates a default pure-Go theater guard.
func NewTheaterGuard(repoRoot string) *TheaterGuard {
	return &TheaterGuard{
		RepoRoot: repoRoot,
		StubMarkers: []string{
			"TODO: Implementation",
			"NotImplementedError",
			"panic(\"not implemented\")",
			"/* STUB */",
			"// STUB: PENDING",
		},
		RequireTests: true,
	}
}

// CheckWorktree scans changed files and evidence for compliance theater.
func (g *TheaterGuard) CheckWorktree(ctx context.Context, changedFiles []string, evidencePath string) TheaterCheckResult {
	var violations []TheaterViolation

	for _, relPath := range changedFiles {
		absPath := filepath.Join(g.RepoRoot, relPath)
		info, err := os.Stat(absPath)
		if err != nil {
			violations = append(violations, TheaterViolation{
				Code:    CodeGhostFile,
				Path:    relPath,
				Message: "Declared file does not exist on disk",
			})
			continue
		}

		if info.IsDir() || !isSourceCode(relPath) {
			continue
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		contentStr := string(content)
		for _, marker := range g.StubMarkers {
			if strings.Contains(contentStr, marker) {
				violations = append(violations, TheaterViolation{
					Code:    CodeStubCode,
					Path:    relPath,
					Message: "Contains unfinished stub marker: " + marker,
				})
				break
			}
		}
	}

	return TheaterCheckResult{
		Passed:     len(violations) == 0,
		Violations: violations,
	}
}

func isSourceCode(p string) bool {
	ext := filepath.Ext(p)
	return ext == ".go" || ext == ".ts" || ext == ".js" || ext == ".py" || ext == ".rs"
}
```

#### 5.3 CLI & Machine-Readable Output Contract
```bash
# Executed by F0 supervisor or CI gate
$ g8s verify --task d91b4eb2 --theater-check --json

{
  "v": 1,
  "kind": "verdict",
  "cmd": "verify",
  "data": {
    "task_id": "d91b4eb2",
    "theater_check": {
      "passed": true,
      "violations": []
    },
    "outcome": "PASS",
    "status": "clean"
  },
  "trace_id": "01a15eb1-a391-7b0c-8b86-9c25d8fff0a9",
  "at": "2026-10-21T13:45:00Z"
}
```

---

### Blueprint 2: Tamper-Evident HMAC-SHA256 Append-Only Audit Trail
