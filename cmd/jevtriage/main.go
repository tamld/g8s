// Command jevtriage runs a mutation plan through the g8s System-1 reflex gate
// (Jev / TypeSafe AI) and prints the supervisor verdict as JSON.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/reflex"
)

func main() {
	taskID := flag.String("task", "supervisor-mutation", "mutation task id")
	files := flag.String("files", "", "comma-separated files to be modified")
	summary := flag.String("summary", "", "one-line diff summary")
	allowed := flag.String("allowed", "", "comma-separated allowed path globs")
	flag.Parse()
	if *summary == "" {
		fmt.Fprintln(os.Stderr, "usage: jevtriage -summary '...' -files 'a.go,b.go' -allowed 'pkg/*'")
		os.Exit(2)
	}
	gate := reflex.NewReflexGate()
	req := reflex.TriageRequest{TaskID: *taskID, FilesModified: split(*files), DiffSummary: *summary, AllowedPaths: split(*allowed)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	verdict, err := gate.TriageMutation(ctx, req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "triage error:", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(map[string]any{
		"action": verdict.Action, "risk": verdict.RiskScore, "breach": verdict.BreachProb,
		"confidence": verdict.Confidence, "latency_ms": verdict.LatencyMs,
		"source": verdict.Signal.Source, "fallback": verdict.IsFallback, "reason": verdict.Reason,
	}, "", "  ")
	fmt.Println(string(out))
	if verdict.Action == reflex.ActionInstantKill {
		os.Exit(3)
	}
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
