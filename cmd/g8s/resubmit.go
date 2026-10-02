package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/settings"
)

// loadRetryOpts extracts auto-retry configuration settings or env overrides.
func loadRetryOpts(mgr *settings.Manager) controlplane.ResubmitOpts {
	opts := controlplane.ResubmitOpts{
		Enabled:    false,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	if mgr == nil {
		var err error
		mgr, err = settings.NewManager("")
		if err != nil {
			mgr = nil
		}
	}

	if mgr != nil {
		if val, ok := mgr.Get("auto_retry_enabled"); ok && val != nil {
			switch v := val.(type) {
			case bool:
				opts.Enabled = v
			case string:
				opts.Enabled = strings.ToLower(strings.TrimSpace(v)) == "true" || v == "1"
			}
		}
		if val, ok := mgr.Get("auto_retry_max_per_task"); ok && val != nil {
			switch v := val.(type) {
			case int:
				opts.MaxPerTask = v
			case float64:
				opts.MaxPerTask = int(v)
			case string:
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					opts.MaxPerTask = n
				}
			}
		}
		if val, ok := mgr.Get("auto_retry_max_per_hour"); ok && val != nil {
			switch v := val.(type) {
			case int:
				opts.MaxPerHour = v
			case float64:
				opts.MaxPerHour = int(v)
			case string:
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					opts.MaxPerHour = n
				}
			}
		}
	}

	// Environment variable overrides for testing and containerized execution
	if env := os.Getenv("G8S_AUTO_RETRY_ENABLED"); env != "" {
		opts.Enabled = strings.ToLower(strings.TrimSpace(env)) == "true" || env == "1"
	}
	if env := os.Getenv("G8S_AUTO_RETRY_MAX_PER_TASK"); env != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(env)); err == nil {
			opts.MaxPerTask = n
		}
	}
	if env := os.Getenv("G8S_AUTO_RETRY_MAX_PER_HOUR"); env != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(env)); err == nil {
			opts.MaxPerHour = n
		}
	}

	return opts
}

// extractTaskPermission extracts the permission string from a Task request.
func extractTaskPermission(t *controlplane.Task) string {
	if t == nil || len(t.Request) == 0 {
		return "read_only"
	}
	var req struct {
		Permission string          `json:"permission"`
		Payload    json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(t.Request, &req); err == nil {
		if req.Permission != "" {
			return req.Permission
		}
		if len(req.Payload) > 0 {
			var inner struct {
				Permission string `json:"permission"`
			}
			if err := json.Unmarshal(req.Payload, &inner); err == nil && inner.Permission != "" {
				return inner.Permission
			}
		}
	}
	return "read_only"
}

// executeResubmit runs the core logic of g8s resubmit with injectable I/O.
func executeResubmit(ctx context.Context, args []string, stdout, stderr io.Writer) (int, *cli.Envelope, error) {
	fs := flag.NewFlagSet("resubmit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor

	taskFlag := fs.String("task", "", "task ID to resubmit")
	taskIDFlag := fs.String("task-id", "", "task ID to resubmit (alias)")
	reasonFlag := fs.String("reason", "", "reason for resubmission")
	receiptIDFlag := fs.String("receipt-id", "", "fresh write receipt ID (required if original task used workspace_write)")
	dbFlag := fs.String("db", "", "path to control-plane database")

	if err := fs.Parse(args); err != nil {
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeUsage, err.Error(), "", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 2, &env, err
	}

	taskID := *taskFlag
	if taskID == "" {
		taskID = *taskIDFlag
	}
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		msg := "usage: g8s resubmit --task <id> [--reason <text>] [--receipt-id <id>]"
		hint := "Provide a task ID via --task <id>"
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeUsage, msg, hint, "")
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
			env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeIO, err.Error(), "Failed to resolve database path", "")
			if stderr != nil {
				_ = cli.WriteResponse(stderr, env, *jsonl)
			}
			return 1, &env, err
		}
	}

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeRuntime, err.Error(), "Failed to open controlplane store", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, err
	}
	defer store.Close()

	orig, err := store.GetTask(ctx, taskID)
	if err != nil || orig == nil {
		msg := fmt.Sprintf("task %s not found", taskID)
		hint := "Verify the task ID exists in the queue"
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeNotFound, msg, hint, "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, errors.New(msg)
	}

	if orig.State != controlplane.StateFailed {
		msg := fmt.Sprintf("task %s is not in FAILED state (current state: %s)", taskID, orig.State)
		hint := "Only tasks in terminal FAILED state can be resubmitted"
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeInvalid, msg, hint, "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, errors.New(msg)
	}

	lastErr := ""
	if orig.LastError != nil {
		lastErr = *orig.LastError
	}
	class := controlplane.ClassifyRetryable(string(orig.Result), lastErr)
	if class != controlplane.RetryClassRetryable {
		msg := fmt.Sprintf("task %s is not retryable (class: %s)", taskID, class)
		hint := "Only transient failure classes (timeout, interrupted, spawn-failure, provider-transport) may be resubmitted"
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeDenied, msg, hint, "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, errors.New(msg)
	}

	mgr, _ := settings.NewManager("")
	opts := loadRetryOpts(mgr)
	if !opts.Enabled {
		msg := "auto-retry is disabled"
		hint := "Enable auto-retry via 'g8s config set auto_retry_enabled true'"
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeDenied, msg, hint, "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, errors.New(msg)
	}

	perm := extractTaskPermission(orig)
	if perm == "workspace_write" {
		receiptID := strings.TrimSpace(*receiptIDFlag)
		if receiptID == "" {
			msg := fmt.Sprintf("task %s requires workspace_write permission: fresh receipt required", taskID)
			hint := "Issue a receipt with 'g8s receipt issue' and pass --receipt-id <id>"
			env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeDenied, msg, hint, "")
			if stderr != nil {
				_ = cli.WriteResponse(stderr, env, *jsonl)
			}
			return 1, &env, errors.New(msg)
		}

		rcDbPath, rerr := receiptDatabasePath()
		if rerr == nil {
			if rcMgr, merr := receipt.NewReceiptManager(rcDbPath, nil); merr == nil {
				defer rcMgr.Close()
				if _, verr := rcMgr.VerifyReceipt(receiptID); verr != nil {
					msg := fmt.Sprintf("invalid receipt %s: %v", receiptID, verr)
					hint := "A valid, active write receipt is required for workspace_write resubmissions"
					env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeDenied, msg, hint, "")
					if stderr != nil {
						_ = cli.WriteResponse(stderr, env, *jsonl)
					}
					return 1, &env, errors.New(msg)
				}
			}
		}
	}

	newTaskID, err := store.ResubmitTask(ctx, taskID, opts)
	if err != nil {
		env := cli.NewErrorEnvelope("resubmit", "", *traceID, cli.CodeDenied, err.Error(), "Task resubmission refused by control plane", "")
		if stderr != nil {
			_ = cli.WriteResponse(stderr, env, *jsonl)
		}
		return 1, &env, err
	}

	data := map[string]any{
		"orig_task_id": taskID,
		"new_task_id":  newTaskID,
		"task_id":      newTaskID,
		"permission":   perm,
	}
	if *reasonFlag != "" {
		data["reason"] = *reasonFlag
	}
	if *receiptIDFlag != "" {
		data["receipt_id"] = *receiptIDFlag
	}
	if newTask, err := store.GetTask(ctx, newTaskID); err == nil && newTask != nil {
		data["task"] = newTask
	}

	env := cli.NewEnvelope("resubmit", "resubmit", "", data)
	env.TraceID = *traceID

	if *jsonMode || *jsonl {
		if stdout != nil {
			_ = cli.WriteResponse(stdout, env, *jsonl)
		}
	} else if stdout != nil {
		fmt.Fprintf(stdout, "Task %s resubmitted as new task %s\n", taskID, newTaskID)
	}

	return 0, &env, nil
}

// runResubmit executes the CLI resubmit command.
func runResubmit(args []string) {
	ctx := context.Background()
	code, _, _ := executeResubmit(ctx, args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}
