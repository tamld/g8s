package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
)

func runAudit(args []string) {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list":
		runAuditList(args)
	case "help", "-h", "--help":
		fmt.Println("Usage: g8s audit [list] [--actor <actor>] [--action <action>] [--subject <id>] [--since <rfc3339>] [--limit <n>] [--json]")
	default:
		exitUsage("audit", sub, "", fmt.Sprintf("unknown audit subcommand %q (expected list)", sub), "Run 'g8s audit --help'", false)
	}
}

func runAuditList(args []string) {
	fs := flag.NewFlagSet("audit list", flag.ContinueOnError)
	actorCommon, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	actionFlag := fs.String("action", "", "filter by audit action (e.g. task.submit, task.claim, receipt.issue)")
	subjectFlag := fs.String("subject", "", "filter by subject ID (task_id or receipt_id)")
	sinceFlag := fs.String("since", "", "filter by RFC3339 timestamp lower bound")
	limitFlag := fs.Int("limit", 50, "maximum audit records to return (1..500)")

	var actorFilter string
	for i := 0; i < len(args); i++ {
		if args[i] == "--actor" && i+1 < len(args) {
			actorFilter = args[i+1]
		} else if strings.HasPrefix(args[i], "--actor=") {
			actorFilter = strings.TrimPrefix(args[i], "--actor=")
		}
	}

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("audit", "list", *traceID, err.Error(), "Run 'g8s audit list --help'", *jsonl)
	}
	_ = actorCommon

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("audit", "list", *traceID, cli.CodeIO, err, "Check G8S_STATE_DIR", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("audit", "list", *traceID, cli.CodeRuntime, err, "Run 'g8s doctor'", *jsonl)
	}
	defer store.Close()

	records, err := store.ListAccessAudit(context.Background(), controlplane.AccessAuditFilter{
		Actor:     actorFilter,
		Action:    *actionFlag,
		SubjectID: *subjectFlag,
		Since:     *sinceFlag,
		Limit:     *limitFlag,
	})
	if err != nil {
		exitRuntime("audit", "list", *traceID, cli.CodeRuntime, err, "Run 'g8s doctor'", *jsonl)
	}
	if records == nil {
		records = []controlplane.AccessAuditRecord{}
	}

	payload := map[string]any{
		"count":   len(records),
		"records": records,
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("audit_log", "audit", "list", payload)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			os.Exit(1)
		}
		return
	}
	for _, r := range records {
		fmt.Printf("[%s] actor=%s action=%s subject=%s instance=%s\n", r.Timestamp, r.Actor, r.Action, r.SubjectID, r.Instance)
	}
}
