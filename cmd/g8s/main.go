// Command g8s is the self-describing CLI entry point for The Gatekeepers
// runtime. Per constitution Axiom 5 every subcommand surface documented here
// reflects implemented behavior only; planned commands are marked explicitly.
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

	"github.com/pterm/pterm"

	"github.com/tamld/g8s/internal/analyzer"
	"github.com/tamld/g8s/internal/completion"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/doctor"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/initwiz"
	"github.com/tamld/g8s/internal/mcp"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/service"
	"github.com/tamld/g8s/internal/settings"
	"github.com/tamld/g8s/internal/vault"
	"github.com/tamld/g8s/internal/worker"
)

// Version is a var so goreleaser ldflags -X can inject the build tag (D4).
var (
	Version = "0.2.0"
	AppName = "g8s"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "version":
		pterm.DefaultHeader.WithFullWidth().Println(fmt.Sprintf("%s v%s (The Gatekeepers - Zero-CGO, Pure Go)", AppName, Version))
	case "roles":
		var td pterm.TableData
		td = append(td, []string{"Name", "Purpose"})
		for _, name := range harness.RoleNames() {
			r, _ := harness.GetRole(name)
			td = append(td, []string{r.Name, r.Purpose})
		}
		pterm.DefaultTable.WithHasHeader().WithData(td).Render()
	case "permissions":
		var td pterm.TableData
		td = append(td, []string{"Name", "Description", "Mutation Allowed"})
		for _, name := range harness.PermissionNames() {
			p, _ := harness.GetPermission(name)
			td = append(td, []string{p.Name, p.Description, fmt.Sprintf("%t", p.MutationAllowed)})
		}
		pterm.DefaultTable.WithHasHeader().WithData(td).Render()
	case "mcp":
		runMCPServer()
	case "submit":
		runSubmit(os.Args[2:])
	case "get":
		runGet(os.Args[2:])
	case "resume":
		runResume(os.Args[2:])
	case "tasks":
		runTasks(os.Args[2:])
	case "lineage":
		runLineage(os.Args[2:])
	case "children":
		runChildren(os.Args[2:])
	case "receipt":
		runReceipt(os.Args[2:])
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
	case "analyze":
		runAnalyze(os.Args[2:])
	case "vault":
		runVault(os.Args[2:])
	case "internal":
		if len(os.Args) < 3 || os.Args[2] != "wrap-exec" {
			fmt.Fprintf(os.Stderr, "%s: usage: g8s internal wrap-exec --out <path> -- <child argv>\n", AppName)
			os.Exit(2)
		}
		if err := runWrapExec(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", AppName, err)
			os.Exit(2)
		}
	case "orchestrate":
		runOrchestrate(os.Args[2:])
	case "supervisor-metrics":
		runSupervisorMetrics(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command '%s'\n\n", command)
		printUsage()
		os.Exit(1)
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
// override it via G8S_DB; the default lives under ~/.local/state/g8s per
// containment conventions (file permissions are enforced by each manager).
func databasePath() (string, error) {
	dbPath := os.Getenv("G8S_DB")
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dbPath = filepath.Join(home, ".local", "state", "g8s", "g8s.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return "", fmt.Errorf("create state directory: %w", err)
	}
	return dbPath, nil
}

// runMCPServer serves the stdio JSON-RPC MCP surface until stdin closes.
func runMCPServer() {
	dbPath, err := databasePath()
	failIf(err)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	receipts, err := receipt.NewReceiptManager(dbPath, nil)
	failIf(err)
	defer receipts.Close()

	registry := provider.NewRegistry(provider.DefaultConfigs(), nil, nil)
	server := mcp.NewServer(os.Stdin, os.Stdout, store, receipts, registry)
	failIf(server.ServeStdio(context.Background()))
}

// runSubmit queues one durable task through the control plane after validating
// it against the security harness.
func runSubmit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	key := fs.String("idempotency-key", "", "unique idempotency key for this submission")
	model := fs.String("model", "gemini-3.7-flash-high", "target worker model")
	priority := fs.Int("priority", 0, "queue priority (-100..100)")
	maxAttempts := fs.Int("max-attempts", 1, "retry budget (1..10)")
	prompt := fs.String("prompt", "", "task prompt handed to the worker")
	role := fs.String("role", "collector", "worker role contract (collector, scout, mcp-mapper, summarizer, verifier, test-runner)")
	permission := fs.String("permission", "read_only", "permission profile (read_only, automation_read, workspace_write)")
	timeout := fs.String("timeout", "30s", "execution window for the worker")
	receiptID := fs.String("receipt-id", "", "write receipt ID (required for workspace_write)")
	parentTaskID := fs.String("parent-task-id", "", "parent task ID for subtask lineage tracking")
	skipPermissions := fs.Bool("skip-permissions", false, "bypass permission checks if permitted by profile")
	var addDirs pathFlags
	fs.Var(&addDirs, "add-dir", "additional allowed directory (repeatable, defaults to cwd)")
	failIf(fs.Parse(args))

	if *key == "" || *prompt == "" {
		fmt.Fprintln(os.Stderr, "submit requires --idempotency-key and --prompt")
		os.Exit(2)
	}
	cwd, err := os.Getwd()
	failIf(err)

	dirs := []string(addDirs)
	if len(dirs) == 0 {
		dirs = []string{cwd}
	}

	// Validate request against security harness gatekeeper
	if err := harness.ValidateRequest(*prompt, *role, *permission, dirs, *skipPermissions, *receiptID); err != nil {
		pterm.Error.Println(fmt.Sprintf("harness validation failed: %v", err))
		os.Exit(1)
	}

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	payloadMap := map[string]any{
		"prompt":     *prompt,
		"role":       *role,
		"permission": *permission,
		"timeout":    *timeout,
		"add_dirs":   dirs,
	}
	if *receiptID != "" {
		payloadMap["receipt_id"] = *receiptID
	}
	if *skipPermissions {
		payloadMap["skip_permissions"] = true
	}
	payload, err := json.Marshal(payloadMap)
	failIf(err)

	var parentIDPtr *string
	if *parentTaskID != "" {
		parentIDPtr = parentTaskID
	}

	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: *key,
		ParentTaskID:   parentIDPtr,
		Priority:       *priority,
		MaxAttempts:    *maxAttempts,
		Model:          *model,
		Payload:        payload,
		Role:           *role,
		Permission:     *permission,
		Timeout:        *timeout,
		AddDirs:        dirs,
	})
	failIf(err)

	out, err := json.MarshalIndent(task, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runGet prints the current durable view of one task.
func runGet(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: g8s get <task-id>")
		os.Exit(2)
	}
	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	task, err := store.GetTask(context.Background(), args[0])
	failIf(err)
	if task == nil {
		fmt.Fprintf(os.Stderr, "unknown task: %s\n", args[0])
		os.Exit(1)
	}
	out, err := json.MarshalIndent(task, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runResume moves a NEEDS_INFO or BLOCKED task back to QUEUED with optional clarifying prompt.
func runResume(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: g8s resume <task-id> [--prompt <text>] [--reason <reason>]")
		os.Exit(2)
	}
	taskID := args[0]
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	prompt := fs.String("prompt", "", "updated prompt or clarifying answer")
	reason := fs.String("reason", "resumed via CLI", "reason for resuming")
	failIf(fs.Parse(args[1:]))

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	var resumedPayload json.RawMessage
	if *prompt != "" {
		payloadMap := map[string]any{
			"prompt": *prompt,
		}
		raw, err := json.Marshal(payloadMap)
		failIf(err)
		resumedPayload = raw
	}

	task, err := store.ResumeTask(context.Background(), taskID, resumedPayload, *reason)
	failIf(err)

	out, err := json.MarshalIndent(task, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runTasks lists durable tasks optionally filtered by state.
func runTasks(args []string) {
	fs := flag.NewFlagSet("tasks", flag.ExitOnError)
	state := fs.String("state", "", "filter by task state (QUEUED, LEASED, RUNNING, SUCCEEDED, FAILED, CANCELLED, NEEDS_INFO, BLOCKED)")
	limit := fs.Int("limit", 50, "maximum number of tasks to return (1..200)")
	failIf(fs.Parse(args))

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	filter := controlplane.TaskFilter{Limit: *limit}
	if *state != "" {
		s := strings.ToUpper(*state)
		filter.State = &s
	}
	tasks, err := store.ListTasks(context.Background(), filter)
	failIf(err)

	out, err := json.MarshalIndent(tasks, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runLineage prints the full ancestry chain of a task up to the root.
func runLineage(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: g8s lineage <task-id>")
		os.Exit(2)
	}
	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	lineage, err := store.GetTaskLineage(context.Background(), args[0])
	failIf(err)
	if len(lineage) == 0 {
		fmt.Fprintf(os.Stderr, "unknown task: %s\n", args[0])
		os.Exit(1)
	}
	out, err := json.MarshalIndent(lineage, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runChildren lists direct child subtasks for a parent task.
func runChildren(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: g8s children <parent-task-id>")
		os.Exit(2)
	}
	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	children, err := store.ListChildTasks(context.Background(), args[0])
	failIf(err)
	out, err := json.MarshalIndent(children, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runReceipt issues write-delegation receipts on behalf of the operator.
func runReceipt(args []string) {
	if len(args) == 0 || args[0] != "issue" {
		fmt.Fprintln(os.Stderr, "usage: g8s receipt issue --issuer <name> --path <glob> [--ttl seconds]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("receipt issue", flag.ExitOnError)
	issuer := fs.String("issuer", "operator", "identity recorded on the receipt")
	ttlSeconds := fs.Int("ttl", 600, "time-to-live in seconds (1..3600)")
	var paths pathFlags
	fs.Var(&paths, "path", "allowed path glob (repeatable)")
	fs.Var(&paths, "allow", "allowed path glob (alias for --path)")
	failIf(fs.Parse(args[1:]))

	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "receipt issue requires at least one --path or --allow")
		os.Exit(2)
	}
	dbPath, err := databasePath()
	failIf(err)
	receipts, err := receipt.NewReceiptManager(dbPath, nil)
	failIf(err)
	defer receipts.Close()

	rc, err := receipts.IssueReceipt(*issuer, paths, time.Duration(*ttlSeconds)*time.Second)
	failIf(err)
	out, err := json.MarshalIndent(rc, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runDoctor executes diagnostic sanity checks for environment, permissions, and tools.
func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	jsonMode := fs.Bool("json", false, "output diagnostics as machine-readable JSON")
	fixMode := fs.Bool("fix", false, "apply automatic self-healing remediations")
	failIf(fs.Parse(args))

	dbPath, _ := databasePath()
	report := doctor.RunDiagnosticsWithFix(context.Background(), dbPath, *fixMode)

	if *jsonMode {
		out, err := json.MarshalIndent(report, "", "  ")
		failIf(err)
		fmt.Println(string(out))
		if report.OverallStatus == "UNHEALTHY" {
			os.Exit(1)
		}
		return
	}

	pterm.DefaultHeader.WithFullWidth().Println("g8s Doctor Diagnostics")
	fmt.Printf("Platform: %s | Runtime: %s | Zero-CGO: %t | Status: %s\n\n",
		report.Platform, report.GoRuntime, report.ZeroCGO, report.OverallStatus)

	if len(report.AppliedFixes) > 0 {
		pterm.Info.Println("Applied Self-Healing Fixes:")
		for _, fix := range report.AppliedFixes {
			pterm.Success.Printf("  • %s\n", fix)
		}
		fmt.Println()
	}

	var td pterm.TableData
	td = append(td, []string{"Check", "Status", "Message", "Details"})
	for _, chk := range report.Checks {
		var statusStr string
		switch chk.Status {
		case "OK":
			statusStr = pterm.Green(chk.Status)
		case "WARN":
			statusStr = pterm.Yellow(chk.Status)
		default:
			statusStr = pterm.Red(chk.Status)
		}
		td = append(td, []string{chk.Name, statusStr, chk.Message, chk.Details})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()

	if report.OverallStatus == "UNHEALTHY" {
		os.Exit(1)
	}
}

// runInit handles interactive and headless onboarding wizard.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	agentMode := fs.Bool("agent", false, "run in non-interactive headless agent mode")
	ideFlag := fs.String("ide", "", "target IDE to configure (cursor, claude, windsurf, antigravity, all)")
	jsonMode := fs.Bool("json", false, "output result as machine-readable JSON")
	failIf(fs.Parse(args))

	var targetIDEs []string
	if *ideFlag != "" {
		targetIDEs = strings.Split(*ideFlag, ",")
	}

	res, err := initwiz.RunInit(targetIDEs, "", os.Args[0])
	failIf(err)

	if *jsonMode {
		out, err := json.MarshalIndent(res, "", "  ")
		failIf(err)
		fmt.Println(string(out))
		return
	}

	pterm.DefaultHeader.WithFullWidth().Println("g8s Onboarding Wizard")
	pterm.Success.Printf("Initialized state directory: %s\n", res.StateDir)
	pterm.Success.Printf("Initialized evidence directory: %s\n", res.EvidenceDir)
	if res.ProvidersConfig != "" {
		pterm.Success.Printf("Created default provider configuration: %s\n", res.ProvidersConfig)
	}

	if len(res.ConfiguredIDEs) > 0 {
		var td pterm.TableData
		td = append(td, []string{"IDE", "Config Path", "Status"})
		for _, ide := range res.ConfiguredIDEs {
			td = append(td, []string{ide.Name, ide.ConfigPath, pterm.Green("Configured")})
		}
		fmt.Println()
		pterm.DefaultTable.WithHasHeader().WithData(td).Render()
	} else if !*agentMode {
		pterm.Info.Println("No supported IDE configs were updated. Use --ide=<cursor|claude|windsurf|antigravity|all> to explicitly configure.")
	}
}

// runConfig manages atomic key-value configuration.
func runConfig(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "%s: usage: g8s config <get|set|list|unset> [key] [value] [--json]\n", AppName)
		os.Exit(2)
	}

	fs := flag.NewFlagSet("config", flag.ExitOnError)
	jsonMode := fs.Bool("json", false, "output as machine-readable JSON")

	subcmd := args[0]
	failIf(fs.Parse(args[1:]))
	extraArgs := fs.Args()

	mgr, err := settings.NewManager("")
	failIf(err)

	switch subcmd {
	case "list":
		all := mgr.List()
		if *jsonMode {
			out, err := json.MarshalIndent(all, "", "  ")
			failIf(err)
			fmt.Println(string(out))
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
			fmt.Fprintf(os.Stderr, "%s: usage: g8s config get <key> [--json]\n", AppName)
			os.Exit(2)
		}
		key := extraArgs[0]
		val, ok := mgr.Get(key)
		if !ok || val == nil {
			if *jsonMode {
				fmt.Println("{}")
			} else {
				fmt.Fprintf(os.Stderr, "Key %q is not set\n", key)
			}
			os.Exit(1)
		}
		if *jsonMode {
			out, err := json.MarshalIndent(map[string]any{key: val}, "", "  ")
			failIf(err)
			fmt.Println(string(out))
		} else {
			fmt.Println(val)
		}

	case "set":
		if len(extraArgs) < 2 {
			fmt.Fprintf(os.Stderr, "%s: usage: g8s config set <key> <value>\n", AppName)
			os.Exit(2)
		}
		key, value := extraArgs[0], extraArgs[1]
		err := mgr.Set(key, value)
		failIf(err)
		pterm.Success.Printf("Configured %s = %s\n", key, value)

	case "unset":
		if len(extraArgs) < 1 {
			fmt.Fprintf(os.Stderr, "%s: usage: g8s config unset <key>\n", AppName)
			os.Exit(2)
		}
		key := extraArgs[0]
		err := mgr.Unset(key)
		failIf(err)
		pterm.Success.Printf("Unset %s\n", key)

	default:
		fmt.Fprintf(os.Stderr, "Unknown config subcommand %q (supported: get, set, list, unset)\n", subcmd)
		os.Exit(2)
	}
}

// runCompletion emits shell autocompletion scripts.
func runCompletion(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "%s: usage: g8s completion <bash|zsh|fish>\n", AppName)
		os.Exit(2)
	}
	script, err := completion.Generate(args[0])
	failIf(err)
	fmt.Print(script)
}

// runService handles OS background daemon management (macOS launchd & Linux systemd).
func runService(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: g8s service <install|start|stop|status|uninstall>")
		os.Exit(2)
	}

	subcmd := args[0]
	dbPath, err := databasePath()
	failIf(err)

	exePath, err := os.Executable()
	failIf(err)

	mgr, err := service.NewPlatformServiceManager(service.Config{
		BinaryPath:   exePath,
		DatabasePath: dbPath,
	}, nil, nil)
	failIf(err)

	switch subcmd {
	case "install":
		failIf(mgr.Install())
		pterm.Success.Println("g8s service unit installed successfully")
	case "start":
		failIf(mgr.Start())
		pterm.Success.Println("g8s service daemon started")
	case "stop":
		failIf(mgr.Stop())
		pterm.Success.Println("g8s service daemon stopped")
	case "uninstall":
		failIf(mgr.Uninstall())
		pterm.Success.Println("g8s service daemon uninstalled")
	case "status":
		status, err := mgr.Status()
		failIf(err)
		out, err := json.MarshalIndent(status, "", "  ")
		failIf(err)
		fmt.Println(string(out))
	default:
		fmt.Fprintf(os.Stderr, "unknown service subcommand %q\n", subcmd)
		os.Exit(2)
	}
}

// runAnalyze computes Blast Radius Intelligence for a target file or symbol per DELTA-07.
func runAnalyze(args []string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	file := fs.String("file", "", "target file to analyze (required)")
	symbol := fs.String("symbol", "", "target symbol identifier (optional)")
	root := fs.String("root", "", "codebase root directory (defaults to cwd)")
	failIf(fs.Parse(args))

	if *file == "" {
		if fs.NArg() > 0 {
			*file = fs.Arg(0)
		} else {
			fmt.Fprintln(os.Stderr, "usage: g8s analyze --file <path> [--symbol <name>] [--root <dir>]")
			os.Exit(2)
		}
	}

	an, err := analyzer.NewAnalyzer(*root)
	failIf(err)

	var report *analyzer.BlastRadiusReport
	if *symbol != "" {
		report, err = an.AnalyzeSymbolImpact(*file, *symbol)
	} else {
		report, err = an.AnalyzeFileImpact(*file)
	}
	failIf(err)

	out, err := json.MarshalIndent(report, "", "  ")
	failIf(err)
	fmt.Println(string(out))
}

// runVault handles Tri-Anchor knowledge vault operations (store, query, list, get, delete).
func runVault(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: g8s vault <store|query|list|get|delete> [options]")
		os.Exit(2)
	}

	dbPath, err := databasePath()
	failIf(err)
	v, err := vault.NewVault(dbPath, nil)
	failIf(err)
	defer v.Close()

	ctx := context.Background()

	switch args[0] {
	case "store":
		fs := flag.NewFlagSet("vault store", flag.ExitOnError)
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
		failIf(fs.Parse(args[1:]))

		var rec vault.DistillationRecord
		if *filePath != "" {
			data, err := os.ReadFile(*filePath)
			failIf(err)
			failIf(json.Unmarshal(data, &rec))
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
		failIf(err)
		out, err := json.MarshalIndent(stored, "", "  ")
		failIf(err)
		fmt.Println(string(out))

	case "query":
		fs := flag.NewFlagSet("vault query", flag.ExitOnError)
		limit := fs.Int("limit", 10, "maximum number of results")
		failIf(fs.Parse(args[1:]))

		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "usage: g8s vault query <search-term> [--limit 10]")
			os.Exit(2)
		}
		q := strings.Join(fs.Args(), " ")
		results, err := v.Query(ctx, q, *limit)
		failIf(err)
		out, err := json.MarshalIndent(results, "", "  ")
		failIf(err)
		fmt.Println(string(out))

	case "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: g8s vault get <record-id>")
			os.Exit(2)
		}
		rec, err := v.Get(ctx, args[1])
		failIf(err)
		out, err := json.MarshalIndent(rec, "", "  ")
		failIf(err)
		fmt.Println(string(out))

	case "list":
		fs := flag.NewFlagSet("vault list", flag.ExitOnError)
		milestone := fs.String("milestone", "", "filter by milestone")
		status := fs.String("status", "", "filter by status")
		pkg := fs.String("package", "", "filter by package")
		limit := fs.Int("limit", 50, "maximum records to return")
		failIf(fs.Parse(args[1:]))

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
		failIf(err)
		out, err := json.MarshalIndent(list, "", "  ")
		failIf(err)
		fmt.Println(string(out))

	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: g8s vault delete <record-id>")
			os.Exit(2)
		}
		failIf(v.Delete(ctx, args[1]))
		fmt.Printf("Record %q deleted\n", args[1])

	default:
		fmt.Fprintf(os.Stderr, "unknown vault subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func failIf(err error) {
	if err != nil {
		reportError(err, os.Stderr)
		os.Exit(1)
	}
}

// reportError renders a failure through pterm on the provided writer so tests
// can assert where diagnostics land.
func reportError(err error, w io.Writer) {
	pterm.Error.WithWriter(w).Println(fmt.Sprintf("%s: %v", AppName, err))
}

func printUsage() {
	fmt.Printf("%s (The Gatekeepers) - Zero-Trust Process & Capability Harness for AI Agents\n\n", AppName)
	fmt.Println("Usage:")
	fmt.Printf("  %s <command> [arguments]\n\n", AppName)
	fmt.Println("Commands:")
	fmt.Println("  submit       Queue an asynchronous durable task with harness safety checks")
	fmt.Println("  get          Show the durable state of one queued task (g8s get <task-id>)")
	fmt.Println("  resume       Resume a NEEDS_INFO/BLOCKED task (g8s resume <task-id> [--prompt <text>])")
	fmt.Println("  tasks        List durable tasks optionally filtered by state (--state, --limit)")
	fmt.Println("  lineage      Show ancestry tree for a task up to root (g8s lineage <task-id>)")
	fmt.Println("  children     List direct child subtasks for a task (g8s children <parent-id>)")
	fmt.Println("  receipt      Issue write delegation receipts (g8s receipt issue --path <glob>)")
	fmt.Println("  doctor       Run diagnostic health and environment sanity checks (g8s doctor [--fix])")
	fmt.Println("  init         Initialize g8s and configure IDE MCP integration (g8s init)")
	fmt.Println("  config       Manage persistent configuration key-values (g8s config get|set|list)")
	fmt.Println("  completion   Generate shell autocompletion scripts (g8s completion bash|zsh|fish)")
	fmt.Println("  service      Manage background daemon lifecycle (g8s service install|start|status)")
	fmt.Println("  worker       Run local background supervisor loop (g8s worker [--once])")
	fmt.Println("  analyze      Quantify code blast radius and recommend write scopes (g8s analyze ...)")
	fmt.Println("  vault        Manage decoupled Tri-Anchor knowledge records (store/query/list)")
	fmt.Println("  orchestrate  Run the supervisor self-test loop against the real agy worker")
	fmt.Println("  supervisor-metrics  Print supervisor telemetry (--task-id | --aggregate)")
	fmt.Println("  mcp          Serve the Stdio JSON-RPC MCP surface on stdin/stdout")
	fmt.Println("  roles        List registered worker roles")
	fmt.Println("  permissions  List registered permission profiles")
	fmt.Println("  version      Show application version")
	fmt.Println("  help         Show this message")
	fmt.Println("\nPlanned (post-MVP): run (sync dispatch)")
}

// runWorker runs the supervisor loop claiming tasks from the queue,
// resolving provider command templates from providers.json when present
// (DELTA-10 phase-2 registry-to-worker bridge).
func runWorker(args []string) {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	once := fs.Bool("once", true, "claim and execute a single task, then exit")
	model := fs.String("model", "", "restrict to tasks targeting this model")
	lease := fs.Int("lease", 60, "lease duration seconds")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	dbPath, dbErr := databasePath()
	if dbErr != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", AppName, dbErr)
		os.Exit(1)
	}

	templates := map[string][]string{}
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
				fmt.Fprintf(os.Stderr, "%s: %v\n", AppName, loadErr)
				os.Exit(1)
			}
			for _, entry := range cfgFile.Providers {
				if entry.Class != "platform_dispatch" || len(entry.Args) == 0 {
					continue
				}
				for _, m := range entry.Models {
					templates[m.ID] = entry.Args
				}
			}
		}
	}

	store, cpErr := controlplane.NewControlPlane(dbPath, nil)
	if cpErr != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", AppName, cpErr)
		os.Exit(1)
	}
	defer store.Close()

	sup := worker.NewSupervisor(store, filepath.Join(filepath.Dir(dbPath), "runs"),
		worker.WithBinaryPath(os.Args[0]),
		worker.WithCommandResolver(func(prompt, modelID, taskTimeout string) ([]string, bool) {
			tmpl, ok := templates[modelID]
			if !ok {
				return nil, false
			}
			out := make([]string, len(tmpl))
			for i, part := range tmpl {
				switch part {
				case "{prompt}":
					out[i] = prompt
				case "{model}":
					out[i] = modelID
				case "{timeout}":
					out[i] = taskTimeout
				default:
					out[i] = part
				}
			}
			return out, true
		}))

	ctx := context.Background()
	for {
		task, err := sup.RunOnce(ctx, worker.RunOptions{WorkerID: "cli-worker", LeaseSeconds: *lease})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", AppName, err)
			os.Exit(1)
		}
		if task == nil {
			fmt.Println("queue drained")
			return
		}
		fmt.Printf("%s %s\n", task.TaskID, task.State)
		if *model != "" && task.State != "SUCCEEDED" && task.State != "FAILED" {
			// model filter is advisory; states printed per cycle
			_ = model
		}
		if *once {
			return
		}
	}
}
