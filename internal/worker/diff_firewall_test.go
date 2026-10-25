package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func initGitRepoForDiffFirewall(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	runGit("init")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "allowed.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	return dir
}

func TestWorktreeDiffFirewallScopeEnforcement(t *testing.T) {
	t.Run("default CLI templates resolve for opencode, commandcode, codex", func(t *testing.T) {
		resolver := NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"opencode", "commandcode", "codex"},
		})

		argv, err := resolver("scan repo", "opencode", "opencode/free", "30s")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"opencode", "run", "--model", "opencode/free", "scan repo"}
		if !reflect.DeepEqual(argv, want) {
			t.Fatalf("got %v, want %v", argv, want)
		}

		argvNoModel, err := resolver("scan repo", "commandcode", "", "30s")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantNoModel := []string{"commandcode", "run", "scan repo"}
		if !reflect.DeepEqual(argvNoModel, wantNoModel) {
			t.Fatalf("got %v, want %v", argvNoModel, wantNoModel)
		}
	})

	t.Run("in-scope mutation under workspace_write passes", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.WriteFile(filepath.Join(dir, "src", "allowed.go"), []byte("package src\n// updated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"})
		if err != nil || len(violations) != 0 {
			t.Fatalf("expected nil error and 0 violations, got violations=%v err=%v", violations, err)
		}
	})

	t.Run("mutation under read_only fails closed", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.WriteFile(filepath.Join(dir, "src", "allowed.go"), []byte("package src\n// sneaky edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := VerifyWorktreeDiffScope(dir, "read_only", nil)
		if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_READ_ONLY_MUTATION") {
			t.Fatalf("expected E_DIFF_FIREWALL_READ_ONLY_MUTATION, got violations=%v err=%v", violations, err)
		}
	})

	t.Run("out-of-scope mutation under workspace_write fails closed", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# unauthorized\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"})
		if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_OUT_OF_SCOPE") {
			t.Fatalf("expected E_DIFF_FIREWALL_OUT_OF_SCOPE, got violations=%v err=%v", violations, err)
		}
		if len(violations) != 1 || violations[0] != "README.md" {
			t.Fatalf("unexpected violations: %v", violations)
		}
	})
}
