package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// TestSASTTriadConfigContract verifies that the SAST Triad (Gosec in
// .golangci.yml, Semgrep rules in .semgrep/g8s-rules.yml, and CodeQL in
// .github/workflows/codeql.yml) plus the Lane S security bundle remain active
// and synchronized with repository governance contracts.
func TestSASTTriadConfigContract(t *testing.T) {
	root := repoRootFromTest(t)

	// 1. Verify .golangci.yml enables gosec and core static analyzers
	golangciBytes, err := os.ReadFile(filepath.Join(root, ".golangci.yml"))
	if err != nil {
		t.Fatalf("read .golangci.yml: %v", err)
	}
	golangciText := string(golangciBytes)
	for _, linter := range []string{"- errcheck", "- gosec", "- govet", "- ineffassign", "- staticcheck", "- unused"} {
		if !strings.Contains(golangciText, linter) {
			t.Errorf(".golangci.yml missing required enabled linter %q", linter)
		}
	}

	// 2. Verify .semgrep/g8s-rules.yml and gate scripts exist and define core rules
	semgrepBytes, err := os.ReadFile(filepath.Join(root, ".semgrep", "g8s-rules.yml"))
	if err != nil {
		t.Fatalf("read .semgrep/g8s-rules.yml: %v", err)
	}
	semgrepText := string(semgrepBytes)
	for _, ruleID := range []string{
		"g8s-zero-cgo-import",
		"g8s-writefile-perm-0600",
		"g8s-no-silent-fs-error-swallow",
		"g8s-http-listenandserve-no-timeout",
		"g8s-github-actions-untrusted-input",
	} {
		if !strings.Contains(semgrepText, ruleID) {
			t.Errorf(".semgrep/g8s-rules.yml missing required rule %q", ruleID)
		}
	}

	for _, relPath := range []string{
		"tools/ci_semgrep_check.sh",
		"tools/ci_semgrep_check_test.sh",
		".github/workflows/codeql.yml",
	} {
		info, statErr := os.Stat(filepath.Join(root, relPath))
		if statErr != nil || info.Size() == 0 {
			t.Errorf("required SAST artifact %q missing or empty: %v", relPath, statErr)
		}
	}

	codeqlBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "codeql.yml"))
	if err != nil {
		t.Fatalf("read .github/workflows/codeql.yml: %v", err)
	}
	codeqlText := string(codeqlBytes)
	if strings.Contains(codeqlText, "github/codeql-action/") && strings.Contains(codeqlText, "@v3") {
		t.Errorf(".github/workflows/codeql.yml still uses deprecated CodeQL Action @v3; must use @v4")
	}
	for _, action := range []string{
		"github/codeql-action/init@v4",
		"github/codeql-action/autobuild@v4",
		"github/codeql-action/analyze@v4",
	} {
		if !strings.Contains(codeqlText, action) {
			t.Errorf(".github/workflows/codeql.yml missing required action %q", action)
		}
	}

	// 3. Verify Lane S (security) in .g8s/lane-bundles.yml includes sast-triad
	lanesBytes, err := os.ReadFile(filepath.Join(root, ".g8s", "lane-bundles.yml"))
	if err != nil {
		t.Fatalf("read .g8s/lane-bundles.yml: %v", err)
	}
	if !strings.Contains(string(lanesBytes), "sast-triad") {
		t.Errorf(".g8s/lane-bundles.yml Lane S is missing 'sast-triad' gate")
	}
}
