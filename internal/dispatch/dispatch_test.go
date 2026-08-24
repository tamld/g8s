package dispatch

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func makeResolver(files map[string]bool, which map[string]string, env map[string]string, home, platform string) ResolveOptions {
	return ResolveOptions{
		EnvLookup: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
		Platform: platform,
		Home:     home,
		Which: func(name string) (string, error) {
			match, ok := which[name]
			if !ok {
				return "", errors.New("exec: not found")
			}
			return match, nil
		},
		Exists: func(path string) bool {
			return files[path]
		},
	}
}

func TestResolveExplicitBeatsEnvAndPath(t *testing.T) {
	opts := makeResolver(
		map[string]bool{"/opt/explicit/agy": true, "/opt/env/agy": true},
		map[string]string{"agy": "/usr/bin/agy"},
		map[string]string{"AGY_BIN": "/opt/env/agy"},
		"/home/u",
		"linux",
	)
	resolved, err := ResolveBinary("/opt/explicit/agy", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/opt/explicit/agy" {
		t.Fatalf("expected explicit path, got %q", resolved)
	}
}

func TestResolveEnvOverrideUsedWithoutExplicit(t *testing.T) {
	opts := makeResolver(
		map[string]bool{"/opt/env/agy": true},
		map[string]string{"agy": "/usr/bin/agy"},
		map[string]string{"AGY_BIN": "/opt/env/agy"},
		"/home/u",
		"linux",
	)
	resolved, err := ResolveBinary("", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/opt/env/agy" {
		t.Fatalf("expected AGY_BIN override, got %q", resolved)
	}
}

func TestResolvePATHLookupFallback(t *testing.T) {
	opts := makeResolver(
		map[string]bool{},
		map[string]string{"agy": "/usr/local/bin/agy"},
		nil,
		"/home/u",
		"linux",
	)
	resolved, err := ResolveBinary("", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/usr/local/bin/agy" {
		t.Fatalf("expected PATH match, got %q", resolved)
	}
}

func TestResolveWindowsSuffixExpansion(t *testing.T) {
	opts := makeResolver(
		map[string]bool{"/tools/agy.cmd": true},
		nil,
		nil,
		`C:\Users\u`,
		"windows",
	)
	resolved, err := ResolveBinary("/tools/agy", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/tools/agy.cmd" {
		t.Fatalf("expected .cmd expansion, got %q", resolved)
	}
}

func TestResolveWindowsSuffixOrderExePreferred(t *testing.T) {
	opts := makeResolver(
		map[string]bool{"/t/agy.exe": true, "/t/agy.cmd": true, "/t/agy.bat": true},
		nil,
		nil,
		"",
		"windows",
	)
	resolved, _ := ResolveBinary("/t/agy", opts)
	if resolved != "/t/agy.exe" {
		t.Fatalf("expected .exe first, got %q", resolved)
	}
}

func TestResolveHomeFallbacksOrderedUnix(t *testing.T) {
	opts := makeResolver(
		map[string]bool{
			"/home/u/.local/bin/agy":                 true,
			"/home/u/AppData/Local/Programs/agy/agy": true,
		},
		nil,
		nil,
		"/home/u",
		"linux",
	)
	resolved, err := ResolveBinary("", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/home/u/.local/bin/agy" {
		t.Fatalf("expected first home fallback, got %q", resolved)
	}
}

func TestResolveHomeFallbackWindowsNPMCmd(t *testing.T) {
	winOpts := makeResolver(
		map[string]bool{"C:/Users/u/AppData/Roaming/npm/agy.cmd": true},
		nil,
		nil,
		"C:/Users/u",
		"windows",
	)
	resolved, err := ResolveBinary("", winOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "C:/Users/u/AppData/Roaming/npm/agy.cmd" {
		t.Fatalf("expected npm agy.cmd fallback, got %q", resolved)
	}
}

func TestResolveTildeExpansion(t *testing.T) {
	opts := makeResolver(
		map[string]bool{"/home/u/bin/agy": true},
		nil,
		nil,
		"/home/u",
		"linux",
	)
	resolved, err := ResolveBinary("~/bin/agy", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/home/u/bin/agy" {
		t.Fatalf("expected expanded tilde path, got %q", resolved)
	}
}

func TestResolveNotFoundReturnsError(t *testing.T) {
	opts := makeResolver(nil, nil, nil, "", "linux")
	_, err := ResolveBinary("", opts)
	if err == nil || !strings.Contains(err.Error(), "AGY binary not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestBuildCommandDefaultSandboxAndArgs(t *testing.T) {
	cmd := BuildCommand(CommandOptions{
		Binary:  "/usr/bin/agy",
		Prompt:  "do things",
		Model:   "Gemini 3.7 Flash (High)",
		Timeout: "5m0s",
		AddDirs: []string{"~/work", "/tmp/x"},
		Home:    "/home/u",
	})
	want := []string{
		"/usr/bin/agy",
		"--prompt", "do things",
		"--model", "Gemini 3.7 Flash (High)",
		"--print-timeout", "5m0s",
		"--sandbox",
		"--add-dir", "/home/u/work",
		"--add-dir", "/tmp/x",
	}
	if strings.Join(cmd, "|") != strings.Join(want, "|") {
		t.Fatalf("argv mismatch:\n got: %v\nwant: %v", cmd, want)
	}
}

func TestBuildCommandSkipPermissionsKeepsSandbox(t *testing.T) {
	cmd := BuildCommand(CommandOptions{
		Binary:          "/agy",
		Prompt:          "p",
		Model:           "m",
		Timeout:         "1s",
		SkipPermissions: true,
	})
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Fatal("missing --dangerously-skip-permissions")
	}
	if !strings.Contains(joined, "--sandbox") {
		t.Fatal("--sandbox must stay enabled by default alongside skip-permissions")
	}
}

func TestBuildCommandNoSandboxOmitsFlags(t *testing.T) {
	cmd := BuildCommand(CommandOptions{
		Binary:    "/agy",
		Prompt:    "p",
		Model:     "m",
		Timeout:   "1s",
		NoSandbox: true,
	})
	for _, arg := range cmd {
		if arg == "--sandbox" || arg == "--dangerously-skip-permissions" {
			t.Fatalf("unexpected flag %q with NoSandbox", arg)
		}
	}
}

func TestDetectIgnoresNegativeInstruction(t *testing.T) {
	violations := DetectReadOnlyContractViolations(
		"Do not run wiki.py reflect. Also avoid git push entirely.\ngit status was clean.",
		"",
	)
	if len(violations) != 0 {
		t.Fatalf("expected zero violations, got %+v", violations)
	}
}

func TestDetectSessionLogSideEffectWithSnippetEscaping(t *testing.T) {
	violations := DetectReadOnlyContractViolations(
		"context line\nSession logged to log.md\ntail line",
		"",
	)
	if len(violations) != 1 {
		t.Fatalf("expected one violation, got %+v", violations)
	}
	if violations[0].Type != "wiki_reflect_side_effect" {
		t.Fatalf("wrong type %q", violations[0].Type)
	}
	if strings.Contains(violations[0].Snippet, "\n") {
		t.Fatalf("snippet newlines must be escaped: %q", violations[0].Snippet)
	}
	if !strings.Contains(violations[0].Snippet, "Session logged") {
		t.Fatalf("snippet lost evidence: %q", violations[0].Snippet)
	}
}

func TestDetectWikiMutationShapes(t *testing.T) {
	commandHits := DetectReadOnlyContractViolations("$ uv run python wiki.py claim task-9", "")
	if len(commandHits) != 1 || commandHits[0].Type != "wiki_mutation_command" {
		t.Fatalf("expected wiki_mutation_command, got %+v", commandHits)
	}
	reportHits := DetectReadOnlyContractViolations("I ran wiki.py write notes/x.md", "")
	if len(reportHits) != 1 || reportHits[0].Type != "wiki_mutation_report" {
		t.Fatalf("expected wiki_mutation_report, got %+v", reportHits)
	}
}

func TestDetectMultipleClassesOrdered(t *testing.T) {
	stdout := "note written:out.md\n[main abc123457] ship it"
	violations := DetectReadOnlyContractViolations(stdout, "")
	if len(violations) != 2 {
		t.Fatalf("expected two violations, got %+v", violations)
	}
	if violations[0].Type != "wiki_write_side_effect" || violations[1].Type != "git_commit_side_effect" {
		t.Fatalf("unexpected class order: %+v", violations)
	}
}

func TestSanitizePostgresURL(t *testing.T) {
	got := SanitizeOutput("conn: postgresql://user:password@db.example.com/sales done")
	want := "conn: postgresql://<REDACTED> done"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSanitizeGenericCredentialsAndBackticks(t *testing.T) {
	got := SanitizeOutput("mysql://alice:hunter2@host:3306/db")
	want := "mysql://<REDACTED>:<REDACTED>@host:3306/db"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	gotBacktick := SanitizeOutput("run specifically `rm -rf /tmp/secrets` now")
	wantBacktick := "run specifically `<REDACTED>` now"
	if gotBacktick != wantBacktick {
		t.Fatalf("got %q want %q", gotBacktick, wantBacktick)
	}
}

func TestSanitizeKeywordSentence(t *testing.T) {
	got := SanitizeOutput("The password should be rotated.")
	want := "The password <REDACTED>."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCaptureUnderLimitPassthrough(t *testing.T) {
	raw := []byte("small output")
	if got := CaptureBounded(raw, MaxCaptureBytes); got != "small output" {
		t.Fatalf("got %q", got)
	}
}

func TestCaptureOversizeTruncatesWithMarker(t *testing.T) {
	raw := bytes.Repeat([]byte("a"), MaxCaptureBytes+1024*1024)
	got := CaptureBounded(raw, MaxCaptureBytes)
	if !strings.Contains(got, TruncationMarker) {
		t.Fatal("missing truncation marker")
	}
	if want := MaxCaptureBytes + len(TruncationMarker); len(got) != want {
		t.Fatalf("bounded length %d, want %d", len(got), want)
	}
	if !strings.HasPrefix(got, "aaa") || !strings.HasSuffix(got, "aaa") {
		t.Fatal("head and tail halves must be preserved")
	}
}

func TestCaptureInvalidUTF8Replaced(t *testing.T) {
	got := CaptureBounded([]byte{'o', 'k', 0xFF, '!'}, MaxCaptureBytes)
	if got != "ok\uFFFD!" {
		t.Fatalf("got %q", got)
	}
}

type recordingRunner struct {
	commands [][]string
	result   ExecResult
	err      error
}

func (r *recordingRunner) run(command []string) (ExecResult, error) {
	r.commands = append(r.commands, command)
	return r.result, r.err
}

func baseRunOptions(runner *recordingRunner) RunOptions {
	clockCalls := 0
	base := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	return RunOptions{
		Prompt:   "summarize internal/dispatch",
		Resolver: makeResolver(nil, map[string]string{"agy": "/fake/agy"}, nil, "/home/u", "linux"),
		Runner:   runner.run,
		AddDirs:  []string{"/workspace"},
		Clock: func() time.Time {
			clockCalls++
			return base.Add(time.Duration(clockCalls) * time.Second)
		},
	}
}

func TestEnvelopeCleanSuccess(t *testing.T) {
	runner := &recordingRunner{result: ExecResult{ReturnCode: 0, Stdout: []byte("all good")}}
	result, err := Run(baseRunOptions(runner))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK || result.ReturnCode != 0 || result.HarnessReturnCode != 0 {
		t.Fatalf("bad codes: %+v", result)
	}
	if result.ContractViolation != nil {
		t.Fatalf("unexpected violations: %+v", result.ContractViolation)
	}
	if result.Model != DefaultModel || result.Role != DefaultRole || result.Permission != DefaultPermission {
		t.Fatalf("defaults not applied: %+v", result)
	}
	if result.AGYBin != "/fake/agy" {
		t.Fatalf("binary mismatch: %q", result.AGYBin)
	}
	if result.DurationSeconds != 1.0 {
		t.Fatalf("duration %v, want 1.0", result.DurationSeconds)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("runner called %d times", len(runner.commands))
	}
	command := runner.commands[0]
	if command[0] != "/fake/agy" {
		t.Fatalf("wrong binary %q", command[0])
	}
	promptIndex := -1
	for i, part := range command {
		if part == "--prompt" {
			promptIndex = i + 1
		}
	}
	contractPrompt := command[promptIndex]
	if !strings.Contains(contractPrompt, "security harness") {
		t.Fatalf("contract prompt missing boundary text: %q", contractPrompt)
	}
	if !strings.Contains(contractPrompt, "summarize internal/dispatch") {
		t.Fatal("contract prompt lost user prompt")
	}
	if result.CommandPreview != "/fake/agy --prompt <prompt> --model 'Gemini 3.7 Flash (High)' --print-timeout 5m0s" {
		t.Fatalf("preview mismatch: %q", result.CommandPreview)
	}
}

func TestEnvelopeSideEffectFailsClosedAndSanitizes(t *testing.T) {
	runner := &recordingRunner{result: ExecResult{
		ReturnCode: 0,
		Stdout:     []byte("Session logged to log.md\npostgresql://user:pass@h/db"),
	}}
	result, err := Run(baseRunOptions(runner))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK || result.ReturnCode != 0 || result.HarnessReturnCode != ReadOnlyContractExit {
		t.Fatalf("fail-closed codes wrong: %+v", result)
	}
	report := result.ContractViolation
	if report == nil || report.Policy != "read_only" || report.ExitCode != ReadOnlyContractExit {
		t.Fatalf("violation report wrong: %+v", report)
	}
	if len(report.Violations) != 1 || report.Violations[0].Type != "wiki_reflect_side_effect" {
		t.Fatalf("violations wrong: %+v", report.Violations)
	}
	if strings.Contains(result.Stdout, "user:pass") {
		t.Fatalf("credentials leaked into result stdout: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "postgresql://<REDACTED>") {
		t.Fatalf("redaction missing: %q", result.Stdout)
	}
}

func TestEnvelopeMutationAllowedSkipsDetection(t *testing.T) {
	runner := &recordingRunner{result: ExecResult{
		ReturnCode: 0,
		Stdout:     []byte("Session logged to log.md"),
	}}
	opts := baseRunOptions(runner)
	opts.Permission = "workspace_write"
	opts.ReceiptID = "rcpt-123"
	result, err := Run(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK || result.HarnessReturnCode != 0 {
		t.Fatalf("mutation profile must skip detection: %+v", result)
	}
	if result.ContractViolation != nil {
		t.Fatalf("unexpected violation block: %+v", result.ContractViolation)
	}
}

func TestEnvelopeGateBlocksBeforeExecution(t *testing.T) {
	runner := &recordingRunner{}
	opts := baseRunOptions(runner)
	opts.SkipPermissions = true
	_, err := Run(opts)
	if err == nil || !strings.Contains(err.Error(), "--skip-permissions is not allowed") {
		t.Fatalf("expected gate block error, got %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("gate failure must not execute anything")
	}
}

func TestEnvelopeBinaryMissingErrors(t *testing.T) {
	runner := &recordingRunner{}
	opts := baseRunOptions(runner)
	opts.Resolver = makeResolver(nil, nil, nil, "", "linux")
	_, err := Run(opts)
	if err == nil || !strings.Contains(err.Error(), "AGY binary not found") {
		t.Fatalf("expected resolution error, got %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("resolution failure must not execute anything")
	}
}

func TestValidateGate(t *testing.T) {
	tests := []struct {
		name        string
		opts        RunOptions
		wantErr     bool
		errContains string
	}{
		{
			name: "happy path with explicit role and permission",
			opts: RunOptions{
				Role:       "scout",
				Permission: "automation_read",
				Prompt:     "find something",
			},
			wantErr: false,
		},
		{
			name: "implicit defaults used when empty",
			opts: RunOptions{
				Prompt: "collect things",
			},
			wantErr: false,
		},
		{
			name: "unknown role",
			opts: RunOptions{
				Role:       "unknown_role",
				Permission: "read_only",
				Prompt:     "test",
			},
			wantErr:     true,
			errContains: "unknown role",
		},
		{
			name: "unknown permission",
			opts: RunOptions{
				Role:       "collector",
				Permission: "unknown_permission",
				Prompt:     "test",
			},
			wantErr:     true,
			errContains: "unknown permission",
		},
		{
			name: "prompt too long",
			opts: RunOptions{
				Role:       "collector",
				Permission: "read_only",
				Prompt:     strings.Repeat("a", 30001), // max for read_only is 30000
			},
			wantErr:     true,
			errContains: "prompt too long",
		},
		{
			name: "skip permissions not allowed by profile",
			opts: RunOptions{
				Role:            "collector",
				Permission:      "read_only", // skip permissions not allowed
				SkipPermissions: true,
				Prompt:          "test",
			},
			wantErr:     true,
			errContains: "--skip-permissions is not allowed",
		},
		{
			name: "skip permissions allowed by profile",
			opts: RunOptions{
				Role:            "collector",
				Permission:      "automation_read", // skip permissions allowed
				SkipPermissions: true,
				Prompt:          "test",
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGate(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("expected error containing %q, got %q", tc.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
