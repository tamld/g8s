package featuremap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

// Audit classification statuses (#686, #921).
const (
	AuditStatusVerifiedLive    = "VERIFIED_LIVE"
	AuditStatusRuntimeProven   = "RUNTIME_PROVEN"
	AuditStatusStaticOnly      = "STATIC_ONLY"
	AuditStatusDriftedContract = "DRIFTED_CONTRACT"
	AuditStatusBrokenRuntime   = "BROKEN_RUNTIME"
)

// AuditOptions configures Audit execution.
type AuditOptions struct {
	FeatureID      string
	Live           bool
	TestReportPath string
}

// FeatureAuditResult holds the static AST and optional live/report test audit outcome for one FeatureNode.
type FeatureAuditResult struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	CodePathsChecked   int      `json:"code_paths_checked"`
	TestSymbolsChecked int      `json:"test_symbols_checked"`
	InterfacesChecked  int      `json:"interfaces_checked"`
	LiveTestsRun       int      `json:"live_tests_run,omitempty"`
	ProvenTests        int      `json:"proven_tests,omitempty"`
	Findings           []string `json:"findings,omitempty"`
}

// AuditReport summarises the conformance audit across the FeatureMap (#686, #921).
type AuditReport struct {
	Valid                bool                 `json:"valid"`
	Live                 bool                 `json:"live"`
	TestReport           string               `json:"test_report,omitempty"`
	TotalFeatures        int                  `json:"total_features"`
	VerifiedLiveCount    int                  `json:"verified_live_count"`
	RuntimeProvenCount   int                  `json:"runtime_proven_count"`
	StaticOnlyCount      int                  `json:"static_only_count"`
	DriftedContractCount int                  `json:"drifted_contract_count"`
	BrokenRuntimeCount   int                  `json:"broken_runtime_count"`
	Results              []FeatureAuditResult `json:"results"`
}

// TestExecutionIndex indexes passed and failed packages/tests parsed from
// a `go test -json` or `go test -v` report (#921).
type TestExecutionIndex struct {
	ModulePath        string
	PassedPackages    map[string]bool
	FailedPackages    map[string]bool
	PassedPkgTests    map[string]bool
	FailedPkgTests    map[string]bool
	PassedTestsByName map[string]bool
	FailedTestsByName map[string]bool
}

// Audit performs a multi-layer conformance audit on fm against repoRoot (#686, #921):
//  1. Static AST & Contract Layer: verifies code_paths exist, parses Go *_test.go
//     ASTs to confirm Test... symbols referenced in visible_tests exist, and checks
//     CLI/MCP interface registrations in cmd/g8s and internal/mcp.
//  2. Execution Evidence Layer (when opts.TestReportPath is set): parses the
//     `go test -json` or `go test -v` report and promotes features whose mapped
//     visible_tests ran and passed to RUNTIME_PROVEN (or BROKEN_RUNTIME on gaps/failures).
//  3. Live Behavioral Layer (when opts.Live is true without TestReportPath):
//     executes the feature's visible_tests via `go test` and classifies as VERIFIED_LIVE.
func Audit(fm *FeatureMap, repoRoot string, opts AuditOptions) (*AuditReport, error) {
	if fm == nil {
		return nil, fmt.Errorf("feature map is nil")
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}

	var testIdx *TestExecutionIndex
	reportPath := strings.TrimSpace(opts.TestReportPath)
	if reportPath != "" {
		raw, err := os.ReadFile(reportPath)
		if err != nil {
			return nil, fmt.Errorf("read test report %q: %w", reportPath, err)
		}
		parsed, err := ParseTestReport(repoRoot, raw)
		if err != nil {
			return nil, fmt.Errorf("parse test report %q: %w", reportPath, err)
		}
		testIdx = parsed
	}

	testSymbolsByFile, allTestSymbols, surfaceCorpus := indexRepoASTAndSurface(repoRoot)

	report := &AuditReport{
		Valid:      true,
		Live:       opts.Live,
		TestReport: reportPath,
	}

	for _, f := range fm.Features {
		if opts.FeatureID != "" && f.ID != opts.FeatureID {
			continue
		}
		res := auditSingleFeature(f, repoRoot, opts.Live, testIdx, testSymbolsByFile, allTestSymbols, surfaceCorpus)
		report.Results = append(report.Results, res)
		report.TotalFeatures++
		switch res.Status {
		case AuditStatusVerifiedLive:
			report.VerifiedLiveCount++
		case AuditStatusRuntimeProven:
			report.RuntimeProvenCount++
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
	testIdx *TestExecutionIndex,
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
	var resolvedFiles []string
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
			resolvedFiles = appendUnique(resolvedFiles, mf)
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

	if testIdx != nil {
		res.ProvenTests = verifyVisibleTestsAgainstReport(f.VisibleTests, resolvedFiles, testIdx, testSymbolsByFile, &res.Findings)
		if len(res.Findings) > 0 {
			res.Status = AuditStatusBrokenRuntime
			return res
		}
		res.Status = AuditStatusRuntimeProven
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

// ParseTestReport parses `go test -json` JSONL events or `go test -v` text output
// into a package+test execution index (#921). Empty/unrecognized reports return an error.
func ParseTestReport(repoRoot string, raw []byte) (*TestExecutionIndex, error) {
	modPath := readGoModulePath(repoRoot)
	idx := &TestExecutionIndex{
		ModulePath:        modPath,
		PassedPackages:    make(map[string]bool),
		FailedPackages:    make(map[string]bool),
		PassedPkgTests:    make(map[string]bool),
		FailedPkgTests:    make(map[string]bool),
		PassedTestsByName: make(map[string]bool),
		FailedTestsByName: make(map[string]bool),
	}

	type goTestJSONEvent struct {
		Action  string `json:"Action"`
		Package string `json:"Package"`
		Test    string `json:"Test"`
	}

	var (
		hasEvents     bool
		pendingPassed []string
		pendingFailed []string
	)

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "{") {
			var ev goTestJSONEvent
			if err := json.Unmarshal([]byte(trimmed), &ev); err == nil && ev.Action != "" {
				pkg := normalizeReportPackage(ev.Package, modPath)
				switch ev.Action {
				case "pass":
					if ev.Test != "" {
						hasEvents = true
						idx.PassedTestsByName[ev.Test] = true
						if pkg != "" {
							idx.PassedPkgTests[pkgTestKey(pkg, ev.Test)] = true
						}
					} else if pkg != "" {
						hasEvents = true
						idx.PassedPackages[pkg] = true
					}
				case "fail":
					if ev.Test != "" {
						hasEvents = true
						top := topLevelTestName(ev.Test)
						idx.FailedTestsByName[ev.Test] = true
						idx.FailedTestsByName[top] = true
						if pkg != "" {
							idx.FailedPkgTests[pkgTestKey(pkg, ev.Test)] = true
							idx.FailedPkgTests[pkgTestKey(pkg, top)] = true
						}
					} else if pkg != "" {
						hasEvents = true
						idx.FailedPackages[pkg] = true
					}
				}
				continue
			}
		}

		// Verbose text format: `--- PASS: TestFoo (0.01s)` / `--- FAIL: TestFoo (0.01s)`
		if strings.HasPrefix(trimmed, "--- PASS:") {
			if name := extractVerboseTestName(strings.TrimPrefix(trimmed, "--- PASS:")); name != "" {
				hasEvents = true
				idx.PassedTestsByName[name] = true
				pendingPassed = append(pendingPassed, name)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			if name := extractVerboseTestName(strings.TrimPrefix(trimmed, "--- FAIL:")); name != "" {
				hasEvents = true
				top := topLevelTestName(name)
				idx.FailedTestsByName[name] = true
				idx.FailedTestsByName[top] = true
				pendingFailed = append(pendingFailed, name)
				if top != name {
					pendingFailed = append(pendingFailed, top)
				}
			}
			continue
		}

		// Top-level package summary lines (never indented):
		// `ok  	github.com/tamld/g8s/internal/lane	0.444s`
		// `FAIL	github.com/tamld/g8s/internal/lane	0.444s`
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 && fields[0] == "ok" {
				pkg := normalizeReportPackage(fields[1], modPath)
				if pkg != "" {
					hasEvents = true
					idx.PassedPackages[pkg] = true
					for _, tname := range pendingPassed {
						idx.PassedPkgTests[pkgTestKey(pkg, tname)] = true
					}
					for _, tname := range pendingFailed {
						idx.FailedPkgTests[pkgTestKey(pkg, tname)] = true
					}
				}
				pendingPassed = pendingPassed[:0]
				pendingFailed = pendingFailed[:0]
				continue
			}
			if len(fields) >= 2 && fields[0] == "FAIL" && strings.HasPrefix(line, "FAIL\t") {
				pkg := normalizeReportPackage(fields[1], modPath)
				if pkg != "" {
					hasEvents = true
					idx.FailedPackages[pkg] = true
					for _, tname := range pendingPassed {
						idx.PassedPkgTests[pkgTestKey(pkg, tname)] = true
					}
					for _, tname := range pendingFailed {
						idx.FailedPkgTests[pkgTestKey(pkg, tname)] = true
					}
				}
				pendingPassed = pendingPassed[:0]
				pendingFailed = pendingFailed[:0]
				continue
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan test report: %w", err)
	}
	if !hasEvents {
		return nil, fmt.Errorf("test report contains no recognized go test pass/fail events")
	}
	return idx, nil
}

func verifyVisibleTestsAgainstReport(
	visibleTests []string,
	resolvedFiles []string,
	testIdx *TestExecutionIndex,
	testSymbolsByFile map[string][]string,
	findings *[]string,
) int {
	provenCount := 0
	for _, mf := range resolvedFiles {
		pkgDir := filepath.ToSlash(filepath.Dir(mf))
		pkgRel := "."
		if pkgDir != "." && pkgDir != "" {
			pkgRel = "./" + pkgDir
		}
		if testIdx.FailedPackages[pkgRel] {
			*findings = append(*findings, fmt.Sprintf("visible_test %q: package %s failed in test report", mf, pkgRel))
			continue
		}
		if !testIdx.PassedPackages[pkgRel] {
			*findings = append(*findings, fmt.Sprintf("visible_test %q: package %s did not execute and pass in test report", mf, pkgRel))
			continue
		}

		syms := testSymbolsByFile[mf]
		filePassed := 0
		fileFailed := 0
		for _, sym := range syms {
			if testIdx.FailedPkgTests[pkgTestKey(pkgRel, sym)] || (len(testIdx.FailedPkgTests) == 0 && testIdx.FailedTestsByName[sym]) {
				fileFailed++
				*findings = append(*findings, fmt.Sprintf("visible_test %q: test %s failed in test report", mf, sym))
				continue
			}
			if !strings.HasPrefix(sym, "Test") || sym == "TestMain" {
				continue
			}
			if testIdx.PassedPkgTests[pkgTestKey(pkgRel, sym)] || (len(testIdx.PassedPkgTests) == 0 && testIdx.PassedTestsByName[sym]) {
				filePassed++
			}
		}
		if fileFailed == 0 && filePassed == 0 {
			*findings = append(*findings, fmt.Sprintf("visible_test %q: zero AST Test* functions ran and passed in test report", mf))
		}
		provenCount += filePassed
	}

	for _, vt := range visibleTests {
		trimmed := strings.TrimSpace(vt)
		if !isCommandTest(trimmed) {
			continue
		}
		pkgRel := extractGoTestPackage(trimmed)
		if pkgRel != "" {
			if testIdx.FailedPackages[pkgRel] {
				*findings = append(*findings, fmt.Sprintf("visible_test %q: package %s failed in test report", trimmed, pkgRel))
				continue
			}
			if !testIdx.PassedPackages[pkgRel] {
				*findings = append(*findings, fmt.Sprintf("visible_test %q: package %s did not pass in test report", trimmed, pkgRel))
				continue
			}
		}
		if runPat := extractGoTestRunPattern(trimmed); runPat != "" {
			if matchesPatternInMap(runPat, pkgRel, testIdx.FailedPkgTests, testIdx.FailedTestsByName) {
				*findings = append(*findings, fmt.Sprintf("visible_test %q: matching -run %q failed in test report", trimmed, runPat))
				continue
			}
			if !matchesPatternInMap(runPat, pkgRel, testIdx.PassedPkgTests, testIdx.PassedTestsByName) {
				*findings = append(*findings, fmt.Sprintf("visible_test %q: matching -run %q had zero passed tests in test report", trimmed, runPat))
				continue
			}
			provenCount++
		} else if pkgRel != "" {
			provenCount++
		}
	}

	return provenCount
}

func readGoModulePath(repoRoot string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.TrimSpace(fields[1])
		}
	}
	return ""
}

func normalizeReportPackage(rawPkg, modPath string) string {
	p := strings.TrimSpace(rawPkg)
	if p == "" {
		return ""
	}
	if modPath != "" {
		if p == modPath {
			return "."
		}
		if strings.HasPrefix(p, modPath+"/") {
			return "./" + strings.TrimPrefix(p, modPath+"/")
		}
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || strings.HasPrefix(p, "./") {
		return p
	}
	return "./" + p
}

func pkgTestKey(pkg, test string) string {
	return pkg + "::" + test
}

func topLevelTestName(test string) string {
	if idx := strings.IndexByte(test, '/'); idx > 0 {
		return test[:idx]
	}
	return test
}

func extractVerboseTestName(afterPrefix string) string {
	fields := strings.Fields(strings.TrimSpace(afterPrefix))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func extractGoTestPackage(cmdLine string) string {
	for _, f := range strings.Fields(cmdLine) {
		if strings.HasPrefix(f, "./") || f == "." {
			return strings.TrimSuffix(f, "/...")
		}
	}
	return ""
}

func matchesPatternInMap(runPattern, pkgRel string, byPkg map[string]bool, byName map[string]bool) bool {
	if pkgRel != "" && len(byPkg) > 0 {
		prefix := pkgRel + "::"
		for key := range byPkg {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			sym := strings.TrimPrefix(key, prefix)
			if matchesAnyASTTestSymbol(runPattern, map[string]bool{sym: true}) {
				return true
			}
		}
		return false
	}
	return matchesAnyASTTestSymbol(runPattern, byName)
}
