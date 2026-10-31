package main

import (
	"context"
	"encoding/json"

	"flag"
	"fmt"

	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"

	"github.com/tamld/g8s/internal/analyzer"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/completion"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"

	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/pathutil"

	"github.com/tamld/g8s/internal/service"
	"github.com/tamld/g8s/internal/settings"
	"github.com/tamld/g8s/internal/vault"
	"github.com/tamld/g8s/internal/worker"
)

func runConfig(args []string) {
	if len(args) == 0 {
		exitUsage("config", "", "", fmt.Sprintf("%s: usage: g8s config <get|set|list|unset> [key] [value] [--json]", AppName), "", false)
	}

	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println("Usage: g8s config <get|set|list|unset> [key] [value] [--json]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  get    Get config key")
		fmt.Println("  set    Set config key value")
		fmt.Println("  list   List all configuration")
		fmt.Println("  unset  Unset config key")
		os.Exit(0)
	}

	fs := flag.NewFlagSet("config", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	scopeFlag := fs.String("scope", "", "installation and execution scope (user or system)")

	var nonFlags []string
	var flagArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else {
			nonFlags = append(nonFlags, a)
		}
	}

	if err := fs.Parse(flagArgs); err != nil {
		exitUsage("config", "", *traceID, err.Error(), "", *jsonl)
	}

	if len(nonFlags) == 0 {
		exitUsage("config", "", *traceID, fmt.Sprintf("%s: usage: g8s config <get|set|list|unset> [key] [value] [--json]", AppName), "", *jsonl)
	}

	subcmd := nonFlags[0]
	extraArgs := nonFlags[1:]

	mgr, err := settings.NewManager("")
	if err != nil {
		exitRuntime("config", subcmd, *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *scopeFlag != "" {
		if *scopeFlag != pathutil.ScopeUser && *scopeFlag != pathutil.ScopeSystem {
			exitUsage("config", "", *traceID, fmt.Sprintf("invalid scope %q (must be %q or %q)", *scopeFlag, pathutil.ScopeUser, pathutil.ScopeSystem), "", *jsonl)
		}
		if err := mgr.Set("scope", *scopeFlag); err != nil {
			exitRuntime("config", "scope", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
	}

	switch subcmd {
	case "list":
		all := mgr.List()
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("config", "config", "list", all)
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
			return
		}
		var td pterm.TableData
		td = append(td, []string{"Key", "Value", "Description"})
		for key, desc := range settings.AllowedConfigKeys {
			val, _ := mgr.Get(key)
			valStr := "<unset>"
			if val != nil {
				valStr = fmt.Sprintf("%v", val)
			}
			td = append(td, []string{key, valStr, desc})
		}
		pterm.DefaultTable.WithHasHeader().WithData(td).Render()

	case "get":
		if len(extraArgs) < 1 {
			exitUsage("config", "get", *traceID, fmt.Sprintf("%s: usage: g8s config get <key> [--json]", AppName), "Provide a configuration key", *jsonl)
		}
		key := extraArgs[0]
		val, ok := mgr.Get(key)
		if !ok || val == nil {
			if *jsonMode || *jsonl {
				exitRuntime("config", "get", *traceID, cli.CodeNotFound, fmt.Errorf("key %q is not set", key), "", *jsonl)
			} else {
				fmt.Fprintf(os.Stderr, "Key %q is not set\n", key)
				os.Exit(1)
			}
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("config", "config", "get", map[string]any{key: val})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Println(val)
		}

	case "set":
		if len(extraArgs) < 2 {
			exitUsage("config", "set", *traceID, fmt.Sprintf("%s: usage: g8s config set <key> <value>", AppName), "Provide both key and value", *jsonl)
		}
		key, value := extraArgs[0], extraArgs[1]
		err := mgr.Set(key, value)
		if err != nil {
			exitRuntime("config", "set", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("config", "config", "set", map[string]any{"key": key, "value": value})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			pterm.Success.Printf("Configured %s = %s\n", key, value)
		}

	case "unset":
		if len(extraArgs) < 1 {
			exitUsage("config", "unset", *traceID, fmt.Sprintf("%s: usage: g8s config unset <key>", AppName), "Provide a key to unset", *jsonl)
		}
		key := extraArgs[0]
		err := mgr.Unset(key)
		if err != nil {
			exitRuntime("config", "unset", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("config", "config", "unset", map[string]any{"key": key, "unset": true})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			pterm.Success.Printf("Unset %s\n", key)
		}

	default:
		exitUsage("config", subcmd, *traceID, fmt.Sprintf("Unknown config subcommand %q (supported: get, set, list, unset)", subcmd), "", *jsonl)
	}
}

// runCompletion emits shell autocompletion scripts.
func runCompletion(args []string) {
	if len(args) < 1 {
		exitUsage("completion", "", "", fmt.Sprintf("%s: usage: g8s completion <bash|zsh|fish>", AppName), "", false)
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Printf("%s: usage: g8s completion <bash|zsh|fish>\n", AppName)
		os.Exit(0)
	}
	fs := flag.NewFlagSet("completion", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	_ = fs.Parse(args[1:])
	shell := args[0]
	script, err := completion.Generate(shell)
	if err != nil {
		exitRuntime("completion", shell, *traceID, cli.CodeInvalid, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("completion", "completion", shell, map[string]string{"shell": shell, "script": script})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Print(script)
}

// runService handles OS background daemon management (macOS launchd & Linux systemd).
func runService(args []string) {
	if len(args) == 0 {
		exitUsage("service", "", "", "usage: g8s service <install|start|stop|status|uninstall>", "", false)
	}

	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println("Usage: g8s service <install|start|stop|status|uninstall>")
		fmt.Println("\nSubcommands:")
		fmt.Println("  install    Install service unit")
		fmt.Println("  start      Start service daemon")
		fmt.Println("  stop       Stop service daemon")
		fmt.Println("  status     Check service status")
		fmt.Println("  uninstall  Uninstall service unit")
		os.Exit(0)
	}

	subcmd := args[0]
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args[1:]); err != nil {
		exitUsage("service", subcmd, *traceID, err.Error(), "", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("service", subcmd, *traceID, cli.CodeIO, err, "", *jsonl)
	}

	exePath, err := os.Executable()
	if err != nil {
		exitRuntime("service", subcmd, *traceID, cli.CodeIO, err, "", *jsonl)
	}

	mgr, err := service.NewPlatformServiceManager(service.Config{
		BinaryPath:   exePath,
		DatabasePath: dbPath,
	}, nil, nil)
	if err != nil {
		exitRuntime("service", subcmd, *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	switch subcmd {
	case "install":
		if err := mgr.Install(); err != nil {
			exitRuntime("service", "install", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("service", "service", "install", map[string]any{"status": "installed"})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			pterm.Success.Println("g8s service unit installed successfully")
		}
	case "start":
		if err := mgr.Start(); err != nil {
			exitRuntime("service", "start", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("service", "service", "start", map[string]any{"status": "started"})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			pterm.Success.Println("g8s service daemon started")
		}
	case "stop":
		if err := mgr.Stop(); err != nil {
			exitRuntime("service", "stop", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("service", "service", "stop", map[string]any{"status": "stopped"})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			pterm.Success.Println("g8s service daemon stopped")
		}
	case "uninstall":
		if err := mgr.Uninstall(); err != nil {
			exitRuntime("service", "uninstall", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("service", "service", "uninstall", map[string]any{"status": "uninstalled"})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			pterm.Success.Println("g8s service daemon uninstalled")
		}
	case "status":
		status, err := mgr.Status()
		if err != nil {
			exitRuntime("service", "status", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("service_status", "service", "status", status)
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			out, _ := json.MarshalIndent(status, "", "  ")
			fmt.Println(string(out))
		}
	default:
		exitUsage("service", subcmd, *traceID, fmt.Sprintf("unknown service subcommand %q", subcmd), "", *jsonl)
	}
}

// runAnalyze computes Blast Radius Intelligence for a target file or symbol per DELTA-07.
func runAnalyze(args []string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	file := fs.String("file", "", "target file to analyze (required)")
	symbol := fs.String("symbol", "", "target symbol identifier (optional)")
	root := fs.String("root", "", "codebase root directory (defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		exitUsage("analyze", "", *traceID, err.Error(), "", *jsonl)
	}

	if *file == "" {
		if fs.NArg() > 0 {
			*file = fs.Arg(0)
		} else {
			exitUsage("analyze", "", *traceID, "usage: g8s analyze --file <path> [--symbol <name>] [--root <dir>]", "Provide a target file path", *jsonl)
		}
	}

	an, err := analyzer.NewAnalyzer(*root)
	if err != nil {
		exitRuntime("analyze", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	var report *analyzer.BlastRadiusReport
	if *symbol != "" {
		report, err = an.AnalyzeSymbolImpact(*file, *symbol)
	} else {
		report, err = an.AnalyzeFileImpact(*file)
	}
	if err != nil {
		exitRuntime("analyze", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	env := cli.NewEnvelope("blast_radius_report", "analyze", "", report)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("analyze", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

// runVault handles Tri-Anchor knowledge vault operations (store, query, list, get, delete).
func runVault(args []string) {
	if len(args) == 0 {
		exitUsage("vault", "", "", "usage: g8s vault <store|query|list|get|delete> [options]", "", false)
	}

	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println("Usage: g8s vault <store|query|list|get|delete> [options]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  store   Store a distillation record")
		fmt.Println("  query   Query vault records")
		fmt.Println("  list    List vault records")
		fmt.Println("  get     Get a vault record")
		fmt.Println("  delete  Delete a vault record")
		os.Exit(0)
	}

	subcmd := args[0]
	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("vault", subcmd, "", cli.CodeIO, err, "", false)
	}
	v, err := vault.NewVault(dbPath, nil)
	if err != nil {
		exitRuntime("vault", subcmd, "", cli.CodeRuntime, err, "", false)
	}
	defer v.Close()

	ctx := context.Background()

	switch subcmd {
	case "store":
		fs := flag.NewFlagSet("vault store", flag.ExitOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		id := fs.String("id", "", "distillation record id (e.g. DELTA-01-A)")
		title := fs.String("title", "", "record title")
		milestone := fs.String("milestone", "v0.3.0", "target milestone")
		status := fs.String("status", "APPLIED", "record status (PROPOSED, ACCEPTED, APPLIED, DEPRECATED)")
		pkg := fs.String("package", "", "target package name")
		file := fs.String("file", "", "target source file path")
		symbol := fs.String("symbol", "", "target code symbol")
		problem := fs.String("problem", "", "causality problem statement")
		tradeOff := fs.String("trade-off", "", "causality architectural trade-off")
		rootCause := fs.String("root-cause", "", "causality root cause")
		testFile := fs.String("test-file", "", "forensic verification test file")
		testCase := fs.String("test-case", "", "forensic verification test case")
		filePath := fs.String("from-file", "", "load complete record JSON from file")
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("vault", "store", *traceID, err.Error(), "", *jsonl)
		}

		var rec vault.DistillationRecord
		if *filePath != "" {
			data, err := os.ReadFile(*filePath)
			if err != nil {
				exitRuntime("vault", "store", *traceID, cli.CodeIO, err, "Failed to read file", *jsonl)
			}
			if err := json.Unmarshal(data, &rec); err != nil {
				exitRuntime("vault", "store", *traceID, cli.CodeInvalid, err, "Invalid record JSON", *jsonl)
			}
		} else {
			rec = vault.DistillationRecord{
				ID:        *id,
				Title:     *title,
				Milestone: *milestone,
				Status:    *status,
				Causality: vault.CausalityAnchor{
					Problem:   *problem,
					TradeOff:  *tradeOff,
					RootCause: *rootCause,
				},
				SpatialCoordinates: vault.SpatialAnchor{
					Package: *pkg,
					File:    *file,
					Symbol:  *symbol,
				},
				ForensicVerification: vault.ForensicAnchor{
					TestFile: *testFile,
					TestCase: *testCase,
				},
			}
		}

		stored, err := v.Store(ctx, rec)
		if err != nil {
			exitRuntime("vault", "store", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("vault_record", "vault", "store", stored)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("vault", "store", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "query":
		fs := flag.NewFlagSet("vault query", flag.ExitOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		limit := fs.Int("limit", 10, "maximum number of results")
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("vault", "query", *traceID, err.Error(), "", *jsonl)
		}

		if fs.NArg() == 0 {
			exitUsage("vault", "query", *traceID, "usage: g8s vault query <search-term> [--limit 10]", "Provide search query", *jsonl)
		}
		q := strings.Join(fs.Args(), " ")
		results, err := v.Query(ctx, q, *limit)
		if err != nil {
			exitRuntime("vault", "query", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("vault_records", "vault", "query", results)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("vault", "query", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "get":
		fs := flag.NewFlagSet("vault get", flag.ExitOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("vault", "get", *traceID, err.Error(), "", *jsonl)
		}
		if fs.NArg() < 1 {
			exitUsage("vault", "get", *traceID, "usage: g8s vault get <record-id>", "Provide a record ID", *jsonl)
		}
		recID := fs.Arg(0)
		rec, err := v.Get(ctx, recID)
		if err != nil {
			exitRuntime("vault", "get", *traceID, cli.CodeNotFound, err, "Record not found", *jsonl)
		}
		env := cli.NewEnvelope("vault_record", "vault", "get", rec)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("vault", "get", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "list":
		fs := flag.NewFlagSet("vault list", flag.ExitOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		milestone := fs.String("milestone", "", "filter by milestone")
		status := fs.String("status", "", "filter by status")
		pkg := fs.String("package", "", "filter by package")
		limit := fs.Int("limit", 50, "maximum records to return")
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("vault", "list", *traceID, err.Error(), "", *jsonl)
		}

		filter := vault.VaultFilter{Limit: *limit}
		if *milestone != "" {
			filter.Milestone = milestone
		}
		if *status != "" {
			filter.Status = status
		}
		if *pkg != "" {
			filter.Package = pkg
		}

		list, err := v.List(ctx, filter)
		if err != nil {
			exitRuntime("vault", "list", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("vault_records", "vault", "list", list)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("vault", "list", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "delete":
		fs := flag.NewFlagSet("vault delete", flag.ExitOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		idFlag := fs.String("id", "", "record ID to delete")
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("vault", "delete", *traceID, err.Error(), "", *jsonl)
		}
		recID := *idFlag
		if recID == "" && fs.NArg() > 0 {
			recID = fs.Arg(0)
		}
		if recID == "" {
			exitUsage("vault", "delete", *traceID, "usage: g8s vault delete <record-id> or --id <id>", "Provide a record ID to delete", *jsonl)
		}
		if err := v.Delete(ctx, recID); err != nil {
			exitRuntime("vault", "delete", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("vault_delete", "vault", "delete", map[string]any{"id": recID, "deleted": true})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Printf("Record %q deleted\n", recID)
		}

	default:
		exitUsage("vault", subcmd, "", fmt.Sprintf("unknown vault subcommand %q", subcmd), "Supported: store, query, get, list, delete", false)
	}
}

// reportError renders a failure through pterm on the provided writer so tests
// can assert where diagnostics land.

func runWorker(args []string) {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	once := fs.Bool("once", true, "claim and execute a single task, then exit")
	model := fs.String("model", "", "restrict claims to tasks targeting this model (serial and concurrent)")
	provider := fs.String("provider", "", "restrict claims to tasks targeting this provider (serial and concurrent)")
	lease := fs.Int("lease", 60, "lease duration seconds")
	concurrency := fs.Int("concurrency", 1, "drain with up to N simultaneous attempts; N>1 requires a git checkout (per-attempt worktree isolation) and --once=false")
	if err := fs.Parse(args); err != nil {
		exitUsage("worker", "", *traceID, err.Error(), "", *jsonl)
	}
	if *concurrency < 1 {
		exitUsage("worker", "concurrency", *traceID, "--concurrency must be >= 1", "", *jsonl)
	}
	if *concurrency > 1 && *once {
		exitUsage("worker", "concurrency", *traceID,
			"--concurrency N>1 requires --once=false",
			"usage: g8s worker --once=false --concurrency N", *jsonl)
	}
	dbPath, dbErr := databasePath()
	if dbErr != nil {
		exitRuntime("worker", "", *traceID, cli.CodeIO, dbErr, "", *jsonl)
	}

	modelTemplates := map[string][]string{}
	providerTemplates := map[string][]string{}
	var platformDispatchNames []string
	apiCallNames := map[string]bool{}

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
				exitRuntime("worker", "", *traceID, cli.CodeRuntime, loadErr, "", *jsonl)
			}
			for _, entry := range cfgFile.Providers {
				switch entry.Class {
				case "platform_dispatch":
					if entry.Name != "" {
						platformDispatchNames = append(platformDispatchNames, entry.Name)
						if len(entry.Args) > 0 {
							providerTemplates[entry.Name] = entry.Args
						}
					}
					if len(entry.Args) > 0 {
						for _, m := range entry.Models {
							modelTemplates[m.ID] = entry.Args
						}
					}
				case "api_call":
					if entry.Name != "" {
						if entry.DisableQueueExec {
							apiCallNames[entry.Name] = true
						} else if len(entry.Args) > 0 {
							providerTemplates[entry.Name] = entry.Args
							for _, m := range entry.Models {
								modelTemplates[m.ID] = entry.Args
							}
						} else if selfExe, exeErr := os.Executable(); exeErr == nil {
							tmpl := []string{
								selfExe, "llm-call",
								"--worker-envelope",
								"--provider", entry.Name,
								"--model", "{model}",
								"--timeout", "{timeout}",
								"--prompt", "{prompt}",
							}
							providerTemplates[entry.Name] = tmpl
							for _, m := range entry.Models {
								if !strings.Contains(m.ID, "*") {
									modelTemplates[m.ID] = tmpl
								}
							}
						} else {
							apiCallNames[entry.Name] = true
						}
					}
				}
			}
		}
	}
	for _, builtinName := range []string{"agy", "claude", "codex", "opencode", "commandcode", "qwen", "cursor"} {
		found := false
		for _, existing := range platformDispatchNames {
			if existing == builtinName {
				found = true
				break
			}
		}
		if !found && !apiCallNames[builtinName] {
			platformDispatchNames = append(platformDispatchNames, builtinName)
		}
	}
	if os.Getenv("CURSOR_API_KEY") == "" {
		hydrateCursorAPIKeyEnv()
	}

	store, cpErr := controlplane.NewControlPlane(dbPath, nil)
	if cpErr != nil {
		exitRuntime("worker", "", *traceID, cli.CodeRuntime, cpErr, "", *jsonl)
	}
	defer store.Close()

	// #427 PR-2: workspace_write attempts get per-attempt worktree
	// isolation when running inside a git checkout (concurrent code-writing
	// attempts would otherwise clobber each other in the shared tree).
	// Non-git cwd / pool failure → legacy shared-checkout behavior.
	opts := []worker.Option{
		worker.WithProviderCommandResolver(worker.NewProviderCommandResolver(worker.ProviderResolverOptions{
			ProviderTemplates:     providerTemplates,
			ModelTemplates:        modelTemplates,
			PlatformDispatchNames: platformDispatchNames,
			APICallNames:          apiCallNames,
		})),
	}
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if pool, poolErr := orchestrator.NewPool(orchestrator.PoolOptions{Repo: cwd, Prefix: "wt"}); poolErr == nil {
			opts = append(opts, worker.WithWorktreeIsolator(workerPoolIsolator{pool: pool}))
		}
	}

	sup := worker.NewSupervisor(store, filepath.Join(filepath.Dir(dbPath), "runs"), opts...)

	ctx := context.Background()
	if *concurrency > 1 {
		// #394 PR-B: concurrent drain — hard-gated on worktree isolation,
		// session-registered for provenance and cleanup.
		runWorkerConcurrentDrain(ctx, sup, store, *actor, *provider, *model, *lease, *concurrency, *jsonMode, *jsonl, *traceID)
		return
	}
	for {
		task, err := sup.RunOnce(ctx, worker.RunOptions{
			WorkerID:     *actor,
			LeaseSeconds: *lease,
			Provider:     *provider,
			Model:        *model,
		})
		if err != nil {
			exitRuntime("worker", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		if task == nil {
			if *jsonMode || *jsonl {
				env := cli.NewEnvelope("worker_status", "worker", "", map[string]any{"status": "drained"})
				env.TraceID = *traceID
				_ = cli.WriteResponse(os.Stdout, env, *jsonl)
			} else {
				fmt.Println("queue drained")
			}
			return
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("worker_task", "worker", "", map[string]any{"task_id": task.TaskID, "state": task.State})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Printf("%s %s\n", task.TaskID, task.State)
		}
		isFailure := false
		if len(task.Result) > 0 {
			var resultMap map[string]any
			if json.Unmarshal(task.Result, &resultMap) == nil {
				// Check for error envelope or ok=false
				if kind, _ := resultMap["kind"].(string); kind == "error" {
					isFailure = true
				} else if ok, _ := resultMap["ok"].(bool); !ok {
					isFailure = true
				}
			}
		}
		recordWorkerTaskTelemetry(store, task, isFailure)
		if *once {
			if isFailure {
				os.Exit(1)
			}
			return
		}
	}
}

// runState handles state introspection and event log replay per DEBT-31.
func runState(args []string) {
	if len(args) == 0 {
		exitUsage("state", "", "", "usage: g8s state <show|replay> <id> [flags]", "Use 'g8s state show <id>' or 'g8s state replay <id>'", false)
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println("Usage: g8s state <show|replay> <id> [flags]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  show    Show state history for an ID")
		fmt.Println("  replay  Replay state history for an ID")
		os.Exit(0)
	}
	sub := args[0]
	switch sub {
	case "show":
		runStateShow(args[1:])
	case "replay":
		runStateReplay(args[1:])
	default:
		exitUsage("state", sub, "", fmt.Sprintf("unknown state subcommand %q (valid: show, replay)", sub), "", false)
	}
}

func runStateShow(args []string) {
	fs := flag.NewFlagSet("state show", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	dbFlag := fs.String("db", "", "path to control-plane database")
	limitFlag := fs.Int("limit", 20, "maximum number of events to show (default 20)")
	if err := fs.Parse(args); err != nil {
		exitUsage("state", "show", *traceID, err.Error(), "", *jsonl)
	}
	if fs.NArg() == 0 {
		exitUsage("state", "show", *traceID, "usage: g8s state show <id> [--limit 20] [--json]", "Provide a subject or task ID", *jsonl)
	}
	id := fs.Arg(0)

	dbPath := *dbFlag
	if dbPath == "" {
		var err error
		dbPath, err = databasePath()
		if err != nil {
			exitRuntime("state", "show", *traceID, cli.CodeIO, err, "", *jsonl)
		}
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("state", "show", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	ctx := context.Background()
	events, err := store.ShowStateEvents(ctx, id, *limitFlag)
	if err != nil {
		exitRuntime("state", "show", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	// Resolve current state
	currentState := ""
	subjectType := ""
	if task, tErr := store.GetTask(ctx, id); tErr == nil && task != nil {
		currentState = task.State
		subjectType = "task"
	} else if brief, bErr := store.GetBrief(ctx, id); bErr == nil && brief.ID != "" {
		currentState = brief.Status
		subjectType = "brief"
	} else if sup, sErr := store.GetSupervisorTask(ctx, id); sErr == nil && sup.ID != "" {
		currentState = sup.State
		subjectType = "orchestrator"
	} else if len(events) > 0 {
		currentState = string(events[len(events)-1].ToState)
		subjectType = string(events[len(events)-1].Subject)
	} else {
		exitRuntime("state", "show", *traceID, cli.CodeNotFound, fmt.Errorf("subject %q not found", id), "Ensure the ID exists in the database", *jsonl)
	}

	data := map[string]any{
		"id":            id,
		"subject":       subjectType,
		"current_state": currentState,
		"events":        events,
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("state", "state", "show", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("state", "show", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	pterm.DefaultHeader.WithFullWidth().Println(fmt.Sprintf("State: %s (Subject: %s, ID: %s)", currentState, subjectType, id))
	if len(events) > 0 {
		var td pterm.TableData
		td = append(td, []string{"Time", "From", "To", "Event", "Actor", "Reason"})
		for _, ev := range events {
			td = append(td, []string{
				ev.Timestamp.Format(time.RFC3339),
				string(ev.FromState),
				string(ev.ToState),
				string(ev.Event),
				ev.Actor,
				ev.Reason,
			})
		}
		pterm.DefaultTable.WithHasHeader().WithData(td).Render()
	} else {
		fmt.Println("No transition events recorded in event_log.")
	}
}

func runStateReplay(args []string) {
	fs := flag.NewFlagSet("state replay", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	_ = jsonMode
	dbFlag := fs.String("db", "", "path to control-plane database")
	if err := fs.Parse(args); err != nil {
		exitUsage("state", "replay", *traceID, err.Error(), "", *jsonl)
	}
	if fs.NArg() == 0 {
		exitUsage("state", "replay", *traceID, "usage: g8s state replay <id>", "Provide a subject or task ID", *jsonl)
	}
	id := fs.Arg(0)

	dbPath := *dbFlag
	if dbPath == "" {
		var err error
		dbPath, err = databasePath()
		if err != nil {
			exitRuntime("state", "replay", *traceID, cli.CodeIO, err, "", *jsonl)
		}
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("state", "replay", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	ctx := context.Background()
	events, err := store.ReplayStateEvents(ctx, id)
	if err != nil {
		exitRuntime("state", "replay", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	for _, ev := range events {
		line, mErr := json.Marshal(ev)
		if mErr != nil {
			continue
		}
		fmt.Println(string(line))
	}
}

// workerPoolIsolator adapts the orchestrator worktree pool to the worker
// loop's WorktreeIsolator seam (#427 PR-2): each workspace_write attempt
// gets a private worktree; release(keep=true) preserves the deliverable
// (worktree dir + branch) for inspection/merge.
type workerPoolIsolator struct {
	pool *orchestrator.Pool
}

func (p workerPoolIsolator) AcquireWorktree(ctx context.Context, taskID string) (string, func(keep bool), error) {
	wt, err := p.pool.Acquire(ctx, taskID)
	if err != nil {
		return "", nil, err
	}
	return wt.Path, func(keep bool) {
		if rerr := p.pool.Release(context.Background(), wt, keep); rerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] worker: release worktree: %v\n", rerr)
		}
	}, nil
}

func (p workerPoolIsolator) AcquireRepoWorktree(ctx context.Context, repoDir, taskID string) (string, func(keep bool), error) {
	if strings.TrimSpace(repoDir) != "" {
		if targetPool, err := orchestrator.NewPool(orchestrator.PoolOptions{Repo: repoDir, Prefix: "wt"}); err == nil {
			wt, acqErr := targetPool.Acquire(ctx, taskID)
			if acqErr == nil {
				return wt.Path, func(keep bool) {
					if rerr := targetPool.Release(context.Background(), wt, keep); rerr != nil {
						fmt.Fprintf(os.Stderr, "[warn] worker: release worktree: %v\n", rerr)
					}
				}, nil
			}
		}
	}
	return p.AcquireWorktree(ctx, taskID)
}
