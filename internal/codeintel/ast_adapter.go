package codeintel

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ASTAdapter implements Tier 0 code intelligence using Go standard library
// parser and byte-matching scanners. Zero external dependencies, 100% Pure Go.
type ASTAdapter struct {
	rootDir string
}

// NewASTAdapter creates an ASTAdapter rooted at the given repository directory.
func NewASTAdapter(rootDir string) (*ASTAdapter, error) {
	if rootDir == "" {
		var err error
		rootDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get root dir: %w", err)
		}
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve abs root dir: %w", err)
	}
	return &ASTAdapter{rootDir: abs}, nil
}

// Name returns "ast-tier0".
func (a *ASTAdapter) Name() string {
	return "ast-tier0"
}

// Capabilities returns Tier 0 capabilities.
func (a *ASTAdapter) Capabilities() Capabilities {
	return Capabilities{
		CanReferences:    true,
		CanCallHierarchy: false,
		CanDiagnostics:   true,
		IsSemantic:       false,
	}
}

// References scans the repository for references to symbol in file.
func (a *ASTAdapter) References(_ context.Context, file string, symbol string) ([]Location, error) {
	if symbol == "" {
		return nil, fmt.Errorf("empty symbol")
	}

	symBytes := []byte(symbol)
	var locations []Location

	err := filepath.WalkDir(a.rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		if bytes.Contains(data, symBytes) {
			relPath, relErr := filepath.Rel(a.rootDir, path)
			if relErr != nil {
				relPath = path
			}
			count := bytes.Count(data, symBytes)
			locations = append(locations, Location{
				File:      relPath,
				Reference: count,
			})
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk dir: %w", err)
	}
	return locations, nil
}

// CallHierarchy is unsupported in Tier 0.
func (a *ASTAdapter) CallHierarchy(_ context.Context, _ string, _ string) (*CallTree, error) {
	return nil, fmt.Errorf("%w: call hierarchy requires Tier 0.5 (SSA) or Tier 1 (LSP)", ErrNoCapableAdapter)
}

// Diagnostics parses the file and reports syntax errors.
func (a *ASTAdapter) Diagnostics(_ context.Context, file string) ([]Diagnostic, error) {
	target := file
	if !filepath.IsAbs(target) {
		target = filepath.Join(a.rootDir, file)
	}

	if !strings.HasSuffix(target, ".go") {
		return nil, nil // non-Go files have no AST diagnostics in Tier 0
	}

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, target, nil, parser.AllErrors)
	if err == nil {
		return nil, nil
	}

	var diags []Diagnostic
	diags = append(diags, Diagnostic{
		File:     file,
		Message:  err.Error(),
		Severity: "ERROR",
	})
	return diags, nil
}
