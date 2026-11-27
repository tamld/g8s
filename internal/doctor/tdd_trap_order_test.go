package doctor

// Red-test for #1041: CheckTDDPitfalls registered methods only when the
// receiver type had already been seen during the lexical (filepath.Walk)
// file iteration, so a receiver type declared in an alphabetically-later
// file of the same package silently lost every method declared before it.
// Real-world hit: internal/controlplane declares type Store in store.go but
// its lifecycle.go/session.go/brief_store.go/access_audit.go methods sort
// BEFORE store.go — 30+ Store methods were invisible to the checker and any
// test calling them through a *Store parameter was flagged as fabricated.
// The fixture mirrors that mechanism: the call site must type the receiver
// through a function parameter (the only construct the checker's
// scopedTypes resolver handles), not a plain `var v Value` declaration.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckTDDPitfallsMethodBeforeTypeDeclarationOrder(t *testing.T) {
	tempDir := t.TempDir()
	pkgDir := filepath.Join(tempDir, "internal", "order")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// a_methods.go sorts BEFORE b_types.go, so the pre-fix single-pass
	// collector processed the method before its receiver type existed.
	methodsCode := `package order

func (v Value) Advance() int {
	return v.N + 1
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "a_methods.go"), []byte(methodsCode), 0o644); err != nil {
		t.Fatalf("write methods: %v", err)
	}

	typesCode := `package order

type Value struct {
	N int
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "b_types.go"), []byte(typesCode), 0o644); err != nil {
		t.Fatalf("write types: %v", err)
	}

	testCode := `package order

import "testing"

func driveValue(v Value) bool {
	return v.Advance() == 2 && v.N >= 0
}

func TestValueAdvance(t *testing.T) {
	if !driveValue(Value{N: 1}) {
		t.Fatal("advance failed")
	}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "b_methods_test.go"), []byte(testCode), 0o644); err != nil {
		t.Fatalf("write test: %v", err)
	}

	// Mirror control: a genuinely fabricated method must still be caught
	// even when the type file sorts later (checker must not overcorrect).
	ghostDir := filepath.Join(tempDir, "internal", "ghost")
	if err := os.MkdirAll(ghostDir, 0o755); err != nil {
		t.Fatalf("mkdir ghost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ghostDir, "a_methods.go"), []byte(methodsCode), 0o644); err != nil {
		t.Fatalf("write ghost methods: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ghostDir, "b_types.go"), []byte(typesCode), 0o644); err != nil {
		t.Fatalf("write ghost types: %v", err)
	}
	ghostTest := `package order

import "testing"

func driveGhost(v Value) bool {
	v.GhostMethod()
	return true
}

func TestValueGhost(t *testing.T) {
	if !driveGhost(Value{N: 1}) {
		t.Fatal("driveGhost failed")
	}
}
`
	if err := os.WriteFile(filepath.Join(ghostDir, "b_ghost_test.go"), []byte(ghostTest), 0o644); err != nil {
		t.Fatalf("write ghost test: %v", err)
	}

	pitfalls, err := CheckTDDPitfalls(tempDir)
	if err != nil {
		t.Fatalf("CheckTDDPitfalls failed: %v", err)
	}

	for _, p := range pitfalls {
		if !strings.Contains(p.Symbol, "Value.GhostMethod") {
			t.Fatalf("false positive on method/field declared before its type file (#1041): %+v", p)
		}
	}
	found := false
	for _, p := range pitfalls {
		if strings.Contains(p.Symbol, "Value.GhostMethod") {
			found = true
		}
	}
	if !found {
		t.Fatalf("genuine fabricated method Value.GhostMethod not detected — checker overcorrected")
	}
}
