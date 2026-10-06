package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/lane"
	"github.com/tamld/g8s/internal/routing"
	"github.com/tamld/g8s/internal/settings"
)

// runSubmit queues one durable task through the control plane after validating
// it against the security harness.
func runSubmit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = jsonMode
	key := fs.String("idempotency-key", "", "unique idempotency key for this submission")
	model := fs.String("model", "gemini-3.8-flash-high", "target worker model (defaults to gemini-3.8-flash-high)")
	providerFlag := fs.String("provider", "", "target worker provider (e.g. codex, agy)")
	effortFlag := fs.String("effort", config.EffortMedium, "target worker effort level (none|minimal|low|medium|high|xhigh|max, defaults to medium)")
	priority := fs.Int("priority", 0, "queue priority (-100..100)")
	maxAttempts := fs.Int("max-attempts", 1, "retry budget (1..10)")
	promptFlag := fs.String("prompt", "", "task prompt handed to the worker")
	promptFile := fs.String("prompt-file", "", "path to file containing task prompt")
	role := fs.String("role", "collector", "worker role contract (collector, scout, mcp-mapper, summarizer, verifier, test-runner)")
	permission := fs.String("permission", "read_only", "permission profile (read_only, automation_read, workspace_write)")
	timeout := fs.String("timeout", "30s", "execution window for the worker")
	receiptID := fs.String("receipt-id", "", "write receipt ID (required for workspace_write)")
	parentTaskID := fs.String("parent-task-id", "", "parent task ID for subtask lineage tracking")
	skipPermissions := fs.Bool("skip-permissions", false, "bypass permission checks if permitted by profile")
	var addDirs pathFlags
	fs.Var(&addDirs, "add-dir", "additional allowed directory (repeatable, defaults to cwd; must stay inside scope roots)")
	scopeRootFlag := fs.String("scope-root", "", "comma-separated scope roots extending the jail beyond the working directory (#348)")
	routeFlag := fs.String("route", "manual", "task routing mode (manual|auto, defaults to manual)")
	blastRadiusFlag := fs.String("blast-radius", "", "declared blast radius (low|medium|high)")
	locEstimateFlag := fs.Int("loc-estimate", 0, "declared LOC estimate (positive int)")
	if err := fs.Parse(args); err != nil {
		exitUsage("submit", "", *traceID, err.Error(), "Check 'g8s submit --help'", *jsonl)
	}

	if *routeFlag != "auto" && *routeFlag != "manual" {
		exitUsage("submit", "route", *traceID, fmt.Sprintf("invalid --route %q: must be 'auto' or 'manual'", *routeFlag), "Specify --route=auto or --route=manual", *jsonl)
	}

	if !config.IsValidEffortLevel(*effortFlag) {
		exitUsage("submit", "effort", *traceID, fmt.Sprintf("invalid --effort %q: allowed ladder is [%s]", *effortFlag, strings.Join(config.EffortLadder, ", ")), fmt.Sprintf("Specify an effort level from [%s]", strings.Join(config.EffortLadder, ", ")), *jsonl)
	}

	var effortPassed bool
	var providerPassed bool
	var blastRadiusPassed bool
	var locEstimatePassed bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "provider" {
			providerPassed = true
		}
		if f.Name == "effort" {
			effortPassed = true
		}
		if f.Name == "blast-radius" {
			blastRadiusPassed = true
		}
		if f.Name == "loc-estimate" {
			locEstimatePassed = true
		}
	})

	if blastRadiusPassed {
		trimmed := strings.ToLower(strings.TrimSpace(*blastRadiusFlag))
		if !lane.IsValidBlastRadius(trimmed) {
			exitUsage("submit", "blast-radius", *traceID, fmt.Sprintf("invalid --blast-radius %q: allowed values are low, medium, high", *blastRadiusFlag), "Specify --blast-radius=low, medium, or high", *jsonl)
		}
		*blastRadiusFlag = trimmed
	}

	if locEstimatePassed && *locEstimateFlag <= 0 {
		exitUsage("submit", "loc-estimate", *traceID, fmt.Sprintf("invalid --loc-estimate %d: must be a positive integer", *locEstimateFlag), "Specify a positive integer for --loc-estimate", *jsonl)
	}

	var effectiveProvider string
	if providerPassed {
		trimmed := strings.TrimSpace(*providerFlag)
		if trimmed == "" {
			exitUsage("submit", "provider", *traceID, "--provider must be a non-empty string when specified", "Provide a non-empty provider name", *jsonl)
		}
		effectiveProvider = trimmed
	} else {
		if mgr, err := settings.NewManager(""); err == nil {
			if val, ok := mgr.Get("default_provider"); ok && val != nil {
				if s, ok := val.(string); ok {
					s = strings.TrimSpace(s)
					if s != "" {
						effectiveProvider = s
					}
				}
			}
		}
	}

	var prompt string
	if *promptFile != "" {
		// #348: the prompt file is task input and must pass the same
		// denied-path gate as the task itself before it is read.
		if err := harness.ValidateScopePath(*promptFile); err != nil {
			exitRuntime("submit", "", *traceID, cli.CodeHarness, fmt.Errorf("prompt file rejected: %w", err), "Choose a prompt file outside denied/sensitive paths", *jsonl)
		}
		content, err := os.ReadFile(*promptFile)
		failRuntime(err)
		prompt = string(content)
	} else if *promptFlag != "" {
		prompt = *promptFlag
	} else if !term.IsTerminal(int(os.Stdin.Fd())) {
		content, err := io.ReadAll(os.Stdin)
		failRuntime(err)
		prompt = string(content)
	}

	if *key == "" || prompt == "" {
		exitUsage("submit", "", *traceID, "submit requires --idempotency-key and prompt (via --prompt, --prompt-file, or stdin)", "Provide both --idempotency-key and prompt", *jsonl)
	}
	cwd, err := os.Getwd()
	if err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeIO, err, "Failed to resolve working directory", *jsonl)
	}

	dirs := []string(addDirs)
	if len(dirs) == 0 {
		dirs = []string{cwd}
	}

	// #348 workspace jail (operator-approved design): scope dirs must resolve
	// inside the declared roots — cwd by default, extended with repeatable
	// --scope-root. Cross-root workflows stay supported by explicit opt-in.
	scopeRoots := []string{cwd}
	if *scopeRootFlag != "" {
		scopeRoots = append(scopeRoots, splitComma(*scopeRootFlag)...)
	}
	if err := harness.ValidateScopeJail(dirs, scopeRoots); err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeHarness, fmt.Errorf("scope jail: %w", err), "Add --scope-root for legitimate directories outside the working directory", *jsonl)
	}

	effectiveModel := *model
	if effectiveModel == "" {
		effectiveModel = "gemini-3.8-flash-high"
	}
	effectiveRole := *role

	var routeDecision *routing.Decision
	var manifest *config.File
	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			providersPath = filepath.Join(home, ".config", "g8s", "providers.json")
		}
	}
	if providersPath != "" {
		if _, statErr := os.Stat(providersPath); statErr == nil {
			cfgFile, loadErr := config.Load(providersPath)
			if loadErr != nil {
				if *routeFlag == "auto" {
					exitRuntime("submit", "", *traceID, cli.CodeRuntime, loadErr, "Failed to load providers config", *jsonl)
				}
			} else {
				manifest = cfgFile
			}
		}
	}

	var classPaths []string
	for _, p := range addDirs {
		clean := filepath.Clean(p)
		if rel, err := filepath.Rel(cwd, clean); err == nil && !strings.HasPrefix(rel, "..") {
			classPaths = append(classPaths, filepath.ToSlash(rel))
		} else {
			classPaths = append(classPaths, filepath.ToSlash(clean))
		}
	}

	className, defaultEffort, ecErr := lane.ResolveClassForRoots(classPaths, scopeRoots)
	if ecErr != nil {
		exitRuntime("submit", "", *traceID, cli.CodeRuntime, ecErr, "Failed to load effort classes", *jsonl)
	}

	hasSignals := blastRadiusPassed || locEstimatePassed
	var sigRes lane.SignalResult
	var explicitEffort string
	if effortPassed {
		explicitEffort = *effortFlag
	}

	requestedEffort := defaultEffort
	var effortOverride bool
	var effortOverrideDown bool
	if hasSignals {
		sigRes = lane.ResolveEffortSignals(
			explicitEffort,
			*blastRadiusFlag,
			*locEstimateFlag,
			className,
			defaultEffort,
		)
		requestedEffort = sigRes.Effort
		if effortPassed {
			effortOverride = true
			if sigRes.OverrideDown {
				effortOverrideDown = true
			}
		}
	} else {
		if effortPassed {
			requestedEffort = *effortFlag
			effortOverride = true
			if lane.IsOverrideDown(*effortFlag, defaultEffort) {
				effortOverrideDown = true
			}
		}
	}

	if *routeFlag == "auto" {
		var routePaths []string
		if len(addDirs) > 0 {
			routePaths = append(routePaths, addDirs...)
		}

		dec, routeErr := routing.Route(context.Background(), routing.RouteRequest{
			Prompt:      prompt,
			Paths:       routePaths,
			TimeoutHint: *timeout,
			Manifest:    manifest,
			Effort:      requestedEffort,
		})
		if routeErr != nil {
			var mandErr *routing.MandatoryEffortError
			if errors.As(routeErr, &mandErr) {
				exitUsage("submit", "effort", *traceID, routeErr.Error(), "Reasoning cannot be disabled for this model", *jsonl)
			}
			exitUsage("submit", "route", *traceID, fmt.Sprintf("auto-routing failed: %v", routeErr), "Verify provider manifest and task parameters or use --route=manual", *jsonl)
		}
		if dec.Provider == "" || dec.Model == "" || dec.Role == "" {
			exitUsage("submit", "route", *traceID, "auto-routing could not determine provider, model, or role", "Check providers manifest or specify --route=manual", *jsonl)
		}
		routeDecision = &dec
		effectiveProvider = dec.Provider
		effectiveModel = dec.Model
		effectiveRole = dec.Role
	}

	// Resolve effort for the effective provider and model across both manual and auto routes
	effortRes, effortErr := routing.ResolveEffort(manifest, effectiveProvider, effectiveModel, requestedEffort)
	if effortErr != nil {
		var mandErr *routing.MandatoryEffortError
		if errors.As(effortErr, &mandErr) {
			exitUsage("submit", "effort", *traceID, effortErr.Error(), "Reasoning cannot be disabled for this model", *jsonl)
		}
		exitRuntime("submit", "", *traceID, cli.CodeRuntime, effortErr, "Failed to resolve effort", *jsonl)
	}

	// Baked-name model/effort alignment:
	// When effective model matches baked-name pattern (-<level>) for a baked-name provider
	// and differs from effort_applied, rewrite model to -{effort_applied}, record model_requested
	// and model_realigned=true. Plain pass-through when they already agree or model is not baked-name.
	var modelRequested string
	var modelRealigned bool
	if isBakedNameProvider(manifest, effectiveProvider, effectiveModel) {
		if baseName, suffixLvl, ok := parseBakedModelSuffix(effectiveModel); ok {
			if effortRes.Applied != "" && !strings.EqualFold(suffixLvl, effortRes.Applied) {
				modelRequested = effectiveModel
				effectiveModel = baseName + "-" + effortRes.Applied
				modelRealigned = true
			}
		}
	}

	// Validate request against security harness gatekeeper
	if err := harness.ValidateRequest(prompt, effectiveRole, *permission, dirs, *skipPermissions, *receiptID); err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeHarness, fmt.Errorf("harness validation failed: %w", err), "Ensure role and permissions allow the requested action", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeIO, err, "Failed to resolve database path", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeRuntime, err, "Failed to open control plane database", *jsonl)
	}
	defer store.Close()

	payloadMap := map[string]any{
		"prompt":       prompt,
		"model":        effectiveModel,
		"role":         effectiveRole,
		"permission":   *permission,
		"timeout":      *timeout,
		"add_dirs":     dirs,
		"actor":        *actor,
		"effort_class": className,
	}
	if modelRealigned {
		payloadMap["model_requested"] = modelRequested
		payloadMap["model_realigned"] = true
	}
	if effortOverride {
		payloadMap["effort_override"] = true
	}
	if effortOverrideDown {
		payloadMap["effort_override_down"] = true
	}
	if hasSignals {
		payloadMap["effort_source"] = sigRes.Source
		payloadMap["effort_signals"] = sigRes.EffortSignals()
	}
	if effectiveProvider != "" {
		payloadMap["provider"] = effectiveProvider
	}
	if *receiptID != "" {
		payloadMap["receipt_id"] = *receiptID
	}
	if *skipPermissions {
		payloadMap["skip_permissions"] = true
	}
	if routeDecision != nil {
		payloadMap["provider"] = routeDecision.Provider
		payloadMap["model"] = effectiveModel
		payloadMap["role"] = routeDecision.Role
		payloadMap["route_source"] = routeDecision.Source
		payloadMap["route_reason"] = routeDecision.Reason
	}
	if effortRes.Applied != "" {
		payloadMap["effort"] = effortRes.Applied
	}
	payloadMap["effort_requested"] = effortRes.Requested
	payloadMap["effort_applied"] = effortRes.Applied
	if effortRes.Mismatch {
		payloadMap["effort_mismatch"] = true
	}
	if effortRes.BudgetTokens > 0 {
		payloadMap["effort_budget_tokens"] = effortRes.BudgetTokens
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeRuntime, err, "Failed to serialize task payload", *jsonl)
	}

	var parentIDPtr *string
	if *parentTaskID != "" {
		parentIDPtr = parentTaskID
	}

	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: *key,
		ParentTaskID:   parentIDPtr,
		Priority:       *priority,
		MaxAttempts:    *maxAttempts,
		Model:          effectiveModel,
		Payload:        payload,
		Role:           effectiveRole,
		Permission:     *permission,
		Timeout:        *timeout,
		AddDirs:        dirs,
	})
	if err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeRuntime, err, "Failed to submit task", *jsonl)
	}

	env := cli.NewEnvelope("task", "submit", "", task)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

func parseBakedModelSuffix(modelID string) (string, string, bool) {
	for _, lvl := range config.EffortLadder {
		suffix := "-" + lvl
		if strings.HasSuffix(strings.ToLower(modelID), suffix) {
			base := modelID[:len(modelID)-len(suffix)]
			return base, lvl, true
		}
	}
	return "", "", false
}

func matchModelID(candID, targetID string) bool {
	if strings.EqualFold(candID, targetID) {
		return true
	}
	candTrimmed := strings.TrimSuffix(candID, "-{effort}")
	targetTrimmed := strings.TrimSuffix(targetID, "-{effort}")
	if strings.EqualFold(candTrimmed, targetTrimmed) {
		return true
	}
	for _, lvl := range config.EffortLadder {
		if strings.EqualFold(candTrimmed, strings.TrimSuffix(targetTrimmed, "-"+lvl)) {
			return true
		}
		if strings.EqualFold(strings.TrimSuffix(candTrimmed, "-"+lvl), targetTrimmed) {
			return true
		}
	}
	return false
}

func isBakedNameProvider(manifest *config.File, provider, modelID string) bool {
	// 0. Manual submits may omit --provider entirely; the model id alone must
	// decide (issue #568: the realignment never fired for provider-less
	// submits because this gate required a provider name). Manifest ownership
	// first — if any manifest provider declares this id, that pairing decides
	// (the realignment must never move a model onto a provider that does not
	// declare the target variant). Otherwise the catalog decides.
	if provider == "" {
		if manifest != nil {
			for _, p := range manifest.Providers {
				for _, m := range p.Models {
					if matchModelID(m.ID, modelID) {
						return m.EffortStyle == config.EffortStyleBakedName
					}
				}
			}
		}
		if cat, err := config.LoadDefaultCatalog(); err == nil && cat != nil {
			for _, cp := range cat.Providers {
				if cp.EffortStyle != config.EffortStyleBakedName {
					continue
				}
				for _, m := range cp.Models {
					if matchModelID(m.ID, modelID) {
						return true
					}
				}
			}
		}
		return false
	}

	if verdict, decided := manifestBakedNameVerdict(manifest, provider, modelID); decided {
		return verdict
	}
	// Undeclared style on the matched manifest model, or the manifest knows the
	// provider but not this id: fall through to the catalog, the effort-style
	// SSoT. (Issue #568: platform manifests declare model ids without styles and
	// shadowed the catalog's baked-name verdict, so mismatched (model, effort)
	// combos reached the worker CLI and died.)

	// 2. Check default catalog for the provider
	cat, err := config.LoadDefaultCatalog()
	if err == nil && cat != nil {
		if cp, ok := cat.FindProvider(provider); ok {
			if cp.EffortStyle == config.EffortStyleBakedName {
				return true
			}
			for _, m := range cp.Models {
				if matchModelID(m.ID, modelID) {
					return m.EffortStyle == config.EffortStyleBakedName
				}
			}
		}
	}

	return false
}

// manifestBakedNameVerdict reports whether the user manifest itself decides the
// baked-name question for (provider, modelID). decided=false means the catalog
// must be consulted: either the matched model entry declares no effort style, or
// the manifest knows the provider but not this model id — platform manifests list
// one variant of a family while the catalog is the id+style SSoT for the rest
// (issue #568: gemini-3.7-flash-high in the manifest shadowed the catalog verdict
// for gemini-3.8-flash-high, so mismatched (model, effort) combos reached the
// worker CLI and died).
func manifestBakedNameVerdict(manifest *config.File, provider, modelID string) (baked bool, decided bool) {
	if manifest == nil {
		return false, false
	}
	for _, p := range manifest.Providers {
		if !strings.EqualFold(p.Name, provider) {
			continue
		}
		for _, m := range p.Models {
			if matchModelID(m.ID, modelID) {
				if m.EffortStyle == "" {
					return false, false
				}
				return m.EffortStyle == config.EffortStyleBakedName, true
			}
		}
		return false, false
	}
	return false, false
}
