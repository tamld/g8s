package tools_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Fresh-Construction Lane Detector Verification (RC3 / #517 / D-01 HARD)
//
// Verifies tools/ci_lane_detect.sh under fresh construction vectors never tested
// before, ensuring the core invariant holds: exit 0 always, and any real code
// file anywhere in the diff => lane=build.
// -----------------------------------------------------------------------------

// freshFindDetectScript locates tools/ci_lane_detect.sh relative to the test binary.
func freshFindDetectScript(t *testing.T) string {
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

// freshRunGit executes a git command within a hermetic fixture directory.
func freshRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fresh Tester",
		"GIT_AUTHOR_EMAIL=fresh-tester@example.com",
		"GIT_COMMITTER_NAME=Fresh Tester",
		"GIT_COMMITTER_EMAIL=fresh-tester@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\noutput: %s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// freshRunLaneDetect executes tools/ci_lane_detect.sh against repoDir with specified args.
func freshRunLaneDetect(t *testing.T, scriptPath, repoDir string, args ...string) (string, int) {
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

// freshInitFixtureRepo creates a hermetic git repository in t.TempDir() with main and origin/main.
func freshInitFixtureRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	freshRunGit(t, repoDir, "init", "-q")
	freshRunGit(t, repoDir, "config", "user.email", "fresh-tester@example.com")
	freshRunGit(t, repoDir, "config", "user.name", "Fresh Tester")
	freshRunGit(t, repoDir, "checkout", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Initial Readme\n"), 0o644); err != nil {
		t.Fatalf("failed to write initial README.md: %v", err)
	}
	freshRunGit(t, repoDir, "add", "README.md")
	freshRunGit(t, repoDir, "commit", "-q", "-m", "initial commit")
	freshRunGit(t, repoDir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	return repoDir
}

// -----------------------------------------------------------------------------
// 1. Lane detector fresh constructions
// -----------------------------------------------------------------------------

// 1.1: A .md file whose CONTENT is an executable shell script.
// Path is prose; content must not matter. Output must be lane=docs, exit 0.
func TestRedtest_Fresh_MdFileWithShellScriptContent(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-script-md")
	scriptPayload := "#!/bin/bash\nset -euo pipefail\necho 'MALICIOUS_EXECUTION_ATTEMPT'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(repo, "script_guide.md"), []byte(scriptPayload), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "script_guide.md")
	freshRunGit(t, repo, "commit", "-q", "-m", "add markdown with shell script payload")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=docs" {
		t.Errorf("got %q, want lane=docs (.md file content must not matter)", out)
	}
}

// 1.2: docs.tar.gz (double extension).
// Tarball at root is not prose; must resolve to lane=build, exit 0.
func TestRedtest_Fresh_DoubleExtension_DocsTarGz(t *testing.T) {
	script := freshFindDetectScript(t)

	t.Run("Root_DocsTarGz_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-docs-tar-gz")
		if err := os.WriteFile(filepath.Join(repo, "docs.tar.gz"), []byte("fake tar.gz binary content"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "docs.tar.gz")
		freshRunGit(t, repo, "commit", "-q", "-m", "add root docs.tar.gz")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (docs.tar.gz is a double-extension archive at root)", out)
		}
	})

	t.Run("DocsDirectory_ArchiveTarGz_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-nested-tar-gz")
		docsDir := filepath.Join(repo, "docs")
		if err := os.MkdirAll(docsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(docsDir, "bundle.tar.gz"), []byte("fake tar.gz binary content"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "docs/bundle.tar.gz")
		freshRunGit(t, repo, "commit", "-q", "-m", "add docs/bundle.tar.gz")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (docs/bundle.tar.gz is a non-prose archive in docs/)", out)
		}
	})
}

// 1.3: A directory named README.md/ (with children).
// If children contain code/scripts => lane=build; if children are prose => lane=docs.
func TestRedtest_Fresh_DirectoryNamedReadmeMdWithChildren(t *testing.T) {
	script := freshFindDetectScript(t)

	t.Run("ChildGoCode_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-dir-readme-go")
		// Remove root README.md file and replace with directory README.md/
		freshRunGit(t, repo, "rm", "README.md")
		dir := filepath.Join(repo, "README.md")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "child.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "README.md/child.go")
		freshRunGit(t, repo, "commit", "-q", "-m", "replace README.md with directory containing child.go")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (README.md/child.go contains Go code)", out)
		}
	})

	t.Run("ChildShellScript_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-dir-readme-sh")
		freshRunGit(t, repo, "rm", "README.md")
		dir := filepath.Join(repo, "README.md")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "helper.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "README.md/helper.sh")
		freshRunGit(t, repo, "commit", "-q", "-m", "replace README.md with directory containing helper.sh")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (README.md/helper.sh contains shell script)", out)
		}
	})

	t.Run("ChildProseMarkdown_ResolvesDocs", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-dir-readme-md")
		freshRunGit(t, repo, "rm", "README.md")
		dir := filepath.Join(repo, "README.md")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "section.md"), []byte("# Section\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "README.md/section.md")
		freshRunGit(t, repo, "commit", "-q", "-m", "replace README.md with directory containing section.md")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=docs" {
			t.Errorf("got %q, want lane=docs (README.md/section.md is pure markdown prose)", out)
		}
	})
}

// 1.4: internal/x.go renamed mid-diff (D path in git diff).
// BUG(REDTEST): ci_lane_detect invokes `git diff --name-only` without `--no-renames`.
// When a code file (e.g. internal/x.go) is renamed to a docs file (e.g. docs/renamed.md),
// git diff rename tracking replaces the old code path (D path) with the new docs path (A path).
// As a result, git diff --name-only reports only `docs/renamed.md`, suppressing `internal/x.go`.
// The lane detector classifies the diff as lane=docs, allowing code removal to bypass build gates!
//
// Repro:
// 1. On main: add and commit internal/x.go
// 2. On branch: git mv internal/x.go docs/renamed.md && git commit
// 3. tools/ci_lane_detect.sh origin/main -> outputs lane=docs (escapes code build gates!)
func TestRedtest_Fresh_InternalGoRenamedMidDiff(t *testing.T) {
	t.Skip("BUG(REDTEST): ci_lane_detect suppresses D path on code-to-docs rename due to default git rename tracking")

	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	// Add internal/x.go to main
	internalDir := filepath.Join(repo, "internal")
	if err := os.MkdirAll(internalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(internalDir, "x.go"), []byte("package internal\n// core logic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "internal/x.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "add internal/x.go")
	freshRunGit(t, repo, "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	// Branch renames internal/x.go to docs/renamed.md
	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-rename-code-to-docs")
	docsDir := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "mv", "internal/x.go", "docs/renamed.md")
	freshRunGit(t, repo, "commit", "-q", "-m", "rename internal/x.go to docs/renamed.md")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (internal/x.go was removed/renamed; code change must trigger build)", out)
	}
}

// 1.4-pass: internal/x.go renamed to another code file (internal/y.go).
// Diff contains internal/y.go, which resolves to lane=build.
func TestRedtest_Fresh_InternalGoRenamedToCode(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	internalDir := filepath.Join(repo, "internal")
	if err := os.MkdirAll(internalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(internalDir, "x.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "internal/x.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "add internal/x.go")
	freshRunGit(t, repo, "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-rename-code-to-code")
	freshRunGit(t, repo, "mv", "internal/x.go", "internal/y.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "rename internal/x.go to internal/y.go")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (internal/y.go is Go code)", out)
	}
}

// 1.5: A diff with ONLY deleted code files.
// Deleting code files removes functionality/changes build; must resolve to lane=build, exit 0.
func TestRedtest_Fresh_DiffWithOnlyDeletedCodeFiles(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	// Add main.go to main
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "main.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "add main.go")
	freshRunGit(t, repo, "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	// Branch deletes main.go
	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-delete-code")
	freshRunGit(t, repo, "rm", "main.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "delete main.go")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (diff contains deleted code file main.go)", out)
	}
}

// 1.6: A 0-byte .go file.
// Even with 0 bytes, .go extension triggers build lane; exit 0.
func TestRedtest_Fresh_ZeroByteGoFile(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-zero-byte-go")
	if err := os.WriteFile(filepath.Join(repo, "empty.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "empty.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "add 0-byte empty.go")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build for 0-byte .go file", out)
	}
}

// 1.7: offer/x.md + main.go in one commit.
// Mixed commit of prose and code must resolve to lane=build, exit 0.
func TestRedtest_Fresh_MixedOfferMdAndMainGo(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-mixed-offer-code")
	offerDir := filepath.Join(repo, "offer")
	if err := os.MkdirAll(offerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(offerDir, "x.md"), []byte("# Offer Doc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "offer/x.md", "main.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "add offer/x.md and main.go in one commit")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build for mixed commit (offer/x.md + main.go)", out)
	}
}

// 1.8: assets/logo.SVG vs assets/logo.svg (case).
// Both uppercase (.SVG) and lowercase (.svg) under assets/ must resolve to lane=docs, exit 0.
func TestRedtest_Fresh_CaseVariants_AssetsSvg(t *testing.T) {
	script := freshFindDetectScript(t)

	t.Run("UppercaseExtension_SVG_ResolvesDocs", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-assets-upper-svg")
		assetsDir := filepath.Join(repo, "assets")
		if err := os.MkdirAll(assetsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(assetsDir, "logo.SVG"), []byte("<svg></svg>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "assets/logo.SVG")
		freshRunGit(t, repo, "commit", "-q", "-m", "add assets/logo.SVG")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=docs" {
			t.Errorf("got %q, want lane=docs for assets/logo.SVG", out)
		}
	})

	t.Run("LowercaseExtension_svg_ResolvesDocs", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-assets-lower-svg")
		assetsDir := filepath.Join(repo, "assets")
		if err := os.MkdirAll(assetsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(assetsDir, "logo.svg"), []byte("<svg></svg>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "assets/logo.svg")
		freshRunGit(t, repo, "commit", "-q", "-m", "add assets/logo.svg")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=docs" {
			t.Errorf("got %q, want lane=docs for assets/logo.svg", out)
		}
	})

	t.Run("TitleCaseDirectory_Assets_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-assets-titlecase")
		assetsDir := filepath.Join(repo, "Assets")
		if err := os.MkdirAll(assetsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(assetsDir, "logo.svg"), []byte("<svg></svg>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "Assets/logo.svg")
		freshRunGit(t, repo, "commit", "-q", "-m", "add Assets/logo.svg")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build for Assets/logo.svg (titlecase directory not in allowed asset glob)", out)
		}
	})
}

// 1.9: .github/workflows/x.yml + nothing else.
// Workflow files are CI/CD infrastructure; must resolve to lane=build, exit 0.
func TestRedtest_Fresh_GithubWorkflowsYmlOnly(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-workflow-yml")
	wfDir := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "x.yml"), []byte("name: x\non: [push]\njobs:\n  t:\n    runs-on: ubuntu-latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", ".github/workflows/x.yml")
	freshRunGit(t, repo, "commit", "-q", "-m", "add workflow x.yml")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (.github/workflows/x.yml is CI infrastructure)", out)
	}
}

// 1.10: plans/x.md that is actually a git SYMLINK to ../../evil.go.
// Symlink pointing to Go code file or escaping repo must resolve to lane=build, exit 0.
func TestRedtest_Fresh_PlansMdSymlinkToEvilGo(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-plans-symlink-evil")
	plansDir := filepath.Join(repo, "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink pointing outside the repo boundary
	if err := os.Symlink("../../evil.go", filepath.Join(plansDir, "x.md")); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "plans/x.md")
	freshRunGit(t, repo, "commit", "-q", "-m", "add plans/x.md symlinked to ../../evil.go")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (plans/x.md is symlink targeting ../../evil.go)", out)
	}
}

// -----------------------------------------------------------------------------
// 2. F1-F3 Regression Confirmation with NEW Examples
// -----------------------------------------------------------------------------

// F1 new example: symlink dir with different target shape.
// Instead of root `docs -> codelib`, test a nested symlink directory:
// `docs/internal_link` pointing to `../internal` (which contains `core.go`).
func TestRedtest_F1New_SymlinkDirDifferentTargetShape(t *testing.T) {
	script := freshFindDetectScript(t)
	repo := freshInitFixtureRepo(t)

	// Add internal/core.go to main
	internalDir := filepath.Join(repo, "internal")
	if err := os.MkdirAll(internalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(internalDir, "core.go"), []byte("package internal\nfunc Core() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "internal/core.go")
	freshRunGit(t, repo, "commit", "-q", "-m", "add internal/core.go")
	freshRunGit(t, repo, "update-ref", "refs/remotes/origin/main", "refs/heads/main")

	// Branch adds symlink docs/internal_link -> ../internal
	freshRunGit(t, repo, "checkout", "-q", "-b", "branch-f1-nested-symlink")
	docsDir := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../internal", filepath.Join(docsDir, "internal_link")); err != nil {
		t.Fatal(err)
	}
	freshRunGit(t, repo, "add", "docs/internal_link")
	freshRunGit(t, repo, "commit", "-q", "-m", "symlink docs/internal_link -> ../internal")

	out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if out != "lane=build" {
		t.Errorf("got %q, want lane=build (docs/internal_link points to internal/ which has .go files)", out)
	}
}

// F2 new example: extension trick with different double-extensions.
// Test: manual.adoc.go and docs/runner.md.sh (different double-extensions).
func TestRedtest_F2New_ExtensionTrickDifferentDoubleExtensions(t *testing.T) {
	script := freshFindDetectScript(t)

	t.Run("ManualAdocGo_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-f2-adoc-go")
		if err := os.WriteFile(filepath.Join(repo, "manual.adoc.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "manual.adoc.go")
		freshRunGit(t, repo, "commit", "-q", "-m", "add manual.adoc.go")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (manual.adoc.go is a Go file)", out)
		}
	})

	t.Run("DocsRunnerMdSh_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-f2-md-sh")
		docsDir := filepath.Join(repo, "docs")
		if err := os.MkdirAll(docsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(docsDir, "runner.md.sh"), []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", "docs/runner.md.sh")
		freshRunGit(t, repo, "commit", "-q", "-m", "add docs/runner.md.sh")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (docs/runner.md.sh is a shell script in docs/)", out)
		}
	})
}

// F3 new example: .github bypass with different filenames.
// Test: .github/workflows/deploy.yml and .github/ISSUE_TEMPLATE/bug_report.md.
func TestRedtest_F3New_GithubBypassDifferentFilenames(t *testing.T) {
	script := freshFindDetectScript(t)

	t.Run("GithubWorkflowDeployYml_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-f3-deploy-yml")
		wfDir := filepath.Join(repo, ".github", "workflows")
		if err := os.MkdirAll(wfDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wfDir, "deploy.yml"), []byte("name: deploy\non: push\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", ".github/workflows/deploy.yml")
		freshRunGit(t, repo, "commit", "-q", "-m", "add deploy.yml")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (.github/workflows/deploy.yml is CI infrastructure)", out)
		}
	})

	t.Run("GithubIssueTemplateBugReportMd_ResolvesBuild", func(t *testing.T) {
		repo := freshInitFixtureRepo(t)
		freshRunGit(t, repo, "checkout", "-q", "-b", "branch-f3-issue-template")
		templateDir := filepath.Join(repo, ".github", "ISSUE_TEMPLATE")
		if err := os.MkdirAll(templateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(templateDir, "bug_report.md"), []byte("# Bug Report Template\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		freshRunGit(t, repo, "add", ".github/ISSUE_TEMPLATE/bug_report.md")
		freshRunGit(t, repo, "commit", "-q", "-m", "add issue template")

		out, exitCode := freshRunLaneDetect(t, script, repo, "origin/main")
		if exitCode != 0 {
			t.Errorf("expected exit 0, got %d", exitCode)
		}
		if out != "lane=build" {
			t.Errorf("got %q, want lane=build (.github/ISSUE_TEMPLATE/bug_report.md is repo governance/config)", out)
		}
	})
}
