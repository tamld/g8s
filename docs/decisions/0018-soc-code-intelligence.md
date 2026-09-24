# ADR-0018: Separation of Concerns for Multi-Tier Code Intelligence

> **Status**: Accepted
> **Date**: 2026-09-20
> **Deciders**: tamld (owner), g8s architecture council
> **Implements**: DELTA-20 code-intel-adapter
> **Supersedes**: none

## Context

In initial versions of `g8s`, `internal/analyzer` directly coupled AST parsing with filesystem traversal for Go source files. As `g8s` evolves to support multi-language repositories, LSP sidecars, and call-graph intelligence, hardcoding parser mechanics inside the analyzer violates Separation of Concerns (SoC) and bloats kernel complexity.

Two approaches were evaluated:

1. **Monolithic Analyzer**: Embed multiple language parsers, tree-sitter CGO bindings, and LSP client logic directly inside `internal/analyzer`.
2. **Fat Kernel + Thin Adapters (SoC)**: Establish a lightweight `CodeIntelAdapter` interface in `internal/codeintel` that abstracts symbol extraction, references, and blast radius calculation across pluggable tiers (Tier 0 pure AST, Tier 0.5 Go SSA/callgraph, Tier 1 LSP sidecar, Tier 2 Tree-sitter).

## Decision

Adopt the **Fat Kernel + Thin Adapters (SoC)** architecture:

- `internal/analyzer` remains focused on blast radius scoring, risk weighting, and write-scope recommendations.
- Pluggable `CodeIntelAdapter` implementations in `internal/codeintel` handle language-specific syntax analysis, references, and diagnostics.
- Tier 0 remains the zero-dependency pure-Go fallback using `go/ast`.
- Higher tiers (Go SSA, LSP JSON-RPC sidecars) are activated dynamically based on environment availability without violating Zero-CGO invariants.

## Rationale

1. **Zero-CGO Invariant**: Pure-Go AST parsing remains available on every platform with zero external tooling.
2. **Extensibility**: Multi-language support (Python, TypeScript, Rust) can be wired via standard JSON-RPC LSP sidecars without modifying core gating logic.
3. **Testability**: Interfaces allow deterministic mock testing of blast radius calculations without spinning up language servers.
