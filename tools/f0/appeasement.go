package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// AppeasementViolation represents a detected gate-appeasement signal.
type AppeasementViolation struct {
	Kind   string `json:"kind"` // "token-rewrite", "unread-config-key", "test-skip"
	File   string `json:"file"`
	Detail string `json:"detail"`
}

var (
	tokenRewriteRegex = regexp.MustCompile(`(?i)\b(to-do|to_do|to-be-determined|place-holder)\b`)
	testSkipRegex     = regexp.MustCompile(`t\.Skip(Now|f)?\(`)

	validConfigKeys = map[string][]string{
		"trust-boundaries": {"trust_boundaries"},
		"f0":               {"ci", "verify_commands", "manual_free", "parsers"},
		"verifier-classes": {"classes", "verifier_classes", "version"},
		"lane-bundles":     {"bundles", "lanes", "version"},
		"effort-classes":   {"classes", "effort_classes", "default_effort", "version"},
		"agent-models":     {"providers", "models", "version"},
	}
)

func isGuardOrVerifierFile(path string) bool {
	clean := filepath.ToSlash(path)
	lower := strings.ToLower(clean)

	if strings.HasSuffix(lower, "_test.go") || strings.Contains(lower, "/testdata/") || strings.HasPrefix(lower, "testdata/") {
		return false
	}

	base := filepath.Base(lower)
	if strings.Contains(base, "guard") || strings.Contains(base, "verifier") || strings.Contains(base, "gate") || strings.Contains(base, "appeasement") {
		return true
	}
	if strings.Contains(lower, "internal/verifier/") || strings.Contains(lower, "pkg/verifier/") || strings.Contains(lower, "tools/f0/") {
		return true
	}
	return false
}

func isConfigYAML(path string) (string, bool) {
	clean := filepath.ToSlash(path)
	var base string
	if strings.HasPrefix(clean, ".g8s/") || strings.Contains(clean, "/.g8s/") {
		base = filepath.Base(clean)
	} else if !strings.Contains(clean, "/") {
		base = clean
	} else {
		return "", false
	}

	ext := filepath.Ext(base)
	if ext != ".yml" && ext != ".yaml" {
		return "", false
	}
	schemaKey := strings.TrimSuffix(base, ext)
	return schemaKey, true
}

func readBlobOrFile(repoDir, headSHA, path string) ([]byte, error) {
	cleanPath := filepath.Clean(path)
	cleanPath = strings.TrimPrefix(cleanPath, "./")

	if headSHA != "" {
		cmd := exec.Command("git", "-C", repoDir, "show", headSHA+":"+cleanPath)
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
	}

	filePath := filepath.Join(repoDir, cleanPath)
	return os.ReadFile(filePath)
}

func getGitDiffAndFiles(repoDir, baseRef, headSHA string) (string, []string, error) {
	if repoDir == "" {
		repoDir = "."
	}
	if headSHA == "" {
		headSHA = "HEAD"
	}

	runGit := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	}

	var diffText string
	var filesText string
	var err error

	if baseRef != "" {
		diffText, err = runGit("diff", "-U0", baseRef, headSHA)
		if err == nil {
			filesText, _ = runGit("diff", "--name-only", baseRef, headSHA)
		}
	}

	if diffText == "" || err != nil || baseRef == "" {
		diffText, err = runGit("diff", "-U0", headSHA+"~1", headSHA)
		if err == nil {
			filesText, _ = runGit("diff", "--name-only", headSHA+"~1", headSHA)
		} else {
			diffText, err = runGit("diff", "-U0", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", headSHA)
			if err == nil {
				filesText, _ = runGit("diff", "--name-only", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", headSHA)
			}
		}
	}

	if err != nil {
		return "", nil, err
	}

	var changedFiles []string
	seen := make(map[string]bool)
	for _, f := range strings.Split(filesText, "\n") {
		f = strings.TrimSpace(f)
		if f != "" && !seen[f] {
			seen[f] = true
			changedFiles = append(changedFiles, f)
		}
	}

	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				f := strings.TrimPrefix(parts[3], "b/")
				if f != "" && !seen[f] {
					seen[f] = true
					changedFiles = append(changedFiles, f)
				}
			}
		}
	}

	return diffText, changedFiles, nil
}

// DetectGateAppeasement inspects the diff and changed files for evasion patterns.
func DetectGateAppeasement(repoDir, baseRef, headSHA string) ([]AppeasementViolation, error) {
	if repoDir == "" {
		repoDir = "."
	}
	if headSHA == "" {
		out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
		if err == nil {
			headSHA = strings.TrimSpace(string(out))
		} else {
			headSHA = "HEAD"
		}
	}

	diffText, changedFiles, err := getGitDiffAndFiles(repoDir, baseRef, headSHA)
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	var violations []AppeasementViolation
	var currentFile string
	testSkipFiles := make(map[string][]string)

	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				currentFile = strings.TrimPrefix(parts[3], "b/")
			}
		} else if strings.HasPrefix(line, "+++ b/") {
			currentFile = strings.TrimPrefix(line, "+++ b/")
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			addedLine := line[1:]

			// Signal 1: token-rewrite
			if tokenRewriteRegex.MatchString(addedLine) {
				match := tokenRewriteRegex.FindString(addedLine)
				violations = append(violations, AppeasementViolation{
					Kind:   "token-rewrite",
					File:   currentFile,
					Detail: fmt.Sprintf("token rewrite %q detected in %s: %s", match, currentFile, strings.TrimSpace(addedLine)),
				})
			}

			// Signal 3: test-skip candidate lines
			if strings.HasSuffix(currentFile, "_test.go") && testSkipRegex.MatchString(addedLine) {
				testSkipFiles[currentFile] = append(testSkipFiles[currentFile], strings.TrimSpace(addedLine))
			}
		}
	}

	// Signal 3 check: only report test-skip if a non-test guard/verifier file was also touched
	var guardFile string
	for _, f := range changedFiles {
		if isGuardOrVerifierFile(f) {
			guardFile = f
			break
		}
	}
	if guardFile != "" && len(testSkipFiles) > 0 {
		for testFile, skips := range testSkipFiles {
			violations = append(violations, AppeasementViolation{
				Kind:   "test-skip",
				File:   testFile,
				Detail: fmt.Sprintf("test skip added in %s (%s) while modifying guard/verifier %s", testFile, strings.Join(skips, ", "), guardFile),
			})
		}
	}

	// Signal 2: unread-config-key
	for _, f := range changedFiles {
		schemaKey, isConfig := isConfigYAML(f)
		if !isConfig {
			continue
		}
		validKeys, hasSchema := validConfigKeys[schemaKey]
		if !hasSchema {
			continue
		}

		content, readErr := readBlobOrFile(repoDir, headSHA, f)
		if readErr != nil {
			continue
		}

		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			continue
		}

		if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
			for i := 0; i < len(doc.Content[0].Content); i += 2 {
				keyNode := doc.Content[0].Content[i]
				key := keyNode.Value
				if !sliceContains(validKeys, key) {
					violations = append(violations, AppeasementViolation{
						Kind:   "unread-config-key",
						File:   f,
						Detail: fmt.Sprintf("unread config key %q in %s (valid: %s)", key, f, strings.Join(validKeys, ", ")),
					})
				}
			}
		}
	}

	return violations, nil
}

func extractReasonFromEntry(e Entry) string {
	if e.Extra != nil {
		if r, ok := e.Extra["reason"].(string); ok && strings.TrimSpace(r) != "" {
			return strings.TrimSpace(r)
		}
	}
	if strings.TrimSpace(e.Tail) != "" {
		return strings.TrimSpace(e.Tail)
	}
	return ""
}

// CheckGateAppeasement performs gate evaluation for G4, handling overrides and debits.
func CheckGateAppeasement(repoDir, baseRef, headSHA, overrideReason string, ledgerEntries []Entry) (GateCheckResult, error) {
	violations, err := DetectGateAppeasement(repoDir, baseRef, headSHA)
	if err != nil {
		return GateCheckResult{
			ID:     "G4",
			Status: "FAIL",
			Detail: "appeasement check failed: " + err.Error(),
		}, err
	}

	if len(violations) == 0 {
		return GateCheckResult{
			ID:     "G4",
			Status: "PASS",
			Detail: "no gate-appeasement signals detected",
		}, nil
	}

	reason := strings.TrimSpace(overrideReason)
	if reason != "" {
		_ = RecordDebit(repoDir, "g4-override: "+reason, "")
		return GateCheckResult{
			ID:     "G4",
			Status: "PASS",
			Detail: "appeasement overridden (debited): " + reason,
		}, nil
	}

	for _, e := range ledgerEntries {
		if (e.Kind == "g4-override" || e.Kind == "override") && (shaMatches(e.SHA, headSHA) || e.SHA == headSHA || e.SHA == "") {
			r := extractReasonFromEntry(e)
			if r != "" {
				return GateCheckResult{
					ID:     "G4",
					Status: "PASS",
					Detail: "appeasement overridden (debited): " + r,
				}, nil
			}
		}
	}

	var vStrings []string
	for _, v := range violations {
		vStrings = append(vStrings, fmt.Sprintf("%s: %s", v.Kind, v.Detail))
	}
	joinedViolations := strings.Join(vStrings, "; ")

	return GateCheckResult{
		ID:     "G4",
		Status: "FAIL",
		Detail: joinedViolations + " (hint: use sanctioned guard allowlistPaths or override with reason)",
	}, nil
}
