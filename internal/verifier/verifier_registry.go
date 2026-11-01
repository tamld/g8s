package verifier

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/tamld/g8s/internal/telemetry"
)

// Status indicates the enforcement mode of a verifier class.

func (v *Verifier) emitTelemetry(taskID string, verdict Verdict) {
	gateOverride := strings.TrimSpace(os.Getenv("G8S_GATE_OUTCOME_PATH")) != ""
	if gateOverride {
		_ = RecordGateOutcomes("", taskID, verdict)
	}
	if v.telemetrySink != nil {
		v.telemetrySink(taskID, verdict)
		return
	}
	if !gateOverride {
		_ = RecordGateOutcomes("", taskID, verdict)
	}

	cfg := telemetry.DefaultTelemetryConfig()
	if p := os.Getenv("G8S_TELEMETRY_DB"); p != "" {
		cfg.DBPath = p
	}
	eng, err := telemetry.NewTelemetryEngine(cfg)
	if err != nil {
		return
	}
	defer eng.Close()

	ev := telemetry.TraceEvent{
		TaskID:    taskID,
		EventType: telemetry.TraceEventVerifierVerdict,
		Timestamp: verdict.RecordedAt,
		Payload: map[string]any{
			"class":      verdict.Class,
			"status":     string(verdict.Status),
			"outcome":    string(verdict.Outcome),
			"checks_run": len(verdict.Checks),
		},
	}
	if err := eng.IngestEvent(context.Background(), ev); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] verifier telemetry ingest: %v\n", err)
	}
}

// derivePackagesFromPaths extracts Go package import paths from target paths.
// Rule: prefers non-test Go source files when present; falls back to test files
// if only test files were modified.
func derivePackagesFromPaths(paths []string) []string {
	var nonTestGoFiles []string
	var testGoFiles []string

	for _, p := range paths {
		clean := filepath.ToSlash(filepath.Clean(p))
		if !strings.HasSuffix(clean, ".go") {
			continue
		}
		if strings.HasSuffix(clean, "_test.go") {
			testGoFiles = append(testGoFiles, clean)
		} else {
			nonTestGoFiles = append(nonTestGoFiles, clean)
		}
	}

	selected := nonTestGoFiles
	if len(selected) == 0 {
		selected = testGoFiles
	}
	if len(selected) == 0 {
		return nil
	}

	pkgMap := make(map[string]bool)
	for _, f := range selected {
		dir := filepath.Dir(f)
		dir = filepath.ToSlash(filepath.Clean(dir))
		var pkg string
		if dir == "." || dir == "" {
			pkg = "."
		} else if !strings.HasPrefix(dir, "./") {
			pkg = "./" + dir
		} else {
			pkg = dir
		}
		pkgMap[pkg] = true
	}

	var res []string
	for pkg := range pkgMap {
		res = append(res, pkg)
	}
	sort.Strings(res)
	return res
}

// Package-level default verifier.
var defaultVerifier = NewVerifier()

// Verify evaluates acceptance checks using the default verifier.
func Verify(target TaskRef, caller TaskRef) (Verdict, error) {
	return defaultVerifier.Verify(target, caller)
}

// Resolve matches write-scope paths against the default registry.
func Resolve(paths []string) (Class, bool) {
	if defaultVerifier == nil || defaultVerifier.registry == nil {
		return Class{Name: "unregistered", Status: StatusAdvisory}, false
	}
	return defaultVerifier.registry.Resolve(paths)
}

// LoadRegistry parses and validates the verifier classes configuration file.
// If the file is missing or invalid, an empty/error Registry is returned.
func LoadRegistry(relPath string) (*Registry, error) {
	path, err := findFile(relPath)
	if err != nil {
		return &Registry{err: err}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &Registry{err: err}, err
	}
	return ParseRegistry(data)
}

// ParseRegistry parses verifier classes YAML data using pure Go stdlib.
// Enforces fail-closed rules:
//   - status: hard without a valid citation (#<digits> or incident:<slug>) -> entry treated as UNREGISTERED
//   - duplicate names -> refused
//   - unknown check type -> entry unregistered
//   - malformed YAML -> error returned, classes unregistered, never panics
func ParseRegistry(data []byte) (*Registry, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	reg := &Registry{
		classes: make([]Class, 0),
	}

	type rawCheck struct {
		typ      string
		run      string
		packages []string
		hasPkgs  bool
	}

	type rawClass struct {
		name          string
		status        string
		foundingCatch string
		priority      int
		paths         []string
		checks        []rawCheck
	}

	var rawClasses []rawClass
	var currentClass *rawClass
	var currentCheck *rawCheck

	inClassesSection := false
	inPathsSection := false
	inChecksSection := false
	inPackagesSection := false

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		rawLine := scanner.Text()
		line := stripYAMLComment(rawLine)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		leadingSpaces := len(line) - len(strings.TrimLeft(line, " "))

		// Top-level key: "classes:"
		if leadingSpaces == 0 {
			if trimmed == "classes:" {
				inClassesSection = true
				inPathsSection = false
				inChecksSection = false
				inPackagesSection = false
				continue
			}
			// Another top-level section: end classes
			inClassesSection = false
			inPathsSection = false
			inChecksSection = false
			inPackagesSection = false
			continue
		}

		if !inClassesSection {
			continue
		}

		// A class item starts with "- " at indent 2 (or 1..3 spaces)
		if leadingSpaces >= 1 && leadingSpaces <= 3 && strings.HasPrefix(trimmed, "- ") {
			if currentCheck != nil && currentClass != nil {
				currentClass.checks = append(currentClass.checks, *currentCheck)
				currentCheck = nil
			}
			if currentClass != nil {
				rawClasses = append(rawClasses, *currentClass)
			}
			currentClass = &rawClass{
				priority: 100, // default priority
			}
			inPathsSection = false
			inChecksSection = false
			inPackagesSection = false

			itemContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if strings.HasPrefix(itemContent, "name:") {
				val := strings.TrimPrefix(itemContent, "name:")
				currentClass.name = cleanYAMLValue(val)
			}
			continue
		}

		if currentClass == nil {
			return &Registry{err: fmt.Errorf("unexpected content outside class at line %d: %q", lineNum, trimmed)}, fmt.Errorf("unexpected content outside class at line %d", lineNum)
		}

		// Inside paths list
		if inPathsSection {
			if strings.HasPrefix(trimmed, "- ") {
				p := cleanYAMLValue(strings.TrimPrefix(trimmed, "- "))
				currentClass.paths = append(currentClass.paths, p)
				continue
			}
			inPathsSection = false
		}

		// Inside packages list for go-test check
		if inPackagesSection {
			if strings.HasPrefix(trimmed, "- ") {
				pkg := cleanYAMLValue(strings.TrimPrefix(trimmed, "- "))
				if currentCheck != nil {
					currentCheck.packages = append(currentCheck.packages, pkg)
				}
				continue
			}
			inPackagesSection = false
		}

		// Check item starts with "- " under checks:
		if inChecksSection {
			if strings.HasPrefix(trimmed, "- ") {
				if currentCheck != nil {
					currentClass.checks = append(currentClass.checks, *currentCheck)
				}
				currentCheck = &rawCheck{}
				inPackagesSection = false

				itemContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if strings.HasPrefix(itemContent, "type:") {
					val := strings.TrimPrefix(itemContent, "type:")
					currentCheck.typ = cleanYAMLValue(val)
				}
				continue
			}

			if currentCheck != nil {
				if strings.HasPrefix(trimmed, "type:") {
					val := strings.TrimPrefix(trimmed, "type:")
					currentCheck.typ = cleanYAMLValue(val)
					continue
				}
				if strings.HasPrefix(trimmed, "run:") {
					val := strings.TrimPrefix(trimmed, "run:")
					currentCheck.run = cleanYAMLValue(val)
					continue
				}
				if strings.HasPrefix(trimmed, "packages:") {
					val := strings.TrimSpace(strings.TrimPrefix(trimmed, "packages:"))
					currentCheck.hasPkgs = true
					if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
						inner := strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
						for _, it := range strings.Split(inner, ",") {
							c := cleanYAMLValue(it)
							if c != "" {
								currentCheck.packages = append(currentCheck.packages, c)
							}
						}
						inPackagesSection = false
					} else {
						inPackagesSection = true
					}
					continue
				}
			}

			// If line doesn't match check fields and doesn't start with "- ", exit checks section
			if !strings.HasPrefix(trimmed, "type:") && !strings.HasPrefix(trimmed, "run:") && !strings.HasPrefix(trimmed, "packages:") {
				inChecksSection = false
				if currentCheck != nil {
					currentClass.checks = append(currentClass.checks, *currentCheck)
					currentCheck = nil
				}
			}
		}

		// Class scalar fields
		if strings.HasPrefix(trimmed, "paths:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "paths:"))
			if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				inner := strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
				for _, it := range strings.Split(inner, ",") {
					c := cleanYAMLValue(it)
					if c != "" {
						currentClass.paths = append(currentClass.paths, c)
					}
				}
				inPathsSection = false
			} else {
				inPathsSection = true
			}
			inChecksSection = false
			inPackagesSection = false
			continue
		}

		if strings.HasPrefix(trimmed, "checks:") {
			inChecksSection = true
			inPathsSection = false
			inPackagesSection = false
			continue
		}

		if strings.HasPrefix(trimmed, "name:") {
			val := strings.TrimPrefix(trimmed, "name:")
			currentClass.name = cleanYAMLValue(val)
			continue
		}

		if strings.HasPrefix(trimmed, "status:") {
			val := strings.TrimPrefix(trimmed, "status:")
			currentClass.status = cleanYAMLValue(val)
			continue
		}

		if strings.HasPrefix(trimmed, "founding_catch:") {
			val := strings.TrimPrefix(trimmed, "founding_catch:")
			currentClass.foundingCatch = cleanYAMLValue(val)
			continue
		}

		if strings.HasPrefix(trimmed, "priority:") {
			val := strings.TrimPrefix(trimmed, "priority:")
			p, err := strconv.Atoi(cleanYAMLValue(val))
			if err == nil {
				currentClass.priority = p
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		reg.err = err
		return reg, err
	}

	if currentCheck != nil && currentClass != nil {
		currentClass.checks = append(currentClass.checks, *currentCheck)
	}
	if currentClass != nil {
		rawClasses = append(rawClasses, *currentClass)
	}

	// Validate classes and enforce fail-closed rules
	nameCounts := make(map[string]int)
	for _, rc := range rawClasses {
		if rc.name != "" {
			nameCounts[rc.name]++
		}
	}

	for _, rc := range rawClasses {
		// 1. Must have a valid name
		if rc.name == "" {
			continue
		}
		// 2. Duplicate names -> refused
		if nameCounts[rc.name] > 1 {
			continue
		}
		// 3. Status must be advisory or hard
		st := Status(rc.status)
		if st != StatusAdvisory && st != StatusHard {
			continue
		}
		// 4. Hard without valid citation -> load error for entry -> unregistered
		if st == StatusHard && !isValidCitation(rc.foundingCatch) {
			continue
		}

		// 5. Unknown check type -> entry unregistered
		unknownCheckFound := false
		var checks []Check
		for _, chk := range rc.checks {
			cType := CheckType(chk.typ)
			if cType != CheckTypeScript && cType != CheckTypeGoTest && cType != CheckTypeDiffGuard {
				unknownCheckFound = true
				break
			}
			checks = append(checks, Check{
				Type:     cType,
				Run:      chk.run,
				Packages: chk.packages,
			})
		}
		if unknownCheckFound {
			continue
		}

		reg.classes = append(reg.classes, Class{
			Name:          rc.name,
			Status:        st,
			FoundingCatch: rc.foundingCatch,
			Priority:      rc.priority,
			Paths:         rc.paths,
			Checks:        checks,
		})
	}

	return reg, nil
}

func cleanYAMLValue(val string) string {
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			return val[1 : len(val)-1]
		}
	}
	return val
}

func stripYAMLComment(line string) string {
	inDouble := false
	inSingle := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' && !inSingle {
			inDouble = !inDouble
		} else if ch == '\'' && !inDouble {
			inSingle = !inSingle
		} else if ch == '#' && !inDouble && !inSingle {
			return line[:i]
		}
	}
	return line
}

// pathMatches tests whether cleanFile matches pattern.
// Handles exact match, globstar ("**"), directory boundaries ("/*"), and filepath.Match globs.
func pathMatches(file, pattern string) bool {
	cleanFile := filepath.ToSlash(filepath.Clean(file))
	cleanFile = strings.TrimPrefix(cleanFile, "./")
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))
	cleanPattern = strings.TrimPrefix(cleanPattern, "./")

	// Confusable-character guard: a path containing non-ASCII characters never
	// matches a registry pattern (deny-by-default direction — an unregistered
	// verdict means human acceptance, never over-matching). Windows filesystem
	// normalization can fold look-alike Unicode (e.g. Greek omicron in "dοcs")
	// onto ASCII names, which would silently widen the docs class on one
	// platform only; this guard keeps class resolution identical everywhere
	// (release red cell, issue #517).
	if strings.ContainsFunc(cleanFile, func(r rune) bool { return r > unicode.MaxASCII }) {
		return false
	}

	// 1. Exact path match
	if cleanFile == cleanPattern {
		return true
	}
	// 1a. Globstar
	if strings.Contains(cleanPattern, "**") {
		return globstarMatch(
			strings.Split(cleanFile, "/"),
			strings.Split(cleanPattern, "/"),
		)
	}
	// 2. Directory boundary match: pattern ends in /* or / or is a bare directory name
	if strings.HasSuffix(pattern, "/*") || strings.HasSuffix(pattern, "/") {
		dirPrefix := strings.TrimSuffix(cleanPattern, "*")
		if !strings.HasSuffix(dirPrefix, "/") {
			dirPrefix += "/"
		}
		if strings.HasPrefix(cleanFile, dirPrefix) {
			return true
		}
	} else if !strings.Contains(cleanPattern, "*") {
		if strings.HasPrefix(cleanFile, cleanPattern+"/") {
			return true
		}
	}
	// 3. Glob matching (e.g. *.md, *_test.go). path.Match (not filepath.Match)
	// is required here: paths are already slash-normalized, and filepath.Match
	// is case-insensitive on Windows, which would let "DOCS/x.md" match the
	// lowercase "docs/**" registry patterns and silently widen the class
	// (release red cell, issue #517 — Windows-oracle catch).
	m, _ := path.Match(cleanPattern, cleanFile)
	return m
}

func globstarMatch(fileSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(fileSegs) == 0
	}
	if patSegs[0] == "**" {
		for i := 0; i <= len(fileSegs); i++ {
			if globstarMatch(fileSegs[i:], patSegs[1:]) {
				return true
			}
		}
		return false
	}
	if len(fileSegs) == 0 {
		return false
	}
	m, _ := path.Match(patSegs[0], fileSegs[0])
	if !m {
		return false
	}
	return globstarMatch(fileSegs[1:], patSegs[1:])
}

// findFile attempts to locate a file by path, searching upward from current working directory.
func findFile(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		if _, err := os.Stat(relPath); err == nil {
			return relPath, nil
		}
		return "", os.ErrNotExist
	}
	if _, err := os.Stat(relPath); err == nil {
		return relPath, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	dir := cwd
	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", os.ErrNotExist
}

// findRepoRoot finds the repository root directory by walking up looking for go.mod or .g8s.
func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd, nil
}
