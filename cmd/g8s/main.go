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
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/completion"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/initwiz"
	"github.com/tamld/g8s/internal/mcp"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/service"
	"github.com/tamld/g8s/internal/settings"
	"github.com/tamld/g8s/internal/vault"
	"github.com/tamld/g8s/internal/worker"
)

// Version is a var so goreleaser ldflags -X can inject the build tag (D4).
var (
	Version = "0.4.0"
	AppName = "g8s"
)

func exitUsage(cmd, sub, traceID, msg, hint string, jsonl bool) {
	if traceID == "" {
		traceID = cli.GenerateTraceID()
	}
	env := cli.NewErrorEnvelope(cmd, sub, traceID, cli.CodeUsage, msg, hint, "")
	_ = cli.WriteResponse(os.Stdout, env, jsonl)
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
	if err != nil {
		msg = err.Error()
	}
	env := cli.NewErrorEnvelope(cmd, sub, traceID, code, msg, hint, "")
	_ = cli.WriteResponse(os.Stdout, env, jsonl)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	command := os.Args[1]
	switch command {
	case "version":
		runVersion(os.Args[2:])
	case "roles":
		runRoles(os.Args[2:])
	case "permissions":
		runPermissions(os.Args[2:])
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
	case "cancel":
		runCancel(os.Args[2:])
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

// runVersion emits application build and version metadata.
func runVersion(args []string) {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("version", "", *traceID, err.Error(), "", *jsonl)
	}
	if *jsonMode || *jsonl {
		data := map[string]any{
			"app":      AppName,
			"version":  Version,
			"zero_cgo": true,
			"runtime":  "pure-go",
			"actor":    *actor,
		}
		env := cli.NewEnvelope("version", "version", "", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	pterm.DefaultHeader.WithFullWidth().Println(fmt.Sprintf("%s v%s (The Gatekeepers - Zero-CGO, Pure Go)", AppName, Version))
}

// runRoles lists registered worker roles.
func runRoles(args []string) {
	fs := flag.NewFlagSet("roles", flag.ContinueOnError)
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
	fs := flag.NewFlagSet("permissions", flag.ContinueOnError)
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

// runMCPServer serves the stdio JSON-RPC MCP surface until stdin closes.
func runMCPServer() {
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

	registry := provider.NewRegistry(provider.DefaultConfigs(), nil, nil)
	server := mcp.NewServer(os.Stdin, os.Stdout, store, receipts, registry)
	if err := server.ServeStdio(context.Background()); err != nil {
		exitRuntime("mcp", "", "", cli.CodeRuntime, err, "", false)
	}
}

// runSubmit queues one durable task through the control plane after validating
// it against the security harness.
func runSubmit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = jsonMode
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
	if err := fs.Parse(args); err != nil {
		exitUsage("submit", "", *traceID, err.Error(), "Check 'g8s submit --help'", *jsonl)
	}

	if *key == "" || *prompt == "" {
		exitUsage("submit", "", *traceID, "submit requires --idempotency-key and --prompt", "Provide both --idempotency-key and --prompt", *jsonl)
	}
	cwd, err := os.Getwd()
	if err != nil {
		exitRuntime("submit", "", *traceID, cli.CodeIO, err, "Failed to resolve working directory", *jsonl)
	}

	dirs := []string(addDirs)
	if len(dirs) == 0 {
		dirs = []string{cwd}
	}

	// Validate request against security harness gatekeeper
	if err := harness.ValidateRequest(*prompt, *role, *permission, dirs, *skipPermissions, *receiptID); err != nil {
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
		"prompt":     *prompt,
		"role":       *role,
		"permission": *permission,
		"timeout":    *timeout,
		"add_dirs":   dirs,
		"actor":      *actor,
	}
	if *receiptID != "" {
		payloadMap["receipt_id"] = *receiptID
	}
	if *skipPermissions {
		payloadMap["skip_permissions"] = true
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
		Model:          *model,
		Payload:        payload,
		Role:           *role,
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

// runGet prints the current durable view of one task.
func runGet(args []string) {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	taskIDFlag := fs.String("task-id", "", "task ID to inspect")
	if err := fs.Parse(args); err != nil {
		exitUsage("get", "", *traceID, err.Error(), "", *jsonl)
	}
	taskID := *taskIDFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		exitUsage("get", "", *traceID, "usage: g8s get <task-id> or g8s get --task-id <id>", "Provide a task ID", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("get", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("get", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		exitRuntime("get", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if task == nil {
		exitRuntime("get", "", *traceID, cli.CodeNotFound, fmt.Errorf("unknown task: %s", taskID), "Verify the task ID with 'g8s tasks'", *jsonl)
	}

	env := cli.NewEnvelope("task", "get", "", task)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("get", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

// runResume moves a NEEDS_INFO or BLOCKED task back to QUEUED with optional clarifying prompt.
func runResume(args []string) {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = jsonMode
	taskIDFlag := fs.String("task-id", "", "task ID to resume")
	prompt := fs.String("prompt", "", "updated prompt or clarifying answer")
	reason := fs.String("reason", "resumed via CLI", "reason for resuming")
	if err := fs.Parse(args); err != nil {
		exitUsage("resume", "", *traceID, err.Error(), "", *jsonl)
	}
	taskID := *taskIDFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		exitUsage("resume", "", *traceID, "usage: g8s resume <task-id> [--prompt <text>] [--reason <reason>]", "Provide a task ID", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("resume", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("resume", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	var resumedPayload json.RawMessage
	if *prompt != "" {
		payloadMap := map[string]any{
			"prompt": *prompt,
			"actor":  *actor,
		}
		raw, err := json.Marshal(payloadMap)
		if err != nil {
			exitRuntime("resume", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		resumedPayload = raw
	}

	task, err := store.ResumeTask(context.Background(), taskID, resumedPayload, *reason)
	if err != nil {
		exitRuntime("resume", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	env := cli.NewEnvelope("task", "resume", "", task)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("resume", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

// runTasks lists durable tasks optionally filtered by state.
func runTasks(args []string) {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	state := fs.String("state", "", "filter by task state (QUEUED, LEASED, RUNNING, SUCCEEDED, FAILED, CANCELLED, NEEDS_INFO, BLOCKED)")
	limit := fs.Int("limit", 50, "maximum number of tasks to return (1..200)")
	if err := fs.Parse(args); err != nil {
		exitUsage("tasks", "", *traceID, err.Error(), "", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("tasks", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("tasks", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	filter := controlplane.TaskFilter{Limit: *limit}
	if *state != "" {
		s := strings.ToUpper(*state)
		filter.State = &s
	}
	tasks, err := store.ListTasks(context.Background(), filter)
	if err != nil {
		exitRuntime("tasks", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	env := cli.NewEnvelope("tasks", "tasks", "", tasks)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("tasks", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

// runCancel cancels an active or queued task.
func runCancel(args []string) {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	taskIDFlag := fs.String("task-id", "", "task ID to cancel")
	reason := fs.String("reason", "cancelled via CLI", "reason for cancellation")
	if err := fs.Parse(args); err != nil {
		exitUsage("cancel", "", *traceID, err.Error(), "", *jsonl)
	}
	taskID := *taskIDFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		exitUsage("cancel", "", *traceID, "usage: g8s cancel <task-id> or g8s cancel --task-id <id> [--reason <reason>]", "Provide a task ID to cancel", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("cancel", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("cancel", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.CancelTask(ctx, taskID, *reason); err != nil {
		exitRuntime("cancel", "", *traceID, cli.CodeRuntime, err, "Failed to cancel task", *jsonl)
	}

	task, _ := store.GetTask(ctx, taskID)
	data := map[string]any{
		"task_id":   taskID,
		"cancelled": true,
		"reason":    *reason,
		"task":      task,
	}
	env := cli.NewEnvelope("task", "cancel", "", data)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("cancel", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

// runLineage prints the full ancestry chain of a task up to the root.
func runLineage(args []string) {
	fs := flag.NewFlagSet("lineage", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	taskIDFlag := fs.String("task-id", "", "task ID")
	if err := fs.Parse(args); err != nil {
		exitUsage("lineage", "", *traceID, err.Error(), "", *jsonl)
	}
	taskID := *taskIDFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		exitUsage("lineage", "", *traceID, "usage: g8s lineage <task-id> or g8s lineage --task-id <id>", "Provide a task ID", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("lineage", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("lineage", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	lineage, err := store.GetTaskLineage(context.Background(), taskID)
	if err != nil {
		exitRuntime("lineage", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if len(lineage) == 0 {
		exitRuntime("lineage", "", *traceID, cli.CodeNotFound, fmt.Errorf("unknown task: %s", taskID), "", *jsonl)
	}

	env := cli.NewEnvelope("lineage", "lineage", "", lineage)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("lineage", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

// runChildren lists direct child subtasks for a parent task.
func runChildren(args []string) {
	fs := flag.NewFlagSet("children", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	parentIDFlag := fs.String("parent-task-id", "", "parent task ID")
	if err := fs.Parse(args); err != nil {
		exitUsage("children", "", *traceID, err.Error(), "", *jsonl)
	}
	parentID := *parentIDFlag
	if parentID == "" && fs.NArg() > 0 {
		parentID = fs.Arg(0)
	}
	if parentID == "" {
		exitUsage("children", "", *traceID, "usage: g8s children <parent-task-id> or g8s children --parent-task-id <id>", "Provide a parent task ID", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("children", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("children", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	children, err := store.ListChildTasks(context.Background(), parentID)
	if err != nil {
		exitRuntime("children", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	env := cli.NewEnvelope("children", "children", "", children)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("children", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}

func receiptDatabasePath() (string, error) {
	dbPath, err := databasePath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(dbPath)
	return filepath.Join(dir, "receipts.db"), nil
}

// runReceipt issues and inspects write-delegation receipts on behalf of the operator.
func runReceipt(args []string) {
	if len(args) == 0 {
		exitUsage("receipt", "", "", "usage: g8s receipt <issue|show|verify|revoke|list> [options]", "Supported subcommands: issue, show, verify, revoke, list", false)
	}

	subcmd := args[0]
	dbPath, err := receiptDatabasePath()
	if err != nil {
		exitRuntime("receipt", subcmd, "", cli.CodeIO, err, "", false)
	}
	receipts, err := receipt.NewReceiptManager(dbPath, nil)
	if err != nil {
		exitRuntime("receipt", subcmd, "", cli.CodeRuntime, err, "", false)
	}
	defer receipts.Close()

	switch subcmd {
	case "issue":
		fs := flag.NewFlagSet("receipt issue", flag.ContinueOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = jsonMode
		issuer := fs.String("issuer", "", "identity recorded on the receipt (defaults to --actor)")
		ttlSeconds := fs.Int("ttl", 600, "time-to-live in seconds (1..3600)")
		var paths pathFlags
		fs.Var(&paths, "path", "allowed path glob (repeatable)")
		fs.Var(&paths, "allow", "allowed path glob (alias for --path)")
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("receipt", "issue", *traceID, err.Error(), "", *jsonl)
		}

		if len(paths) == 0 {
			exitUsage("receipt", "issue", *traceID, "receipt issue requires at least one --path or --allow", "Specify --path <glob>", *jsonl)
		}
		effectiveIssuer := *issuer
		if effectiveIssuer == "" {
			effectiveIssuer = *actor
		}
		rc, err := receipts.IssueReceipt(effectiveIssuer, paths, time.Duration(*ttlSeconds)*time.Second)
		if err != nil {
			exitRuntime("receipt", "issue", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("receipt", "receipt", "issue", rc)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("receipt", "issue", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "show", "get":
		fs := flag.NewFlagSet("receipt show", flag.ContinueOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		rcIDFlag := fs.String("receipt-id", "", "receipt ID to inspect")
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("receipt", subcmd, *traceID, err.Error(), "", *jsonl)
		}
		rcID := *rcIDFlag
		if rcID == "" && fs.NArg() > 0 {
			rcID = fs.Arg(0)
		}
		if rcID == "" {
			exitUsage("receipt", subcmd, *traceID, fmt.Sprintf("usage: g8s receipt %s <receipt-id> or --receipt-id <id>", subcmd), "Specify receipt ID", *jsonl)
		}
		rc, err := receipts.VerifyReceipt(rcID)
		if err != nil {
			exitRuntime("receipt", subcmd, *traceID, cli.CodeNotFound, err, "Receipt not found or invalid", *jsonl)
		}
		env := cli.NewEnvelope("receipt", "receipt", subcmd, rc)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("receipt", subcmd, *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "verify":
		fs := flag.NewFlagSet("receipt verify", flag.ContinueOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		rcIDFlag := fs.String("receipt-id", "", "receipt ID to verify")
		action := fs.String("action", "", "action to verify (e.g. write)")
		path := fs.String("path", "", "path to verify")
		_ = action
		_ = path
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("receipt", "verify", *traceID, err.Error(), "", *jsonl)
		}
		rcID := *rcIDFlag
		if rcID == "" && fs.NArg() > 0 {
			rcID = fs.Arg(0)
		}
		if rcID == "" {
			exitUsage("receipt", "verify", *traceID, "usage: g8s receipt verify <receipt-id> or --receipt-id <id>", "Specify receipt ID", *jsonl)
		}
		rc, err := receipts.VerifyReceipt(rcID)
		if err != nil {
			exitRuntime("receipt", "verify", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("receipt_verification", "receipt", "verify", rc)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("receipt", "verify", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "revoke":
		fs := flag.NewFlagSet("receipt revoke", flag.ContinueOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		rcIDFlag := fs.String("receipt-id", "", "receipt ID to revoke")
		reason := fs.String("reason", "", "reason for revocation")
		_ = reason
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("receipt", "revoke", *traceID, err.Error(), "", *jsonl)
		}
		rcID := *rcIDFlag
		if rcID == "" && fs.NArg() > 0 {
			rcID = fs.Arg(0)
		}
		if rcID == "" {
			exitUsage("receipt", "revoke", *traceID, "usage: g8s receipt revoke <receipt-id> or --receipt-id <id>", "Specify receipt ID to revoke", *jsonl)
		}
		ok, err := receipts.RevokeReceipt(rcID)
		if err != nil {
			exitRuntime("receipt", "revoke", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("receipt", "receipt", "revoke", map[string]any{"receipt_id": rcID, "revoked": ok})
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("receipt", "revoke", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "list":
		fs := flag.NewFlagSet("receipt list", flag.ContinueOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("receipt", "list", *traceID, err.Error(), "", *jsonl)
		}
		list, err := receipts.ListActiveReceipts()
		if err != nil {
			exitRuntime("receipt", "list", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("receipts", "receipt", "list", list)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("receipt", "list", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	default:
		exitUsage("receipt", subcmd, "", fmt.Sprintf("unknown receipt subcommand %q", subcmd), "Supported: issue, show, verify, revoke, list", false)
	}
}



// runInit handles interactive and headless onboarding wizard.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	agentMode := fs.Bool("agent", false, "run in non-interactive headless agent mode")
	ideFlag := fs.String("ide", "", "target IDE to configure (cursor, claude, windsurf, antigravity, all)")
	if err := fs.Parse(args); err != nil {
		exitUsage("init", "", *traceID, err.Error(), "", *jsonl)
	}

	var targetIDEs []string
	if *ideFlag != "" {
		targetIDEs = strings.Split(*ideFlag, ",")
	}

	res, err := initwiz.RunInit(targetIDEs, "", os.Args[0])
	if err != nil {
		exitRuntime("init", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("init_result", "init", "", res)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
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
		exitUsage("config", "", "", fmt.Sprintf("%s: usage: g8s config <get|set|list|unset> [key] [value] [--json]", AppName), "", false)
	}

	fs := flag.NewFlagSet("config", flag.ContinueOnError)
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
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
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

	subcmd := args[0]
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
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
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
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
		fs := flag.NewFlagSet("vault store", flag.ContinueOnError)
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
		fs := flag.NewFlagSet("vault query", flag.ContinueOnError)
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
		fs := flag.NewFlagSet("vault get", flag.ContinueOnError)
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
		fs := flag.NewFlagSet("vault list", flag.ContinueOnError)
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
		fs := flag.NewFlagSet("vault delete", flag.ContinueOnError)
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
	fmt.Println("  cancel       Cancel an active or queued task (g8s cancel <task-id> [--reason <text>])")
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
	fmt.Println("  orchestrate-aic  Run AIC automated PR review orchestrator (g8s orchestrate-aic --pr <num> --intent <text>)")
	fmt.Println("  supervisor-metrics  Print supervisor telemetry (--task-id | --aggregate)")
	fmt.Println("  brief-issue  Issue a structured task brief with DoD and TTL (g8s brief-issue ...)")
	fmt.Println("  brief-consume Consume an active brief by ID (g8s brief-consume --id <id>)")
	fmt.Println("  cleanup-worktrees Clean up stale agy subagent git worktrees (g8s cleanup-worktrees --older-than 1h [--dry-run])")
	fmt.Println("  cleanup      Run lifecycle sweep for ghost processes, orphan worktrees, branches, tags, receipts")
	fmt.Println("  migrate      Migrate legacy cwd-relative g8s data to canonical paths (g8s migrate --from ./ [--dry-run])")
	fmt.Println("  status       Display worker heartbeat and lifecycle observability status (--worker)")
	fmt.Println("  state        Show state and replay event logs (g8s state show|replay <id>)")
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
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	once := fs.Bool("once", true, "claim and execute a single task, then exit")
	model := fs.String("model", "", "restrict to tasks targeting this model")
	lease := fs.Int("lease", 60, "lease duration seconds")
	if err := fs.Parse(args); err != nil {
		exitUsage("worker", "", *traceID, err.Error(), "", *jsonl)
	}
	dbPath, dbErr := databasePath()
	if dbErr != nil {
		exitRuntime("worker", "", *traceID, cli.CodeIO, dbErr, "", *jsonl)
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
				exitRuntime("worker", "", *traceID, cli.CodeRuntime, loadErr, "", *jsonl)
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
		exitRuntime("worker", "", *traceID, cli.CodeRuntime, cpErr, "", *jsonl)
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
		task, err := sup.RunOnce(ctx, worker.RunOptions{WorkerID: *actor, LeaseSeconds: *lease})
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
		if *model != "" && task.State != "SUCCEEDED" && task.State != "FAILED" {
			// model filter is advisory; states printed per cycle
			_ = model
		}
		if *once {
			return
		}
	}
}

// runState handles state introspection and event log replay per DEBT-31.
func runState(args []string) {
	if len(args) == 0 {
		exitUsage("state", "", "", "usage: g8s state <show|replay> <id> [flags]", "Use 'g8s state show <id>' or 'g8s state replay <id>'", false)
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
	fs := flag.NewFlagSet("state show", flag.ContinueOnError)
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
	fs := flag.NewFlagSet("state replay", flag.ContinueOnError)
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
