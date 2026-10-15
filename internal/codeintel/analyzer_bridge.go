package codeintel

import (
	"context"
	"sort"

	"github.com/tamld/g8s/internal/analyzer"
)

// AnalyzerAdapter bridges DELTA-07's AST BlastRadius Analyzer into the DELTA-20
// MultiTierRouter Adapter contract, unifying the Code Intelligence bounded context.
type AnalyzerAdapter struct {
	inner *analyzer.Analyzer
}

// NewAnalyzerAdapter creates a codeintel Adapter backed by internal/analyzer.
func NewAnalyzerAdapter(rootDir string) (*AnalyzerAdapter, error) {
	a, err := analyzer.NewAnalyzer(rootDir)
	if err != nil {
		return nil, err
	}
	return &AnalyzerAdapter{inner: a}, nil
}

// Name returns the adapter identifier.
func (a *AnalyzerAdapter) Name() string {
	return "blast-radius-analyzer"
}

// Capabilities declares the operations supported by the blast-radius analyzer bridge.
func (a *AnalyzerAdapter) Capabilities() Capabilities {
	return Capabilities{
		CanReferences:    true,
		CanCallHierarchy: true,
		CanDiagnostics:   true,
		IsSemantic:       false,
	}
}

// References resolves affected files for a symbol using the blast-radius analyzer.
func (a *AnalyzerAdapter) References(_ context.Context, file string, symbol string) ([]Location, error) {
	report, err := a.BlastRadius(file, symbol)
	if err != nil {
		return nil, err
	}
	locs := make([]Location, 0, len(report.AffectedFiles))
	for _, f := range report.AffectedFiles {
		locs = append(locs, Location{
			File:      f,
			Line:      1,
			Column:    1,
			Reference: 1,
		})
	}
	return locs, nil
}

// CallHierarchy resolves direct callers using the blast-radius analyzer.
func (a *AnalyzerAdapter) CallHierarchy(_ context.Context, file string, symbol string) (*CallTree, error) {
	report, err := a.BlastRadius(file, symbol)
	if err != nil {
		return nil, err
	}
	callers := make([]Location, 0, len(report.DirectCallers))
	for _, caller := range report.DirectCallers {
		callers = append(callers, Location{
			File:    caller,
			Line:    1,
			Column:  1,
			Snippet: symbol,
		})
	}
	return &CallTree{
		RootSymbol: symbol,
		Callers:    callers,
	}, nil
}

// Diagnostics translates analyzer diagnostics into codeintel Diagnostic entries.
func (a *AnalyzerAdapter) Diagnostics(_ context.Context, file string) ([]Diagnostic, error) {
	report, err := a.inner.AnalyzeFileImpact(file)
	if err != nil {
		return nil, err
	}
	diags := make([]Diagnostic, 0, len(report.Diagnostics))
	for _, msg := range report.Diagnostics {
		diags = append(diags, Diagnostic{
			File:     report.TargetFile,
			Line:     1,
			Message:  msg,
			Severity: "INFO",
		})
	}
	return diags, nil
}

// BlastRadius returns the underlying DELTA-07 BlastRadiusReport for a file or symbol.
func (a *AnalyzerAdapter) BlastRadius(file string, symbol string) (*analyzer.BlastRadiusReport, error) {
	if symbol != "" {
		return a.inner.AnalyzeSymbolImpact(file, symbol)
	}
	return a.inner.AnalyzeFileImpact(file)
}

// BlastRadius computes a unified BlastRadiusReport across the router's adapters
// and merges any additional references discovered by higher-tier adapters.
func (r *MultiTierRouter) BlastRadius(ctx context.Context, rootDir, file, symbol string) (*analyzer.BlastRadiusReport, error) {
	bridge, err := NewAnalyzerAdapter(rootDir)
	if err != nil {
		return nil, err
	}
	report, err := bridge.BlastRadius(file, symbol)
	if err != nil {
		return nil, err
	}

	if symbol != "" {
		if locs, err := r.References(ctx, file, symbol); err == nil {
			seen := make(map[string]bool, len(report.AffectedFiles))
			for _, f := range report.AffectedFiles {
				seen[f] = true
			}
			for _, loc := range locs {
				if loc.File != "" && !seen[loc.File] {
					seen[loc.File] = true
					report.AffectedFiles = append(report.AffectedFiles, loc.File)
				}
			}
			sort.Strings(report.AffectedFiles)
		}
	}
	return report, nil
}
