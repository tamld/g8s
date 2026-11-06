package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/routing"
)

// runGates records or lists CI workflow run outcomes for the gate history ledger (D9-S1, #714).
func runGates(args []string) {
	code := runGatesWithIO(args, os.Stdin, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func runGatesWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		exitUsageWithWriter(stderr, "gates", "", "", "usage: g8s gates <record|list> [flags]", "Use 'g8s gates record' or 'g8s gates list'", false)
		return 2
	}
	switch args[0] {
	case "record":
		return runGatesRecordWithIO(args[1:], stdin, stdout, stderr)
	case "list":
		return runGatesListWithIO(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, "Usage: g8s gates <record|list> [flags]")
		fmt.Fprintln(stdout, "\nSubcommands:")
		fmt.Fprintln(stdout, "  record  Append workflow run rows from JSON (stdin or --file) to gate_ci_run_events.jsonl")
		fmt.Fprintln(stdout, "  list    Print all recorded CI gate run rows from the ledger")
		return 0
	default:
		exitUsageWithWriter(stderr, "gates", args[0], "", fmt.Sprintf("unknown gates subcommand %q (valid: record, list)", args[0]), "Run 'g8s gates help'", false)
		return 2
	}
}

func runGatesRecordWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gates record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	stateDir := fs.String("state-dir", "", "state directory root (default: G8S_STATE_DIR or platform default)")
	filePath := fs.String("file", "", "read JSON from file instead of stdin")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		exitUsageWithWriter(stderr, "gates", "record", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	var raw []byte
	var rerr error
	if strings.TrimSpace(*filePath) != "" {
		raw, rerr = os.ReadFile(*filePath)
	} else {
		raw, rerr = io.ReadAll(stdin)
	}
	if rerr != nil {
		exitRuntimeWithWriter(stderr, "gates", "record", *traceID, cli.CodeIO, rerr, "", *jsonl)
		return 1
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		exitUsageWithWriter(stderr, "gates", "record", *traceID, "no JSON input (pipe JSON or use --file)", "Example: gh run list --limit 1 --json ... | g8s gates record", *jsonl)
		return 2
	}

	var recs []routing.CIGateRunRecord
	if err := json.Unmarshal(raw, &recs); err != nil {
		var one routing.CIGateRunRecord
		if err2 := json.Unmarshal(raw, &one); err2 != nil {
			exitUsageWithWriter(stderr, "gates", "record", *traceID, fmt.Sprintf("JSON must be an object or array of {workflow,name,conclusion,sha,started_at}: %v", err), "", *jsonl)
			return 2
		}
		recs = []routing.CIGateRunRecord{one}
	}

	root := strings.TrimSpace(*stateDir)
	if root == "" {
		root = pathutil.DefaultStateDir()
	}
	if err := routing.RecordCIGateRuns(root, recs); err != nil {
		exitRuntimeWithWriter(stderr, "gates", "record", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}

	data := map[string]any{
		"state_dir": root,
		"ledger":    routing.CIGateHistoryPath(root),
		"appended":  len(recs),
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("ci_gate_history_record", "gates", "record", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
			exitRuntimeWithWriter(stderr, "gates", "record", *traceID, cli.CodeIO, err, "", *jsonl)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "recorded %d row(s) -> %s\n", len(recs), routing.CIGateHistoryPath(root))
	return 0
}

func runGatesListWithIO(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gates list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	stateDir := fs.String("state-dir", "", "state directory root (default: G8S_STATE_DIR or platform default)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		exitUsageWithWriter(stderr, "gates", "list", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	root := strings.TrimSpace(*stateDir)
	if root == "" {
		root = pathutil.DefaultStateDir()
	}
	rows, err := routing.ReadCIGateRuns(root)
	if err != nil {
		exitRuntimeWithWriter(stderr, "gates", "list", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}

	if *jsonMode || *jsonl {
		data := map[string]any{
			"state_dir": root,
			"ledger":    routing.CIGateHistoryPath(root),
			"rows":      rows,
		}
		env := cli.NewEnvelope("ci_gate_history_list", "gates", "list", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
			exitRuntimeWithWriter(stderr, "gates", "list", *traceID, cli.CodeIO, err, "", *jsonl)
			return 1
		}
		return 0
	}
	for _, r := range rows {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", r.Workflow, r.Name, r.Conclusion, r.SHA, r.StartedAt)
	}
	return 0
}
