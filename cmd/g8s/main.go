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
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/doctor"
	"github.com/tamld/g8s/internal/worker"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/mcp"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/service"
)

// Version is a var so goreleaser ldflags -X can inject the build tag (D4).
var (
	Version = "0.1.0"
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
	case "service":
		runService(os.Args[2:])
	case "worker":
		runWorker(os.Args[2:])
	case "analyze":
		runAnalyze(os.Args[2:])
	case "internal":
		if len(os.Args) < 3 || os.Args[2] != "wrap-exec" {
			fmt.Fprintf(os.Stderr, "%s: usage: g8s internal wrap-exec --out <path> -- <child argv>\n", AppName)
			os.Exit(2)
		}
		if err := runWrapExec(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", AppName, err)
			os.Exit(2)
		}
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
	defer func() { _ = store.Close() }()

	receipts, err := receipt.NewReceiptManager(dbPath, nil)
	failIf(err)
	defer func() { _ = receipts.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = receipts.Close() }()

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
	failIf(fs.Parse(args))

	dbPath, _ := databasePath()
	report := doctor.RunDiagnostics(context.Background(), dbPath)

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

	var td pterm.TableData
	td = append(td, []string{"Check", "Status", "Message", "Details"})
	for _, chk := range report.Checks {
		statusStr := chk.Status
		if chk.Status == "OK" {
			statusStr = pterm.Green(chk.Status)
		} else if chk.Status == "WARN" {
			statusStr = pterm.Yellow(chk.Status)
		} else {
			statusStr = pterm.Red(chk.Status)
		}
		td = append(td, []string{chk.Name, statusStr, chk.Message, chk.Details})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()

	if report.OverallStatus == "UNHEALTHY" {
		os.Exit(1)
	}
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
>>>>>>> e02525f (feat(analyzer): implement LSP & AST Blast Radius Analyzer for automated write scoping)
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
	fmt.Println("  doctor       Run diagnostic health and environment sanity checks (g8s doctor)")
	fmt.Println("  service      Manage background daemon lifecycle (g8s service install|start|status)")
	fmt.Println("  analyze      Quantify code blast radius and recommend write scopes (g8s analyze ...)")
	fmt.Println("  mcp          Serve the Stdio JSON-RPC MCP surface on stdin/stdout")
	fmt.Println("  roles        List registered worker roles")
	fmt.Println("  permissions  List registered permission profiles")
	fmt.Println("  version      Show application version")
	fmt.Println("  help         Show this message")
	fmt.Println("\nPlanned (post-MVP): run (sync dispatch)")
}

// sliceFlags collects repeated occurrences of one flag into a slice,
// enabling repeatable -add-dir scope roots on the submit command.
type sliceFlags []string

func (s *sliceFlags) String() string { return strings.Join(*s, ",") }

func (s *sliceFlags) Set(v string) error {
	*s = append(*s, v)
	return nil
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
