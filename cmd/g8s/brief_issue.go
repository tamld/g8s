package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
)

func runBriefIssue(args []string) {
	fs := flag.NewFlagSet("brief-issue", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = jsonMode
	title := fs.String("title", "", "brief title (required)")
	payloadFile := fs.String("payload-file", "", "path to markdown payload file")
	payloadStr := fs.String("payload", "", "inline markdown payload")
	dodFile := fs.String("dod-file", "", "path to markdown DoD file")
	dodStr := fs.String("dod", "", "inline markdown DoD")
	issuedBy := fs.String("issued-by", "", "issuer identity (defaults to --actor)")
	ttlStr := fs.String("ttl", "2h", "time-to-live duration (e.g. 2h, 30m, 3600s)")
	if err := fs.Parse(args); err != nil {
		exitUsage("brief-issue", "", *traceID, err.Error(), "", *jsonl)
	}

	if *title == "" {
		exitUsage("brief-issue", "", *traceID, "brief-issue requires --title", "Provide --title <string>", *jsonl)
	}

	payload := *payloadStr
	if *payloadFile != "" {
		data, err := os.ReadFile(*payloadFile)
		if err != nil {
			exitRuntime("brief-issue", "", *traceID, cli.CodeIO, err, "Failed to read payload file", *jsonl)
		}
		payload = string(data)
	}
	if payload == "" {
		exitUsage("brief-issue", "", *traceID, "brief-issue requires --payload-file or --payload", "Provide either --payload-file or --payload", *jsonl)
	}

	dod := *dodStr
	if *dodFile != "" {
		data, err := os.ReadFile(*dodFile)
		if err != nil {
			exitRuntime("brief-issue", "", *traceID, cli.CodeIO, err, "Failed to read DoD file", *jsonl)
		}
		dod = string(data)
	}
	if dod == "" {
		exitUsage("brief-issue", "", *traceID, "brief-issue requires --dod-file or --dod", "Provide either --dod-file or --dod", *jsonl)
	}

	ttl, err := time.ParseDuration(*ttlStr)
	if err != nil {
		exitRuntime("brief-issue", "", *traceID, cli.CodeInvalid, err, "Use valid duration format (e.g. 2h, 30m)", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("brief-issue", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("brief-issue", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	effectiveIssuer := *issuedBy
	if effectiveIssuer == "" {
		effectiveIssuer = *actor
	}
	if effectiveIssuer == "" {
		effectiveIssuer = "sisyphus"
	}

	_, err = executeBriefIssue(os.Stdout, store, *title, payload, dod, effectiveIssuer, ttl, *traceID, *jsonl)
	if err != nil {
		exitRuntime("brief-issue", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
}

func executeBriefIssue(w io.Writer, store *controlplane.Store, title, payload, dod, issuedBy string, ttl time.Duration, extra ...any) (brief.Brief, error) {
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

	b, err := brief.Issue(store, title, payload, dod, issuedBy, ttl)
	if err != nil {
		return brief.Brief{}, err
	}
	env := cli.NewEnvelope("brief", "brief-issue", "", b)
	env.TraceID = traceID
	if err := cli.WriteResponse(w, env, jsonl); err != nil {
		return brief.Brief{}, fmt.Errorf("format brief json: %w", err)
	}
	return b, nil
}
