package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/harness"
)

// runSubmit queues one durable task through the control plane after validating
// it against the security harness.
func runSubmit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = jsonMode
	key := fs.String("idempotency-key", "", "unique idempotency key for this submission")
	model := fs.String("model", "", "target worker model (defaults to first ready provider's first model)")
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
	fs.Var(&addDirs, "add-dir", "additional allowed directory (repeatable, defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		exitUsage("submit", "", *traceID, err.Error(), "Check 'g8s submit --help'", *jsonl)
	}

	var prompt string
	if *promptFile != "" {
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

	// Validate request against security harness gatekeeper
	if err := harness.ValidateRequest(prompt, *role, *permission, dirs, *skipPermissions, *receiptID); err != nil {
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
		"prompt":     prompt,
		"model":      *model,
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
