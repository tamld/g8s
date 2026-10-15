# OpenSpec DELTA-18: AIC PR Review Orchestration, DiffDistiller & From-Intent FanOut

* **Specification ID**: `DELTA-18`
* **Title**: AIC Automated PR Review (`g8s orchestrate-aic`), DiffDistiller & From-Intent Sub-Task FanOut
* **Status**: `APPLIED`
* **Milestone**: `M4` (AIC & Intent Orchestration)
* **Target Packages**: `cmd/g8s`, `internal/diffintel`, `internal/review`
* **Tracking Issues**: #86, #106, T022

---

## 1. Context & Motivation

`DELTA-18` delivers automated Pull Request review orchestration (`g8s orchestrate-aic`), unified-diff noise pruning and risk-scored chunk slicing (`internal/diffintel`), multi-reviewer finding reduction (`internal/review`), and deterministic `--from-intent` / `--from-file` sub-task FanOut (`g8s orchestrate`).

---

## 2. ADDED Requirements

### Requirement: Unified Diff Distillation & Risk Scoring (`internal/diffintel`, `internal/review`)

1. `internal/diffintel` SHALL parse unified git diffs (`ParseUnifiedDiff`), prune generated/lockfile noise (`PruneNoise`), and slice hunks into risk-scored chunks (`SliceChunksAndRisk`).
2. `internal/review` SHALL build bounded verifier prompts (`BuildVerifierPrompt`), parse structured JSON findings (`ParseFindings`), and reduce multi-worker findings by severity (`Reduce`, `FormatMarkdown`).

#### Scenario: diff parsing, noise pruning, risk chunking, and review finding reduction
<!-- tests: TestParseUnifiedDiff, TestPruneNoise, TestSliceChunksAndRisk, TestBuildVerifierPrompt, TestParseFindings, TestReduceStrictVsNonStrict, TestFormatMarkdown -->
- Lockfiles and generated artifacts are pruned from diffs, high-risk hunks are prioritized, and reviewer findings are deduplicated and formatted deterministically.

### Requirement: AIC Automated PR Review & From-Intent FanOut (`cmd/g8s`)

1. `g8s orchestrate-aic --pr <num> --intent <text>` SHALL fetch the PR diff, distill high-signal chunks, dispatch bounded `verifier` tasks, and emit a structured review envelope (returning early with a clean verdict when all diff hunks are pruned as noise).
2. `g8s orchestrate --from-intent <text>` and `--from-file <path>` SHALL split natural-language intent into bounded `collector` (`read_only`) sub-tasks and execute them via `orchestrator.FanOut`.

#### Scenario: orchestrate-aic PR review and from-intent FanOut execution
<!-- tests: TestOrchestrateAICWrapper, TestOrchestrateAICCleanDiffNoisePruned, TestRunOrchestrateAICCLI, TestOrchestrateIntentSplitMultiLine, TestOrchestrateIntentFromFile, TestOrchestrateIntentEmptyReturnsUsageError -->
- `orchestrate-aic` distills diffs and dispatches verifier runs; `--from-intent` and `--from-file` fan out sub-tasks and reject empty intents with exit code 2.
