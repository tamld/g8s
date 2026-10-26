package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	commands["verify-t3"] = verifyT3Command
	commands["t3-verify"] = verifyT3Command
}

// T3VerifyOptions configures T3 independent second verification.
type T3VerifyOptions struct {
	RepoDir            string
	HeadSHA            string
	BaseRef            string
	F0Provider         string
	AuthorModel        string
	AvailableProviders []string
	ProviderRunner     func(provider, repoDir, diff string) (passed bool, detail string, err error)
	EvalProbeRunner    func(repoDir string) (passed bool, detail string, err error)
}

// T3Verdict captures the outcome of T3 verification.
type T3Verdict struct {
	Status           string `json:"status"` // "PASS", "VETO_ESCALATE"
	Approved         bool   `json:"approved"`
	Veto             bool   `json:"veto"`
	EscalateToPiC    bool   `json:"escalate_to_pic"`
	SelectedVerifier string `json:"selected_verifier,omitempty"`
	Reason           string `json:"reason"`
}

// SelectIndependentProviders returns all unique available providers that are independent
// from F0's provider, preserving order. F0's provider, "none", and "" are excluded.
func SelectIndependentProviders(f0Provider string, available []string) []string {
	normF0 := strings.ToLower(strings.TrimSpace(f0Provider))
	seen := make(map[string]bool)
	var out []string
	for _, p := range available {
		trimmed := strings.TrimSpace(p)
		normP := strings.ToLower(trimmed)
		if normP == "" || normP == "none" || normP == normF0 {
			continue
		}
		if seen[normP] {
			continue
		}
		seen[normP] = true
		out = append(out, trimmed)
	}
	return out
}

// SelectIndependentProvider chooses an available provider that is independent from F0's provider.
// Provider names are normalized (trimmed and lowercased). F0's provider, "none", and "" are excluded.
// The first matching independent provider is returned.
func SelectIndependentProvider(f0Provider string, available []string) (string, bool) {
	candidates := SelectIndependentProviders(f0Provider, available)
	if len(candidates) == 0 {
		return "", false
	}
	return candidates[0], true
}

var t3IdentityPrefixes = []string{
	"author:",
	"commit:",
	"co-authored-by:",
	"signed-off-by:",
	"reviewed-by:",
	"task-id:",
	"receipt-id:",
	"worker-id:",
}

// sanitizeT3Diff strips author, commit, and worker self-report metadata lines from a diff
// before sending it to an independent T3 verifier (SoC: Blind Sanitization).
func sanitizeT3Diff(diff string) string {
	if diff == "" {
		return ""
	}
	lines := strings.Split(diff, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Strip added/context commit trailer or header lines carrying identity metadata
		check := strings.TrimSpace(strings.TrimLeft(trimmed, "+-/#* "))
		lower := strings.ToLower(check)
		skip := false
		for _, prefix := range t3IdentityPrefixes {
			if strings.HasPrefix(lower, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// RunT3Verification executes the T3 independent second verifier workflow (#628).
func RunT3Verification(opts T3VerifyOptions) (T3Verdict, error) {
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	headSHA := opts.HeadSHA
	if headSHA == "" {
		if out, err := exec.Command("git", "-C", absRepo, "rev-parse", "HEAD").Output(); err == nil {
			headSHA = strings.TrimSpace(string(out))
		}
	}

	baseRef := opts.BaseRef
	if baseRef == "" {
		baseRef = findDefaultBranch(absRepo)
	}

	// Compute diff
	var diff string
	if headSHA != "" && baseRef != "" {
		diffCmd := exec.Command("git", "-C", absRepo, "diff", "--end-of-options", baseRef+"..."+headSHA)
		diffOut, err := diffCmd.Output()
		if err != nil {
			diffCmd = exec.Command("git", "-C", absRepo, "diff", "--end-of-options", baseRef, headSHA)
			diffOut, _ = diffCmd.Output()
		}
		diff = sanitizeT3Diff(string(diffOut))
	}

	var selectedVerifier string
	var passed bool
	var detail string
	var verifierRan bool
	var allProvidersErrored bool

	candidates := SelectIndependentProviders(opts.F0Provider, opts.AvailableProviders)
	if len(candidates) > 0 && opts.ProviderRunner != nil {
		allProvidersErrored = true
		for _, candidate := range candidates {
			selectedVerifier = candidate
			p, d, runErr := opts.ProviderRunner(candidate, absRepo, diff)
			verifierRan = true
			if runErr == nil {
				// Substantive verdict (PASS or VETO) from a reachable independent provider; do not shop for another verdict.
				allProvidersErrored = false
				passed = p
				detail = d
				break
			}
			// Infrastructure/unreachable error on this candidate; try next candidate in failover chain.
			passed = false
			if d == "" {
				d = runErr.Error()
			}
			detail = d
		}
	}

	if (!verifierRan || allProvidersErrored) && opts.EvalProbeRunner != nil {
		selectedVerifier = "eval-probes"
		p, d, probeErr := opts.EvalProbeRunner(absRepo)
		if probeErr != nil {
			p = false
			if d == "" {
				d = probeErr.Error()
			}
		}
		passed = p
		detail = d
		verifierRan = true
	}

	if !verifierRan {
		return T3Verdict{
			Status:        "VETO_ESCALATE",
			Approved:      false,
			Veto:          true,
			EscalateToPiC: true,
			Reason:        "no independent second verifier available (automatic veto + escalate to PiC per D9)",
		}, nil
	}

	ledger, _ := OpenRepoLedger(absRepo)

	if !passed {
		if detail == "" {
			detail = fmt.Sprintf("independent verifier %q rejected changes", selectedVerifier)
		}
		verdict := T3Verdict{
			Status:           "VETO_ESCALATE",
			Approved:         false,
			Veto:             true,
			EscalateToPiC:    true,
			SelectedVerifier: selectedVerifier,
			Reason:           detail,
		}
		if ledger != nil {
			entry := &Entry{
				ID:           newEvidenceID(),
				Ts:           time.Now().UTC().Format(time.RFC3339),
				Kind:         "t3-verify",
				Repo:         absRepo,
				SHA:          headSHA,
				Cmd:          []string{"verify-t3"},
				Status:       "FAIL",
				ItemsChecked: 1,
				Extra: map[string]any{
					"selected_verifier": selectedVerifier,
					"reason":            detail,
					"approved":          false,
					"veto":              true,
					"escalate_to_pic":   true,
				},
			}
			_ = ledger.Append(entry)
		}
		return verdict, nil
	}

	if detail == "" {
		detail = fmt.Sprintf("independent verifier %q approved changes", selectedVerifier)
	}
	verdict := T3Verdict{
		Status:           "PASS",
		Approved:         true,
		Veto:             false,
		EscalateToPiC:    false,
		SelectedVerifier: selectedVerifier,
		Reason:           detail,
	}
	if ledger != nil {
		entry := &Entry{
			ID:           newEvidenceID(),
			Ts:           time.Now().UTC().Format(time.RFC3339),
			Kind:         "t3-verify",
			Repo:         absRepo,
			SHA:          headSHA,
			Cmd:          []string{"verify-t3"},
			Status:       "PASS",
			ItemsChecked: 1,
			Extra: map[string]any{
				"selected_verifier": selectedVerifier,
				"reason":            detail,
				"approved":          true,
				"veto":              false,
				"escalate_to_pic":   false,
			},
		}
		_ = ledger.Append(entry)
	}

	return verdict, nil
}

// ExtractModelFamily maps a concrete model identifier or provider token to its
// canonical foundation model family (#662). Generic proxy/combo names ("9router",
// "cliproxyapi", "reasoning-high", etc.) return "" because their lineage is
// only known after inspecting model_resolved.
func ExtractModelFamily(modelOrProvider string) string {
	s := strings.ToLower(strings.TrimSpace(modelOrProvider))
	if s == "" {
		return ""
	}
	switch {
	case strings.Contains(s, "claude"), strings.Contains(s, "anthropic"), strings.Contains(s, "sonnet"), strings.Contains(s, "opus"), strings.Contains(s, "haiku"):
		return "anthropic"
	case strings.Contains(s, "gpt-"), strings.Contains(s, "gpt4"), strings.Contains(s, "gpt5"), strings.Contains(s, "openai"), strings.Contains(s, "codex"), strings.HasPrefix(s, "o1"), strings.HasPrefix(s, "o3"), strings.HasPrefix(s, "o4"):
		return "openai"
	case strings.Contains(s, "gemini"), strings.Contains(s, "google"), strings.Contains(s, "gemma"), s == "agy":
		return "google"
	case strings.Contains(s, "qwen"), strings.Contains(s, "alibaba"):
		return "qwen"
	case strings.Contains(s, "deepseek"):
		return "deepseek"
	case strings.Contains(s, "glm"), strings.Contains(s, "zhipu"):
		return "zhipu"
	case strings.Contains(s, "nemotron"), strings.Contains(s, "nvidia"):
		return "nvidia"
	case strings.Contains(s, "llama"), strings.Contains(s, "meta"):
		return "meta"
	case strings.Contains(s, "kimi"), strings.Contains(s, "moonshot"):
		return "moonshot"
	case strings.Contains(s, "minimax"):
		return "minimax"
	default:
		return ""
	}
}

// AreModelFamiliesDisjoint reports whether authorModel and verifierModel belong
// to different foundation model families (#662). If either model has an unknown
// family (e.g., an unresolved proxy name), it returns true so proxy candidates
// can be queried and checked on their resolved model.
func AreModelFamiliesDisjoint(authorModel, verifierModel string) bool {
	famA := ExtractModelFamily(authorModel)
	famV := ExtractModelFamily(verifierModel)
	if famA == "" || famV == "" {
		return true
	}
	return famA != famV
}

// buildT3BlindReviewPrompt constructs a BlindReviewPacket-grade prompt (#662)
// combining repository invariants (spec/constitution.md or .g8s/trust-boundaries.yml)
// with the identity-sanitized git diff.
func buildT3BlindReviewPrompt(repoDir, diff string) string {
	truncatedDiff := sanitizeT3Diff(diff)
	if len(truncatedDiff) > 12000 {
		truncatedDiff = truncatedDiff[:12000]
	}

	var invariants string
	for _, rel := range []string{".g8s/trust-boundaries.yml", "spec/constitution.md"} {
		p := filepath.Join(repoDir, rel)
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			s := strings.TrimSpace(string(b))
			if len(s) > 2000 {
				s = s[:2000]
			}
			invariants = fmt.Sprintf("Repository Invariants (%s):\n%s\n\n", rel, s)
			break
		}
	}

	return fmt.Sprintf(
		"You are an independent Double-Blind T3 Verifier. Author identity and self-reports have been stripped.\n"+
			"Falsify this change against the repository invariants and inspect for security, regression, or correctness bugs.\n"+
			"You MUST conclude with an explicit line: 'VERDICT: PASS' (if safe) or 'VERDICT: VETO' (with concrete counterexample).\n\n"+
			"%sSanitized Git Diff:\n%s",
		invariants, truncatedDiff,
	)
}

// EvaluateT3Response parses the raw output from `g8s llm-call --json` (or plain
// text), verifies model-family lineage disjointness against authorModel (#662),
// and enforces fail-closed verdict parsing (requiring explicit 'VERDICT: PASS').
func EvaluateT3Response(rawOutput string, authorModel string) (passed bool, resolvedModel string, detail string) {
	trimmed := strings.TrimSpace(rawOutput)
	if trimmed == "" {
		return false, "", "veto: empty verifier response (fail-closed)"
	}

	responseText := trimmed
	if strings.HasPrefix(trimmed, "{") {
		var env struct {
			Data struct {
				ModelResolved  string `json:"model_resolved"`
				ModelRequested string `json:"model_requested"`
				Response       string `json:"response"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(trimmed), &env); err == nil {
			resolvedModel = strings.TrimSpace(env.Data.ModelResolved)
			if resolvedModel == "" {
				resolvedModel = strings.TrimSpace(env.Data.ModelRequested)
			}
			responseText = strings.TrimSpace(env.Data.Response)
		}
	}

	if authorModel != "" && resolvedModel != "" && !AreModelFamiliesDisjoint(authorModel, resolvedModel) {
		return false, resolvedModel, fmt.Sprintf(
			"veto: model family collision between author (%s -> %s) and verifier (%s -> %s)",
			authorModel, ExtractModelFamily(authorModel), resolvedModel, ExtractModelFamily(resolvedModel),
		)
	}

	if responseText == "" {
		return false, resolvedModel, "veto: empty response body in verifier envelope (fail-closed)"
	}

	upper := strings.ToUpper(responseText)
	if strings.Contains(upper, "VERDICT: VETO") || strings.Contains(upper, "VERDICT: FAIL") || strings.Contains(upper, "VERDICT: REJECT") {
		return false, resolvedModel, responseText
	}
	if (strings.Contains(upper, "VETO") || strings.Contains(upper, "FAIL")) && !strings.Contains(upper, "VERDICT: PASS") {
		return false, resolvedModel, responseText
	}
	if !strings.Contains(upper, "VERDICT: PASS") {
		return false, resolvedModel, fmt.Sprintf("veto: missing explicit 'VERDICT: PASS' in verifier output (fail-closed): %s", responseText)
	}

	return true, resolvedModel, responseText
}

func defaultT3ProviderRunner(provider, repoDir, diff string) (bool, string, error) {
	g8sBin := filepath.Join(repoDir, "bin", "g8s")
	if _, err := os.Stat(g8sBin); err != nil {
		g8sBin = "g8s"
	}

	prompt := buildT3BlindReviewPrompt(repoDir, diff)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmdArgs := []string{"llm-call", "--provider", provider}
	if envModel := strings.TrimSpace(os.Getenv("G8S_T3_MODEL")); envModel != "" {
		cmdArgs = append(cmdArgs, "--model", envModel)
	} else if strings.EqualFold(provider, "9router") {
		cmdArgs = append(cmdArgs, "--model", "reasoning-high")
	}
	cmdArgs = append(cmdArgs, "--prompt", prompt, "--json")

	cmd := exec.CommandContext(ctx, g8sBin, cmdArgs...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		detail := outStr
		if detail == "" {
			detail = err.Error()
		}
		return false, detail, err
	}

	authorModel := strings.TrimSpace(os.Getenv("G8S_AUTHOR_MODEL"))
	if authorModel == "" {
		authorModel = strings.TrimSpace(os.Getenv("F0_PROVIDER"))
	}
	passed, resolvedModel, detail := EvaluateT3Response(outStr, authorModel)
	if !passed {
		return false, detail, nil
	}
	if resolvedModel != "" {
		return true, fmt.Sprintf("independent provider %s (model=%s) verified diff", provider, resolvedModel), nil
	}
	return true, fmt.Sprintf("independent provider %s verified diff", provider), nil
}

func defaultT3EvalProbeRunner(repoDir string) (bool, string, error) {
	g8sBin := filepath.Join(repoDir, "bin", "g8s")
	if _, err := os.Stat(g8sBin); err != nil {
		g8sBin = "g8s"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, g8sBin, "eval", "run", "--json")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		detail := outStr
		if detail == "" {
			detail = err.Error()
		}
		return false, detail, err
	}

	return true, "g8s eval probes passed", nil
}

// loadConfiguredProviderNames reads provider names from a providers.json file.
func loadConfiguredProviderNames(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Providers []struct {
			Name string `json:"name"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	var names []string
	for _, p := range cfg.Providers {
		name := strings.TrimSpace(p.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// WireDefaultT3Options configures default provider settings and runners if not already set.
func WireDefaultT3Options(opts *T3VerifyOptions) {
	if opts == nil {
		return
	}
	if opts.F0Provider == "" {
		opts.F0Provider = "agy"
	}
	if len(opts.AvailableProviders) == 0 {
		if cfgPath := strings.TrimSpace(os.Getenv("G8S_PROVIDERS")); cfgPath != "" {
			if configured := loadConfiguredProviderNames(cfgPath); len(configured) > 0 {
				opts.AvailableProviders = configured
			}
		}
		if len(opts.AvailableProviders) == 0 {
			opts.AvailableProviders = []string{"9router", "claude", "agy"}
		}
	}
	if opts.ProviderRunner == nil {
		opts.ProviderRunner = defaultT3ProviderRunner
	}
	if opts.EvalProbeRunner == nil {
		opts.EvalProbeRunner = defaultT3EvalProbeRunner
	}
}

func verifyT3Command(args []string) int {
	var opts T3VerifyOptions
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "check", "run":
			// optional subcommand
		case "--repo":
			if i+1 < len(args) {
				opts.RepoDir = args[i+1]
				i++
			}
		case "--head-sha":
			if i+1 < len(args) {
				opts.HeadSHA = args[i+1]
				i++
			}
		case "--base":
			if i+1 < len(args) {
				opts.BaseRef = args[i+1]
				i++
			}
		case "--f0-provider":
			if i+1 < len(args) {
				opts.F0Provider = args[i+1]
				i++
			}
		case "--available-providers":
			if i+1 < len(args) {
				for _, p := range strings.Split(args[i+1], ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						opts.AvailableProviders = append(opts.AvailableProviders, p)
					}
				}
				i++
			}
		case "--json":
			jsonOutput = true
		}
	}

	if opts.F0Provider == "" {
		if envP := os.Getenv("F0_PROVIDER"); envP != "" {
			opts.F0Provider = envP
		} else if envP := os.Getenv("G8S_F0_PROVIDER"); envP != "" {
			opts.F0Provider = envP
		}
	}
	if len(opts.AvailableProviders) == 0 {
		if envAvail := os.Getenv("G8S_AVAILABLE_PROVIDERS"); envAvail != "" {
			for _, p := range strings.Split(envAvail, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					opts.AvailableProviders = append(opts.AvailableProviders, p)
				}
			}
		} else if os.Getenv("G8S_PROVIDERS") == "" {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				defaultCfg := filepath.Join(home, ".config", "g8s", "providers.json")
				if configured := loadConfiguredProviderNames(defaultCfg); len(configured) > 0 {
					opts.AvailableProviders = configured
				}
			}
		}
	}

	WireDefaultT3Options(&opts)

	verdict, err := RunT3Verification(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 verify-t3: %v\n", err)
		return 1
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(verdict)
	} else {
		fmt.Printf("T3 Verification: %s\n", verdict.Status)
		if verdict.SelectedVerifier != "" {
			fmt.Printf("Verifier: %s\n", verdict.SelectedVerifier)
		}
		if verdict.Reason != "" {
			fmt.Printf("Reason: %s\n", verdict.Reason)
		}
	}

	if verdict.Approved {
		return 0
	}
	return 1
}
