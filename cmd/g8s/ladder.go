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
	"sort"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/ladder"
	"github.com/tamld/g8s/internal/lane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/telemetry"
)

func telemetryDatabasePath() string {
	if p := os.Getenv("G8S_TELEMETRY_DB"); p != "" {
		return p
	}
	if p, err := databasePath(); err == nil {
		return filepath.Join(filepath.Dir(p), "telemetry.db")
	}
	return filepath.Join(pathutil.DefaultStateDir(), "telemetry.db")
}

// executeLadder runs the core quality ladder CLI command.
func executeLadder(ctx context.Context, args []string, stdout, stderr io.Writer) (int, *cli.Envelope, error) {
	if len(args) == 0 {
		printLadderUsage(stderr)
		env := cli.NewErrorEnvelope("ladder", "", "", cli.CodeUsage, "subcommand required: status, advance, or gauges", "Run 'g8s ladder --help'", "")
		return 2, &env, errors.New("subcommand required")
	}

	subcmd := strings.ToLower(args[0])
	subArgs := args[1:]

	switch subcmd {
	case "status":
		return executeLadderStatus(ctx, subArgs, stdout, stderr)
	case "advance":
		return executeLadderAdvance(ctx, subArgs, stdout, stderr)
	case "gauges":
		return executeLadderGauges(ctx, subArgs, stdout, stderr)
	case "help", "-h", "--help":
		printLadderUsage(stdout)
		return 0, nil, nil
	default:
		msg := fmt.Sprintf("unknown ladder subcommand %q (valid: status, advance, gauges)", subcmd)
		env := cli.NewErrorEnvelope("ladder", subcmd, "", cli.CodeUsage, msg, "Run 'g8s ladder --help'", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, false)
		}
		return 2, &env, errors.New(msg)
	}
}

func parseWithPositional(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	return positional, nil
}

func printLadderUsage(w io.Writer) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, "Usage: g8s ladder <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  status <task-id>       Display ladder lineage, tokens used, and next rung plan")
	fmt.Fprintln(w, "  advance <task-id>      Execute the next ladder rung or route to HITL")
	fmt.Fprintln(w, "  gauges [class]         Report pass-rates, escalation-rates, and HITL metrics")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags (common):")
	fmt.Fprintln(w, "  --json                 Output envelope in JSON format")
	fmt.Fprintln(w, "  --jsonl                Output envelope in JSON Lines format")
	fmt.Fprintln(w, "  --out <path>           Write HITL evidence packet to file (advance only)")
	fmt.Fprintln(w, "  --prompt <text>        Task prompt for escalation re-dos (advance only)")
	fmt.Fprintln(w, "  --prompt-file <path>   File containing task prompt for escalation re-dos (advance only)")
	fmt.Fprintln(w, "  --receipt-id <id>      Fresh write receipt ID for workspace_write tasks")
	fmt.Fprintln(w, "  --db <path>            Control-plane database path")
	fmt.Fprintln(w, "  --telemetry-db <path>  Telemetry database path")
}

type lineageDetails struct {
	rootTask         *controlplane.Task
	childTasks       []*controlplane.Task
	class            string
	originalRole     string
	originalPerm     string
	originalModel    string
	originalEffort   string
	tokenBudget      int
	cumulativeTokens int
	history          []ladder.RungRecord
	latestTask       *controlplane.Task
	latestSucceeded  bool
	latestVerdict    ladder.ClassifierVerdict
	failingChecks    []string
}

func walkLineage(ctx context.Context, store *controlplane.Store, telemEvents []telemetry.TraceEvent, taskID string) (*lineageDetails, error) {
	orig, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task %s: %w", taskID, err)
	}
	if orig == nil {
		return nil, fmt.Errorf("%w: %s", controlplane.ErrUnknownTask, taskID)
	}

	// 1. Walk parent_task_id to root
	visited := map[string]bool{orig.TaskID: true}
	curr := orig
	for curr.ParentTaskID != nil && strings.TrimSpace(*curr.ParentTaskID) != "" {
		parentID := strings.TrimSpace(*curr.ParentTaskID)
		if visited[parentID] {
			return nil, fmt.Errorf("lineage cycle detected at task %s", parentID)
		}
		visited[parentID] = true
		parentTask, err := store.GetTask(ctx, parentID)
		if err != nil || parentTask == nil {
			break
		}
		curr = parentTask
	}
	rootTask := curr

	// 2. Query all descendants
	var allDescendants []*controlplane.Task
	queue := []string{rootTask.TaskID}
	seen := map[string]bool{rootTask.TaskID: true}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		children, err := store.ListChildTasks(ctx, pid)
		if err != nil {
			continue
		}
		for _, ch := range children {
			if !seen[ch.TaskID] {
				seen[ch.TaskID] = true
				allDescendants = append(allDescendants, ch)
				queue = append(queue, ch.TaskID)
			}
		}
	}

	sort.Slice(allDescendants, func(i, j int) bool {
		return allDescendants[i].CreatedAt < allDescendants[j].CreatedAt
	})

	// Parse root payload properties
	var rootPayload map[string]any
	if len(rootTask.Request) > 0 {
		_ = json.Unmarshal(rootTask.Request, &rootPayload)
	}

	role := "coder"
	if r, ok := rootPayload["role"].(string); ok && r != "" {
		role = r
	}
	perm := "read_only"
	if p, ok := rootPayload["permission"].(string); ok && p != "" {
		perm = p
	}
	model := "gemini-3.8-flash-high"
	if m, ok := rootPayload["model"].(string); ok && m != "" {
		model = m
	}
	effort := "medium"
	if e, ok := rootPayload["effort"].(string); ok && e != "" {
		effort = e
	} else if e, ok := rootPayload["effort_requested"].(string); ok && e != "" {
		effort = e
	}

	cls := lane.UnregisteredClassName
	if c, ok := rootPayload["class"].(string); ok && c != "" {
		cls = c
	} else if c, ok := rootPayload["effort_class"].(string); ok && c != "" {
		cls = c
	}

	// Index telemetry by task ID
	telemByTask := make(map[string]telemetry.TraceEvent)
	for _, ev := range telemEvents {
		if ev.TaskID != "" {
			telemByTask[ev.TaskID] = ev
		}
	}

	// Load token budgets from effort-classes.yml
	budgets := ladder.LoadTokenBudgets("")
	tokenBudget := budgets[cls]

	// Extract history from child tasks
	var history []ladder.RungRecord
	cumTokens := 0
	var allChecksFailed []string

	for idx, ch := range allDescendants {
		var chPayload map[string]any
		if len(ch.Request) > 0 {
			_ = json.Unmarshal(ch.Request, &chPayload)
		}

		chRole := role
		if r, ok := chPayload["role"].(string); ok && r != "" {
			chRole = r
		}
		chPerm := perm
		if p, ok := chPayload["permission"].(string); ok && p != "" {
			chPerm = p
		}
		chModel := model
		if m, ok := chPayload["model"].(string); ok && m != "" {
			chModel = m
		}
		chEffort := effort
		if e, ok := chPayload["effort"].(string); ok && e != "" {
			chEffort = e
		}

		// Tokens accounting from telemetry or result
		tokens := 0
		inTokens := 0
		outTokens := 0
		if ev, ok := telemByTask[ch.TaskID]; ok {
			inTokens = ev.InputTokens
			outTokens = ev.OutputTokens
			tokens = inTokens + outTokens
		}
		if tokens == 0 && len(ch.Result) > 0 {
			var res struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(ch.Result, &res) == nil && res.Usage != nil {
				inTokens = res.Usage.InputTokens
				outTokens = res.Usage.OutputTokens
				tokens = inTokens + outTokens
			}
		}
		cumTokens += tokens

		// Extract failure evidence
		lastErr := ""
		if ch.LastError != nil {
			lastErr = *ch.LastError
		}
		contractViol := ""
		if ch.ContractValidation != nil && !ch.ContractValidation.Valid {
			var parts []string
			parts = append(parts, ch.ContractValidation.PathViolations...)
			parts = append(parts, ch.ContractValidation.ToolViolations...)
			parts = append(parts, ch.ContractValidation.SchemaErrors...)
			contractViol = strings.Join(parts, "; ")
		}

		var checks []string
		verdictStr := ch.State
		if ch.State == controlplane.StateSucceeded {
			verdictStr = "passed"
		} else {
			if lastErr != "" {
				checks = append(checks, lastErr)
			}
			allChecksFailed = append(allChecksFailed, checks...)
		}

		shapeVerdict := ladder.ClassifyFailure(ladder.FailureEvidence{
			ErrorText:         lastErr,
			ContractViolation: contractViol,
			ChecksFailed:      checks,
			Model:             chModel,
			Effort:            chEffort,
		})

		history = append(history, ladder.RungRecord{
			RungIndex:    idx,
			TaskID:       ch.TaskID,
			Role:         chRole,
			Permission:   chPerm,
			Model:        chModel,
			Effort:       chEffort,
			Shape:        shapeVerdict.Shape,
			Verdict:      verdictStr,
			Tokens:       tokens,
			InputTokens:  inTokens,
			OutputTokens: outTokens,
			ChecksFailed: checks,
		})
	}

	// Identify latest task in lineage
	latestTask := rootTask
	if len(allDescendants) > 0 {
		latestTask = allDescendants[len(allDescendants)-1]
	}

	latestSucceeded := latestTask.State == controlplane.StateSucceeded
	lastErr := ""
	if latestTask.LastError != nil {
		lastErr = *latestTask.LastError
	}
	contractViol := ""
	if latestTask.ContractValidation != nil && !latestTask.ContractValidation.Valid {
		var parts []string
		parts = append(parts, latestTask.ContractValidation.PathViolations...)
		parts = append(parts, latestTask.ContractValidation.ToolViolations...)
		parts = append(parts, latestTask.ContractValidation.SchemaErrors...)
		contractViol = strings.Join(parts, "; ")
	}

	latestVerdict := ladder.ClassifyFailure(ladder.FailureEvidence{
		ErrorText:         lastErr,
		ContractViolation: contractViol,
		ChecksFailed:      allChecksFailed,
		Model:             model,
		Effort:            effort,
	})

	return &lineageDetails{
		rootTask:         rootTask,
		childTasks:       allDescendants,
		class:            cls,
		originalRole:     role,
		originalPerm:     perm,
		originalModel:    model,
		originalEffort:   effort,
		tokenBudget:      tokenBudget,
		cumulativeTokens: cumTokens,
		history:          history,
		latestTask:       latestTask,
		latestSucceeded:  latestSucceeded,
		latestVerdict:    latestVerdict,
		failingChecks:    allChecksFailed,
	}, nil
}

func executeLadderStatus(ctx context.Context, args []string, stdout, stderr io.Writer) (int, *cli.Envelope, error) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	taskFlag := fs.String("task", "", "task ID to inspect")
	taskIDFlag := fs.String("task-id", "", "task ID to inspect (alias)")
	dbFlag := fs.String("db", "", "path to controlplane database")
	telemFlag := fs.String("telemetry-db", "", "path to telemetry database")

	posArgs, err := parseWithPositional(fs, args)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "status", *traceID, cli.CodeUsage, err.Error(), "", "")
		return 2, &env, err
	}

	taskID := *taskFlag
	if taskID == "" {
		taskID = *taskIDFlag
	}
	if taskID == "" && len(posArgs) > 0 {
		taskID = posArgs[0]
	}
	if taskID == "" {
		msg := "task ID is required: g8s ladder status <task-id>"
		env := cli.NewErrorEnvelope("ladder", "status", *traceID, cli.CodeUsage, msg, "Provide a task ID", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 2, &env, errors.New(msg)
	}

	dbPath := *dbFlag
	if dbPath == "" {
		var err error
		dbPath, err = databasePath()
		if err != nil {
			env := cli.NewErrorEnvelope("ladder", "status", *traceID, cli.CodeIO, err.Error(), "", "")
			return 1, &env, err
		}
	}

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "status", *traceID, cli.CodeRuntime, err.Error(), "Open control plane database", "")
		return 1, &env, err
	}
	defer store.Close()

	telemDB := *telemFlag
	if telemDB == "" {
		telemDB = telemetryDatabasePath()
	}
	telemEvents, _ := ladder.LoadTelemetryEvents(ctx, telemDB)

	lin, err := walkLineage(ctx, store, telemEvents, taskID)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "status", *traceID, cli.CodeNotFound, err.Error(), "", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, err
	}

	// Compute next plan
	plan := ladder.EvaluateNextRung(ladder.PolicyContext{
		RootTaskID:       lin.rootTask.TaskID,
		Class:            lin.class,
		OriginalRole:     lin.originalRole,
		OriginalPerm:     lin.originalPerm,
		OriginalModel:    lin.originalModel,
		OriginalEffort:   lin.originalEffort,
		LatestSucceeded:  lin.latestSucceeded,
		LatestVerdict:    lin.latestVerdict,
		History:          lin.history,
		TokenBudget:      lin.tokenBudget,
		CumulativeTokens: lin.cumulativeTokens,
	})

	data := map[string]any{
		"root_task_id":     lin.rootTask.TaskID,
		"class":            lin.class,
		"current_rung":     len(lin.history),
		"max_rungs":        ladder.MaxLadderRungs,
		"total_tokens":     lin.cumulativeTokens,
		"token_budget":     lin.tokenBudget,
		"latest_task_id":   lin.latestTask.TaskID,
		"latest_succeeded": lin.latestSucceeded,
		"latest_shape":     lin.latestVerdict.Shape,
		"history":          lin.history,
		"next_plan":        plan,
		"checks_failed":    lin.failingChecks,
	}

	env := cli.NewEnvelope("ladder", "status", "", data)
	env.TraceID = *traceID

	if *jsonMode || *jsonl {
		if stdout != nil {
			_ = cli.WriteResponse(stdout, env, *jsonl)
		}
		return 0, &env, nil
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "Ladder Status for Task %s (Root: %s, Class: %s)\n", taskID, lin.rootTask.TaskID, lin.class)
		fmt.Fprintf(stdout, "Rung Position: %d / %d | Cumulative Tokens: %d (Budget: %d)\n", len(lin.history), ladder.MaxLadderRungs, lin.cumulativeTokens, lin.tokenBudget)
		fmt.Fprintf(stdout, "Latest Task: %s | State: %s | Shape: %s\n", lin.latestTask.TaskID, lin.latestTask.State, lin.latestVerdict.Shape)
		if len(lin.history) > 0 {
			fmt.Fprintln(stdout, "\nHistory:")
			for _, h := range lin.history {
				fmt.Fprintf(stdout, "  Rung %d [%s]: model=%s effort=%s tokens=%d shape=%s verdict=%s\n",
					h.RungIndex, h.TaskID, h.Model, h.Effort, h.Tokens, h.Shape, h.Verdict)
			}
		}
		fmt.Fprintf(stdout, "\nNext Action: %s — %s\n", plan.Action, plan.Reason)
	}

	return 0, &env, nil
}

// GenerateDiagnosisPrompt constructs a deterministic prompt for Rung 0 diagnosis dispatch
// containing the task failure evidence.
func GenerateDiagnosisPrompt(taskID, class, lastError, resultSummary string) string {
	if strings.TrimSpace(taskID) == "" {
		taskID = "unknown"
	}
	if strings.TrimSpace(class) == "" {
		class = "unregistered"
	}
	if strings.TrimSpace(lastError) == "" {
		lastError = "none"
	}
	if strings.TrimSpace(resultSummary) == "" {
		resultSummary = "none"
	}
	return fmt.Sprintf(`Quality ladder diagnosis dispatch for failed task %s.

Task Evidence:
- Task ID: %s
- Class: %s
- Last Error: %s
- Result Summary: %s

Objective:
You are a diagnosis worker operating in read-only mode with low effort.
Your job is to:
1. Inspect the workspace and task failure evidence.
2. Classify the failure shape (effort-shaped, brief-shaped, or env-shaped).
3. Verify class checks and identify the minimal corrective action required.`,
		taskID, taskID, class, lastError, resultSummary)
}

func extractResultSummary(resultBytes []byte) string {
	if len(resultBytes) == 0 {
		return "none"
	}
	var resMap map[string]any
	if err := json.Unmarshal(resultBytes, &resMap); err == nil {
		if s, ok := resMap["summary"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		if s, ok := resMap["status"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		if compact, err := json.Marshal(resMap); err == nil && len(compact) < 200 {
			return string(compact)
		}
	}
	s := strings.TrimSpace(string(resultBytes))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	if s == "" || s == "null" {
		return "none"
	}
	return s
}

func executeLadderAdvance(ctx context.Context, args []string, stdout, stderr io.Writer) (int, *cli.Envelope, error) {
	fs := flag.NewFlagSet("advance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	taskFlag := fs.String("task", "", "task ID to advance")
	taskIDFlag := fs.String("task-id", "", "task ID to advance (alias)")
	receiptIDFlag := fs.String("receipt-id", "", "fresh write receipt ID for workspace_write tasks")
	outFlag := fs.String("out", "", "optional file path to write HITL evidence packet")
	promptFlag := fs.String("prompt", "", "task prompt for escalation re-dos")
	promptFileFlag := fs.String("prompt-file", "", "path to file containing task prompt for escalation re-dos")
	dbFlag := fs.String("db", "", "path to controlplane database")
	telemFlag := fs.String("telemetry-db", "", "path to telemetry database")

	posArgs, err := parseWithPositional(fs, args)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeUsage, err.Error(), "", "")
		return 2, &env, err
	}

	var advancePrompt string
	if *promptFileFlag != "" {
		content, err := os.ReadFile(*promptFileFlag)
		if err != nil {
			msg := fmt.Sprintf("read --prompt-file %s: %v", *promptFileFlag, err)
			env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeIO, msg, "Ensure prompt file exists and is readable", "")
			if stderr != nil {
				_ = cli.WriteResponse(stderr, env, *jsonl)
			}
			return 1, &env, err
		}
		advancePrompt = string(content)
	} else if *promptFlag != "" {
		advancePrompt = *promptFlag
	}

	taskID := *taskFlag
	if taskID == "" {
		taskID = *taskIDFlag
	}
	if taskID == "" && len(posArgs) > 0 {
		taskID = posArgs[0]
	}
	if taskID == "" {
		msg := "task ID is required: g8s ladder advance <task-id>"
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeUsage, msg, "Provide a task ID", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 2, &env, errors.New(msg)
	}

	dbPath := *dbFlag
	if dbPath == "" {
		var err error
		dbPath, err = databasePath()
		if err != nil {
			env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeIO, err.Error(), "", "")
			return 1, &env, err
		}
	}

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeRuntime, err.Error(), "Open control plane database", "")
		return 1, &env, err
	}
	defer store.Close()

	telemDB := *telemFlag
	if telemDB == "" {
		telemDB = telemetryDatabasePath()
	}
	telemEvents, _ := ladder.LoadTelemetryEvents(ctx, telemDB)

	lin, err := walkLineage(ctx, store, telemEvents, taskID)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeNotFound, err.Error(), "", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, err
	}

	// Refuse if latest task is still active
	if lin.latestTask.State == controlplane.StateQueued ||
		lin.latestTask.State == controlplane.StateLeased ||
		lin.latestTask.State == controlplane.StateRunning {
		msg := fmt.Sprintf("cannot advance task %s: latest task %s is still in active state %s",
			taskID, lin.latestTask.TaskID, lin.latestTask.State)
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeInvalid, msg, "Wait for latest task to finish", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, errors.New(msg)
	}

	// Refuse if latest task succeeded
	if lin.latestSucceeded {
		msg := fmt.Sprintf("task %s already succeeded (checks passed): no ladder advance needed", lin.latestTask.TaskID)
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeInvalid, msg, "Task is complete", "")
		if *jsonMode || *jsonl {
			if stdout != nil {
				_ = cli.WriteResponse(stdout, env, *jsonl)
			}
		} else if stdout != nil {
			fmt.Fprintln(stdout, msg)
		}
		return 0, &env, nil
	}

	// Evaluate next rung
	plan := ladder.EvaluateNextRung(ladder.PolicyContext{
		RootTaskID:       lin.rootTask.TaskID,
		Class:            lin.class,
		OriginalRole:     lin.originalRole,
		OriginalPerm:     lin.originalPerm,
		OriginalModel:    lin.originalModel,
		OriginalEffort:   lin.originalEffort,
		LatestSucceeded:  lin.latestSucceeded,
		LatestVerdict:    lin.latestVerdict,
		History:          lin.history,
		TokenBudget:      lin.tokenBudget,
		CumulativeTokens: lin.cumulativeTokens,
	})

	// If plan is HITL or refused: construct and emit evidence packet
	if plan.Action == ladder.ActionHITL || plan.RefusalError != nil {
		packet := ladder.BuildEvidencePacket(
			lin.rootTask.TaskID,
			lin.class,
			string(plan.Action),
			plan.Reason,
			lin.failingChecks,
			lin.history,
			lin.tokenBudget,
		)

		if *outFlag != "" {
			_ = packet.WriteToFile(*outFlag)
		}

		data := map[string]any{
			"hitl_packet": packet,
			"reason":      plan.Reason,
		}
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeDenied, plan.Reason, "Human-in-the-loop review required", "")
		env.Data = data

		if *jsonMode || *jsonl {
			if stdout != nil {
				_ = cli.WriteResponse(stdout, env, *jsonl)
			}
		} else {
			if stdout != nil {
				_ = packet.WriteJSON(stdout)
			}
		}
		return 1, &env, plan.RefusalError
	}

	// Escalation re-dos (rungs 1..5) require an explicit prompt (--prompt or --prompt-file)
	if plan.RungIndex > 0 && strings.TrimSpace(advancePrompt) == "" {
		msg := fmt.Sprintf("advancing task %s to rung %d requires --prompt or --prompt-file: original prompt was redacted", taskID, plan.RungIndex)
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeUsage, msg, "Pass --prompt-file <path> or --prompt <text>", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 2, &env, errors.New(msg)
	}

	// Execute rung
	var origPayload map[string]any
	if len(lin.latestTask.Request) > 0 {
		_ = json.Unmarshal(lin.latestTask.Request, &origPayload)
	} else {
		origPayload = make(map[string]any)
	}

	// Trust boundary: drop receipt_id unconditionally from previous payload
	delete(origPayload, "receipt_id")

	// Set updated params
	origPayload["role"] = plan.Role
	origPayload["permission"] = plan.Permission
	origPayload["model"] = plan.Model
	origPayload["effort"] = plan.Effort
	origPayload["effort_requested"] = plan.Effort
	origPayload["ladder_rung"] = plan.RungIndex

	// Prompt resolution: Rung 0 generates a diagnosis prompt; Rungs 1..5 use the provided prompt
	var promptToUse string
	if plan.RungIndex == 0 {
		lastErr := ""
		if lin.latestTask.LastError != nil {
			lastErr = *lin.latestTask.LastError
		}
		promptToUse = GenerateDiagnosisPrompt(lin.latestTask.TaskID, lin.class, lastErr, extractResultSummary(lin.latestTask.Result))
	} else {
		promptToUse = advancePrompt
	}
	origPayload["prompt"] = promptToUse
	delete(origPayload, "prompt_redacted")
	delete(origPayload, "prompt_hash")

	// Timeout resolution: carry original task's timeout from payload with record/default fallback
	timeout := ""
	if t, ok := origPayload["timeout"].(string); ok && strings.TrimSpace(t) != "" {
		timeout = strings.TrimSpace(t)
	} else if len(lin.rootTask.Request) > 0 {
		var rp map[string]any
		if json.Unmarshal(lin.rootTask.Request, &rp) == nil {
			if t, ok := rp["timeout"].(string); ok && strings.TrimSpace(t) != "" {
				timeout = strings.TrimSpace(t)
			}
		}
	}
	if timeout == "" {
		timeout = "30s"
	}
	origPayload["timeout"] = timeout

	// Verify write receipt if advancing a workspace_write rung
	if strings.EqualFold(plan.Permission, "workspace_write") {
		rcID := strings.TrimSpace(*receiptIDFlag)
		if rcID == "" {
			msg := fmt.Sprintf("task %s requires workspace_write permission: fresh receipt required", taskID)
			env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeDenied, msg, "Pass --receipt-id <id>", "")
			if stderr != nil {
				_ = cli.WriteResponse(stderr, env, *jsonl)
			}
			return 1, &env, errors.New(msg)
		}
		rcDbPath, _ := receiptDatabasePath()
		if rcMgr, merr := receipt.NewReceiptManager(rcDbPath, nil); merr == nil {
			defer rcMgr.Close()
			rc, verr := rcMgr.VerifyReceipt(rcID)
			if verr != nil || (rc != nil && rc.Consumed) {
				msg := fmt.Sprintf("invalid receipt %s: write receipt expired or already consumed", rcID)
				env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeDenied, msg, "Issue a fresh receipt", "")
				if stderr != nil {
					_ = cli.WriteResponse(stderr, env, *jsonl)
				}
				return 1, &env, errors.New(msg)
			}
		}
		origPayload["receipt_id"] = rcID
	}

	newPayloadBytes, err := json.Marshal(origPayload)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeRuntime, err.Error(), "", "")
		return 1, &env, err
	}

	addDirs := []string{"."}
	if dirs, ok := origPayload["add_dirs"].([]any); ok {
		addDirs = nil
		for _, d := range dirs {
			if ds, ok := d.(string); ok {
				addDirs = append(addDirs, ds)
			}
		}
	}

	rootID := lin.rootTask.TaskID
	newReq := controlplane.SubmitTaskRequest{
		IdempotencyKey: fmt.Sprintf("%s#ladder-r%d", rootID, plan.RungIndex),
		Priority:       lin.rootTask.Priority,
		MaxAttempts:    lin.rootTask.MaxAttempts,
		ParentTaskID:   &rootID,
		Payload:        newPayloadBytes,
		Role:           plan.Role,
		Permission:     plan.Permission,
		Model:          plan.Model,
		Timeout:        timeout,
		AddDirs:        addDirs,
		OrchestratorID: lin.rootTask.OrchestratorID,
		WorktreeID:     lin.rootTask.WorktreeID,
		WorkerName:     lin.rootTask.WorkerName,
		SessionID:      lin.rootTask.SessionID,
	}

	newTask, err := store.SubmitTask(ctx, newReq)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "advance", *traceID, cli.CodeDenied, err.Error(), "Control plane refused submission", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, err
	}

	data := map[string]any{
		"root_task_id": rootID,
		"new_task_id":  newTask.TaskID,
		"rung_index":   plan.RungIndex,
		"action":       plan.Action,
		"role":         plan.Role,
		"permission":   plan.Permission,
		"model":        plan.Model,
		"effort":       plan.Effort,
		"reason":       plan.Reason,
	}

	env := cli.NewEnvelope("ladder", "advance", "", data)
	env.TraceID = *traceID

	if *jsonMode || *jsonl {
		if stdout != nil {
			_ = cli.WriteResponse(stdout, env, *jsonl)
		}
	} else if stdout != nil {
		fmt.Fprintf(stdout, "Advanced ladder for root %s to Rung %d: new task %s\n", rootID, plan.RungIndex, newTask.TaskID)
		fmt.Fprintf(stdout, "Action: %s | Model: %s | Effort: %s | Permission: %s\n", plan.Action, plan.Model, plan.Effort, plan.Permission)
	}

	return 0, &env, nil
}

func executeLadderGauges(ctx context.Context, args []string, stdout, stderr io.Writer) (int, *cli.Envelope, error) {
	fs := flag.NewFlagSet("gauges", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	classFlag := fs.String("class", "", "filter gauges for specific class")
	dbFlag := fs.String("db", "", "path to controlplane database")
	telemFlag := fs.String("telemetry-db", "", "path to telemetry database")

	posArgs, err := parseWithPositional(fs, args)
	if err != nil {
		env := cli.NewErrorEnvelope("ladder", "gauges", *traceID, cli.CodeUsage, err.Error(), "", "")
		return 2, &env, err
	}

	filterClass := *classFlag
	if filterClass == "" && len(posArgs) > 0 {
		filterClass = posArgs[0]
	}

	telemDB := *telemFlag
	if telemDB == "" {
		telemDB = telemetryDatabasePath()
	}
	telemEvents, _ := ladder.LoadTelemetryEvents(ctx, telemDB)

	var tasks []*controlplane.Task
	dbPath := *dbFlag
	if dbPath == "" {
		dbPath, _ = databasePath()
	}
	if dbPath != "" {
		if store, err := controlplane.NewControlPlane(dbPath, nil); err == nil {
			defer store.Close()
			tasks, _ = store.ListTasks(ctx, controlplane.TaskFilter{})
		}
	}

	report := ladder.ComputeGauges(telemEvents, tasks, filterClass)

	data := map[string]any{
		"filter_class":     report.FilterClass,
		"pass_rates":       report.PassRates,
		"escalation_rates": report.EscalationRates,
		"hitl":             report.HITL,
	}

	env := cli.NewEnvelope("ladder", "gauges", "", data)
	env.TraceID = *traceID

	if *jsonMode || *jsonl {
		if stdout != nil {
			_ = cli.WriteResponse(stdout, env, *jsonl)
		}
		return 0, &env, nil
	}

	if stdout != nil {
		fmt.Fprintln(stdout, "Quality Ladder Gauges (P4 Pass/Escalation Rates & P6 HITL Metric)")
		if report.FilterClass != "" {
			fmt.Fprintf(stdout, "Filter: class=%s\n", report.FilterClass)
		}
		fmt.Fprintln(stdout, "\nPass Rates per (Class, Effort):")
		if len(report.PassRates) == 0 {
			fmt.Fprintln(stdout, "  (no event data)")
		} else {
			for _, g := range report.PassRates {
				fmt.Fprintf(stdout, "  - [%s, %s]: pass_rate=%.2f%% (%d passed / %d total)\n",
					g.Class, g.Effort, g.PassRate*100, g.PassCount, g.TotalCount)
			}
		}

		fmt.Fprintln(stdout, "\nEscalation Rates per Class (rungs fired / tasks):")
		if len(report.EscalationRates) == 0 {
			fmt.Fprintln(stdout, "  (no event data)")
		} else {
			for _, g := range report.EscalationRates {
				fmt.Fprintf(stdout, "  - [%s]: escalation_rate=%.2f (%d rungs fired / %d tasks)\n",
					g.Class, g.EscalationRate, g.RungsFired, g.TasksCount)
			}
		}

		fmt.Fprintln(stdout, "\nHITL System Metric:")
		fmt.Fprintf(stdout, "  - HITL Rate: %.2f%% (%d HITL packets / %d rounds)\n",
			report.HITL.HITLRate*100, report.HITL.HITLPackets, report.HITL.TotalRounds)
	}

	return 0, &env, nil
}

func runLadder(args []string) {
	ctx := context.Background()
	code, _, _ := executeLadder(ctx, args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}
