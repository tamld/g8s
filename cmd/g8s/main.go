// Command g8s is the self-describing CLI entry point for The Gatekeepers
// runtime. Per constitution Axiom 5 every subcommand surface documented here
// reflects implemented behavior only; planned commands are marked explicitly.
package main

import (
	"context"

	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"

	"github.com/tamld/g8s/internal/cli"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/harness"

	"github.com/tamld/g8s/internal/mcp"

	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
)

var AppName = "g8s"

func exitUsage(cmd, sub, traceID, msg, hint string, jsonl bool) {
	if traceID == "" {
		traceID = cli.GenerateTraceID()
	}
	env := cli.NewErrorEnvelope(cmd, sub, traceID, cli.CodeUsage, msg, hint, "")
	_ = cli.WriteResponse(os.Stderr, env, jsonl)
	os.Exit(2)
}

func exitRuntime(cmd, sub, traceID, code string, err error, hint string, jsonl bool) {
	if traceID == "" {
		traceID = cli.GenerateTraceID()
	}
	if code == "" {
		code = cli.CodeRuntime
	}
	msg := ""
	cause := ""
	if err != nil {
		msg = err.Error()
		if unwrapped := errors.Unwrap(err); unwrapped != nil {
			cause = unwrapped.Error()
		}
	}
	env := cli.NewErrorEnvelope(cmd, sub, traceID, code, msg, hint, cause)
	_ = cli.WriteResponse(os.Stderr, env, jsonl)
	os.Exit(1)
}

func extractSelectorFromArgs() {
	if len(os.Args) < 2 {
		if envSel := os.Getenv("G8S_SELECT"); envSel != "" && cli.DefaultSelector() == "" {
			cli.SetDefaultSelector(envSel)
		}
		return
	}

	var command string
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, "-") {
			command = arg
			break
		}
	}

	if command != "select" {
		var filtered []string
		filtered = append(filtered, os.Args[0])
		var expr string
		for i := 1; i < len(os.Args); i++ {
			arg := os.Args[i]
			if arg == "--select" || arg == "-select" {
				if i+1 < len(os.Args) {
					expr = os.Args[i+1]
					i++
				}
				continue
			}
			if strings.HasPrefix(arg, "--select=") {
				expr = strings.TrimPrefix(arg, "--select=")
				continue
			}
			if strings.HasPrefix(arg, "-select=") {
				expr = strings.TrimPrefix(arg, "-select=")
				continue
			}
			filtered = append(filtered, arg)
		}
		if expr != "" {
			cli.SetDefaultSelector(expr)
			os.Args = filtered
		}
	}

	if envSel := os.Getenv("G8S_SELECT"); envSel != "" && cli.DefaultSelector() == "" {
		cli.SetDefaultSelector(envSel)
	}
}

func main() {
	extractSelectorFromArgs()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	command := os.Args[1]
	switch command {
	case "version", "-v", "--version":
		runVersion(os.Args[2:])
	case "eval":
		runEval(os.Args[2:])
	case "offer":
		runOffer(os.Args[2:])
	case "search":
		runSearch(os.Args[2:])
	case "read":
		runRead(os.Args[2:])
	case "edit":
		runEdit(os.Args[2:])
	case "select":
		runSelect(os.Args[2:])
	case "watch":
		runWatch(os.Args[2:])
	case "reflex":
		runReflex(os.Args[2:])
	case "roles":
		runRoles(os.Args[2:])
	case "permissions":
		runPermissions(os.Args[2:])
	case "mcp":
		runMCPServer(os.Args[2:])
	case "submit":
		runSubmit(os.Args[2:])
	case "get":
		runGet(os.Args[2:])
	case "deliver":
		runDeliver(os.Args[2:])
	case "resume":
		runResume(os.Args[2:])
	case "tasks":
		runTasks(os.Args[2:])
	case "cancel":
		runCancel(os.Args[2:])
	case "resubmit":
		runResubmit(os.Args[2:])
	case "lineage":
		runLineage(os.Args[2:])
	case "children":
		runChildren(os.Args[2:])
	case "receipt":
		runReceipt(os.Args[2:])
	case "receipt-export":
		runReceiptExport(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "init":
		runInit(os.Args[2:])
	case "config":
		runConfig(os.Args[2:])
	case "completion":
		runCompletion(os.Args[2:])
	case "service":
		runService(os.Args[2:])
	case "worker":
		runWorker(os.Args[2:])
	case "memory":
		runMemory(os.Args[2:])
	case "analyze":
		runAnalyze(os.Args[2:])
	case "vault":
		runVault(os.Args[2:])
	case "internal":
		if len(os.Args) < 3 || os.Args[2] != "wrap-exec" {
			exitUsage("internal", "", "", fmt.Sprintf("%s: usage: g8s internal wrap-exec --out <path> -- <child argv>", AppName), "", false)
		}
		if err := runWrapExec(os.Args[2:]); err != nil {
			exitUsage("internal", "wrap-exec", "", err.Error(), "", false)
		}
	case "orchestrate":
		runOrchestrate(os.Args[2:])
	case "orchestrate-aic":
		runOrchestrateAIC(os.Args[2:])
	case "supervisor-metrics":
		runSupervisorMetrics(os.Args[2:])
	case "supervisor-metrics-update-false":
		runUpdateFalseEscalation(os.Args[2:])
	case "brief-issue":
		runBriefIssue(os.Args[2:])
	case "brief-consume":
		runBriefConsume(os.Args[2:])
	case "cleanup-worktrees":
		runCleanupWorktrees(os.Args[2:])
	case "cleanup":
		runCleanup(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "state":
		runState(os.Args[2:])
	case "migrate":
		runMigrate(os.Args[2:])
	case "converge":
		runConverge(os.Args[2:])
	case "sleep":
		runSleep(os.Args[2:])
	case "wake":
		runWake(os.Args[2:])
	case "providers":
		runProviders(os.Args[2:])
	case "llm-call":
		runLLMCall(os.Args[2:])
	case "autopilot":
		runAutopilot(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "verify":
		runVerify(os.Args[2:])
	case "feature":
		runFeature(os.Args[2:])
	case "lesson":
		runLesson(os.Args[2:])
	case "feedback":
		runFeedback(os.Args[2:])
	case "ladder":
		runLadder(os.Args[2:])
	case "fleet":
		runFleet(os.Args[2:])
	case "charter":
		runCharter(os.Args[2:])
	case "context-pack":
		runContextPack(os.Args[2:])
	case "qa-report":
		runQAReport(os.Args[2:])
	case "update", "uninstall":
		runLifecycle(command, os.Args[2:])
	case "claim":
		runClaim(os.Args[2:])
	case "f0":
		runF0(os.Args[2:])
	case "coordinator":
		runCoordinator(os.Args[2:])
	case "scorecard":
		runScorecard(os.Args[2:])
	case "audit":
		runAudit(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		exitUsage(command, "", "", fmt.Sprintf("Unknown command '%s'", command), "Run 'g8s help' for available commands", false)
	}
}

// pathFlags collects repeated flag values for paths and directories.
type pathFlags []string

func (p *pathFlags) String() string { return strings.Join(*p, ",") }
func (p *pathFlags) Set(v string) error {
	*p = append(*p, v)
	return nil
}

// databasePath resolves the shared SQLite database location. Operators may
// override it via G8S_DB; the default lives under canonical state directory per
// containment conventions (file permissions are enforced by each manager).
func databasePath() (string, error) {
	dbPath := pathutil.DefaultDatabasePath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return "", fmt.Errorf("create state directory: %w", err)
	}
	return dbPath, nil
}

// runRoles lists registered worker roles.
func runRoles(args []string) {
	fs := flag.NewFlagSet("roles", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		exitUsage("roles", "", *traceID, err.Error(), "", *jsonl)
	}
	var roles []harness.RoleProfile
	for _, name := range harness.RoleNames() {
		r, _ := harness.GetRole(name)
		roles = append(roles, r)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("roles", "roles", "", roles)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	var td pterm.TableData
	td = append(td, []string{"Name", "Purpose"})
	for _, r := range roles {
		td = append(td, []string{r.Name, r.Purpose})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()
}

// runPermissions lists registered permission profiles.
func runPermissions(args []string) {
	fs := flag.NewFlagSet("permissions", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		exitUsage("permissions", "", *traceID, err.Error(), "", *jsonl)
	}
	var perms []harness.PermissionProfile
	for _, name := range harness.PermissionNames() {
		p, _ := harness.GetPermission(name)
		perms = append(perms, p)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("permissions", "permissions", "", perms)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	var td pterm.TableData
	td = append(td, []string{"Name", "Description", "Mutation Allowed"})
	for _, p := range perms {
		td = append(td, []string{p.Name, p.Description, fmt.Sprintf("%t", p.MutationAllowed)})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()
}

// runProviders lists detected AI agent CLI providers and their availability status,
// or discovers live models when invoked as 'g8s providers discover'.
func runProviders(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "discover":
			runProvidersDiscover(args[1:])
			return
		case "scorecard":
			runProvidersScorecard(args[1:])
			return
		case "evaluate":
			runProvidersEvaluate(args[1:])
			return
		case "conformance":
			runProvidersConformance(args[1:])
			return
		}
	}

	fs := flag.NewFlagSet("providers", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		exitUsage("providers", "", *traceID, err.Error(), "", *jsonl)
	}

	reg := provider.NewRegistry()

	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			providersPath = filepath.Join(home, ".config", "g8s", "providers.json")
		}
	}
	if providersPath != "" {
		if _, statErr := os.Stat(providersPath); statErr == nil {
			if cfgFile, loadErr := config.Load(providersPath); loadErr == nil && cfgFile != nil {
				for _, entry := range cfgFile.Providers {
					if entry.Class == "api_call" {
						reg.Register(provider.NewHTTPProviderFromConfig(entry))
					}
				}
			}
		}
	}
	statuses := reg.List(context.Background())

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("providers_list", "providers", "", statuses)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("providers", "", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	var td pterm.TableData
	td = append(td, []string{"Name", "Status", "Binary", "Version / Reason"})
	for _, st := range statuses {
		detail := st.Version
		bin := st.BinaryPath
		if st.Status == "NO" {
			detail = st.Reason
			bin = "-"
		}
		td = append(td, []string{st.Name, st.Status, bin, detail})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()
}

func runProvidersDiscover(args []string) {
	fs := flag.NewFlagSet("providers discover", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	providerFlag := fs.String("provider", "", "provider name to discover models from (e.g. 9router)")
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("providers", "discover", *traceID, err.Error(), "usage: g8s providers discover [--provider <name>] [--json]", *jsonl)
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

	var targetEntries []config.ProviderEntry
	discoverCursorOnly := strings.EqualFold(*providerFlag, "cursor") || strings.EqualFold(*providerFlag, "cursor-agent")
	if *providerFlag != "" && !discoverCursorOnly {
		if cfgFile != nil {
			if p, ok := config.FindProvider(cfgFile, *providerFlag); ok && p.Class == "api_call" {
				targetEntries = append(targetEntries, *p)
			}
		}
		if len(targetEntries) == 0 {
			if strings.EqualFold(*providerFlag, "9router") {
				targetEntries = append(targetEntries, config.ProviderEntry{
					Class:               "api_call",
					Name:                "9router",
					Protocol:            "9router",
					BaseURL:             "http://localhost:20128/v1",
					BaseURLEnv:          "NINEROUTER_URL",
					AuthEnv:             "OPENAI_API_KEY",
					AllowUnlistedModels: true,
				})
			} else {
				exitUsage("providers", "discover", *traceID, fmt.Sprintf("provider %q not found or not an api_call provider", *providerFlag), "Declare provider in providers.json or use 9router / cursor", *jsonl)
			}
		}
	} else if !discoverCursorOnly {
		if cfgFile != nil {
			for _, p := range cfgFile.Providers {
				if p.Class == "api_call" {
					targetEntries = append(targetEntries, p)
				}
			}
		}
		if len(targetEntries) == 0 {
			targetEntries = append(targetEntries, config.ProviderEntry{
				Class:               "api_call",
				Name:                "9router",
				Protocol:            "9router",
				BaseURL:             "http://localhost:20128/v1",
				BaseURLEnv:          "NINEROUTER_URL",
				AuthEnv:             "OPENAI_API_KEY",
				AllowUnlistedModels: true,
			})
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type discoveryResult struct {
		Provider string   `json:"provider"`
		Models   []string `json:"models"`
		Error    string   `json:"error,omitempty"`
	}
	var results []discoveryResult

	if discoverCursorOnly {
		cp := provider.NewCursorProvider()
		models, err := cp.DiscoverModels(ctx)
		if err != nil {
			exitRuntime("providers", "discover", *traceID, cli.CodeRuntime, fmt.Errorf("provider cursor discover failed: %w", err), "", *jsonl)
		}
		results = append(results, discoveryResult{
			Provider: "cursor",
			Models:   models,
		})
	}

	for _, entry := range targetEntries {
		p := provider.NewHTTPProviderFromConfig(entry)
		models, err := p.DiscoverModels(ctx)
		res := discoveryResult{
			Provider: entry.Name,
			Models:   models,
		}
		if err != nil {
			res.Error = err.Error()
			if len(targetEntries) == 1 {
				exitRuntime("providers", "discover", *traceID, cli.CodeRuntime, fmt.Errorf("provider %s discover failed: %w", entry.Name, err), "", *jsonl)
			}
		}
		results = append(results, res)
	}

	if *jsonMode || *jsonl {
		var data any
		if len(results) == 1 {
			data = map[string]any{
				"provider": results[0].Provider,
				"models":   results[0].Models,
			}
		} else {
			data = map[string]any{
				"providers": results,
			}
		}
		env := cli.NewEnvelope("models_discovery", "providers", "discover", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("providers", "discover", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	for _, res := range results {
		if len(results) > 1 {
			fmt.Printf("[%s]\n", res.Provider)
		}
		if res.Error != "" {
			fmt.Printf("  Error: %s\n", res.Error)
			continue
		}
		for _, m := range res.Models {
			fmt.Println(m)
		}
	}
}

// runMCPServer serves the stdio JSON-RPC MCP surface until stdin closes.
func runMCPServer(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("mcp", "", "", cli.CodeIO, err, "", false)
	}

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("mcp", "", "", cli.CodeRuntime, err, "", false)
	}
	defer store.Close()

	receiptDbPath, err := receiptDatabasePath()
	if err != nil {
		exitRuntime("mcp", "", "", cli.CodeIO, err, "", false)
	}
	receipts, err := receipt.NewReceiptManager(receiptDbPath, nil)
	if err != nil {
		exitRuntime("mcp", "", "", cli.CodeRuntime, err, "", false)
	}
	defer receipts.Close()

	registry := provider.NewPoolRegistry(provider.DefaultConfigs(), nil, nil)
	providersPath := os.Getenv("G8S_PROVIDERS")
	if providersPath == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			providersPath = filepath.Join(home, ".config", "g8s", "providers.json")
		}
	}
	if providersPath != "" {
		if _, statErr := os.Stat(providersPath); statErr == nil {
			_ = registry.LoadProvidersJSON(providersPath)
		}
	}
	server := mcp.NewServer(os.Stdin, os.Stdout, store, receipts, registry)
	if err := server.ServeStdio(context.Background()); err != nil {
		exitRuntime("mcp", "", "", cli.CodeRuntime, err, "", false)
	}
}

// runGet prints the current durable view of one task.

func reportError(err error, w io.Writer) {
	pterm.Error.WithWriter(w).Println(fmt.Sprintf("%s: %v", AppName, err))
}

func printUsage() {
	fmt.Printf("%s (The Gatekeepers) - Zero-Trust Process & Capability Harness for AI Agents\n\n", AppName)
	fmt.Println("Usage:")
	fmt.Printf("  %s <command> [arguments]\n\n", AppName)
	fmt.Println("Commands:")
	for _, c := range cli.CanonicalCommandContracts() {
		if c.Hidden {
			continue
		}
		fmt.Printf("  %-12s %s\n", c.Name, c.Description)
	}
	fmt.Println("\nPlanned (post-MVP): run (sync dispatch)")
}

func hydrateCursorAPIKeyEnv() {
	if apiKey := provider.ResolveCursorAPIKey(); apiKey != "" {
		_ = os.Setenv("CURSOR_API_KEY", apiKey)
	}
}

// runWorker runs the supervisor loop claiming tasks from the queue,
// resolving provider command templates from providers.json when present
// (DELTA-10 phase-2 registry-to-worker bridge).
