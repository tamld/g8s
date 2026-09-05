package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type mockAdapter struct {
	name         string
	capabilities Capabilities
	refLocations []Location
	refErr       error
	callTree     *CallTree
	callErr      error
	diagnostics  []Diagnostic
	diagErr      error
}

func (m *mockAdapter) Name() string                     { return m.name }
func (m *mockAdapter) Capabilities() Capabilities       { return m.capabilities }
func (m *mockAdapter) References(_ context.Context, _ string, _ string) ([]Location, error) {
	return m.refLocations, m.refErr
}
func (m *mockAdapter) CallHierarchy(_ context.Context, _ string, _ string) (*CallTree, error) {
	return m.callTree, m.callErr
}
func (m *mockAdapter) Diagnostics(_ context.Context, _ string) ([]Diagnostic, error) {
	return m.diagnostics, m.diagErr
}

func TestASTAdapter_References(t *testing.T) {
	tmp := t.TempDir()

	fileA := filepath.Join(tmp, "a.go")
	if err := os.WriteFile(fileA, []byte("package main\n\nfunc Foo() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fileB := filepath.Join(tmp, "b.go")
	if err := os.WriteFile(fileB, []byte("package main\n\nfunc Bar() { Foo(); Foo() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter, err := NewASTAdapter(tmp)
	if err != nil {
		t.Fatalf("NewASTAdapter failed: %v", err)
	}

	locs, err := adapter.References(context.Background(), "a.go", "Foo")
	if err != nil {
		t.Fatalf("References failed: %v", err)
	}

	if len(locs) != 2 {
		t.Fatalf("expected 2 files referencing Foo, got %d", len(locs))
	}
}

func TestASTAdapter_Diagnostics(t *testing.T) {
	tmp := t.TempDir()

	validFile := filepath.Join(tmp, "valid.go")
	if err := os.WriteFile(validFile, []byte("package main\n\nfunc Hello() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	brokenFile := filepath.Join(tmp, "broken.go")
	if err := os.WriteFile(brokenFile, []byte("package main\n\nfunc Broken( {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter, err := NewASTAdapter(tmp)
	if err != nil {
		t.Fatal(err)
	}

	diags, err := adapter.Diagnostics(context.Background(), validFile)
	if err != nil || len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for valid file, got %d (err: %v)", len(diags), err)
	}

	diagsBroken, err := adapter.Diagnostics(context.Background(), brokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagsBroken) == 0 {
		t.Fatal("expected diagnostics for broken file, got 0")
	}
}

func TestMultiTierRouter_Fallback(t *testing.T) {
	tier1Mock := &mockAdapter{
		name: "tier1-lsp",
		capabilities: Capabilities{
			CanReferences:    true,
			CanCallHierarchy: true,
			CanDiagnostics:   true,
			IsSemantic:       true,
		},
		callTree: &CallTree{
			RootSymbol: "RootFunc",
			Callers:    []Location{{File: "caller.go", Line: 10}},
		},
	}

	tier0Mock := &mockAdapter{
		name: "tier0-ast",
		capabilities: Capabilities{
			CanReferences:    true,
			CanCallHierarchy: false,
			CanDiagnostics:   true,
			IsSemantic:       false,
		},
		refLocations: []Location{{File: "fallback.go", Reference: 3}},
	}

	router := NewMultiTierRouter(tier1Mock, tier0Mock)

	tree, err := router.CallHierarchy(context.Background(), "main.go", "RootFunc")
	if err != nil {
		t.Fatalf("CallHierarchy failed: %v", err)
	}
	if tree.RootSymbol != "RootFunc" || len(tree.Callers) != 1 {
		t.Fatalf("unexpected call tree: %+v", tree)
	}

	locs, err := router.References(context.Background(), "main.go", "AnySymbol")
	if err != nil {
		t.Fatalf("References failed: %v", err)
	}
	if len(locs) != 1 || locs[0].File != "fallback.go" {
		t.Fatalf("expected fallback locations, got %+v", locs)
	}
}

func TestCodeIntel_Concurrency(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Test() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter, err := NewASTAdapter(tmp)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = adapter.References(context.Background(), "main.go", "Test")
			_, _ = adapter.Diagnostics(context.Background(), "main.go")
		}()
	}
	wg.Wait()
}
