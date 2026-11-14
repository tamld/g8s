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
	modelsFlag := fs.String("model", "fast-agent,reasoning-high,deep-coding,docs-retriever", "comma-separated models/combos to probe and evaluate")
	promptFlag := fs.String("prompt", "Reply with the exact single token: READY_OK", "canary evaluation prompt")
	expectContainsFlag := fs.String("expect-contains", "READY_OK", "expected substring in model response for canary verification")
	verifyCmdFlag := fs.String("verify-cmd", "", "optional shell verification command to execute after response")
	timeoutFlag := fs.String("timeout", "30s", "per-model probe timeout")
	maxTokensFlag := fs.Int("max-tokens", 512, "max output tokens for probe requests (default 512 for reasoning models)")
	discoverCombosFlag := fs.Bool("discover-combos", false, "discover live models from provider and probe top-level combo IDs (no slash in ID)")
	auditCatalogFlag := fs.Bool("audit-catalog", false, "load .g8s/agent-models.yml catalog and append cataloged model IDs for target provider to probe list")
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("providers", "evaluate", *traceID, err.Error(), "usage: g8s providers evaluate [--provider 9router] [--model m1,m2] [--discover-combos] [--audit-catalog] [--json]", *jsonl)
	}

	timeoutDur, err := time.ParseDuration(*timeoutFlag)
	if err != nil || timeoutDur <= 0 {
		timeoutDur = 30 * time.Second
	}

	// Resolve provider using the unified helper supporting both api_call and platform_dispatch
	ctx := context.Background()
	cfgFile := loadProvidersManifestFile()
	cat, _ := config.LoadDefaultCatalog()
	httpProv, err := resolveCallableProvider(ctx, *providerFlag)
	if err != nil {
		exitUsage("providers", "evaluate", *traceID, err.Error(), "Configure provider in providers.json or use a registered platform_dispatch provider", *jsonl)
	}
	_, isHTTP := httpProv.(*provider.OpenAIProvider)
	isCLI := !isHTTP

	// Get the provider name for recording outcomes
	entryName := *providerFlag
	if p, ok := httpProv.(interface{ Name() string }); ok {
		entryName = p.Name()
	}

	modelExplicitlyPassed := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "model" {
			modelExplicitlyPassed = true
		}
	})

	var models []string
	if !modelExplicitlyPassed && !strings.EqualFold(entryName, "9router") {
		if defModel := config.ResolveProviderDefaultModel(cfgFile, cat, entryName); defModel != "" {
			models = []string{defModel}
		}
	} else {
		for _, raw := range strings.Split(*modelsFlag, ",") {
			m := strings.TrimSpace(raw)
			if m != "" {
				models = append(models, m)
			}
		}
	}
	if *discoverCombosFlag {
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)
		// Try to call DiscoverModels via type assertion for platform_dispatch providers
		var discovered []string
		var err error
		if p, ok := httpProv.(interface {
			DiscoverModels(context.Context) ([]string, error)
		}); ok {
			discovered, err = p.DiscoverModels(ctx)
		}
		cancel()
		if err == nil {
			var combos []string
			for _, m := range discovered {
				if !strings.Contains(m, "/") {
					combos = append(combos, m)
				}
			}
			if !modelExplicitlyPassed && len(combos) > 0 {
				models = combos
			} else {
				models = append(models, combos...)
			}
		}
	}

	// --audit-catalog: load .g8s/agent-models.yml and append cataloged model IDs for target provider
	if *auditCatalogFlag {
		cat, err := config.LoadDefaultCatalog()
		if err == nil && cat != nil {
			if provCat, ok := cat.FindProvider(entryName); ok && len(provCat.Models) > 0 {
				if !modelExplicitlyPassed && !*discoverCombosFlag {
					models = nil
				}
				for _, m := range provCat.Models {
					models = append(models, m.ID)
				}
			}
		}
	}

	// Deduplicate models
	seen := make(map[string]bool)
	deduped := make([]string, 0, len(models))
	for _, m := range models {
		if !seen[m] {
			seen[m] = true
			deduped = append(deduped, m)
		}
	}
	models = deduped

	if len(models) == 0 {
		exitUsage("providers", "evaluate", *traceID, "at least one --model is required", "", *jsonl)
	}

	type probeOutcome struct {
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		ModelResolved string `json:"model_resolved,omitempty"`
		Family        string `json:"family,omitempty"`
		CostTier      string `json:"cost_tier,omitempty"`
		Status        string `json:"status,omitempty"`
		Success       bool   `json:"success"`
		VerifyPassed  bool   `json:"verify_passed"`
		DurationMs    int64  `json:"duration_ms"`
		Error         string `json:"error,omitempty"`
		FailureClass  string `json:"failure_class,omitempty"`
	}

	var outcomes []probeOutcome
	for _, m := range models {
		fam, costTier, mStatus := lookupDeclarativeModelMeta(cfgFile, cat, entryName, m)
		ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)
		spec := provider.Spec{
			Brief:     *promptFlag,
			Model:     m,
			MaxTokens: *maxTokensFlag,
			Timeout:   timeoutDur,
			TraceID:   *traceID,
		}
		var iterCleanup func()
		if isCLI {
			if hookDir, _, cl, hErr := dispatch.SetupToolPolicyNoneWorkspace(); hErr == nil {
				iterCleanup = cl
				spec.WorktreeDir = hookDir
				spec.AddDirs = []string{hookDir}
				spec.Env = map[string]string{"G8S_SANDBOX": "1"}
			}
		}
		start := time.Now()
		handle, sErr := httpProv.Spawn(ctx, spec)
		if sErr != nil {
			cancel()
			if iterCleanup != nil {
				iterCleanup()
			}
			dur := time.Since(start).Milliseconds()
			errMsg := dispatch.SanitizeOutput(sErr.Error())
			fc := classifyProbeFailure(false, false, errMsg)
			_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
				Provider:       entryName,
				ModelRequested: m,
				Success:        false,
				VerifyChecked:  true,
				VerifyPassed:   false,
				DurationMs:     dur,
				ErrorReason:    errMsg,
			})
			outcomes = append(outcomes, probeOutcome{
				Provider:     entryName,
				Model:        m,
				Family:       fam,
				CostTier:     costTier,
				Status:       mStatus,
				Success:      false,
				DurationMs:   dur,
				Error:        errMsg,
				FailureClass: fc,
			})
			continue
		}

		res, wErr := handle.Wait(ctx)
		cancel()
		if iterCleanup != nil {
			iterCleanup()
		}
		dur := res.DurationMs
		if dur <= 0 {
			dur = time.Since(start).Milliseconds()
		}
		if wErr != nil {
			errMsg := dispatch.SanitizeOutput(wErr.Error())
			fc := classifyProbeFailure(false, false, errMsg)
			_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
				Provider:       entryName,
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
				Provider:      entryName,
				Model:         m,
				ModelResolved: res.ModelResolved,
				Family:        fam,
				CostTier:      costTier,
				Status:        mStatus,
				Success:       false,
				DurationMs:    dur,
				Error:         errMsg,
				FailureClass:  fc,
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

		fc := classifyProbeFailure(true, vPassed, errMsg)
		_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
			Provider:       entryName,
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
			Provider:      entryName,
			Model:         m,
			ModelResolved: res.ModelResolved,
			Family:        fam,
			CostTier:      costTier,
			Status:        mStatus,
			Success:       true,
			VerifyPassed:  vPassed,
			DurationMs:    dur,
			Error:         errMsg,
			FailureClass:  fc,
		})
	}

	scores, _ := routing.ComputeScorecard(pathutil.DefaultStateDir(), entryName, routing.DefaultScorecardWindow)
	if *jsonMode || *jsonl {
		data := map[string]any{
			"provider":  entryName,
			"probes":    outcomes,
			"scorecard": scores,
		}
		env := cli.NewEnvelope("models_evaluate", "providers", "evaluate", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	runProvidersScorecard([]string{"--provider", entryName})
}

func classifyProbeFailure(success bool, verifyPassed bool, errMsg string) string {
	if success && verifyPassed {
		return "HEALTHY"
	}
	if success && !verifyPassed {
		return "CANARY_MISMATCH"
	}
	lower := strings.ToLower(errMsg)
	if strings.Contains(lower, "status 410") || strings.Contains(lower, "[410]") || strings.Contains(lower, "end of life") || strings.Contains(lower, "no longer available") {
		return "MODEL_EOL_410"
	}
	if strings.Contains(lower, "status 404") || strings.Contains(lower, "[404]") || strings.Contains(lower, "not found") || strings.Contains(lower, "page not found") {
		return "MODEL_NOT_FOUND_404"
	}
	if strings.Contains(lower, "status 400") || strings.Contains(lower, "agentservice") {
		return "PROTOCOL_MISMATCH_400"
	}
	if strings.Contains(lower, "context deadline exceeded") || strings.Contains(lower, "client.timeout") {
		return "TIMEOUT"
	}
	return "UPSTREAM_ERROR"
}

func loadProvidersManifestFile() *config.File {
	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			providersPath = filepath.Join(home, ".config", "g8s", "providers.json")
		}
	}
	if providersPath != "" {
		if _, statErr := os.Stat(providersPath); statErr == nil {
			cfgFile, _ := config.Load(providersPath)
			return cfgFile
		}
	}
	return nil
}

func lookupDeclarativeModelMeta(cfgFile *config.File, cat *config.Catalog, providerName, modelID string) (family, costTier, status string) {
	if cfgFile != nil {
		if p, ok := config.FindProvider(cfgFile, providerName); ok {
			for _, m := range p.Models {
				if strings.EqualFold(m.ID, modelID) {
					family, costTier, status = m.Family, m.CostTier, m.Status
					break
				}
			}
		}
	}
	if cat != nil {
		cp, ok := cat.FindProvider(providerName)
		if !ok {
			switch strings.ToLower(strings.TrimSpace(providerName)) {
			case "codex":
				cp, ok = cat.FindProvider("openai")
			case "claude":
				cp, ok = cat.FindProvider("anthropic")
			case "gemini":
				cp, ok = cat.FindProvider("google")
			}
		}
		if ok {
			cm, found := cp.FindModel(modelID)
			if !found {
				for _, lvl := range config.EffortLadder {
					if strings.HasSuffix(strings.ToLower(modelID), "-"+lvl) {
						baseID := modelID[:len(modelID)-len("-"+lvl)]
						if bm, bFound := cp.FindModel(baseID); bFound {
							cm, found = bm, true
							break
						}
					}
				}
			}
			if found {
				if family == "" {
					family = cm.Family
				}
				if costTier == "" {
					costTier = cm.CostTier
				}
				if status == "" {
					status = cm.Status
				}
			}
			if family == "" {
				family = cp.Family
			}
			if costTier == "" {
				costTier = cp.CostTier
			}
			if status == "" {
				status = cp.Status
			}
		}
	}
	return family, costTier, status
}

// resolveCallableProvider resolves a provider to a callable provider instance.
// It checks the providers.json manifest first, then the default catalog,
// and falls back to platform_dispatch providers (agy, cursor, etc.) if available.
func resolveCallableProvider(ctx context.Context, providerName string) (provider.Provider, error) {
	cfgFile := loadProvidersManifestFile()
	if cfgFile != nil {
		if p, ok := config.FindProvider(cfgFile, providerName); ok {
			if p.Class == "api_call" {
				return provider.NewHTTPProviderFromConfig(*p), nil
			}
			if p.Class == "platform_dispatch" {
				if cliProv, err := provider.NewRegistry().Get(strings.ToLower(p.Name)); err == nil {
					if aErr := cliProv.Available(ctx); aErr == nil {
						return cliProv, nil
					}
				}
			}
		}
	}

	// Check default catalog for 9router provider
	if cat, err := config.LoadDefaultCatalog(); err == nil && cat != nil {
		if cp, ok := cat.FindProvider(providerName); ok {
			// Check if it's a 9router provider (has 9router protocol or name)
			if strings.EqualFold(cp.Name, "9router") || strings.Contains(strings.ToLower(cp.Name), "9router") {
				// Create a 9router HTTP provider entry
				return provider.NewHTTPProviderFromConfig(config.ProviderEntry{
					Class:               "api_call",
					Name:                cp.Name,
					Protocol:            "9router",
					BaseURL:             "http://127.0.0.1:20128/v1",
					BaseURLEnv:          "NINEROUTER_URL",
					AuthEnv:             "OPENAI_API_KEY",
					AllowUnlistedModels: true,
				}), nil
			}
		}
	}

	// Fall back to platform_dispatch providers
	reg := provider.NewRegistry()
	if p, err := reg.Get(providerName); err == nil {
		if err := p.Available(ctx); err == nil {
			return p, nil
		}
	}

	return nil, fmt.Errorf("provider %q not callable (not in providers.json as api_call, not in catalog as 9router, or not available as platform_dispatch)", providerName)
}
