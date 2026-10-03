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

func (m *mockAdapter) Name() string               { return m.name }
func (m *mockAdapter) Capabilities() Capabilities { return m.capabilities }
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

func TestMultiTierRouter_NoCapableAdapter(t *testing.T) {
	// Red test verification: router without capable adapter returns ErrNoCapableAdapter
	emptyRouter := NewMultiTierRouter()

	_, err := emptyRouter.References(context.Background(), "file.go", "Sym")
	if err == nil {
		t.Fatal("expected error on empty router references, got nil")
	}

	_, err = emptyRouter.CallHierarchy(context.Background(), "file.go", "Sym")
	if err == nil {
		t.Fatal("expected error on empty router call hierarchy, got nil")
	}
}

func BenchmarkMultiTierRouter_References(b *testing.B) {
	tier1Mock := &mockAdapter{
		name: "tier1-lsp",
		capabilities: Capabilities{
			CanReferences: true,
		},
		refLocations: []Location{{File: "test.go", Reference: 1}},
	}
	router := NewMultiTierRouter(tier1Mock)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = router.References(ctx, "test.go", "Sym")
	}
}

func BenchmarkASTAdapter_Diagnostics(b *testing.B) {
	tmp := b.TempDir()
	filePath := filepath.Join(tmp, "bench.go")
	_ = os.WriteFile(filePath, []byte("package bench\nfunc F() int { return 42 }\n"), 0o600)

	adapter, _ := NewASTAdapter(tmp)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = adapter.Diagnostics(ctx, filePath)
	}
}

func TestASTAdapter_Name_Capabilities_CallHierarchy(t *testing.T) {
	adapter, err := NewASTAdapter("")
	if err != nil {
		t.Fatalf("NewASTAdapter failed: %v", err)
	}
	if name := adapter.Name(); name != "ast-tier0" {
		t.Errorf("expected ast-tier0, got %s", name)
	}
	caps := adapter.Capabilities()
	if !caps.CanReferences || caps.CanCallHierarchy || !caps.CanDiagnostics || caps.IsSemantic {
		t.Errorf("unexpected capabilities: %+v", caps)
	}
	tree, err := adapter.CallHierarchy(context.Background(), "main.go", "Foo")
	if err == nil || tree != nil {
		t.Errorf("expected error from CallHierarchy, got tree=%v, err=%v", tree, err)
	}
}

func TestASTAdapter_References_EdgeCases(t *testing.T) {
	tmp := t.TempDir()
	adapter, err := NewASTAdapter(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// Empty symbol error
	_, err = adapter.References(context.Background(), "a.go", "")
	if err == nil {
		t.Error("expected error for empty symbol")
	}

	// Subdirectories to skip: .git, vendor, node_modules
	for _, dir := range []string{".git", "vendor", "node_modules", "sub"} {
		p := filepath.Join(tmp, dir)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "test.go"), []byte("package p\nfunc Secret() {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Secret in .git, vendor, node_modules should be ignored, only sub/test.go found
	locs, err := adapter.References(context.Background(), "sub/test.go", "Secret")
	if err != nil {
		t.Fatalf("References failed: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(locs), locs)
	}
	if locs[0].File != filepath.Join("sub", "test.go") {
		t.Errorf("expected sub/test.go, got %s", locs[0].File)
	}
}

func TestASTAdapter_Diagnostics_NonGoFile(t *testing.T) {
	tmp := t.TempDir()
	txtFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(txtFile, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewASTAdapter(tmp)
	if err != nil {
		t.Fatal(err)
	}
	diags, err := adapter.Diagnostics(context.Background(), "note.txt")
	if err != nil || len(diags) != 0 {
		t.Errorf("expected nil diagnostics for non-go file, got diags=%v, err=%v", diags, err)
	}
}

func TestMultiTierRouter_Diagnostics(t *testing.T) {
	// Adapter with error
	errAdapter := &mockAdapter{
		name: "err-mock",
		capabilities: Capabilities{
			CanDiagnostics: true,
		},
		diagErr: os.ErrPermission,
	}

	// Adapter with valid diagnostics
	okAdapter := &mockAdapter{
		name: "ok-mock",
		capabilities: Capabilities{
			CanDiagnostics: true,
		},
		diagnostics: []Diagnostic{{File: "f.go", Message: "err", Severity: "ERROR"}},
	}

	// Fallback should hit okAdapter
	router := NewMultiTierRouter(errAdapter, okAdapter)
	diags, err := router.Diagnostics(context.Background(), "f.go")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(diags) != 1 || diags[0].Message != "err" {
		t.Fatalf("unexpected diags: %+v", diags)
	}

	// Router without CanDiagnostics adapter
	noDiagMock := &mockAdapter{
		name:         "no-diag",
		capabilities: Capabilities{},
	}
	emptyRouter := NewMultiTierRouter(noDiagMock)
	diags, err = emptyRouter.Diagnostics(context.Background(), "f.go")
	if err != nil || diags != nil {
		t.Fatalf("expected nil, nil, got diags=%v, err=%v", diags, err)
	}
}
