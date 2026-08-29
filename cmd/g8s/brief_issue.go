package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/controlplane"
)

func runBriefIssue(args []string) {
	fs := flag.NewFlagSet("brief-issue", flag.ExitOnError)
	title := fs.String("title", "", "brief title (required)")
	payloadFile := fs.String("payload-file", "", "path to markdown payload file")
	payloadStr := fs.String("payload", "", "inline markdown payload")
	dodFile := fs.String("dod-file", "", "path to markdown DoD file")
	dodStr := fs.String("dod", "", "inline markdown DoD")
	issuedBy := fs.String("issued-by", "sisyphus", "issuer identity")
	ttlStr := fs.String("ttl", "2h", "time-to-live duration (e.g. 2h, 30m, 3600s)")
	failIf(fs.Parse(args))

	if *title == "" {
		fmt.Fprintln(os.Stderr, "brief-issue requires --title")
		os.Exit(2)
	}

	payload := *payloadStr
	if *payloadFile != "" {
		data, err := os.ReadFile(*payloadFile)
		failIf(err)
		payload = string(data)
	}
	if payload == "" {
		fmt.Fprintln(os.Stderr, "brief-issue requires --payload-file or --payload")
		os.Exit(2)
	}

	dod := *dodStr
	if *dodFile != "" {
		data, err := os.ReadFile(*dodFile)
		failIf(err)
		dod = string(data)
	}
	if dod == "" {
		fmt.Fprintln(os.Stderr, "brief-issue requires --dod-file or --dod")
		os.Exit(2)
	}

	ttl, err := time.ParseDuration(*ttlStr)
	failIf(err)

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	_, err = executeBriefIssue(os.Stdout, store, *title, payload, dod, *issuedBy, ttl)
	failIf(err)
}

func executeBriefIssue(w io.Writer, store *controlplane.Store, title, payload, dod, issuedBy string, ttl time.Duration) (brief.Brief, error) {
	b, err := brief.Issue(store, title, payload, dod, issuedBy, ttl)
	if err != nil {
		return brief.Brief{}, err
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return brief.Brief{}, fmt.Errorf("format brief json: %w", err)
	}
	fmt.Fprintln(w, string(out))
	return b, nil
}
