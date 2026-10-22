package verifier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// VetoCanarySurvived is emitted when a seeded synthetic mutant survives the verification suite undetected.
const VetoCanarySurvived = "VETO_CANARY_SURVIVED"

// CanaryResult captures the outcome of a synthetic mutant canary test.
type CanaryResult struct {
	Executed     bool   `json:"executed"`
	CanaryCaught bool   `json:"canary_caught"`
	MutatedFile  string `json:"mutated_file,omitempty"`
	MutationType string `json:"mutation_type,omitempty"`
	VetoStatus   string `json:"veto_status,omitempty"`
	Detail       string `json:"detail"`
}

var (
	reBoolTrue  = regexp.MustCompile(`(?i)\btrue\b`)
	reBoolFalse = regexp.MustCompile(`(?i)\bfalse\b`)
	reCode206   = regexp.MustCompile(`\b206\b`)
	reCode200   = regexp.MustCompile(`\b200\b`)
)

// InjectSyntheticMutant deterministically injects a single semantic regression into source code,
// ignoring comment lines starting with // or #.
func InjectSyntheticMutant(src []byte, filename string) ([]byte, string, bool) {
	lines := strings.Split(string(src), "\n")

	// Helper to check if line is a comment line
	isCommentLine := func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")
	}

	// Rule 1: JS Strict Equality flip: === to !== ("js_strict_eq_flip")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		if strings.Contains(line, "===") {
			lines[i] = strings.Replace(line, "===", "!==", 1)
			return []byte(strings.Join(lines, "\n")), "js_strict_eq_flip", true
		}
	}

	// Rule 2: Equality flip: == to != ("operator_flip_eq_to_neq") (avoiding ===)
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		if strings.Contains(line, "==") && !strings.Contains(line, "===") {
			lines[i] = strings.Replace(line, "==", "!=", 1)
			return []byte(strings.Join(lines, "\n")), "operator_flip_eq_to_neq", true
		}
	}

	// Rule 3: Inequality flip: != to == ("operator_flip_neq_to_eq") (avoiding !==)
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		if strings.Contains(line, "!=") && !strings.Contains(line, "!==") {
			lines[i] = strings.Replace(line, "!=", "==", 1)
			return []byte(strings.Join(lines, "\n")), "operator_flip_neq_to_eq", true
		}
	}

	// Rule 4: Boolean true to false ("bool_flip_true_to_false")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		if loc := reBoolTrue.FindStringIndex(line); loc != nil {
			orig := line[loc[0]:loc[1]]
			rep := "false"
			switch orig {
			case "True":
				rep = "False"
			case "TRUE":
				rep = "FALSE"
			}
			lines[i] = line[:loc[0]] + rep + line[loc[1]:]
			return []byte(strings.Join(lines, "\n")), "bool_flip_true_to_false", true
		}
	}

	// Rule 5: Boolean false to true ("bool_flip_false_to_true")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		if loc := reBoolFalse.FindStringIndex(line); loc != nil {
			orig := line[loc[0]:loc[1]]
			rep := "true"
			switch orig {
			case "False":
				rep = "True"
			case "FALSE":
				rep = "TRUE"
			}
			lines[i] = line[:loc[0]] + rep + line[loc[1]:]
			return []byte(strings.Join(lines, "\n")), "bool_flip_false_to_true", true
		}
	}

	// Rule 6: Status code inversion: 206 -> 500 or 200 -> 500 ("status_code_inversion")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		if loc := reCode206.FindStringIndex(line); loc != nil {
			lines[i] = line[:loc[0]] + "500" + line[loc[1]:]
			return []byte(strings.Join(lines, "\n")), "status_code_inversion", true
		}
		if loc := reCode200.FindStringIndex(line); loc != nil {
			lines[i] = line[:loc[0]] + "500" + line[loc[1]:]
			return []byte(strings.Join(lines, "\n")), "status_code_inversion", true
		}
	}

	return src, "", false
}

// resolveCandidateCodeFiles finds eligible source files from candidate paths or directories.
func resolveCandidateCodeFiles(repoRoot string, candidates []string) []string {
	var results []string
	seen := make(map[string]bool)

	isEligibleSourceFile := func(path string) bool {
		base := filepath.Base(path)
		// Skip test files
		if strings.HasSuffix(base, "_test.go") ||
			strings.HasSuffix(base, ".test.js") ||
			strings.HasSuffix(base, ".test.ts") ||
			strings.HasSuffix(base, ".spec.js") ||
			strings.HasSuffix(base, ".spec.ts") ||
			strings.HasSuffix(base, "_test.py") ||
			strings.HasPrefix(base, "test_") ||
			strings.HasSuffix(base, "_test.sh") {
			return false
		}
		ext := filepath.Ext(base)
		return ext == ".go" || ext == ".js" || ext == ".ts" || ext == ".py" || ext == ".sh"
	}

	for _, cand := range candidates {
		cand = strings.TrimSpace(cand)
		if cand == "" {
			continue
		}

		cleanCand := cand
		isGlob := false
		if strings.HasSuffix(cleanCand, "/**") {
			cleanCand = strings.TrimSuffix(cleanCand, "/**")
			isGlob = true
		} else if strings.HasSuffix(cleanCand, "/*") {
			cleanCand = strings.TrimSuffix(cleanCand, "/*")
			isGlob = true
		} else if strings.Contains(cleanCand, "*") {
			isGlob = true
			cleanCand = filepath.Dir(cleanCand)
		}

		fullPath := cleanCand
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(repoRoot, cleanCand)
		}

		fi, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		if fi.IsDir() || isGlob {
			_ = filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
				if err != nil || info == nil {
					return nil
				}
				if info.IsDir() {
					name := info.Name()
					if name == ".git" || name == "node_modules" || name == "vendor" || name == ".bin" {
						return filepath.SkipDir
					}
					return nil
				}
				if info.Mode().IsRegular() && isEligibleSourceFile(path) {
					if !seen[path] {
						seen[path] = true
						results = append(results, path)
					}
				}
				return nil
			})
		} else if fi.Mode().IsRegular() && isEligibleSourceFile(fullPath) {
			if !seen[fullPath] {
				seen[fullPath] = true
				results = append(results, fullPath)
			}
		}
	}

	return results
}

// RunCanaryCheck injects a temporary synthetic mutant into a candidate file,
// runs the probe runner, and checks whether the mutant was caught.
// If the mutant survived undetected, it returns VetoCanarySurvived.
func RunCanaryCheck(
	ctx context.Context,
	repoRoot string,
	candidateFiles []string,
	probeRunner func(ctx context.Context, targetDir string) (passed bool, detail string, err error),
) CanaryResult {
	if repoRoot == "" {
		repoRoot = "."
	}

	codeFiles := resolveCandidateCodeFiles(repoRoot, candidateFiles)
	if len(codeFiles) == 0 {
		return CanaryResult{
			Executed: false,
			Detail:   "no mutable candidate file found",
		}
	}

	var targetPath string
	var origBytes []byte
	var mutatedBytes []byte
	var mutType string
	var targetMode os.FileMode

	for _, path := range codeFiles {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		mutated, mType, ok := InjectSyntheticMutant(src, filepath.Base(path))
		if ok {
			targetPath = path
			origBytes = src
			mutatedBytes = mutated
			mutType = mType

			if fi, err := os.Stat(path); err == nil {
				targetMode = fi.Mode()
			} else {
				targetMode = 0o644
			}
			break
		}
	}

	if targetPath == "" {
		return CanaryResult{
			Executed: false,
			Detail:   "no mutable candidate file found",
		}
	}

	relFile, err := filepath.Rel(repoRoot, targetPath)
	if err != nil {
		relFile = targetPath
	}

	// Guarantee disk restoration in all execution paths
	restored := false
	restore := func() {
		if !restored {
			if err := os.WriteFile(targetPath, origBytes, targetMode); err != nil {
				fmt.Fprintf(os.Stderr, "[canary] warning: failed to restore %s: %v\n", relFile, err)
			}
			restored = true
		}
	}
	defer restore()

	// Write mutated source to disk
	if err := os.WriteFile(targetPath, mutatedBytes, targetMode); err != nil {
		return CanaryResult{
			Executed: false,
			Detail:   fmt.Sprintf("failed to write seeded mutant to %s: %v", relFile, err),
		}
	}

	// Execute probeRunner against target repo
	passed, _, _ := probeRunner(ctx, repoRoot)

	// Explicitly restore original file immediately after probe finishes
	restore()

	if passed {
		// Test passed despite mutant -> mutant survived undetected!
		return CanaryResult{
			Executed:     true,
			CanaryCaught: false,
			MutatedFile:  relFile,
			MutationType: mutType,
			VetoStatus:   VetoCanarySurvived,
			Detail:       "verification blindness veto: seeded canary mutant survived undetected",
		}
	}

	// Test failed -> mutant was caught by verification!
	return CanaryResult{
		Executed:     true,
		CanaryCaught: true,
		MutatedFile:  relFile,
		MutationType: mutType,
		VetoStatus:   "",
		Detail:       "seeded canary mutant caught by verification suite",
	}
}
