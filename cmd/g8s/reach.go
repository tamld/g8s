package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/reach"
)

type reachPayload struct {
	Channel    string          `json:"channel"`
	Action     string          `json:"action"`
	Backend    string          `json:"backend"`
	Provenance string          `json:"provenance"`
	Body       string          `json:"body"`
	URL        string          `json:"url"`
	FetchedAt  string          `json:"fetched_at"`
	BodySHA256 string          `json:"body_sha256"`
	Attempts   []reach.Attempt `json:"attempts"`
}

func runReach(args []string) {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		printReachUsage()
		return
	}

	// Sugar: g8s reach read <url>
	if len(args) > 0 && args[0] == "read" {
		runReachCommand(reach.ChannelWeb, reach.ActionRead, args[1:])
		return
	}

	if len(args) < 2 {
		exitUsage("reach", "", "", "missing channel and action (or use 'g8s reach read <url>')", "Run 'g8s reach --help'", false)
	}
	channel := reach.Channel(args[0])
	action := reach.Action(args[1])
	runReachCommand(channel, action, args[2:])
}

func runReachCommand(channel reach.Channel, action reach.Action, args []string) {
	fs := flag.NewFlagSet("reach", flag.ContinueOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	urlFlag := fs.String("url", "", "target URL for the read fetch")

	fs.SetOutput(os.Stdout)
	flagArgs, posArgs := partitionReachArgs(args)
	if err := fs.Parse(append(flagArgs, posArgs...)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("reach", string(action), *traceID, err.Error(), "Run 'g8s reach --help'", *jsonl)
	}

	targetURL := strings.TrimSpace(*urlFlag)
	if targetURL == "" && len(fs.Args()) > 0 {
		targetURL = strings.TrimSpace(fs.Args()[0])
	}
	if targetURL == "" {
		exitUsage("reach", string(action), *traceID, "missing URL (positional or --url)", "Run 'g8s reach read <url>' or 'g8s reach <channel> read --url <url>'", *jsonl)
	}

	req := reach.Request{URL: targetURL}
	ledger := reach.NewHealthLedger()
	_ = ledger.Load()
	defer func() { _ = ledger.Save() }()
	chain := reach.Chain{Orderer: ledger, Recorder: ledger}
	resp, attempts, err := chain.Execute(context.Background(), channel, action, req)
	if err != nil {
		exitRuntime("reach", string(action), *traceID, cli.CodeRuntime, err, "Verify URL, channel, and upstream tool availability (gh for github)", *jsonl)
	}

	fetchedAt := time.Now().UTC().Format(time.RFC3339)
	data := reachPayload{
		Channel:    string(channel),
		Action:     string(action),
		Backend:    resp.Provenance,
		Provenance: resp.Provenance,
		Body:       resp.Body,
		URL:        req.URL,
		FetchedAt:  fetchedAt,
		BodySHA256: reach.DigestBody(resp.Body),
		Attempts:   attempts,
	}

	if *jsonMode || *jsonl || cli.DefaultSelector() != "" {
		env := cli.NewEnvelope("reach", "reach", string(action), data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	fmt.Print(resp.Body)
}

func printReachUsage() {
	fmt.Println(`Usage:
  g8s reach read <url> [--json]
  g8s reach <channel> <action> --url <url> [--json]

Channels (S1): web, github. Actions: read (GET-only, read-only).`)
}

func partitionReachArgs(args []string) (flags, positional []string) {
	valueFlags := map[string]bool{
		"-url": true, "--url": true,
		"-actor": true, "--actor": true,
		"-trace-id": true, "--trace-id": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := a
			if eq := strings.Index(name, "="); eq != -1 {
				name = name[:eq]
			}
			if valueFlags[name] && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return flags, positional
}
