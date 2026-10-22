package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestSelectIndependentProvider_ExcludesF0AndEmpty(t *testing.T) {
	tests := []struct {
		name       string
		f0         string
		available  []string
		wantResult string
		wantOK     bool
	}{
		{
			name:       "excludes exact match",
			f0:         "claude",
			available:  []string{"claude", "gemini", "openai"},
			wantResult: "gemini",
			wantOK:     true,
		},
		{
			name:       "case-insensitive and trimmed normalization",
			f0:         "Gemini",
			available:  []string{"  gemini  ", "OpenAI"},
			wantResult: "OpenAI",
			wantOK:     true,
		},
		{
			name:       "excludes 'none' and empty strings",
			f0:         "claude",
			available:  []string{"", "none", "NONE", "  ", "CLAUDE"},
			wantResult: "",
			wantOK:     false,
		},
		{
			name:       "empty available list",
			f0:         "claude",
			available:  []string{},
			wantResult: "",
			wantOK:     false,
		},
		{
			name:       "anthropic f0 with multiple independent candidates",
			f0:         "anthropic",
			available:  []string{"Anthropic", "None", "", "google-vertex", "bedrock"},
			wantResult: "google-vertex",
			wantOK:     true,
		},
		{
			name:       "empty f0 allows first valid provider",
			f0:         "",
			available:  []string{"claude", "gemini"},
			wantResult: "claude",
			wantOK:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotResult, gotOK := SelectIndependentProvider(tt.f0, tt.available)
			if gotOK != tt.wantOK {
				t.Errorf("SelectIndependentProvider() gotOK = %v, wantOK = %v", gotOK, tt.wantOK)
			}
			if gotResult != tt.wantResult {
				t.Errorf("SelectIndependentProvider() gotResult = %q, wantResult = %q", gotResult, tt.wantResult)
			}
		})
	}
}

func TestRunT3Verification_NoVerifierAvailable_VetoAndEscalate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// Case 1: only F0's provider is in available list, no EvalProbeRunner
	opts := T3VerifyOptions{
		RepoDir:            repoDir,
		HeadSHA:            headSHA,
		BaseRef:            "main",
		F0Provider:         "claude",
		AvailableProviders: []string{"claude", "none", ""},
		ProviderRunner: func(provider, repoDir, diff string) (bool, string, error) {
			t.Fatalf("ProviderRunner should not be called when no independent provider is available")
			return true, "", nil
		},
		EvalProbeRunner: nil,
	}

	verdict, err := RunT3Verification(opts)
	if err != nil {
		t.Fatalf("RunT3Verification returned unexpected err: %v", err)
	}
	if verdict.Status != "VETO_ESCALATE" {
		t.Fatalf("expected status VETO_ESCALATE, got %s", verdict.Status)
	}
	if verdict.Approved {
		t.Fatalf("expected Approved == false")
	}
	if !verdict.Veto {
		t.Fatalf("expected Veto == true")
	}
	if !verdict.EscalateToPiC {
		t.Fatalf("expected EscalateToPiC == true")
	}
	if !strings.Contains(verdict.Reason, "no independent second verifier available (automatic veto + escalate to PiC per D9)") {
		t.Fatalf("expected specific D9 veto reason, got %q", verdict.Reason)
	}

	// Case 2: Available list is completely empty
	optsEmpty := T3VerifyOptions{
		RepoDir:            repoDir,
		HeadSHA:            headSHA,
		BaseRef:            "main",
		F0Provider:         "gemini",
		AvailableProviders: nil,
	}
	verdictEmpty, err := RunT3Verification(optsEmpty)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if verdictEmpty.Status != "VETO_ESCALATE" || !verdictEmpty.Veto || !verdictEmpty.EscalateToPiC {
		t.Fatalf("expected VETO_ESCALATE on empty providers, got %+v", verdictEmpty)
	}
}

func TestRunT3Verification_IndependentProvider_Passed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	var capturedProvider, capturedRepo, capturedDiff string
	opts := T3VerifyOptions{
		RepoDir:            repoDir,
		HeadSHA:            headSHA,
		BaseRef:            "main",
		F0Provider:         "claude",
		AvailableProviders: []string{"claude", "gemini-pro"},
		ProviderRunner: func(provider, repoDir, diff string) (bool, string, error) {
			capturedProvider = provider
			capturedRepo = repoDir
			capturedDiff = diff
			return true, "independent review passed clean", nil
		},
	}

	verdict, err := RunT3Verification(opts)
	if err != nil {
		t.Fatalf("RunT3Verification failed: %v", err)
	}

	if verdict.Status != "PASS" {
		t.Fatalf("expected status PASS, got %s", verdict.Status)
	}
	if !verdict.Approved {
		t.Fatalf("expected Approved == true")
	}
	if verdict.Veto {
		t.Fatalf("expected Veto == false")
	}
	if verdict.EscalateToPiC {
		t.Fatalf("expected EscalateToPiC == false")
	}
	if verdict.SelectedVerifier != "gemini-pro" {
		t.Fatalf("expected SelectedVerifier gemini-pro, got %s", verdict.SelectedVerifier)
	}
	if verdict.Reason != "independent review passed clean" {
		t.Fatalf("expected reason preserved, got %s", verdict.Reason)
	}
	if capturedProvider != "gemini-pro" {
		t.Fatalf("expected captured provider gemini-pro, got %s", capturedProvider)
	}
	if capturedRepo != repoDir {
		t.Fatalf("expected captured repo %s, got %s", repoDir, capturedRepo)
	}
	_ = capturedDiff

	// Verify repo ledger entry
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("OpenRepoLedger failed: %v", err)
	}
	entries, err := ledger.ReadEntries()
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Kind == "t3-verify" {
			found = true
			if e.Status != "PASS" {
				t.Fatalf("expected ledger entry status PASS, got %s", e.Status)
			}
			if sel, _ := e.Extra["selected_verifier"].(string); sel != "gemini-pro" {
				t.Fatalf("expected extra selected_verifier gemini-pro, got %v", sel)
			}
		}
	}
	if !found {
		t.Fatalf("expected t3-verify entry in repo ledger")
	}
}

func TestRunT3Verification_IndependentProvider_Disagreement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	opts := T3VerifyOptions{
		RepoDir:            repoDir,
		HeadSHA:            headSHA,
		BaseRef:            "main",
		F0Provider:         "claude",
		AvailableProviders: []string{"claude", "openai-gpt4"},
		ProviderRunner: func(provider, repoDir, diff string) (bool, string, error) {
			return false, "veto: potential regression in auth boundary", nil
		},
	}

	verdict, err := RunT3Verification(opts)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if verdict.Status != "VETO_ESCALATE" {
		t.Fatalf("expected status VETO_ESCALATE on disagreement, got %s", verdict.Status)
	}
	if verdict.Approved {
		t.Fatalf("expected Approved == false")
	}
	if !verdict.Veto {
		t.Fatalf("expected Veto == true")
	}
	if !verdict.EscalateToPiC {
		t.Fatalf("expected EscalateToPiC == true")
	}
	if verdict.SelectedVerifier != "openai-gpt4" {
		t.Fatalf("expected SelectedVerifier openai-gpt4, got %s", verdict.SelectedVerifier)
	}
	if !strings.Contains(verdict.Reason, "potential regression in auth boundary") {
		t.Fatalf("expected detail preserved, got %s", verdict.Reason)
	}

	// Verify repo ledger entry
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("OpenRepoLedger failed: %v", err)
	}
	entries, err := ledger.ReadEntries()
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Kind == "t3-verify" {
			found = true
			if e.Status != "FAIL" {
				t.Fatalf("expected ledger entry status FAIL, got %s", e.Status)
			}
		}
	}
	if !found {
		t.Fatalf("expected t3-verify entry in repo ledger")
	}
}

func TestRunT3Verification_IndependentProvider_Error(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	opts := T3VerifyOptions{
		RepoDir:            repoDir,
		HeadSHA:            headSHA,
		BaseRef:            "main",
		F0Provider:         "claude",
		AvailableProviders: []string{"gemini"},
		ProviderRunner: func(provider, repoDir, diff string) (bool, string, error) {
			return false, "", errors.New("provider API rate limit exceeded")
		},
	}

	verdict, err := RunT3Verification(opts)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if verdict.Status != "VETO_ESCALATE" || !verdict.Veto || !verdict.EscalateToPiC {
		t.Fatalf("expected VETO_ESCALATE on provider error, got %+v", verdict)
	}
	if !strings.Contains(verdict.Reason, "provider API rate limit exceeded") {
		t.Fatalf("expected error message in reason, got %s", verdict.Reason)
	}
}

func TestRunT3Verification_FallbackToEvalProbeRunner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// No independent provider available
	probeCalled := false
	opts := T3VerifyOptions{
		RepoDir:            repoDir,
		HeadSHA:            headSHA,
		BaseRef:            "main",
		F0Provider:         "claude",
		AvailableProviders: []string{"claude"},
		EvalProbeRunner: func(r string) (bool, string, error) {
			probeCalled = true
			return true, "all eval probes passed clean", nil
		},
	}

	verdict, err := RunT3Verification(opts)
	if err != nil {
		t.Fatalf("RunT3Verification failed: %v", err)
	}
	if !probeCalled {
		t.Fatalf("expected EvalProbeRunner to be called")
	}
	if verdict.Status != "PASS" || !verdict.Approved {
		t.Fatalf("expected PASS on successful probes, got %+v", verdict)
	}
	if verdict.SelectedVerifier != "eval-probes" {
		t.Fatalf("expected SelectedVerifier eval-probes, got %s", verdict.SelectedVerifier)
	}

	// Probe fails
	optsFail := T3VerifyOptions{
		RepoDir:            repoDir,
		HeadSHA:            headSHA,
		BaseRef:            "main",
		F0Provider:         "claude",
		AvailableProviders: []string{"claude"},
		EvalProbeRunner: func(r string) (bool, string, error) {
			return false, "eval probe test_auth_isolation failed", nil
		},
	}
	verdictFail, err := RunT3Verification(optsFail)
	if err != nil {
		t.Fatalf("RunT3Verification failed: %v", err)
	}
	if verdictFail.Status != "VETO_ESCALATE" || !verdictFail.Veto || !verdictFail.EscalateToPiC {
		t.Fatalf("expected VETO_ESCALATE on probe failure, got %+v", verdictFail)
	}
	if verdictFail.SelectedVerifier != "eval-probes" {
		t.Fatalf("expected SelectedVerifier eval-probes, got %s", verdictFail.SelectedVerifier)
	}
}

func TestVerifyT3Command_CLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// Run with no providers -> should exit 1 (VETO_ESCALATE)
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := verifyT3Command([]string{
		"--repo", repoDir,
		"--head-sha", headSHA,
		"--f0-provider", "claude",
		"--available-providers", "claude,none",
		"--json",
	})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected verifyT3Command to exit 1 on veto/escalate, got %d", code)
	}

	var v T3Verdict
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("unmarshaling json from verifyT3Command: %v, out: %s", err, string(out))
	}
	if v.Status != "VETO_ESCALATE" || !v.Veto || !v.EscalateToPiC {
		t.Fatalf("expected VETO_ESCALATE verdict, got %+v", v)
	}
}

func TestWireDefaultT3Options(t *testing.T) {
	// Case 1: Empty options are populated with defaults
	var emptyOpts T3VerifyOptions
	WireDefaultT3Options(&emptyOpts)

	if emptyOpts.F0Provider != "agy" {
		t.Fatalf("expected F0Provider agy, got %q", emptyOpts.F0Provider)
	}
	expectedProviders := []string{"9router", "claude", "agy"}
	if len(emptyOpts.AvailableProviders) != len(expectedProviders) {
		t.Fatalf("expected %d available providers, got %v", len(expectedProviders), emptyOpts.AvailableProviders)
	}
	for i, p := range expectedProviders {
		if emptyOpts.AvailableProviders[i] != p {
			t.Fatalf("expected provider[%d] == %q, got %q", i, p, emptyOpts.AvailableProviders[i])
		}
	}
	if emptyOpts.ProviderRunner == nil {
		t.Fatalf("expected ProviderRunner to be non-nil")
	}
	if emptyOpts.EvalProbeRunner == nil {
		t.Fatalf("expected EvalProbeRunner to be non-nil")
	}

	// Verify SelectIndependentProvider with populated defaults selects "9router" when F0 is "agy"
	selected, ok := SelectIndependentProvider(emptyOpts.F0Provider, emptyOpts.AvailableProviders)
	if !ok {
		t.Fatalf("expected SelectIndependentProvider to succeed")
	}
	if selected != "9router" {
		t.Fatalf("expected selected provider 9router, got %q", selected)
	}

	// Case 2: Preserves already-set fields if non-empty
	customRunner := func(provider, repoDir, diff string) (bool, string, error) {
		return true, "custom", nil
	}
	customProbe := func(repoDir string) (bool, string, error) {
		return true, "custom probe", nil
	}
	customOpts := T3VerifyOptions{
		F0Provider:         "custom-f0",
		AvailableProviders: []string{"custom-f0", "custom-indep"},
		ProviderRunner:     customRunner,
		EvalProbeRunner:    customProbe,
	}
	WireDefaultT3Options(&customOpts)

	if customOpts.F0Provider != "custom-f0" {
		t.Fatalf("expected custom-f0, got %q", customOpts.F0Provider)
	}
	if len(customOpts.AvailableProviders) != 2 || customOpts.AvailableProviders[1] != "custom-indep" {
		t.Fatalf("expected custom providers preserved, got %v", customOpts.AvailableProviders)
	}
	p, d, _ := customOpts.ProviderRunner("p", "r", "d")
	if !p || d != "custom" {
		t.Fatalf("expected custom ProviderRunner to be preserved")
	}
	pProbe, dProbe, _ := customOpts.EvalProbeRunner("r")
	if !pProbe || dProbe != "custom probe" {
		t.Fatalf("expected custom EvalProbeRunner to be preserved")
	}

	// Case 3: Nil options safety
	WireDefaultT3Options(nil)
}
