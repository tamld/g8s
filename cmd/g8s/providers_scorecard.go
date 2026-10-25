package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/routing"
)

func runProvidersScorecard(args []string) {
	fs := flag.NewFlagSet("providers scorecard", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	providerFlag := fs.String("provider", "", "filter scorecard by provider name (e.g. 9router)")
	windowFlag := fs.Int("window", routing.DefaultScorecardWindow, "rolling observation window size per model")
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("providers", "scorecard", *traceID, err.Error(), "usage: g8s providers scorecard [--provider <name>] [--window 20] [--json]", *jsonl)
	}

	scores, err := routing.ComputeScorecard(pathutil.DefaultStateDir(), *providerFlag, *windowFlag)
	if err != nil {
		exitRuntime("providers", "scorecard", *traceID, cli.CodeIO, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		data := map[string]any{
			"window":         *windowFlag,
			"scorecard_path": routing.ScorecardPath(pathutil.DefaultStateDir()),
			"models":         scores,
		}
		env := cli.NewEnvelope("models_scorecard", "providers", "scorecard", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("providers", "scorecard", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	if len(scores) == 0 {
		fmt.Println("No model quality observations recorded yet. Run 'g8s llm-call' or 'g8s providers evaluate' to populate the scorecard.")
		return
	}

	var td pterm.TableData
	td = append(td, []string{"Provider", "Model", "Samples", "Success", "VerifyPass", "P50(ms)", "Score", "Health", "Recommendation"})
	for _, s := range scores {
		td = append(td, []string{
			s.Provider,
			s.Model,
			fmt.Sprintf("%d", s.WindowSamples),
			fmt.Sprintf("%.0f%%", s.SuccessRate*100),
			fmt.Sprintf("%.0f%%", s.VerifyPassRate*100),
			fmt.Sprintf("%d", s.P50LatencyMs),
			fmt.Sprintf("%.1f", s.CompositeScore),
			string(s.HealthStatus),
			s.Recommendation,
		})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()
}

func runProvidersEvaluate(args []string) {
	fs := flag.NewFlagSet("providers evaluate", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	providerFlag := fs.String("provider", "9router", "provider name to evaluate (default: 9router)")
	modelsFlag := fs.String("model", "reasoning-free,fast-agent,docs-retriever", "comma-separated models/combos to probe and evaluate")
	promptFlag := fs.String("prompt", "Reply with the exact single token: READY_OK", "canary evaluation prompt")
	expectContainsFlag := fs.String("expect-contains", "READY_OK", "expected substring in model response for canary verification")
	verifyCmdFlag := fs.String("verify-cmd", "", "optional shell verification command to execute after response")
	timeoutFlag := fs.String("timeout", "30s", "per-model probe timeout")
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("providers", "evaluate", *traceID, err.Error(), "usage: g8s providers evaluate [--provider 9router] [--model m1,m2] [--json]", *jsonl)
	}

	timeoutDur, err := time.ParseDuration(*timeoutFlag)
	if err != nil || timeoutDur <= 0 {
		timeoutDur = 30 * time.Second
	}

	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			providersPath = filepath.Join(home, ".config", "g8s", "providers.json")
		}
	}
	var cfgFile *config.File
	if providersPath != "" {
		if _, statErr := os.Stat(providersPath); statErr == nil {
			cfgFile, _ = config.Load(providersPath)
		}
	}

	var entry config.ProviderEntry
	found := false
	if cfgFile != nil {
		if p, ok := config.FindProvider(cfgFile, *providerFlag); ok && p.Class == "api_call" {
			entry = *p
			found = true
		}
	}
	if !found {
		if strings.EqualFold(*providerFlag, "9router") {
			entry = config.ProviderEntry{
				Class:               "api_call",
				Name:                "9router",
				Protocol:            "9router",
				BaseURL:             "http://127.0.0.1:20128/v1",
				BaseURLEnv:          "NINEROUTER_URL",
				AuthEnv:             "OPENAI_API_KEY",
				AllowUnlistedModels: true,
			}
			found = true
		}
	}
	if !found {
		exitUsage("providers", "evaluate", *traceID, fmt.Sprintf("provider %q not found", *providerFlag), "Configure provider in providers.json", *jsonl)
	}

	httpProv := provider.NewHTTPProviderFromConfig(entry)
	var models []string
	for _, raw := range strings.Split(*modelsFlag, ",") {
		m := strings.TrimSpace(raw)
		if m != "" {
			models = append(models, m)
		}
	}
	if len(models) == 0 {
		exitUsage("providers", "evaluate", *traceID, "at least one --model is required", "", *jsonl)
	}

	type probeOutcome struct {
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		ModelResolved string `json:"model_resolved,omitempty"`
		Success       bool   `json:"success"`
		VerifyPassed  bool   `json:"verify_passed"`
		DurationMs    int64  `json:"duration_ms"`
		Error         string `json:"error,omitempty"`
	}

	var outcomes []probeOutcome
	for _, m := range models {
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)
		spec := provider.Spec{
			Brief:     *promptFlag,
			Model:     m,
			MaxTokens: 256,
			Timeout:   timeoutDur,
			TraceID:   *traceID,
		}
		start := time.Now()
		handle, sErr := httpProv.Spawn(ctx, spec)
		if sErr != nil {
			cancel()
			dur := time.Since(start).Milliseconds()
			errMsg := dispatch.SanitizeOutput(sErr.Error())
			_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
				Provider:       entry.Name,
				ModelRequested: m,
				Success:        false,
				VerifyChecked:  true,
				VerifyPassed:   false,
				DurationMs:     dur,
				ErrorReason:    errMsg,
			})
			outcomes = append(outcomes, probeOutcome{
				Provider:   entry.Name,
				Model:      m,
				Success:    false,
				DurationMs: dur,
				Error:      errMsg,
			})
			continue
		}

		res, wErr := handle.Wait(ctx)
		cancel()
		dur := res.DurationMs
		if dur <= 0 {
			dur = time.Since(start).Milliseconds()
		}
		if wErr != nil {
			errMsg := dispatch.SanitizeOutput(wErr.Error())
			_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
				Provider:       entry.Name,
				ModelRequested: m,
				ModelResolved:  res.ModelResolved,
				Success:        false,
				VerifyChecked:  true,
				VerifyPassed:   false,
				DurationMs:     dur,
				TotalTokens:    res.TotalTokens,
				ErrorReason:    errMsg,
			})
			outcomes = append(outcomes, probeOutcome{
				Provider:      entry.Name,
				Model:         m,
				ModelResolved: res.ModelResolved,
				Success:       false,
				DurationMs:    dur,
				Error:         errMsg,
			})
			continue
		}

		vPassed := true
		errMsg := ""
		if expect := strings.TrimSpace(*expectContainsFlag); expect != "" {
			if !strings.Contains(res.Stdout, expect) {
				vPassed = false
				errMsg = fmt.Sprintf("canary mismatch: response did not contain %q", expect)
			}
		}
		if vPassed && strings.TrimSpace(*verifyCmdFlag) != "" {
			vCtx, vCancel := context.WithTimeout(context.Background(), 30*time.Second)
			vCmd := exec.CommandContext(vCtx, "sh", "-c", *verifyCmdFlag)
			vCmd.Env = scrubVerifyEnv(os.Environ())
			if out, vErr := vCmd.CombinedOutput(); vErr != nil {
				vPassed = false
				errMsg = dispatch.SanitizeOutput(strings.TrimSpace(string(out)))
			}
			vCancel()
		}

		_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
			Provider:       entry.Name,
			ModelRequested: m,
			ModelResolved:  res.ModelResolved,
			Success:        true,
			VerifyChecked:  true,
			VerifyPassed:   vPassed,
			DurationMs:     dur,
			TotalTokens:    res.TotalTokens,
			ErrorReason:    errMsg,
		})
		outcomes = append(outcomes, probeOutcome{
			Provider:      entry.Name,
			Model:         m,
			ModelResolved: res.ModelResolved,
			Success:       true,
			VerifyPassed:  vPassed,
			DurationMs:    dur,
			Error:         errMsg,
		})
	}

	scores, _ := routing.ComputeScorecard(pathutil.DefaultStateDir(), entry.Name, routing.DefaultScorecardWindow)
	if *jsonMode || *jsonl {
		data := map[string]any{
			"provider":  entry.Name,
			"probes":    outcomes,
			"scorecard": scores,
		}
		env := cli.NewEnvelope("models_evaluate", "providers", "evaluate", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	runProvidersScorecard([]string{"--provider", entry.Name})
}
