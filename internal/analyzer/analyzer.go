// Package analyzer implements AST-based reference tracking and Blast Radius
// Intelligence per OpenSpec DELTA-07, quantifying code change impact and
// generating optimal write receipt path scopes.
package analyzer

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BlastRadiusReport represents the quantitative impact assessment of modifying
// a target file or symbol.
type BlastRadiusReport struct {
	TargetSymbol       string   `json:"target_symbol,omitempty"`
	TargetFile         string   `json:"target_file"`
	RiskLevel          string   `json:"risk_level"` // LOW, MEDIUM, HIGH, CRITICAL
	BlastRadiusScore   float64  `json:"blast_radius_score"`
	DirectCallers      []string `json:"direct_callers"`
	AffectedFiles      []string `json:"affected_files"`
	SuggestedPaths     []string `json:"suggested_allowed_paths"`
	HasBreakingChanges bool     `json:"has_breaking_changes"`
	Diagnostics        []string `json:"diagnostics,omitempty"`
}

// Analyzer performs codebase dependency analysis and blast radius scoring.
type Analyzer struct {
	rootDir string
}

// NewAnalyzer creates an Analyzer rooted at rootDir (defaults to cwd if empty).
func NewAnalyzer(rootDir string) (*Analyzer, error) {
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
	return &Analyzer{rootDir: abs}, nil
}

// AnalyzeFileImpact analyzes all exported symbols and references for a given file.
func (a *Analyzer) AnalyzeFileImpact(targetFile string) (*BlastRadiusReport, error) {
	absTarget, err := filepath.Abs(targetFile)
	if err != nil {
		return nil, fmt.Errorf("abs target file: %w", err)
	}

	if _, err := os.Stat(absTarget); err != nil {
		return nil, fmt.Errorf("stat target file: %w", err)
	}

	relTarget, err := filepath.Rel(a.rootDir, absTarget)
	if err != nil {
		relTarget = targetFile
	}

	// If not Go file, fallback to textual reference scanner
	if !strings.HasSuffix(targetFile, ".go") {
		return a.analyzeGenericFile(relTarget, absTarget)
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, absTarget, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse target go file: %w", err)
	}

	var exportedSymbols []string
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				exportedSymbols = append(exportedSymbols, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
					exportedSymbols = append(exportedSymbols, ts.Name.Name)
				}
			}
		}
	}

	return a.aggregateSymbolsImpact(relTarget, absTarget, exportedSymbols)
}

// AnalyzeSymbolImpact computes direct callers and blast radius for a specific symbol.
func (a *Analyzer) AnalyzeSymbolImpact(targetFile, symbol string) (*BlastRadiusReport, error) {
	absTarget, err := filepath.Abs(targetFile)
	if err != nil {
		return nil, fmt.Errorf("abs target file: %w", err)
	}

	relTarget, err := filepath.Rel(a.rootDir, absTarget)
	if err != nil {
		relTarget = targetFile
	}
	symBytes := []byte(symbol)

	affectedMap := make(map[string]int)
	var directCallers []string

	affectedMap[relTarget] = 1

	// Add sibling test file if exists
	testFile := strings.TrimSuffix(relTarget, ".go") + "_test.go"
	if _, err := os.Stat(filepath.Join(a.rootDir, testFile)); err == nil {
		affectedMap[testFile] = 1
	}

	err = filepath.WalkDir(a.rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		relPath, _ := filepath.Rel(a.rootDir, path)
		if relPath == relTarget || relPath == testFile {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		if bytes.Contains(data, symBytes) {
			occurrences := bytes.Count(data, symBytes)
			affectedMap[relPath] = occurrences
			directCallers = append(directCallers, fmt.Sprintf("%s (%d refs)", relPath, occurrences))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workspace: %w", err)
	}

	var affectedFiles []string
	var suggestedPaths []string
	totalRefs := 0

	for file, count := range affectedMap {
		affectedFiles = append(affectedFiles, file)
		suggestedPaths = append(suggestedPaths, file)
		totalRefs += count
	}

	score := calculateScore(relTarget, len(affectedFiles), totalRefs)
	riskLevel := categorizeRisk(relTarget, len(affectedFiles), score)

	return &BlastRadiusReport{
		TargetSymbol:     symbol,
		TargetFile:       relTarget,
		RiskLevel:        riskLevel,
		BlastRadiusScore: score,
		DirectCallers:    directCallers,
		AffectedFiles:    affectedFiles,
		SuggestedPaths:   suggestedPaths,
	}, nil
}

func (a *Analyzer) aggregateSymbolsImpact(relTarget, absTarget string, symbols []string) (*BlastRadiusReport, error) {
	affectedMap := make(map[string]int)
	affectedMap[relTarget] = 1

	testFile := strings.TrimSuffix(relTarget, ".go") + "_test.go"
	if _, err := os.Stat(filepath.Join(a.rootDir, testFile)); err == nil {
		affectedMap[testFile] = 1
	}

	var directCallers []string

	var symBytes [][]byte
	for _, sym := range symbols {
		symBytes = append(symBytes, []byte(sym))
	}

	if len(symbols) > 0 {
		_ = filepath.WalkDir(a.rootDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() {
					name := d.Name()
					if name == ".git" || name == "vendor" || name == "node_modules" {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			relPath, _ := filepath.Rel(a.rootDir, path)
			if relPath == relTarget || relPath == testFile {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for i, sym := range symbols {
				if bytes.Contains(data, symBytes[i]) {
					count := bytes.Count(data, symBytes[i])
					affectedMap[relPath] += count
					directCallers = append(directCallers, fmt.Sprintf("%s:%s (%d refs)", relPath, sym, count))
				}
			}
			return nil
		})
	}

	var affectedFiles []string
	var suggestedPaths []string
	totalRefs := 0

	for file, count := range affectedMap {
		affectedFiles = append(affectedFiles, file)
		suggestedPaths = append(suggestedPaths, file)
		totalRefs += count
	}

	score := calculateScore(relTarget, len(affectedFiles), totalRefs)
	riskLevel := categorizeRisk(relTarget, len(affectedFiles), score)

	return &BlastRadiusReport{
		TargetFile:       relTarget,
		RiskLevel:        riskLevel,
		BlastRadiusScore: score,
		DirectCallers:    directCallers,
		AffectedFiles:    affectedFiles,
		SuggestedPaths:   suggestedPaths,
	}, nil
}

func (a *Analyzer) analyzeGenericFile(relTarget, absTarget string) (*BlastRadiusReport, error) {
	base := filepath.Base(relTarget)
	baseBytes := []byte(base)
	affectedMap := make(map[string]int)
	affectedMap[relTarget] = 1

	var directCallers []string

	_ = filepath.WalkDir(a.rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "vendor" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		relPath, _ := filepath.Rel(a.rootDir, path)
		if relPath == relTarget {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.Contains(data, baseBytes) {
			count := bytes.Count(data, baseBytes)
			affectedMap[relPath] = count
			directCallers = append(directCallers, fmt.Sprintf("%s (%d refs)", relPath, count))
		}
		return nil
	})

	var affectedFiles []string
	var suggestedPaths []string
	totalRefs := 0

	for file, count := range affectedMap {
		affectedFiles = append(affectedFiles, file)
		suggestedPaths = append(suggestedPaths, file)
		totalRefs += count
	}

	score := calculateScore(relTarget, len(affectedFiles), totalRefs)
	riskLevel := categorizeRisk(relTarget, len(affectedFiles), score)

	return &BlastRadiusReport{
		TargetFile:       relTarget,
		RiskLevel:        riskLevel,
		BlastRadiusScore: score,
		DirectCallers:    directCallers,
		AffectedFiles:    affectedFiles,
		SuggestedPaths:   suggestedPaths,
	}, nil
}

func calculateScore(targetFile string, affectedFilesCount, totalRefs int) float64 {
	baseScore := float64(affectedFilesCount)*1.5 + float64(totalRefs)*0.5
	// Core packages carry higher risk weight
	if strings.Contains(targetFile, "internal/harness") ||
		strings.Contains(targetFile, "internal/receipt") ||
		strings.Contains(targetFile, "internal/controlplane") {
		baseScore *= 2.0
	}
	return baseScore
}

func categorizeRisk(targetFile string, affectedFilesCount int, score float64) string {
	if strings.Contains(targetFile, "internal/harness") || strings.Contains(targetFile, "internal/receipt") {
		if affectedFilesCount > 1 || score > 5.0 {
			return "CRITICAL"
		}
	}
	if score > 15.0 || affectedFilesCount > 5 {
		return "HIGH"
	}
	if score > 3.0 || affectedFilesCount > 1 {
		return "MEDIUM"
	}
	return "LOW"
}
