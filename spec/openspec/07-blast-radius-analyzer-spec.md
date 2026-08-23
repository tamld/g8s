# OpenSpec DELTA-07: LSP & Dependency Blast Radius Analyzer

**Status**: `PROPOSED`  
**Milestone**: M2 (Advanced Capabilities)  
**Package**: `internal/analyzer`  

---

## 1. Goal & Context
Empower the Supervisor (Brain) with deterministic **Blast Radius Intelligence** before issuing Write Receipts. By connecting to standard Language Server Protocol (LSP) servers (such as `gopls`, `pyright`, `vtsls`, `rust-analyzer`) and computing AST dependency graphs, `g8s` quantifies the exact structural impact of a proposed code change.

## 2. Core Capabilities

1. **Symbol Reference & Call Hierarchy Traversal**:
   - Query incoming/outgoing call graphs for any target function, struct, or interface.
   - Map all downstream call sites that depend on the modified symbol.

2. **Blast Radius Index (BRI)**:
   - Compute a quantitative risk score based on:
     $$\text{BRI} = \sum_{f \in \text{AffectedFiles}} \text{Weight}(f) \times \text{CallSiteCount}(f)$$
   - Categorize impact: `LOW` (isolated to 1 file/tests), `MEDIUM` (2-5 downstream callers), `HIGH` (>5 callers or cross-boundary packages), `CRITICAL` (core security/governance modules).

3. **Auto-Generated Receipt Scopes**:
   - Automatically recommend optimal `allowed_paths` globs based on the computed dependency tree.

4. **Post-Mutation Diagnostic Verification**:
   - Query LSP `textDocument/publishDiagnostics` after worker edits to detect compiler/type errors before Brain review.

## 3. Interface Definition

```go
package analyzer

type BlastRadiusReport struct {
    TargetSymbol       string   `json:"target_symbol"`
    TargetFile         string   `json:"target_file"`
    RiskLevel          string   `json:"risk_level"` // LOW, MEDIUM, HIGH, CRITICAL
    BlastRadiusScore   float64  `json:"blast_radius_score"`
    DirectCallers      []string `json:"direct_callers"`
    AffectedFiles      []string `json:"affected_files"`
    SuggestedPaths     []string `json:"suggested_allowed_paths"`
    HasBreakingChanges bool     `json:"has_breaking_changes"`
    Diagnostics        []string `json:"diagnostics,omitempty"`
}

type BlastRadiusAnalyzer interface {
    AnalyzeSymbolImpact(file string, symbol string) (*BlastRadiusReport, error)
    VerifyPatchDiagnostics(file string, patchDiff string) ([]string, error)
}
```
