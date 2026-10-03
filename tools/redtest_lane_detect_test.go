package tools_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func findDetectScript(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"ci_lane_detect.sh",
		filepath.Join("tools", "ci_lane_detect.sh"),
		filepath.Join("..", "tools", "ci_lane_detect.sh"),
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("could not find ci_lane_detect.sh")
	return ""
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=CI Tester",
		"GIT_AUTHOR_EMAIL=ci-test@example.com",
		"GIT_COMMITTER_NAME=CI Tester",
		"GIT_COMMITTER_EMAIL=ci-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\noutput: %s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func runLaneDetect(t *testing.T, scriptPath, repoDir string, args ...string) (string, int) {
	t.Helper()
	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return strings.TrimSpace(string(out)), exitCode
}

func initFixtureRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-q")
	runGit(t, repoDir, "config", "user.email", "ci-test@example.com")
	runGit(t, repoDir, "config", "user.name", "CI Tester")
	runGit(t, repoDir, "checkout", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatalf("failed to write initial README.md: %v", err)
	}
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-q", "-m", "init")

	// Set up origin/main tracking ref so origin/main is resolvable
	runGit(t, repoDir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	return repoDir
}

// -----------------------------------------------------------------------------
// Guarantee 4: CI lane detector resists path tricks
// Verify: any real code file (by extension or location) anywhere in the diff => lane=build,
// and the detector never crashes (exit 0 always).
// -----------------------------------------------------------------------------

// BUG(REDTEST): ci_lane_detect classifies symlink `docs` pointing to a directory
// containing .go code files as lane=docs instead of lane=build.
// Repro:
// 1. mkdir codelib && echo "package codelib" > codelib/lib.go && git add codelib && git commit
// 2. ln -s codelib docs && git add docs && git commit -m "symlink docs -> codelib"
// 3. tools/ci_lane_detect.sh origin/main -> outputs lane=docs (escapes code gates!)
func TestRedtest_Guarantee4_SymlinkDocsToCodeDirectory(t *testing.T) {
	t.Skip("BUG(REDTEST): ci_lane_detect classifies symlink `docs` pointing to directory with .go files as lane=docs instead of lane=build")

	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	// Add codelib to main
	codeDir := filepath.Join(repo, "codelib")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeDir, "lib.go"), []byte("package codelib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "codelib")
	runGit(t, repo, "commit", "-q", "-m", "add codelib")
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	// Create branch with symlink docs -> codelib
	runGit(t, repo, "checkout", "-q", "-b", "branch-symlink")
	if err := os.Symlink("codelib", filepath.Join(repo, "docs")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "docs")
	runGit(t, repo, "commit", "-q", "-m", "symlink docs to codelib")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (symlink points to directory containing .go files)", out)
	}
}

// BUG(REDTEST): ci_lane_detect classifies real code files (.go) placed under docs/
// as lane=docs because of glob `docs/*`, allowing code inside docs/ to bypass build gates.
// Repro:
// 1. mkdir docs && echo "package evil" > docs/evil.go && git add docs/evil.go && git commit
// 2. tools/ci_lane_detect.sh origin/main -> outputs lane=docs (escapes code gates!)
func TestRedtest_Guarantee4_CodeFileInsideDocsDirectory(t *testing.T) {
	t.Skip("BUG(REDTEST): ci_lane_detect classifies docs/evil.go as lane=docs instead of lane=build")

	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "branch-docs-go")
	docsDir := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "evil.go"), []byte("package evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "docs/evil.go")
	runGit(t, repo, "commit", "-q", "-m", "add docs/evil.go")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (docs/evil.go is a real Go code file)", out)
	}
}

// BUG(REDTEST): ci_lane_detect classifies .md files inside .github/workflows/ as lane=docs
// instead of lane=build, violating ADR-0031 location rule (infrastructure is build lane).
// Repro:
// 1. mkdir -p .github/workflows && echo "# workflow note" > .github/workflows/README.md && git add . && git commit
// 2. tools/ci_lane_detect.sh origin/main -> outputs lane=docs (infrastructure changes skip build gates!)
func TestRedtest_Guarantee4_MarkdownInsideGithubWorkflows(t *testing.T) {
	t.Skip("BUG(REDTEST): ci_lane_detect classifies .md inside .github/workflows/ as lane=docs instead of lane=build")

	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "branch-workflow-md")
	workflowDir := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "README.md"), []byte("# Workflow Documentation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".github/workflows/README.md")
	runGit(t, repo, "commit", "-q", "-m", "add workflow readme")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (.github/workflows is CI/infrastructure)", out)
	}
}

func TestRedtest_Guarantee4_EvilMdGoFile(t *testing.T) {
	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "branch-evil-md-go")
	if err := os.WriteFile(filepath.Join(repo, "evil.md.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "evil.md.go")
	runGit(t, repo, "commit", "-q", "-m", "add evil.md.go")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (evil.md.go is a Go file)", out)
	}
}

func TestRedtest_Guarantee4_100MBBlobFile(t *testing.T) {
	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "branch-100mb")
	largeFilePath := filepath.Join(repo, "README.md")
	f, err := os.OpenFile(largeFilePath, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Write 100MB of markdown comment lines
	chunk := bytes.Repeat([]byte("<!-- large blob padding line 64 bytes ------------------------ -->\n"), 16384) // 1MB
	for i := 0; i < 100; i++ {
		if _, err := f.Write(chunk); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	f.Close()

	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-q", "-m", "add 100MB blob README.md")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("detector crashed on 100MB file: exit code %d", exitCode)
	}
	if out != "lane=docs" {
		t.Errorf("got %q, want lane=docs for 100MB README.md", out)
	}
}

func TestRedtest_Guarantee4_PathsWithSpaces(t *testing.T) {
	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "branch-spaces-go")
	if err := os.WriteFile(filepath.Join(repo, "path with spaces.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "path with spaces.go")
	runGit(t, repo, "commit", "-q", "-m", "add file with spaces")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build for path with spaces.go", out)
	}
}

func TestRedtest_Guarantee4_PathsWithNewlines(t *testing.T) {
	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "branch-newlines-go")
	newlineFile := "file\nwith\nnewlines.go"
	if err := os.WriteFile(filepath.Join(repo, newlineFile), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", newlineFile)
	runGit(t, repo, "commit", "-q", "-m", "add file with newlines")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build for file with newlines", out)
	}
}

func TestRedtest_Guarantee4_CaseVariants(t *testing.T) {
	script := findDetectScript(t)

	t.Run("DocsTitlecase_WithGoFile_ResolvesBuild", func(t *testing.T) {
		repo := initFixtureRepo(t)
		runGit(t, repo, "checkout", "-q", "-b", "branch-case-go")
		dir := filepath.Join(repo, "Docs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "add", "Docs/x.go")
		runGit(t, repo, "commit", "-q", "-m", "add Docs/x.go")

		out, exitCode := runLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build for Docs/x.go", out)
		}
	})

	t.Run("DOCSUppercase_WithMdFile_ResolvesDocs", func(t *testing.T) {
		repo := initFixtureRepo(t)
		runGit(t, repo, "checkout", "-q", "-b", "branch-case-md")
		dir := filepath.Join(repo, "DOCS")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte("# Uppercase Docs\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "add", "DOCS/x.md")
		runGit(t, repo, "commit", "-q", "-m", "add DOCS/x.md")

		out, exitCode := runLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=docs" {
			t.Errorf("got %q, want lane=docs for DOCS/x.md", out)
		}
	})
}

func TestRedtest_Guarantee4_EmptyCommit(t *testing.T) {
	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "branch-empty-commit")
	runGit(t, repo, "commit", "--allow-empty", "-q", "-m", "empty commit")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build for empty commit (deny-by-default)", out)
	}
}

// -----------------------------------------------------------------------------
// Guarantee 5: Pre-push fast path cannot skip code gates.
// Construct a diff that is docs files ONLY in origin/main...HEAD but has STAGED
// code changes (the cached fallback path) — verify the detector counts staged code
// and resolves build lane (not docs).
// -----------------------------------------------------------------------------

// BUG(REDTEST): ci_lane_detect ignores staged code changes when origin/main...HEAD
// has docs-only commits, skipping code gates with lane=docs during pre-push.
// Repro:
// 1. git checkout -b feature origin/main
// 2. echo "# doc" > docs/guide.md && git add docs/guide.md && git commit -m "docs"
// 3. echo "package main" > main.go && git add main.go  (staged code change)
// 4. tools/ci_lane_detect.sh origin/main -> outputs lane=docs instead of lane=build!
func TestRedtest_Guarantee5_PrePushStagedCodeBypass(t *testing.T) {
	t.Skip("BUG(REDTEST): ci_lane_detect ignores staged code changes when origin/main...HEAD has docs commits")

	script := findDetectScript(t)
	repo := initFixtureRepo(t)

	// Branch with docs-only commit against origin/main
	runGit(t, repo, "checkout", "-q", "-b", "branch-feature-docs")
	docsDir := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "docs/guide.md")
	runGit(t, repo, "commit", "-q", "-m", "add docs guide")

	// Now introduce STAGED code changes (simulating pre-push with uncommitted staged changes)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "main.go")

	out, exitCode := runLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (staged main.go contains code changes that cannot skip code gates)", out)
	}
}

func TestRedtest_Guarantee5_PrePushFallbackPathsWithoutUpstreamDiff(t *testing.T) {
	script := findDetectScript(t)

	t.Run("StagedCodeOnly_ResolvesBuild", func(t *testing.T) {
		repo := initFixtureRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "service.go"), []byte("package service\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "add", "service.go")

		// Point detector at non-existent ref to force staged/unstaged fallback
		out, exitCode := runLaneDetect(t, script, repo, "nonexistent_ref")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build for staged service.go", out)
		}
	})

	t.Run("StagedDocsOnly_ResolvesDocs", func(t *testing.T) {
		repo := initFixtureRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "notes.md"), []byte("# Notes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "add", "notes.md")

		out, exitCode := runLaneDetect(t, script, repo, "nonexistent_ref")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=docs" {
			t.Errorf("got %q, want lane=docs for staged notes.md", out)
		}
	})
}
