package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
)

func runBriefConsume(args []string) {
	fs := flag.NewFlagSet("brief-consume", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	_ = jsonMode
	id := fs.String("id", "", "brief ID to consume (required)")
	if err := fs.Parse(args); err != nil {
		exitUsage("brief-consume", "", *traceID, err.Error(), "", *jsonl)
	}

	if *id == "" {
		if fs.NArg() > 0 {
			*id = fs.Arg(0)
		} else {
			exitUsage("brief-consume", "", *traceID, "brief-consume requires --id <id>", "Provide --id <brief-id>", *jsonl)
		}
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("brief-consume", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("brief-consume", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	_, err = executeBriefConsume(os.Stdout, store, *id, *traceID, *jsonl)
	if err != nil {
		exitRuntime("brief-consume", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
}

func executeBriefConsume(w io.Writer, store *controlplane.Store, id string, extra ...any) (brief.Brief, error) {
	traceID := cli.GenerateTraceID()
	jsonl := false
	if len(extra) > 0 {
		if t, ok := extra[0].(string); ok && t != "" {
			traceID = t
		}
	}
	if len(extra) > 1 {
		if j, ok := extra[1].(bool); ok {
			jsonl = j
		}
	}

	b, err := brief.Consume(store, id)
	if err != nil {
		return brief.Brief{}, err
	}
	env := cli.NewEnvelope("brief", "brief-consume", "", b)
	env.TraceID = traceID
	if err := cli.WriteResponse(w, env, jsonl); err != nil {
		return brief.Brief{}, fmt.Errorf("format brief json: %w", err)
	}
	return b, nil
}
