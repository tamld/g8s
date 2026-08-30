package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/pathutil"
)

func TestReportErrorWritesToConfiguredWriter(t *testing.T) {
	var buf bytes.Buffer
	reportError(errors.New("boom"), &buf)
	want := fmt.Sprintf("%s: boom", AppName)
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("reportError output = %q, want it to contain %q", buf.String(), want)
	}
}

func TestDatabasePathHonorsEnvOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom", "gatekeepers.db")
	t.Setenv("G8S_DB", override)

	got, err := databasePath()
	if err != nil {
		t.Fatalf("databasePath() error = %v", err)
	}
	if got != override {
		t.Fatalf("databasePath() = %q, want env override %q", got, override)
	}
}

func TestDatabasePathDefaultsToHomeStateDirectory(t *testing.T) {
	t.Setenv("G8S_DB", "")

	got, err := databasePath()
	if err != nil {
		t.Fatalf("databasePath() error = %v", err)
	}
	want := pathutil.DefaultDatabasePath()
	if got != want {
		t.Fatalf("databasePath() = %q, want %q", got, want)
	}
	if info, err := os.Stat(filepath.Dir(got)); err != nil || !info.IsDir() {
		t.Fatalf("parent directory of default path was not created 0700: stat err=%v", err)
	}
}

func TestDatabasePathCreatesParentDirectoriesWithRestrictedPermissions(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "deep", "nested")
	t.Setenv("G8S_DB", filepath.Join(nested, "g8s.db"))

	if _, err := databasePath(); err != nil {
		t.Fatalf("databasePath() error = %v", err)
	}
	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat nested dir: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("created directory perms = %o, want no group/other access", perm)
		}
	}
}

type stringFlagAccumulator interface {
	String() string
	Set(string) error
}

func TestPathFlagsAccumulatesRepeatedValuesInOrder(t *testing.T) {
	var paths pathFlags

	for _, p := range []string{"/tmp/alpha", "/tmp/beta", "/tmp/gamma"} {
		if err := paths.Set(p); err != nil {
			t.Fatalf("Set(%q) error = %v", p, err)
		}
	}
	if paths.String() != "/tmp/alpha,/tmp/beta,/tmp/gamma" {
		t.Fatalf("String() = %q, want comma-joined ordered list", paths.String())
	}
	if len([]string(paths)) != 3 {
		t.Fatalf("len(paths) = %d, want 3", len([]string(paths)))
	}
	var _ stringFlagAccumulator = &paths
}

func TestPrintUsageMentionsEveryLiveCommand(t *testing.T) {
	usage := captureUsage(t)
	commands := []string{
		"submit", "get", "resume", "tasks", "cancel", "lineage", "children",
		"receipt", "doctor", "init", "config", "completion", "service",
		"analyze", "vault", "worker", "mcp", "roles", "permissions", "providers",
		"version", "orchestrate", "orchestrate-aic", "supervisor-metrics",
		"brief-issue", "brief-consume", "cleanup-worktrees", "cleanup", "migrate", "status",
	}
	for _, cmd := range commands {
		if !strings.Contains(usage, cmd) {
			t.Errorf("usage text missing live command %q", cmd)
		}
	}
}

// captureUsage redirects stdout while printUsage runs so the advertised
// command surface can be asserted without spawning a subprocess.
func captureUsage(t *testing.T) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	printUsage()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func TestSubmitHelp(t *testing.T) {
	binPath := buildG8sBinary(t)
	cmd := exec.Command(binPath, "submit", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit --help failed: %v\nOutput: %s", err, string(out))
	}
	output := string(out)
	if strings.Contains(output, `"kind":"error"`) || strings.Contains(output, "E_USAGE") {
		t.Fatalf("submit --help returned error envelope:\n%s", output)
	}
	if !strings.Contains(output, "idempotency-key") || !strings.Contains(output, "prompt") {
		t.Fatalf("submit --help output missing expected flag definitions:\n%s", output)
	}
}

func TestAllSubcommandsHelp(t *testing.T) {
	binPath := buildG8sBinary(t)
	subcommands := []string{
		"version",
		"roles",
		"permissions",
		"providers",
		"submit",
		"get",
		"resume",
		"tasks",
		"cancel",
		"lineage",
		"children",
		"receipt",
		"doctor",
		"init",
		"config",
		"completion",
		"service",
		"worker",
		"analyze",
		"vault",
		"orchestrate",
		"orchestrate-aic",
		"supervisor-metrics",
		"brief-issue",
		"brief-consume",
		"cleanup-worktrees",
		"cleanup",
		"status",
		"state",
		"migrate",
		"converge",
		"sleep",
		"wake",
		"mcp",
	}

	for _, sub := range subcommands {
		t.Run(sub, func(t *testing.T) {
			cmd := exec.Command(binPath, sub, "--help")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s --help failed: %v\nOutput: %s", sub, err, string(out))
			}
			output := string(out)
			if strings.Contains(output, `"kind":"error"`) || strings.Contains(output, "E_USAGE") {
				t.Fatalf("%s --help returned error envelope:\n%s", sub, output)
			}
			if len(strings.TrimSpace(output)) == 0 {
				t.Fatalf("%s --help returned empty output", sub)
			}
		})
	}
}

