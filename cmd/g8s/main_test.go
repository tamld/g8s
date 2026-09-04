package main

import (
	"bytes"
	"encoding/json"
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

func TestSubmitAndWorkerE2E(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	stateDir := filepath.Join(tempDir, "state")
	configDir := filepath.Join(tempDir, "config")
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.MkdirAll(configDir, 0o700)

	// Mock provider configuration that outputs valid success JSON
	providersPath := filepath.Join(configDir, "providers.json")
	providerJSON := `{
  "version": "1.0",
  "providers": [
    {
      "name": "mock-success",
      "class": "platform_dispatch",
      "models": [{"id": "gemini-3.8-flash-high"}],
      "args": ["sh", "-c", "echo '{\"status\":\"succeeded\"}'"]
    }
  ]
}`
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0o600); err != nil {
		t.Fatalf("write mock providers: %v", err)
	}

	envVars := []string{
		"G8S_STATE_DIR=" + stateDir,
		"G8S_PROVIDERS=" + providersPath,
		"PATH=" + os.Getenv("PATH"),
	}

	// 1. Submit a task
	submitCmd := exec.Command(binPath, "submit", "--idempotency-key", "e2e-task-1", "--prompt", "inspect repo", "--json")
	submitCmd.Env = envVars
	submitOut, err := submitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit failed: %v\nOutput: %s", err, string(submitOut))
	}

	var submitEnv struct {
		Data struct {
			TaskID string `json:"task_id"`
			State  string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(submitOut, &submitEnv); err != nil {
		t.Fatalf("unmarshal submit output: %v\nOutput: %s", err, string(submitOut))
	}
	if submitEnv.Data.TaskID == "" {
		t.Fatalf("empty task_id in submit response: %s", string(submitOut))
	}
	taskID := submitEnv.Data.TaskID

	// 2. Run worker --once (should claim and succeed)
	workerCmd := exec.Command(binPath, "worker", "--once", "--json")
	workerCmd.Env = envVars
	workerOut, err := workerCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker failed: %v\nOutput: %s", err, string(workerOut))
	}

	// 3. Inspect task via g8s get
	getCmd := exec.Command(binPath, "get", taskID, "--json")
	getCmd.Env = envVars
	getOut, err := getCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("get failed: %v\nOutput: %s", err, string(getOut))
	}

	var getEnv struct {
		Data struct {
			TaskID string `json:"task_id"`
			State  string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getOut, &getEnv); err != nil {
		t.Fatalf("unmarshal get output: %v\nOutput: %s", err, string(getOut))
	}
	if getEnv.Data.State != "SUCCEEDED" {
		t.Fatalf("task state = %q, want SUCCEEDED\nGet output: %s\nWorker output: %s", getEnv.Data.State, string(getOut), string(workerOut))
	}

	// 4. Test Anti-Success-Theater: Mock worker that exits 0 but returns an error envelope on stdout
	errProviderJSON := `{
  "version": "1.0",
  "providers": [
    {
      "name": "mock-error-envelope",
      "class": "platform_dispatch",
      "models": [{"id": "gemini-3.8-flash-high"}],
      "args": ["sh", "-c", "echo '{\"v\":1,\"kind\":\"error\",\"cmd\":\"g8s\",\"error\":{\"code\":\"E_USAGE\",\"message\":\"unknown command \\\"--prompt-file\\\"\"}}'"]
    }
  ]
}`
	if err := os.WriteFile(providersPath, []byte(errProviderJSON), 0o600); err != nil {
		t.Fatalf("write err mock providers: %v", err)
	}

	// Submit second task
	submitCmd2 := exec.Command(binPath, "submit", "--idempotency-key", "e2e-task-2", "--prompt", "failing prompt", "--json")
	submitCmd2.Env = envVars
	submitOut2, err := submitCmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("submit2 failed: %v\nOutput: %s", err, string(submitOut2))
	}

	var submitEnv2 struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(submitOut2, &submitEnv2); err != nil {
		t.Fatalf("unmarshal submit2 output: %v", err)
	}
	taskID2 := submitEnv2.Data.TaskID

	// Run worker --once (child exits 0, but stdout is error envelope -> must fail)
	workerCmd2 := exec.Command(binPath, "worker", "--once", "--json")
	workerCmd2.Env = envVars
	_ = workerCmd2.Run() // worker may exit non-zero when task fails, which is expected

	// Verify task 2 is FAILED
	getCmd2 := exec.Command(binPath, "get", taskID2, "--json")
	getCmd2.Env = envVars
	getOut2, err := getCmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("get2 failed: %v\nOutput: %s", err, string(getOut2))
	}

	var getEnv2 struct {
		Data struct {
			TaskID    string  `json:"task_id"`
			State     string  `json:"state"`
			LastError *string `json:"last_error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getOut2, &getEnv2); err != nil {
		t.Fatalf("unmarshal get2 output: %v", err)
	}
	if getEnv2.Data.State == "SUCCEEDED" {
		t.Fatalf("anti-success-theater violation: task state is SUCCEEDED despite error envelope on stdout")
	}
	if getEnv2.Data.State != "FAILED" {
		t.Fatalf("task state = %q, want FAILED", getEnv2.Data.State)
	}
	if getEnv2.Data.LastError == nil || !strings.Contains(*getEnv2.Data.LastError, "unknown command") {
		t.Fatalf("expected last_error to mention 'unknown command', got %v", getEnv2.Data.LastError)
	}
}

func TestExitCodeSubmitBadArgs(t *testing.T) {
	binPath := buildG8sBinary(t)
	cmd := exec.Command(binPath, "submit")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected submit with no args to fail, but succeeded with output: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected exit code 2 on bad submit args, got %d\nOutput: %s", exitErr.ExitCode(), string(out))
	}
}

func TestExitCodeGetBadId(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	cmd := exec.Command(binPath, "get", "nonexistent-task-id-12345")
	cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected get nonexistent to fail, but succeeded with output: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1 on get nonexistent, got %d\nOutput: %s", exitErr.ExitCode(), string(out))
	}
}

func TestExitCodeVersion(t *testing.T) {
	binPath := buildG8sBinary(t)
	cmd := exec.Command(binPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected g8s --version to exit 0, got error: %v\nOutput: %s", err, string(out))
	}
}

func TestExitCodeWorkerOnceFailed(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	stateDir := filepath.Join(tempDir, "state")
	configDir := filepath.Join(tempDir, "config")
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.MkdirAll(configDir, 0o700)

	providersPath := filepath.Join(configDir, "providers.json")
	providerJSON := `{
  "version": "1.0",
  "providers": [
    {
      "name": "mock-error-envelope",
      "class": "platform_dispatch",
      "models": [{"id": "gemini-3.8-flash-high"}],
      "args": ["sh", "-c", "echo '{\"v\":1,\"kind\":\"error\",\"cmd\":\"g8s\",\"error\":{\"code\":\"E_USAGE\",\"message\":\"task failed deliberate\"}}'"]
    }
  ]
}`
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0o600); err != nil {
		t.Fatalf("write err mock providers: %v", err)
	}

	envVars := []string{
		"G8S_STATE_DIR=" + stateDir,
		"G8S_PROVIDERS=" + providersPath,
		"PATH=" + os.Getenv("PATH"),
	}

	submitCmd := exec.Command(binPath, "submit", "--idempotency-key", "worker-fail-task", "--prompt", "test fail", "--json")
	submitCmd.Env = envVars
	if out, err := submitCmd.CombinedOutput(); err != nil {
		t.Fatalf("submit failed: %v\nOutput: %s", err, string(out))
	}

	workerCmd := exec.Command(binPath, "worker", "--once", "--json")
	workerCmd.Env = envVars
	out, err := workerCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected worker --once to exit 1 on FAILED task, but got exit 0\nOutput: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1 from worker --once on FAILED task, got %d\nOutput: %s", exitErr.ExitCode(), string(out))
	}
}

func TestNoColorInPipe(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	envVars := append(os.Environ(), "G8S_DB="+dbPath)

	// 1. Submit with --json through pipe (exec.Command pipes stdout by default)
	submitCmd := exec.Command(binPath, "submit", "--idempotency-key", "no-color-task", "--prompt", "test ansi", "--json")
	submitCmd.Env = envVars
	submitOut, err := submitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit failed: %v\nOutput: %s", err, string(submitOut))
	}
	if bytes.Contains(submitOut, []byte("\033")) || bytes.Contains(submitOut, []byte("\x1b")) {
		t.Fatalf("submit --json output contains ANSI escape codes:\n%q", string(submitOut))
	}

	// 2. Doctor output through pipe
	docCmd := exec.Command(binPath, "doctor")
	docCmd.Env = envVars
	docOut, _ := docCmd.CombinedOutput()
	if bytes.Contains(docOut, []byte("\033")) || bytes.Contains(docOut, []byte("\x1b")) {
		t.Fatalf("doctor piped output contains ANSI escape codes:\n%q", string(docOut))
	}
}

func TestSubmitPromptFromFile(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	envVars := append(os.Environ(), "G8S_DB="+dbPath)

	promptFile := filepath.Join(tempDir, "prompt.txt")
	promptContent := "prompt loaded from file content"
	if err := os.WriteFile(promptFile, []byte(promptContent), 0o600); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	submitCmd := exec.Command(binPath, "submit", "--idempotency-key", "file-prompt-task", "--prompt-file", promptFile, "--json")
	submitCmd.Env = envVars
	submitOut, err := submitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit with --prompt-file failed: %v\nOutput: %s", err, string(submitOut))
	}

	var submitEnv struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(submitOut, &submitEnv); err != nil {
		t.Fatalf("unmarshal submit output: %v", err)
	}
	if submitEnv.Data.TaskID == "" {
		t.Fatalf("expected non-empty task_id in response: %s", string(submitOut))
	}

	// Verify task in DB has the file prompt
	getCmd := exec.Command(binPath, "get", submitEnv.Data.TaskID, "--json")
	getCmd.Env = envVars
	getOut, err := getCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("get task failed: %v\nOutput: %s", err, string(getOut))
	}
	if !strings.Contains(string(getOut), promptContent) {
		t.Fatalf("expected get task payload to contain %q, got: %s", promptContent, string(getOut))
	}
}

func TestSubmitPromptFromStdin(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	envVars := append(os.Environ(), "G8S_DB="+dbPath)

	promptContent := "prompt piped through stdin stream"
	submitCmd := exec.Command(binPath, "submit", "--idempotency-key", "stdin-prompt-task", "--json")
	submitCmd.Env = envVars
	submitCmd.Stdin = strings.NewReader(promptContent)
	submitOut, err := submitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit with stdin prompt failed: %v\nOutput: %s", err, string(submitOut))
	}

	var submitEnv struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(submitOut, &submitEnv); err != nil {
		t.Fatalf("unmarshal submit output: %v", err)
	}
	if submitEnv.Data.TaskID == "" {
		t.Fatalf("expected non-empty task_id in response: %s", string(submitOut))
	}

	// Verify task in DB has the stdin prompt
	getCmd := exec.Command(binPath, "get", submitEnv.Data.TaskID, "--json")
	getCmd.Env = envVars
	getOut, err := getCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("get task failed: %v\nOutput: %s", err, string(getOut))
	}
	if !strings.Contains(string(getOut), promptContent) {
		t.Fatalf("expected get task payload to contain %q, got: %s", promptContent, string(getOut))
	}
}

func TestSubmitPromptFileError(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	envVars := append(os.Environ(), "G8S_DB="+dbPath)

	submitCmd := exec.Command(binPath, "submit", "--idempotency-key", "bad-file-task", "--prompt-file", "/nonexistent/path/to/prompt.txt", "--json")
	submitCmd.Env = envVars
	out, err := submitCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected submit with nonexistent prompt file to fail, but got exit 0\nOutput: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1 on nonexistent prompt-file IO error, got %d\nOutput: %s", exitErr.ExitCode(), string(out))
	}
}

func TestVersionStamp(t *testing.T) {
	binPath := buildG8sBinary(t)

	// 1. Text output
	cmd := exec.Command(binPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s --version failed: %v\nOutput: %s", err, string(out))
	}
	output := string(out)
	if !strings.Contains(output, "g8s version") && !strings.Contains(output, Version) {
		t.Fatalf("version output missing version string:\n%s", output)
	}
	if !strings.Contains(output, "commit:") {
		t.Fatalf("version output missing commit stamp:\n%s", output)
	}
	if !strings.Contains(output, "built:") {
		t.Fatalf("version output missing build stamp:\n%s", output)
	}

	// 2. JSON output
	jsonCmd := exec.Command(binPath, "version", "--json")
	jsonOut, err := jsonCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s version --json failed: %v\nOutput: %s", err, string(jsonOut))
	}
	var env struct {
		Data struct {
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			BuildTime string `json:"build_time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonOut, &env); err != nil {
		t.Fatalf("unmarshal version json failed: %v\nOutput: %s", err, string(jsonOut))
	}
	if env.Data.Version != Version {
		t.Fatalf("expected version %q, got %q", Version, env.Data.Version)
	}
	if env.Data.Commit == "" {
		t.Fatalf("expected non-empty commit in version JSON data")
	}
	if env.Data.BuildTime == "" {
		t.Fatalf("expected non-empty build_time in version JSON data")
	}
}

func TestStderrFailureOutput(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	envVars := append(os.Environ(), "G8S_DB="+dbPath)

	tests := []struct {
		name           string
		args           []string
		env            []string
		wantExitCode   int
		wantStderrCont string
		wantNoStdout   bool
	}{
		{
			name:           "submit no args text mode -> envelope to stderr",
			args:           []string{"submit", "--json=false"},
			env:            envVars,
			wantExitCode:   2,
			wantStderrCont: `kind": "error`,
			wantNoStdout:   true,
		},
		{
			name:           "submit no args json mode -> envelope to stderr",
			args:           []string{"submit"},
			env:            envVars,
			wantExitCode:   2,
			wantStderrCont: `kind": "error`,
			wantNoStdout:   true,
		},
		{
			name:           "get nonexistent text mode -> envelope error to stderr",
			args:           []string{"get", "nonexistent-123", "--json=false"},
			env:            envVars,
			wantExitCode:   1,
			wantStderrCont: `kind": "error`,
			wantNoStdout:   true,
		},
		{
			name:           "get nonexistent json mode -> envelope to stderr",
			args:           []string{"get", "nonexistent-123", "--json"},
			env:            envVars,
			wantExitCode:   1,
			wantStderrCont: `kind": "error`,
			wantNoStdout:   true,
		},
		{
			name:           "unknown command text mode -> envelope error to stderr",
			args:           []string{"notacommand", "--json=false"},
			env:            envVars,
			wantExitCode:   2,
			wantStderrCont: `kind": "error`,
			wantNoStdout:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tc.args...)
			cmd.Env = tc.env

			var stdoutBuf, stderrBuf bytes.Buffer
			cmd.Stdout = &stdoutBuf
			cmd.Stderr = &stderrBuf

			err := cmd.Run()
			exitCode := 0
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}

			if exitCode != tc.wantExitCode {
				t.Fatalf("exit code: got %d, want %d\nstdout: %s\nstderr: %s", exitCode, tc.wantExitCode, stdoutBuf.String(), stderrBuf.String())
			}

			stderr := stderrBuf.String()
			stdout := stdoutBuf.String()

			if tc.wantStderrCont != "" && !strings.Contains(stderr, tc.wantStderrCont) {
				t.Fatalf("stderr missing %q:\nstderr: %s\nstdout: %s", tc.wantStderrCont, stderr, stdout)
			}
			if tc.wantNoStdout && strings.TrimSpace(stdout) != "" {
				t.Fatalf("expected empty stdout but got: %q\nstderr: %s", stdout, stderr)
			}
		})
	}
}
