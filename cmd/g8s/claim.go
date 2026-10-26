package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/premise"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/state"
)

// runClaim is the DELTA-42 Premise Challenge & Sycophancy Gate surface:
// `g8s claim check` decomposes an operator request or brief into atomic
// claims and probes every repo claim deterministically before anyone acts
// on it.
func runClaim(args []string) {
	if len(args) == 0 {
		exitUsage("claim", "", "", "usage: g8s claim check --text \"<brief or request>\" [--repo .] [--json]", "Supported subcommands: check", false)
	}
	switch args[0] {
	case "check":
		runClaimCheck(args[1:])
	case "help", "-h", "--help":
		printClaimUsage(os.Stdout)
	default:
		exitUsage("claim", args[0], "", fmt.Sprintf("unknown claim subcommand %q", args[0]), "Supported subcommands: check", false)
	}
}

func printClaimUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  g8s claim check --text \"<brief or request>\" [--repo .] [--json]")
	fmt.Fprintln(w, "  g8s claim check --file brief.md [--run-tests] [--llm --provider 9router --model reasoning-free]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Classifies every atomic claim as MECHANICAL_VERIFIED, OBSERVATIONAL, UNVERIFIED or CONTRADICTED.")
	fmt.Fprintln(w, "Exit code 1 (E_PREMISE_CONTRADICTED) when any claim is refuted by a deterministic repo probe.")
}

func runClaimCheck(args []string) {
	fs := flag.NewFlagSet("claim check", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	textFlag := fs.String("text", "", "operator request or brief to check")
	fileFlag := fs.String("file", "", "read the request or brief from a file")
	repoFlag := fs.String("repo", ".", "repository root used for deterministic probes")
	runTests := fs.Bool("run-tests", false, "execute `go test -run ^Name$` for test pass/fail claims")
	testTimeout := fs.Duration("test-timeout", 120*time.Second, "per-test execution timeout for --run-tests")
	useLLM := fs.Bool("llm", false, "add candidate claims from an LLM decomposer (statuses stay deterministic)")
	providerFlag := fs.String("provider", "9router", "api_call provider for --llm")
	modelFlag := fs.String("model", "reasoning-free", "model/combo for --llm")
	llmTimeout := fs.Duration("llm-timeout", 60*time.Second, "timeout for the --llm decomposition call")
	if err := fs.Parse(args); err != nil {
		exitUsage("claim", "check", *traceID, err.Error(), "", *jsonl)
	}

	text, err := readClaimInput(*textFlag, *fileFlag)
	if err != nil {
		exitUsage("claim", "check", *traceID, err.Error(), "Provide --text, --file, or pipe text on stdin", *jsonl)
	}

	opts := premise.Options{RepoRoot: *repoFlag, RunTests: *runTests, TestTimeout: *testTimeout}
	if *useLLM {
		complete, label, cErr := newProviderCompleteFunc(*providerFlag, *modelFlag, *llmTimeout)
		if cErr != nil {
			exitUsage("claim", "check", *traceID, cErr.Error(), "Declare the provider in providers.json or use --provider 9router", *jsonl)
		}
		opts.Complete = complete
		opts.DecomposerLabel = label
	}

	report, err := premise.Check(context.Background(), text, opts)
	if err != nil {
		exitRuntime("claim", "check", *traceID, cli.CodeInvalid, err, "Check --repo and the input text", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("premise_report", "claim", "check", report)
		env.TraceID = *traceID
		if report.Contradicted() {
			env.Error = &cli.ErrPayload{
				Code:    premise.ErrorCodePremiseContradicted,
				Message: contradictionMessage(report),
				Hint:    "Correct the request: the repository evidence refutes at least one premise",
			}
		}
		if wErr := cli.WriteResponse(os.Stdout, env, *jsonl); wErr != nil {
			exitRuntime("claim", "check", *traceID, cli.CodeIO, wErr, "", *jsonl)
		}
	} else {
		renderPremiseReport(os.Stdout, report)
	}
	if report.Contradicted() {
		os.Exit(1)
	}
}

func readClaimInput(text, file string) (string, error) {
	switch {
	case strings.TrimSpace(text) != "":
		return text, nil
	case file != "":
		data, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			return "", fmt.Errorf("read --file: %w", err)
		}
		return string(data), nil
	case !term.IsTerminal(int(os.Stdin.Fd())):
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		if strings.TrimSpace(string(data)) != "" {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("claim check requires --text, --file, or stdin")
}

func contradictionMessage(r *premise.Report) string {
	var parts []string
	for _, c := range r.ContradictedClaims() {
		parts = append(parts, fmt.Sprintf("%s %q (observed: %s)", c.ID, c.Text, c.Observed))
	}
	return fmt.Sprintf("%d contradicted premise(s): %s", r.Summary.Contradicted, strings.Join(parts, "; "))
}

func renderPremiseReport(w io.Writer, r *premise.Report) {
	fmt.Fprintf(w, "Premise check (%s) repo=%s input_sha256=%s\n", r.Decomposer, r.Repo, r.InputSHA256[:16])
	for _, c := range r.Claims {
		fmt.Fprintf(w, "  [%s] %s %s\n", c.Status, c.ID, c.Text)
		if c.Evidence != nil {
			fmt.Fprintf(w, "      evidence: %s (sha256 %s)\n", firstLine(c.Evidence.Output), c.Evidence.OutputSHA256[:16])
			if c.Evidence.Reproduce != "" {
				fmt.Fprintf(w, "      reproduce: %s\n", c.Evidence.Reproduce)
			}
		}
		if c.Note != "" {
			fmt.Fprintf(w, "      note: %s\n", c.Note)
		}
	}
	for _, warn := range r.Warnings {
		fmt.Fprintf(w, "  warning: %s\n", warn)
	}
	fmt.Fprintf(w, "Verdict: %s (verified=%d observational=%d unverified=%d contradicted=%d)\n",
		r.Verdict, r.Summary.MechanicalVerified, r.Summary.Observational, r.Summary.Unverified, r.Summary.Contradicted)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// resolveAPICallEntry resolves an api_call provider from providers.json,
// falling back to the public 9router loopback defaults (two-layer rule:
// private topology lives only in the untracked operator config).
func resolveAPICallEntry(providerName, model string) (config.ProviderEntry, error) {
	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			providersPath = filepath.Join(home, ".config", "g8s", "providers.json")
		}
	}
	if providersPath != "" {
		if _, statErr := os.Stat(providersPath); statErr == nil {
			if cfgFile, loadErr := config.Load(providersPath); loadErr == nil && cfgFile != nil {
				if resolved, rErr := config.ResolveProviderForModel(cfgFile, providerName, model); rErr == nil && resolved != nil {
					return *resolved, nil
				}
			}
		}
	}
	if strings.EqualFold(providerName, "9router") || providerName == "" {
		return config.ProviderEntry{
			Class:               "api_call",
			Name:                "9router",
			Protocol:            "9router",
			BaseURL:             "http://127.0.0.1:20128/v1",
			BaseURLEnv:          "NINEROUTER_URL",
			AuthEnv:             "OPENAI_API_KEY",
			AllowUnlistedModels: true,
		}, nil
	}
	return config.ProviderEntry{}, fmt.Errorf("provider %q not found in providers.json", providerName)
}

// newProviderCompleteFunc adapts an api_call provider to premise.CompleteFunc.
func newProviderCompleteFunc(providerName, model string, timeout time.Duration) (premise.CompleteFunc, string, error) {
	entry, err := resolveAPICallEntry(providerName, model)
	if err != nil {
		return nil, "", err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	p := provider.NewHTTPProviderFromConfig(entry)
	label := fmt.Sprintf("llm:%s/%s", entry.Name, model)
	complete := func(ctx context.Context, system, prompt string) (string, error) {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		handle, spawnErr := p.Spawn(callCtx, provider.Spec{
			Brief:        prompt,
			Model:        model,
			SystemPrompt: system,
			Timeout:      timeout,
			MaxTokens:    2048,
		})
		if spawnErr != nil {
			return "", fmt.Errorf("spawn %s: %w", label, spawnErr)
		}
		rc, waitErr := handle.Wait(callCtx)
		if waitErr != nil {
			return "", fmt.Errorf("wait %s: %w", label, waitErr)
		}
		return rc.Stdout, nil
	}
	return complete, label, nil
}

// premiseGateResult is the outcome of the optional submit pre-dispatch gate.
type premiseGateResult struct {
	Report     *premise.Report
	Blocked    bool
	Overridden bool
}

// evaluatePremiseGate runs the deterministic premise check on a brief. A
// contradicted brief is blocked unless an explicit override reason is given.
func evaluatePremiseGate(ctx context.Context, brief, repo, overrideReason string) (*premiseGateResult, error) {
	report, err := premise.Check(ctx, brief, premise.Options{RepoRoot: repo})
	if err != nil {
		return nil, err
	}
	res := &premiseGateResult{Report: report}
	if report.Contradicted() {
		if strings.TrimSpace(overrideReason) == "" {
			res.Blocked = true
		} else {
			res.Overridden = true
		}
	}
	return res, nil
}

// premisePayload is the compact premise-check record stored on the task.
func premisePayload(res *premiseGateResult, overrideReason string) map[string]any {
	contradicted := []map[string]any{}
	for _, c := range res.Report.ContradictedClaims() {
		entry := map[string]any{"id": c.ID, "text": c.Text, "observed": c.Observed}
		if c.Evidence != nil {
			entry["evidence_sha256"] = c.Evidence.OutputSHA256
		}
		contradicted = append(contradicted, entry)
	}
	out := map[string]any{
		"schema":       premise.ReportSchema,
		"verdict":      res.Report.Verdict,
		"input_sha256": res.Report.InputSHA256,
		"summary":      res.Report.Summary,
		"contradicted": contradicted,
	}
	if res.Overridden {
		out["override_reason"] = strings.TrimSpace(overrideReason)
	}
	return out
}

const PremiseOverrideEvent state.Event = "premise_override"

func recordPremiseOverride(ctx context.Context, store *controlplane.Store, taskID, actor string, res *premiseGateResult, overrideReason string) error {
	reason, err := json.Marshal(premisePayload(res, overrideReason))
	if err != nil {
		return fmt.Errorf("encode premise override: %w", err)
	}
	if err := store.LogStateEvent(ctx, taskID, state.SubjectTask, state.TaskStateQueued, state.TaskStateQueued, PremiseOverrideEvent, actor, string(reason)); err != nil {
		return fmt.Errorf("log premise override for %s: %w", taskID, err)
	}
	return nil
}
