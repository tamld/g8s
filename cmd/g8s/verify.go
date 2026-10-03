package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/verifier"
)

// runVerify runs verifier-class checks on a task (issue #515).
func runVerify(args []string) {
	code := runVerifyWithIO(args, os.Stdout, os.Stderr, "")
	if code != 0 {
		os.Exit(code)
	}
}

// runVerifyWithIO executes the verify command and writes output to stdout/stderr.
func runVerifyWithIO(args []string, stdout, stderr io.Writer, dbPathOverride string, verifierOpts ...verifier.Option) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	_ = jsonMode

	taskFlag := fs.String("task", "", "task ID to verify")
	asTaskFlag := fs.String("as-task", "", "caller task ID (self-grade guard)")

	if err := fs.Parse(args); err != nil {
		exitUsageWithWriter(stderr, "verify", "", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	taskID := *taskFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		exitUsageWithWriter(stderr, "verify", "", *traceID, "usage: g8s verify --task <id> [--as-task <id>]", "Provide a task ID", *jsonl)
		return 2
	}

	dbPath := dbPathOverride
	if dbPath == "" {
		var err error
		dbPath, err = databasePath()
		if err != nil {
			exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeIO, err, "", *jsonl)
			return 1
		}
	}

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		return 1
	}
	defer store.Close()

	ctx := context.Background()
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		return 1
	}
	if task == nil {
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeNotFound, fmt.Errorf("task %q not found", taskID), "Verify the task ID with 'g8s tasks'", *jsonl)
		return 1
	}

	target := verifier.TaskRef{
		ID: taskID,
	}

	caller := verifier.TaskRef{}
	if *asTaskFlag != "" {
		caller.ID = *asTaskFlag
	}

	// Extract receipt_id and allowed_paths from task payload/request
	var reqPayload struct {
		ReceiptID    string   `json:"receipt_id"`
		AllowedPaths []string `json:"allowed_paths"`
	}
	if len(task.Request) > 0 {
		_ = json.Unmarshal(task.Request, &reqPayload)
	}

	target.ReceiptID = reqPayload.ReceiptID
	target.AllowedPaths = reqPayload.AllowedPaths

	// If receipt_id is present and allowed_paths not embedded in request, resolve from
	// the sibling receipts.db — the canonical receipt ledger (the receipt CLI and the
	// controlplane both write receipts.db next to g8s.db; round-1 of issue #516 caught
	// verify reading g8s.db, where the table does not exist, and landing unregistered).
	if target.ReceiptID != "" && len(target.AllowedPaths) == 0 {
		rcDbPath := filepath.Join(filepath.Dir(dbPath), "receipts.db")
		db, err := sql.Open("sqlite", pathutil.SQLiteURI(rcDbPath, "mode=ro"))
		if err == nil {
			defer db.Close()
			var pathsJSON string
			if err := db.QueryRowContext(ctx, "SELECT allowed_paths_json FROM write_receipts WHERE receipt_id = ?", target.ReceiptID).Scan(&pathsJSON); err == nil {
				var paths []string
				if err := json.Unmarshal([]byte(pathsJSON), &paths); err == nil {
					target.AllowedPaths = paths
				}
			}
		}
	}

	// Run Verify
	v := verifier.NewVerifier(verifierOpts...)
	verdict, err := v.Verify(target, caller)
	if err != nil {
		// Self-grade refusal or typed error
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeInvalid, err, "", *jsonl)
		return 1
	}

	env := cli.NewEnvelope("verdict", "verify", taskID, verdict)
	env.TraceID = *traceID
	if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}

	// Hard failure exits 1
	if verdict.Status == verifier.StatusHard && verdict.Outcome == verifier.OutcomeFail {
		return 1
	}

	return 0
}

func exitUsageWithWriter(w io.Writer, cmd, sub, traceID, msg, hint string, jsonl bool) {
	if traceID == "" {
		traceID = cli.GenerateTraceID()
	}
	env := cli.NewErrorEnvelope(cmd, sub, traceID, cli.CodeUsage, msg, hint, "")
	_ = cli.WriteResponse(w, env, jsonl)
}

func exitRuntimeWithWriter(w io.Writer, cmd, sub, traceID, code string, err error, hint string, jsonl bool) {
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
	_ = cli.WriteResponse(w, env, jsonl)
}
