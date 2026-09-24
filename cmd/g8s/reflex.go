package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/reflex"
)

// runReflex exposes the System-1 reflex gate (Jev / TypeSafe AI) as a
// first-class CLI (ADR-0020 §Decision 3, #329): mutation triage becomes
// enforce-code in scripts and CI instead of prompt-level policy.
//
//	g8s reflex triage --summary "..." --files "a.go,b.go" --allowed "pkg/*"
func runReflex(args []string) {
	if len(args) == 0 || args[0] != "triage" {
		exitUsage("reflex", strings.Join(args, " "), "", "unknown reflex subcommand; usage: g8s reflex triage [flags]", "g8s reflex triage --summary '...' --files 'internal/x/y.go' --allowed 'internal/x/*'", false)
		return
	}

	fs := flag.NewFlagSet("reflex triage", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	taskID := fs.String("task", "cli-reflex-triage", "mutation task id for the audit trail")
	filesFlag := fs.String("files", "", "comma-separated files the mutation will touch")
	summaryFlag := fs.String("summary", "", "one-line diff summary of the planned mutation")
	allowedFlag := fs.String("allowed", "", "comma-separated allowed path globs for the mutation")
	if err := fs.Parse(args[1:]); err != nil {
		exitUsage("reflex", "triage", *traceID, err.Error(), "", *jsonl)
	}
	if strings.TrimSpace(*summaryFlag) == "" {
		exitUsage("reflex", "triage", *traceID, "--summary is required to describe the planned mutation", "g8s reflex triage --summary 'raise test deadline' --files 'internal/runtime/verify_test.go'", *jsonl)
		return
	}

	gate := reflex.NewReflexGate()
	req := reflex.TriageRequest{
		TaskID:        *taskID,
		FilesModified: splitComma(*filesFlag),
		DiffSummary:   *summaryFlag,
		AllowedPaths:  splitComma(*allowedFlag),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	verdict, err := gate.TriageMutation(ctx, req)
	if err != nil {
		exitRuntime("reflex", "triage", *traceID, cli.CodeRuntime, err, "check TYPESAFE_API_KEY or network reachability", *jsonl)
		return
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("reflex_verdict", "reflex", "triage", verdict)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	pterm.DefaultHeader.WithFullWidth().Println("g8s Reflex Triage (System-1 sensor)")
	pterm.Info.Printf("Action: %s\n", verdict.Action)
	pterm.Info.Printf("Risk: %.2f | Breach: %.2f | Confidence: %.2f | Source: %s\n",
		verdict.RiskScore, verdict.BreachProb, verdict.Confidence, verdict.Signal.Source)
	pterm.Info.Printf("Latency: %dms | Fallback: %v\n", verdict.LatencyMs, verdict.IsFallback)
	pterm.Println(verdict.Reason)
	_ = fmt.Sprint()
}
