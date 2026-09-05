# OpenSpec DELTA-20: Multi-Tier Code Intelligence Adapter & Dynamic Blast Radius

* **Specification ID**: `DELTA-20`
* **Title**: Multi-Tier Code Intelligence Adapter & Dynamic Blast Radius
* **Status**: `PROPOSED`
* **Milestone**: `M5` (Robustness & Evals)
* **Target Package**: `internal/codeintel`
* **Tracking Issue**: #256
* **Architecture Decision**: [ADR-0018](decisions/0018-soc-code-intelligence.md)

---

## 1. Goal & Context

`DELTA-20` establishes a **Separation of Concerns ("Fat Kernel + Thin Adapters")** architecture for code intelligence in `g8s`. Rather than hardcoding Go AST and literal substring searches inside `internal/analyzer`, `g8s` introduces a pluggable `CodeIntelAdapter` interface that abstracts code references, call hierarchy, and compiler diagnostics across multiple tiers.

---

## 2. Multi-Tier Strategy

1. **Tier 0 (Built-in Default)**: `go/ast` parser for Go files and token matching for generic files. Zero external dependencies, always available.
2. **Tier 0.5 (Go SSA & Call Graph)**: Uses `golang.org/x/tools/go/callgraph` with VTA/CHA algorithms to construct type-resolved call graphs and interface dispatches in Pure Go.
3. **Tier 1 (Universal LSP Sidecar)**: Connects to running Language Server Protocol servers (`gopls`, `pyright`, `rust-analyzer`) via standard JSON-RPC 2.0 (`go.lsp.dev/protocol`).
4. **Tier 2 (Multi-Language Tree-sitter)**: Pure-Go Tree-sitter runtime (`gotreesitter` / `canopy`) with lazy-loaded grammars for 200+ languages without CGO.

---

## 3. Interface Definition

```go
package codeintel

import (
	"context"
)

type Location struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Snippet   string `json:"snippet,omitempty"`
	Reference int    `json:"reference_count,omitempty"`
}

type CallTree struct {
	RootSymbol string     `json:"root_symbol"`
	Callers    []Location `json:"callers"`
	Callees    []Location `json:"callees"`
}

type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // ERROR, WARNING, INFO
}

type Capabilities struct {
	CanReferences    bool `json:"can_references"`
	CanCallHierarchy bool `json:"can_call_hierarchy"`
	CanDiagnostics   bool `json:"can_diagnostics"`
	IsSemantic       bool `json:"is_semantic"`
}

type Adapter interface {
	Name() string
	Capabilities() Capabilities
	References(ctx context.Context, file string, symbol string) ([]Location, error)
	CallHierarchy(ctx context.Context, file string, symbol string) (*CallTree, error)
	Diagnostics(ctx context.Context, file string) ([]Diagnostic, error)
}
```

---

## 4. Invariant Rules

1. **Zero-CGO (`CGO_ENABLED=0`)**: Every adapter implementation must compile statically across Darwin, Linux, and Windows.
2. **Zero-Orphan Process Guarantee**: Any subprocess spawned by an LSP adapter must be executed with `syscall.SysProcAttr{Setpgid: true}` and cleanly terminated on context cancellation.
3. **Deterministic Fallback**: If a higher-tier adapter fails or is unconfigured, the system must seamlessly fall back to Tier 0 without returning unhandled errors to the caller.
