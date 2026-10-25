# OpenSpec DELTA-36: Pure-Go AST Elision, Personalized PageRank, and Multiple-Choice Knapsack Context Compressor

* **Specification ID**: `DELTA-36`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/contextpack`, `internal/mcp`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Flat Context Window Overflow**: Hydrating full source files into `F1` worker prompts (`g8s llm-call --context-files`) wastes 80-90% of token budgets on interior function implementations of satellite files or hits 413/context-window limits on free/local `9router` models.
2. **CGO Portability Violation of C-Based Tree-Sitter**: External Python/C tools using `tree-sitter` C bindings violate `g8s`'s zero-CGO (`CGO_ENABLED=0`) static binary invariant across Linux, macOS, and Windows.
3. **Inline Secret Leakage Before AST Parsing**: Unredacted API keys and PEM private keys inside workspace files risk leaking into LLM context unless scrubbed before AST/lexical elision while preserving exact line numbers (`L123`) for `g8s edit`.

---

## 2. Normative Requirements

### R1 — Pre-AST Line-Preserving Secret Sanitizer (`internal/contextpack/contextpack.go`)
- `SanitizeSecretsPreservingLines(src string)` MUST redact inline API keys (`sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `AIza*`, `xox*`) and multi-line PEM private keys while preserving exact newline counts (`strings.Count(out, "\n") == strings.Count(src, "\n")`).

### R2 — Pure-Go 4-Tier AST & Lexical Elision (`FULL | SKEL | SIGS | TREE`)
- `ElideFile(relPath, sanitizedSrc, tier)` MUST operate with `CGO_ENABLED=0` using `go/parser` + `go/ast` for `.go` files and a pure-Go lexical structural elider for `.ts`/`.js`/`.py`/`.rs`/`.java`/`.c`/`.cs`/`.md` files across 4 tiers: `FULL`, `SKEL` (eliding interior function bodies while keeping package, imports, types, docstrings, and signatures), `SIGS` (signatures + line numbers), and `TREE` (compact file & symbol summary).

### R3 — Personalized PageRank + BM25F + Multiple-Choice Knapsack Optimizer (`Pack`)
- `Pack(req PackRequest)` MUST score files using a symbol/import reference graph with 15-iteration Personalized PageRank ($\alpha = 0.85$) combined with BM25F ($k_1 = 1.2, b = 0.75$) and pinned boost for `AllowedPaths`.
- `solveMCKP` MUST select at most one tier per file via marginal utility density ($\Delta \text{Utility} / \Delta \text{Tokens}$) so that `TotalTokens <= BudgetTokens`.
- Expose via `g8s context-pack`, `g8s llm-call --context-budget <tokens>`, and MCP tool `g8s_context_pack`.

---

## 3. Verification Scenarios

#### Scenario: Pre-AST Secret Sanitizer Preserves Exact Line Numbers
<!-- tests: TestSanitizeSecretsPreservingLines -->
- **GIVEN** source text containing OpenAI, GitHub PAT, and multi-line RSA PEM private keys
- **WHEN** `SanitizeSecretsPreservingLines` is executed
- **THEN** all secrets are replaced with redaction markers and the output newline count equals the input newline count.

#### Scenario: Four-Tier AST Elision and Multiple-Choice Knapsack Token Budget Optimization
<!-- tests: TestElideFileGoAndLexicalTiers, TestPackKnapsackRespectsBudgetAndPrioritizesPinned, TestMCPContextPackTool -->
- **GIVEN** a workspace with a pinned core file (`AllowedPaths: ["core/**"]`) and a large satellite file under a tight token budget (`BudgetTokens: 350`)
- **WHEN** `contextpack.Pack` and MCP `g8s_context_pack` are invoked
- **THEN** `TotalTokens <= BudgetTokens`, the pinned core file receives `FULL` or `SKEL` resolution, and the large satellite file is elided to fit within the token budget.
