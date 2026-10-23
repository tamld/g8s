package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHook_ClassifierTable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name     string
		cmd      string
		expected string // "allow", "deny", "force_ask"
	}{
		// Exploration commands
		{"ls", "ls", "allow"},
		{"git status", "git status", "allow"},
		{"go test", "go test ./...", "allow"},
		{"g8s submit", "g8s submit", "allow"},
		{"regular git commit", `git commit -m "feat: regular commit"`, "allow"},
		{"cat hooks.json (read-only)", "cat hooks.json", "allow"},
		{"git diff hooks.json (read-only)", "git diff hooks.json", "allow"},

		// Bypass flags
		{"bypass no-verify", "git commit --no-verify", "deny"},
		{"bypass git commit -n", "git commit -n -m x", "deny"},
		{"bypass HUSKY=0", "HUSKY=0 git push", "deny"},
		{"bypass SKIP=", "SKIP=lint git commit", "deny"},
		{"bypass LEFTHOOK=0", `LEFTHOOK=0 git commit -m "bypass"`, "deny"},

		// T4 tamper
		{"tamper global-ledger", "rm ~/.local/state/g8s/f0/global-ledger.jsonl", "deny"},
		{"tamper redirect hooks.json", "echo {} > hooks.json", "deny"},
		{"tamper wrapped bash -c ledger", `bash -c "rm .git/f0/ledger.jsonl"`, "deny"},
		{"tamper cp hooks.json", "cp /tmp/evil hooks.json", "deny"},
		{"tamper mv hooks.json", "mv .agents/hooks.json hooks.bak", "deny"},
		{"tamper sed -i hooks.json", "sed -i 's/a/b/' hooks.json", "deny"},
		{"tamper truncate", "truncate -s 0 ~/.local/state/g8s/f0/mandate.json", "deny"},
		{"tamper tee", "tee hooks.json", "deny"},

		// T4 irreversible
		{"irreversible gh release create", "gh release create v1.0.0", "force_ask"},
		{"irreversible git push --force", "git push --force", "force_ask"},
		{"irreversible git push --tags", "git push origin --tags", "force_ask"},
		{"irreversible git push -f", "git push -f", "force_ask"},
		{"irreversible git push --force-with-lease", "git push --force-with-lease", "force_ask"},
		{"irreversible git push --delete", "git push --delete origin old-branch", "force_ask"},
		{"irreversible git push colon refspec", "git push origin :bad-branch", "force_ask"},
		{"irreversible gh repo edit", "gh repo edit", "force_ask"},
		{"irreversible gh repo delete", "gh repo delete", "force_ask"},
		{"irreversible gh secret", "gh secret set MY_KEY", "force_ask"},
		{"irreversible gh auth", "gh auth login", "force_ask"},

		// Issue close
		{"issue close whisper", "gh issue close 42", "allow"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := PreToolInput{
				ToolName:    "run_command",
				CommandLine: tc.cmd,
			}
			dec := evaluatePreToolHook(input)
			if dec.Decision != tc.expected {
				t.Fatalf("cmd %q: expected %q, got %q (reason: %q)", tc.cmd, tc.expected, dec.Decision, dec.Reason)
			}
			if tc.cmd == "gh issue close 42" && !strings.Contains(dec.Reason, "[jit_whisper]") {
				t.Fatalf("expected whisper in reason for issue close, got %q", dec.Reason)
			}
		})
	}
}

func TestHook_NonCommandToolAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	input := PreToolInput{
		ToolName:    "read_file",
		CommandLine: "rm hooks.json", // Even if command line has something, non-command tool is free
	}
	dec := evaluatePreToolHook(input)
	if dec.Decision != "allow" {
		t.Fatalf("expected allow for read_file, got %q", dec.Decision)
	}
}

func TestHook_MergeCommandGateCheck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)
	t.Cleanup(func() { gateCheckWg.Wait() })

	// Case A: Gate check FAILS (no mandate, no evidence)
	inputFail := PreToolInput{
		ToolName:       "run_command",
		CommandLine:    "gh pr merge 10",
		WorkspacePaths: []string{repoDir},
	}
	decFail := evaluatePreToolHook(inputFail)
	if decFail.Decision != "deny" {
		t.Fatalf("expected deny when gate check fails, got %q (reason: %q)", decFail.Decision, decFail.Reason)
	}
	if !strings.Contains(decFail.Reason, "f0 gate check FAILED:") {
		t.Errorf("expected reason to contain 'f0 gate check FAILED:', got %q", decFail.Reason)
	}
	if !strings.Contains(decFail.Reason, "f0 status --repo") {
		t.Errorf("expected reason to contain 'f0 status --repo', got %q", decFail.Reason)
	}
	if !strings.Contains(decFail.Reason, "--pr 10") {
		t.Errorf("expected reason to contain '--pr 10', got %q", decFail.Reason)
	}

	// Chained commands: echo hi && gh pr merge 10 -> also classified as merge and denied!
	inputChained := PreToolInput{
		ToolName:       "run_command",
		CommandLine:    "echo hi && gh pr merge 10",
		WorkspacePaths: []string{repoDir},
	}
	decChained := evaluatePreToolHook(inputChained)
	if decChained.Decision != "deny" || !strings.Contains(decChained.Reason, "f0 gate check FAILED:") {
		t.Fatalf("expected chained merge to fail gate, got %+v", decChained)
	}

	// Wrapped bash -c: bash -c "gh pr merge 10" -> also classified as merge and denied!
	inputWrapped := PreToolInput{
		ToolName:       "run_command",
		CommandLine:    `bash -c "gh pr merge 10"`,
		WorkspacePaths: []string{repoDir},
	}
	decWrapped := evaluatePreToolHook(inputWrapped)
	if decWrapped.Decision != "deny" || !strings.Contains(decWrapped.Reason, "f0 gate check FAILED:") {
		t.Fatalf("expected wrapped bash merge to fail gate, got %+v", decWrapped)
	}

	// Case B: Gate check PASSES
	// 1. Issue valid mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-hooktest1",
		Issuer:      "test-op",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "hook testing",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("issuing mandate: %v", err)
	}

	// 2. Add config with CI: none and verify_commands: go test ./...
	cfgDir := filepath.Join(repoDir, ".g8s")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

	// 3. Add clean PASS evidence in repo ledger
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Append(&Entry{
		ID:     "ev-g1clean",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "-json", "./..."},
	}); err != nil {
		t.Fatal(err)
	}

	// Direct git push origin main -> classified as merge -> PASS!
	inputPass := PreToolInput{
		ToolName:       "run_command",
		CommandLine:    "git push origin main",
		WorkspacePaths: []string{repoDir},
	}
	decPass := evaluatePreToolHook(inputPass)
	if decPass.Decision != "allow" {
		t.Fatalf("expected allow when gate check passes, got %q (reason: %q)", decPass.Decision, decPass.Reason)
	}
}

func TestHook_MergeTimeoutFailSafe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupTestGitRepo(t)
	t.Cleanup(func() { gateCheckWg.Wait() })

	// Simulate timeout by setting gateCheckTimeout to 1 nanosecond
	oldTimeout := gateCheckTimeout
	gateCheckTimeout = 1 * time.Nanosecond
	defer func() { gateCheckTimeout = oldTimeout }()

	input := PreToolInput{
		ToolName:       "run_command",
		CommandLine:    "gh pr merge 10",
		WorkspacePaths: []string{repoDir},
	}
	dec := evaluatePreToolHook(input)
	if dec.Decision != "deny" {
		t.Fatalf("expected deny on timeout fail-safe, got %q", dec.Decision)
	}
	if !strings.Contains(dec.Reason, "fail-safe deny") {
		t.Errorf("expected reason to contain 'fail-safe deny', got %q", dec.Reason)
	}
}

func TestHook_StopHook(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	uncitedFixture := filepath.Join(repoDir, "uncited.jsonl")
	uncitedContent := `{"step_index":1,"type":"USER_INPUT","content":"Check CI"}
{"step_index":2,"type":"PLANNER_RESPONSE","content":"Tests finished. CI GREEN."}
`
	if err := os.WriteFile(uncitedFixture, []byte(uncitedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	citedFixture := filepath.Join(repoDir, "cited.jsonl")
	citedContent := `{"step_index":1,"type":"USER_INPUT","content":"Check CI"}
{"step_index":2,"type":"PLANNER_RESPONSE","content":"Tests finished. CI GREEN (ev-aabb1122)."}
`
	if err := os.WriteFile(citedFixture, []byte(citedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Uncited "CI GREEN" -> continue
	dec1 := evaluateStopHook(StopInput{
		TranscriptPath: uncitedFixture,
		WorkspacePaths: []string{repoDir},
	})
	if dec1.Decision != "continue" {
		t.Fatalf("expected continue for uncited claim, got %q (reason: %q)", dec1.Decision, dec1.Reason)
	}
	if !strings.Contains(dec1.Reason, "Uncited status claims:") || !strings.Contains(dec1.Reason, "Cite ev-") {
		t.Errorf("expected reason to mention uncited claims and citation hint, got %q", dec1.Reason)
	}

	// 2. Cited "CI GREEN (ev-aabb1122)" where ev-aabb1122 is PASS in repo ledger -> allow
	if err := ledger.Append(&Entry{
		ID:     "ev-aabb1122",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "./..."},
	}); err != nil {
		t.Fatal(err)
	}

	decCited := evaluateStopHook(StopInput{
		TranscriptPath: citedFixture,
		WorkspacePaths: []string{repoDir},
	})
	if decCited.Decision != "allow" {
		t.Fatalf("expected allow for cited valid claim, got %q (reason: %q)", decCited.Decision, decCited.Reason)
	}

	// 3. Calling stop hook 3 times on uncited claims: 1st => continue, 2nd => continue, 3rd => allow (cap of 2 enforced!)
	// Note: call 1 was already run above (dec1). Now call 2:
	dec2 := evaluateStopHook(StopInput{
		TranscriptPath: uncitedFixture,
		WorkspacePaths: []string{repoDir},
	})
	if dec2.Decision != "continue" {
		t.Fatalf("expected 2nd call to continue, got %q (reason: %q)", dec2.Decision, dec2.Reason)
	}

	// Call 3: continuation cap reached -> allow!
	dec3 := evaluateStopHook(StopInput{
		TranscriptPath: uncitedFixture,
		WorkspacePaths: []string{repoDir},
	})
	if dec3.Decision != "allow" {
		t.Fatalf("expected 3rd call to allow due to cap of 2, got %q (reason: %q)", dec3.Decision, dec3.Reason)
	}
	if !strings.Contains(dec3.Reason, "continuation cap (2) reached") {
		t.Errorf("expected reason to mention continuation cap reached, got %q", dec3.Reason)
	}
}

func TestHook_CLIExecution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Test f0 hook pre-tool via CLI
	runPreTool := func(inputJSON string) (int, HookDecision) {
		oldStdin := os.Stdin
		oldStdout := os.Stdout
		rIn, wIn, _ := os.Pipe()
		rOut, wOut, _ := os.Pipe()
		os.Stdin = rIn
		os.Stdout = wOut

		_, _ = wIn.Write([]byte(inputJSON))
		_ = wIn.Close()

		exitCode := hookCommand([]string{"pre-tool"})

		_ = wOut.Close()
		os.Stdin = oldStdin
		os.Stdout = oldStdout

		outData, _ := io.ReadAll(rOut)
		var dec HookDecision
		_ = json.Unmarshal(outData, &dec)
		return exitCode, dec
	}

	exitCode, dec := runPreTool(`{"toolCall":{"name":"run_command","args":{"CommandLine":"ls -la"}}}`)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if dec.Decision != "allow" {
		t.Fatalf("expected allow for ls -la, got %q", dec.Decision)
	}

	exitCode, dec = runPreTool(`{"toolCall":{"name":"run_command","args":{"CommandLine":"rm hooks.json"}}}`)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if dec.Decision != "deny" {
		t.Fatalf("expected deny for rm hooks.json, got %q", dec.Decision)
	}

	// Test f0 hook stop via CLI
	runStop := func(inputJSON string) (int, HookDecision) {
		oldStdin := os.Stdin
		oldStdout := os.Stdout
		rIn, wIn, _ := os.Pipe()
		rOut, wOut, _ := os.Pipe()
		os.Stdin = rIn
		os.Stdout = wOut

		_, _ = wIn.Write([]byte(inputJSON))
		_ = wIn.Close()

		exitCode := hookCommand([]string{"stop"})

		_ = wOut.Close()
		os.Stdin = oldStdin
		os.Stdout = oldStdout

		outData, _ := io.ReadAll(rOut)
		var dec HookDecision
		_ = json.Unmarshal(outData, &dec)
		return exitCode, dec
	}

	exitCode, dec = runStop(`{"message":"Nothing to report"}`)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if dec.Decision != "allow" {
		t.Fatalf("expected allow for empty report, got %q", dec.Decision)
	}
}

func TestHook_FlexibleSchemas(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Schema A: toolCall.name & toolCall.arguments.CommandLine
	var inA PreToolInput
	jsonA := `{"toolCall":{"name":"run_command","arguments":{"CommandLine":"git commit --no-verify"}}}`
	if err := json.Unmarshal([]byte(jsonA), &inA); err != nil {
		t.Fatal(err)
	}
	decA := evaluatePreToolHook(inA)
	if decA.Decision != "deny" {
		t.Fatalf("Schema A: expected deny, got %q", decA.Decision)
	}

	// Schema B: tool_name & tool_input.CommandLine
	var inB PreToolInput
	jsonB := `{"tool_name":"run_command","tool_input":{"CommandLine":"HUSKY=0 git push"}}`
	if err := json.Unmarshal([]byte(jsonB), &inB); err != nil {
		t.Fatal(err)
	}
	decB := evaluatePreToolHook(inB)
	if decB.Decision != "deny" {
		t.Fatalf("Schema B: expected deny, got %q", decB.Decision)
	}

	// Schema C: tool_name & tool_input.command
	var inC PreToolInput
	jsonC := `{"tool_name":"run_command","tool_input":{"command":"gh release create v1.0.0"}}`
	if err := json.Unmarshal([]byte(jsonC), &inC); err != nil {
		t.Fatal(err)
	}
	decC := evaluatePreToolHook(inC)
	if decC.Decision != "force_ask" {
		t.Fatalf("Schema C: expected force_ask, got %q", decC.Decision)
	}
}
