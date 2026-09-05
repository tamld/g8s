// Package codeintel implements multi-tier code intelligence and blast radius
// analysis per OpenSpec DELTA-20, adhering to the "Fat Kernel + Thin Adapters"
// architecture pattern.
package codeintel

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrNoCapableAdapter is returned when no configured adapter can fulfill the capability.
	ErrNoCapableAdapter = errors.New("no code intelligence adapter supports this operation")
	// ErrSymbolNotFound indicates the requested symbol could not be resolved.
	ErrSymbolNotFound = errors.New("symbol not found")
)

// Location represents a code reference or declaration coordinates.
type Location struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Snippet   string `json:"snippet,omitempty"`
	Reference int    `json:"reference_count,omitempty"`
}

// CallTree represents an incoming (callers) and outgoing (callees) invocation graph.
type CallTree struct {
	RootSymbol string     `json:"root_symbol"`
	Callers    []Location `json:"callers"`
	Callees    []Location `json:"callees"`
}

// Diagnostic represents compiler, linter, or syntax issues reported by language servers.
type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // ERROR, WARNING, INFO
}

// Capabilities declares what operations an adapter supports.
type Capabilities struct {
	CanReferences    bool `json:"can_references"`
	CanCallHierarchy bool `json:"can_call_hierarchy"`
	CanDiagnostics   bool `json:"can_diagnostics"`
	IsSemantic       bool `json:"is_semantic"`
}

// Adapter defines the contract for code intelligence providers (AST, SSA, LSP, Tree-sitter).
type Adapter interface {
	Name() string
	Capabilities() Capabilities
	References(ctx context.Context, file string, symbol string) ([]Location, error)
	CallHierarchy(ctx context.Context, file string, symbol string) (*CallTree, error)
	Diagnostics(ctx context.Context, file string) ([]Diagnostic, error)
}

// MultiTierRouter cascades requests across available adapters with automatic fallback.
type MultiTierRouter struct {
	adapters []Adapter
}

// NewMultiTierRouter creates a router with prioritized adapters (highest tier first).
func NewMultiTierRouter(adapters ...Adapter) *MultiTierRouter {
	return &MultiTierRouter{
		adapters: adapters,
	}
}

// References queries the first adapter with CanReferences capability, falling back on error.
func (r *MultiTierRouter) References(ctx context.Context, file string, symbol string) ([]Location, error) {
	for _, a := range r.adapters {
		if a.Capabilities().CanReferences {
			locs, err := a.References(ctx, file, symbol)
			if err == nil && len(locs) > 0 {
				return locs, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: references for %s in %s", ErrNoCapableAdapter, symbol, file)
}

// CallHierarchy queries the first adapter with CanCallHierarchy capability, falling back on error.
func (r *MultiTierRouter) CallHierarchy(ctx context.Context, file string, symbol string) (*CallTree, error) {
	for _, a := range r.adapters {
		if a.Capabilities().CanCallHierarchy {
			tree, err := a.CallHierarchy(ctx, file, symbol)
			if err == nil && tree != nil {
				return tree, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: call hierarchy for %s in %s", ErrNoCapableAdapter, symbol, file)
}

// Diagnostics queries the first adapter with CanDiagnostics capability, falling back on error.
func (r *MultiTierRouter) Diagnostics(ctx context.Context, file string) ([]Diagnostic, error) {
	for _, a := range r.adapters {
		if a.Capabilities().CanDiagnostics {
			diags, err := a.Diagnostics(ctx, file)
			if err == nil {
				return diags, nil
			}
		}
	}
	return nil, nil // No diagnostics is not an error
}
