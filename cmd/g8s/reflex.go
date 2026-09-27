package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	g8scontext "github.com/tamld/g8s/internal/context"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/telemetry"
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
	noEnrich := fs.Bool("no-enrich", false, "skip Context Broker enrichment (legacy minimal request, #396)")
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
	// Context Broker pilot deployment point (L1 pre-mutation, #396): assemble
	// a ContextPacket from vault / telemetry / SOM and attach it. Fail-open:
	// any source failure degrades to fewer sections; --no-enrich forces the
	// legacy minimal request.
	if !*noEnrich {
		attachContextPacket(&req)
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

// attachContextPacket assembles the Context Broker packet (L1 pilot, #396)
// from the operator's local vault (memory.db), telemetry ledger, and the
// latest supervisor task state (SOM stand-in). Every source is
// independently failing — a broken source degrades the packet, never the
// triage.
func attachContextPacket(req *reflex.TriageRequest) {
	broker := g8scontext.NewBroker(g8scontext.Sources{
		VaultNotes:     vaultNotesSource(),
		RecentOutcomes: telemetryOutcomesSource(),
		SomPhase:       somPhaseSource(),
	}, func(event, detail string) {
		fmt.Fprintf(os.Stderr, "[warn] context broker: %s: %s\n", event, detail)
	})
	req.ContextPacket = broker.Assemble(context.Background())
}

func vaultNotesSource() func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		dbPath, err := databasePath()
		if err != nil {
			return nil, err
		}
		adapter, err := memory.NewLocalSQLiteMemoryAdapter(memory.AdapterOptions{
			DBPath: filepath.Join(filepath.Dir(dbPath), "memory.db"),
		})
		if err != nil {
			return nil, err
		}
		defer adapter.Close()
		entries, err := adapter.ListEntries(ctx, memory.MemoryFilter{Lifecycle: memory.LifecycleActive})
		if err != nil {
			return nil, err
		}
		var notes []string
		for _, e := range entries { // ranked newest-first by ListEntries; top-5 by broker fit
			notes = append(notes, fmt.Sprintf("[%s|%s] %s", e.Kind, e.Trust, e.Payload))
		}
		return notes, nil
	}
}

func telemetryOutcomesSource() func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		cfg := telemetry.DefaultTelemetryConfig()
		engine, err := telemetry.NewTelemetryEngine(cfg)
		if err != nil {
			return nil, err
		}
		defer engine.Close()
		events, err := engine.QueryEvents(ctx, telemetry.TraceFilter{Limit: 5})
		if err != nil {
			return nil, err
		}
		var out []string
		for _, ev := range events {
			out = append(out, fmt.Sprintf("%s %s %s", ev.EventType, ev.TaskID, ev.Error))
		}
		return out, nil
	}
}

func somPhaseSource() func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		dbPath, err := databasePath()
		if err != nil {
			return "", err
		}
		store, err := controlplane.NewControlPlane(dbPath, nil)
		if err != nil {
			return "", err
		}
		defer store.Close()
		rows, err := store.ListSupervisorTasks(ctx)
		if err != nil || len(rows) == 0 {
			return "", err
		}
		latest := rows[len(rows)-1]
		return string(latest.State), nil
	}
}
