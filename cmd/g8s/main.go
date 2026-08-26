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

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/mcp"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
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
	fmt.Println("  mcp          Serve the Stdio JSON-RPC MCP surface on stdin/stdout")
	fmt.Println("  roles        List registered worker roles")
	fmt.Println("  permissions  List registered permission profiles")
	fmt.Println("  version      Show application version")
	fmt.Println("  help         Show this message")
	fmt.Println("\nPlanned (post-MVP): run (sync dispatch), service (daemon lifecycle)")
}

// sliceFlags collects repeated occurrences of one flag into a slice,
// enabling repeatable -add-dir scope roots on the submit command.
type sliceFlags []string

func (s *sliceFlags) String() string { return strings.Join(*s, ",") }

func (s *sliceFlags) Set(v string) error {
	*s = append(*s, v)
	return nil
}
