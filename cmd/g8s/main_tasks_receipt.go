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

	"github.com/tamld/g8s/internal/cli"

	"github.com/tamld/g8s/internal/controlplane"

	"github.com/tamld/g8s/internal/initwiz"

	"github.com/tamld/g8s/internal/receipt"
)

func runGet(args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	taskIDFlag := fs.String("task-id", "", "task ID to inspect")
	taskAlias := fs.String("task", "", "alias for --task-id")
	if err := fs.Parse(args); err != nil {
		exitUsage("get", "", *traceID, err.Error(), "", *jsonl)
	}
	taskID := *taskIDFlag
	if taskID == "" {
		taskID = *taskAlias
	}
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
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
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
	fs := flag.NewFlagSet("tasks", flag.ExitOnError)
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
		if s == "PENDING" {
			s = controlplane.StateQueued
		}
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
	fs := flag.NewFlagSet("cancel", flag.ExitOnError)
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
	fs := flag.NewFlagSet("lineage", flag.ExitOnError)
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
	fs := flag.NewFlagSet("children", flag.ExitOnError)
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

	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println("Usage: g8s receipt <issue|show|verify|revoke|list> [options]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  issue   Issue a write receipt")
		fmt.Println("  show    Show receipt details")
		fmt.Println("  verify  Verify a receipt")
		fmt.Println("  revoke  Revoke a receipt")
		fmt.Println("  list    List active receipts")
		os.Exit(0)
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
	case "export":
		runReceiptExport(args[1:])
	case "issue":
		fs := flag.NewFlagSet("receipt issue", flag.ExitOnError)
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
		rc, err := receipts.IssueReceipt(effectiveIssuer, paths, time.Duration(*ttlSeconds)*time.Second,
			receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
				SchemaURI:      "g8s://envelope/write-receipt/v1",
				FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
				RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
			}),
			receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
				RulesetVersion: "v1",
				PipelineDigest: "cli-default",
			}),
			receipt.WithProvenanceContext("g8s/"+Version, Commit, *traceID),
		)
		if err != nil {
			exitRuntime("receipt", "issue", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		env := cli.NewEnvelope("receipt", "receipt", "issue", rc)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("receipt", "issue", *traceID, cli.CodeIO, err, "", *jsonl)
		}

	case "show", "get":
		fs := flag.NewFlagSet("receipt show", flag.ExitOnError)
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
		fs := flag.NewFlagSet("receipt verify", flag.ExitOnError)
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
		fs := flag.NewFlagSet("receipt revoke", flag.ExitOnError)
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
		fs := flag.NewFlagSet("receipt list", flag.ExitOnError)
		actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
		_ = actor
		_ = jsonMode
		taskID := fs.String("task", "", "filter by consumer task ID")
		since := fs.String("since", "", "filter by creation time (e.g., 24h, 7d)")
		verdict := fs.String("verdict", "", "filter by verdict (active|consumed)")
		if err := fs.Parse(args[1:]); err != nil {
			exitUsage("receipt", "list", *traceID, err.Error(), "", *jsonl)
		}

		var sinceTime time.Time
		if *since != "" {
			dur, err := time.ParseDuration(*since)
			if err != nil {
				exitUsage("receipt", "list", *traceID, fmt.Sprintf("invalid --since duration: %v", err), "", *jsonl)
			}
			sinceTime = time.Now().Add(-dur)
		}

		list, err := receipts.ListReceipts(*taskID, sinceTime, *verdict)
		if err != nil {
			exitRuntime("receipt", "list", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}

		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("receipts", "receipt", "list", list)
			env.TraceID = *traceID
			if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
				exitRuntime("receipt", "list", *traceID, cli.CodeIO, err, "", *jsonl)
			}
			return
		}

		// Compact table output
		var td pterm.TableData
		td = append(td, []string{"RECEIPT ID", "ISSUER", "VERDICT", "CREATED", "EXPIRES", "PATHS"})
		for _, r := range list {
			verdictStr := "active"
			if r.Consumed {
				verdictStr = "consumed"
			}
			paths := strings.Join(r.AllowedPaths, ", ")
			if len(paths) > 60 {
				paths = paths[:57] + "..."
			}
			td = append(td, []string{
				r.ReceiptID[:8] + "...",
				r.Issuer,
				verdictStr,
				r.CreatedAt.Format("2006-01-02 15:04"),
				r.ExpiresAt.Format("2006-01-02 15:04"),
				paths,
			})
		}
		pterm.DefaultTable.WithHasHeader().WithData(td).Render()
		if len(list) == 0 {
			pterm.Info.Println("No receipts found matching criteria")
		}

	default:
		exitUsage("receipt", subcmd, "", fmt.Sprintf("unknown receipt subcommand %q", subcmd), "Supported: issue, show, verify, revoke, list", false)
	}
}

// runInit handles interactive and headless onboarding wizard.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
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

	// Show verification result
	if res.Verification != nil {
		fmt.Println()
		pterm.DefaultHeader.WithFullWidth().Println("Initialization Verification")
		if res.Verification.Verified {
			pterm.Success.Printf("✓ Verification PASSED (task: %s, receipt: %s, duration: %ds)\n",
				res.Verification.TaskID, res.Verification.ReceiptID, res.Verification.DurationSecs)
		} else {
			pterm.Error.Printf("✗ Verification FAILED: %s\n", res.Verification.Error)
		}
	}
}

// runConfig manages atomic key-value configuration.
