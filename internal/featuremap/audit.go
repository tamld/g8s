package featuremap

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Audit classification statuses (#686).
const (
	AuditStatusVerifiedLive    = "VERIFIED_LIVE"
	AuditStatusStaticOnly      = "STATIC_ONLY"
	AuditStatusDriftedContract = "DRIFTED_CONTRACT"
	AuditStatusBrokenRuntime   = "BROKEN_RUNTIME"
)

// AuditOptions configures Audit execution.
type AuditOptions struct {
	FeatureID string
	Live      bool
}

// FeatureAuditResult holds the static AST and optional live test audit outcome for one FeatureNode.
type FeatureAuditResult struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	CodePathsChecked   int      `json:"code_paths_checked"`
	TestSymbolsChecked int      `json:"test_symbols_checked"`
	InterfacesChecked  int      `json:"interfaces_checked"`
	LiveTestsRun       int      `json:"live_tests_run,omitempty"`
	Findings           []string `json:"findings,omitempty"`
}

// AuditReport summarises the conformance audit across the FeatureMap (#686).
type AuditReport struct {
	Valid                bool                 `json:"valid"`
	Live                 bool                 `json:"live"`
	TotalFeatures        int                  `json:"total_features"`
	VerifiedLiveCount    int                  `json:"verified_live_count"`
	StaticOnlyCount      int                  `json:"static_only_count"`
	DriftedContractCount int                  `json:"drifted_contract_count"`
	BrokenRuntimeCount   int                  `json:"broken_runtime_count"`
	Results              []FeatureAuditResult `json:"results"`
}

// Audit performs a 2-layer conformance audit on fm against repoRoot (#686):
//  1. Static AST & Contract Layer: verifies code_paths exist, parses Go *_test.go
//     ASTs to confirm Test... symbols referenced in visible_tests exist, and checks
//     CLI/MCP interface registrations in cmd/g8s and internal/mcp.
//  2. Live Behavioral Layer (when opts.Live is true): executes the feature's
//     visible_tests via `go test` and classifies each feature as VERIFIED_LIVE,
//     STATIC_ONLY, DRIFTED_CONTRACT, or BROKEN_RUNTIME.
func Audit(fm *FeatureMap, repoRoot string, opts AuditOptions) (*AuditReport, error) {
	if fm == nil {
		return nil, fmt.Errorf("feature map is nil")
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}

	testSymbolsByFile, allTestSymbols, surfaceCorpus := indexRepoASTAndSurface(repoRoot)

	report := &AuditReport{
		Valid: true,
		Live:  opts.Live,
	}

	for _, f := range fm.Features {
		if opts.FeatureID != "" && f.ID != opts.FeatureID {
			continue
		}
		res := auditSingleFeature(f, repoRoot, opts.Live, testSymbolsByFile, allTestSymbols, surfaceCorpus)
		report.Results = append(report.Results, res)
		report.TotalFeatures++
		switch res.Status {
		case AuditStatusVerifiedLive:
			report.VerifiedLiveCount++
		case AuditStatusStaticOnly:
			report.StaticOnlyCount++
		case AuditStatusDriftedContract:
			report.DriftedContractCount++
			report.Valid = false
		case AuditStatusBrokenRuntime:
			report.BrokenRuntimeCount++
			report.Valid = false
		}
	}

	if opts.FeatureID != "" && report.TotalFeatures == 0 {
		return nil, fmt.Errorf("feature %q not found in feature map", opts.FeatureID)
	}

	return report, nil
}

func auditSingleFeature(
	f FeatureNode,
	repoRoot string,
	live bool,
	testSymbolsByFile map[string][]string,
	allTestSymbols map[string]bool,
	surfaceCorpus string,
) FeatureAuditResult {
	res := FeatureAuditResult{
		ID:    f.ID,
		Title: f.Title,
	}

	// 1. Verify code_paths match physical files
	for _, cp := range f.CodePaths {
		res.CodePathsChecked++
		if !globMatchesAnyFile(repoRoot, cp) {
			res.Findings = append(res.Findings, fmt.Sprintf("code_path %q matches zero files in %s", cp, repoRoot))
		}
	}

	// 2. Verify visible_tests via Go AST
	var liveTargets []string
	if len(f.VisibleTests) == 0 {
		res.Findings = append(res.Findings, "visible_tests is empty")
	}
	for _, vt := range f.VisibleTests {
		trimmed := strings.TrimSpace(vt)
		if isCommandTest(trimmed) {
			res.TestSymbolsChecked++
			runPattern := extractGoTestRunPattern(trimmed)
			if runPattern != "" && !matchesAnyASTTestSymbol(runPattern, allTestSymbols) {
				res.Findings = append(res.Findings, fmt.Sprintf("visible_test %q references -run %q which matches zero AST Test functions", trimmed, runPattern))
			}
			liveTargets = append(liveTargets, trimmed)
			continue
		}

		// File or glob path test entry
		res.TestSymbolsChecked++
		matchedFiles := resolveTestFiles(repoRoot, trimmed)
		if len(matchedFiles) == 0 {
			res.Findings = append(res.Findings, fmt.Sprintf("visible_test path %q does not exist", trimmed))
			continue
		}
		hasTestFunc := false
		for _, mf := range matchedFiles {
			if len(testSymbolsByFile[mf]) > 0 {
				hasTestFunc = true
			}
			pkgDir := "./" + filepath.ToSlash(filepath.Dir(mf))
			liveTargets = appendUnique(liveTargets, "go test "+pkgDir)
		}
		if !hasTestFunc {
			res.Findings = append(res.Findings, fmt.Sprintf("visible_test %q contains zero AST Test functions", trimmed))
		}
	}

	// 3. Verify declared CLI & MCP interfaces exist in surface corpus
	if f.Interfaces != nil && surfaceCorpus != "" {
		for _, cliCmd := range f.Interfaces.CLI {
			res.InterfacesChecked++
			tokenStr := strings.Fields(strings.TrimSpace(cliCmd))[0]
			if tokenStr != "" && !strings.Contains(surfaceCorpus, `"`+tokenStr+`"`) && !strings.Contains(surfaceCorpus, tokenStr) {
				res.Findings = append(res.Findings, fmt.Sprintf("declared CLI interface %q not found in cmd/g8s surface", cliCmd))
			}
		}
		for _, mcpTool := range f.Interfaces.MCPTools {
			res.InterfacesChecked++
			if mcpTool != "" && !strings.Contains(surfaceCorpus, mcpTool) {
				res.Findings = append(res.Findings, fmt.Sprintf("declared MCP tool %q not found in MCP/CLI surface", mcpTool))
			}
		}
	}

	if len(res.Findings) > 0 {
		res.Status = AuditStatusDriftedContract
		return res
	}

	if !live {
		res.Status = AuditStatusStaticOnly
		return res
	}

	// 4. Execute live tests for the feature
	for _, cmdLine := range liveTargets {
		res.LiveTestsRun++
		if err := runLiveTestCommand(repoRoot, cmdLine); err != nil {
			res.Findings = append(res.Findings, fmt.Sprintf("live test %q failed: %v", cmdLine, err))
		}
	}
	if len(res.Findings) > 0 {
		res.Status = AuditStatusBrokenRuntime
		return res
	}
	res.Status = AuditStatusVerifiedLive
	return res
}

func indexRepoASTAndSurface(repoRoot string) (map[string][]string, map[string]bool, string) {
	byFile := make(map[string][]string)
	allSymbols := make(map[string]bool)
	var surfaceBuilder strings.Builder
	fset := token.NewFileSet()

	_ = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rErr := filepath.Rel(repoRoot, path)
		if rErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		if strings.HasPrefix(relSlash, "cmd/g8s/") || strings.HasPrefix(relSlash, "internal/mcp/") {
			if b, readErr := os.ReadFile(path); readErr == nil {
				surfaceBuilder.Write(b)
				surfaceBuilder.WriteByte('\n')
			}
		}

		if !strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		fileAST, pErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if pErr != nil {
			return nil
		}
		for _, decl := range fileAST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil {
				continue
			}
			name := fn.Name.Name
			if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Fuzz") || strings.HasPrefix(name, "Example") {
				byFile[relSlash] = append(byFile[relSlash], name)
				allSymbols[name] = true
			}
		}
		return nil
	})

	return byFile, allSymbols, surfaceBuilder.String()
}

func extractGoTestRunPattern(cmdLine string) string {
	fields := strings.Fields(cmdLine)
	for i := 0; i < len(fields); i++ {
		if (fields[i] == "-run" || fields[i] == "--run") && i+1 < len(fields) {
			return strings.Trim(fields[i+1], `"'`)
		}
		if strings.HasPrefix(fields[i], "-run=") {
			return strings.Trim(strings.TrimPrefix(fields[i], "-run="), `"'`)
		}
	}
	return ""
}

func matchesAnyASTTestSymbol(runPattern string, allSymbols map[string]bool) bool {
	parts := strings.Split(runPattern, "|")
	for _, rawPart := range parts {
		clean := strings.TrimSpace(rawPart)
		clean = strings.TrimPrefix(clean, "^")
		clean = strings.TrimSuffix(clean, "$")
		if idx := strings.IndexByte(clean, '/'); idx > 0 {
			clean = clean[:idx]
		}
		if clean == "" {
			continue
		}
		if allSymbols[clean] {
			return true
		}
		for sym := range allSymbols {
			if strings.Contains(sym, clean) {
				return true
			}
		}
	}
	return false
}

func resolveTestFiles(repoRoot, pattern string) []string {
	ps := NewPathSet([]string{pattern})
	var out []string
	_ = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && (d.Name() == ".git" || d.Name() == "vendor" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rErr := filepath.Rel(repoRoot, path)
		if rErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if ps.Match(relSlash) && strings.HasSuffix(relSlash, "_test.go") {
			out = append(out, relSlash)
		}
		return nil
	})
	return out
}

func globMatchesAnyFile(repoRoot, pattern string) bool {
	clean := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(pattern)), "!")
	if clean == "" {
		return true
	}
	trimmedDir := strings.TrimSuffix(strings.TrimSuffix(clean, "/**"), "/*")
	if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(trimmedDir))); err == nil && info != nil {
		return true
	}
	ps := NewPathSet([]string{clean})
	found := false
	_ = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, rErr := filepath.Rel(repoRoot, path); rErr == nil {
			if ps.Match(filepath.ToSlash(rel)) {
				found = true
				return filepath.SkipDir
			}
		}
		return nil
	})
	return found
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

func runLiveTestCommand(repoRoot, cmdLine string) error {
	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// #nosec G204 -- bounded test execution from declarative feature-map.yml visible_tests
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 240 {
			msg = msg[len(msg)-240:]
		}
		return fmt.Errorf("%w (%s)", err, msg)
	}
	return nil
}
