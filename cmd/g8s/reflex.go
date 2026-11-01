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
	"github.com/tamld/g8s/internal/vault"
)

// runReflex exposes the System-1 reflex gate (Jev / TypeSafe AI) as a
// first-class CLI (ADR-0020 §Decision 3, #329): mutation triage becomes
// enforce-code in scripts and CI instead of prompt-level policy.
//
//	g8s reflex triage --summary "..." --files "a.go,b.go" --allowed "pkg/*"
func runReflex(args []string) {
	if len(args) > 0 && args[0] == "verify" {
		runReflexVerify(args[1:])
		return
	}
	if len(args) == 0 || args[0] != "triage" {
		exitUsage("reflex", strings.Join(args, " "), "", "unknown reflex subcommand; usage: g8s reflex <triage|verify> [flags]", "g8s reflex triage --summary '...' --files 'internal/x/y.go' --allowed 'internal/x/*'", false)
		return
	}

	fs := flag.NewFlagSet("reflex triage", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	taskID := fs.String("task", "cli-reflex-triage", "mutation task id for the audit trail")
	fs.StringVar(taskID, "task-id", "cli-reflex-triage", "alias for --task")
	filesFlag := fs.String("files", "", "comma-separated files the mutation will touch")
	fs.StringVar(filesFlag, "paths", "", "alias for --files")
	fs.StringVar(filesFlag, "path", "", "alias for --files")
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

// runReflexVerify implements the L3 post-run Output Quality & Memory Promotion Gate (#678).
func runReflexVerify(args []string) {
	fs := flag.NewFlagSet("reflex verify", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	taskID := fs.String("task", "cli-reflex-verify", "task ID for L3 post-run verification")
	fs.StringVar(taskID, "task-id", "cli-reflex-verify", "alias for --task")
	excerptFlag := fs.String("output", "", "output excerpt or summary to assess")
	fs.StringVar(excerptFlag, "summary", "", "alias for --output")
	shapeFlag := fs.String("shape", "analysis", "expected output shape (analysis, patch, test_report)")
	exitCodeFlag := fs.Int("exit-code", 0, "worker process exit code")
	durationFlag := fs.Int("duration-sec", 1, "worker execution duration in seconds")
	noEnrich := fs.Bool("no-enrich", false, "skip Context Broker enrichment (#684)")
	if err := fs.Parse(args); err != nil {
		exitUsage("reflex", "verify", *traceID, err.Error(), "", *jsonl)
	}

	gate := reflex.NewReflexGate()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := reflex.OutputQualityRequest{
		TaskID:        *taskID,
		OutputExcerpt: *excerptFlag,
		ExpectedShape: *shapeFlag,
		ExitCode:      *exitCodeFlag,
		DurationSec:   *durationFlag,
	}
	if !*noEnrich {
		req.ContextPacket = assembleContextPacket(nil, *excerptFlag)
	}
	sig, err := gate.EmitOutputQualitySignal(ctx, req)
	if err != nil {
		exitRuntime("reflex", "verify", *traceID, cli.CodeRuntime, err, "", *jsonl)
		return
	}
	verdict := reflex.EvaluateOutputQuality(sig)
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("reflex_l3_verdict", "reflex", "verify", verdict)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("L3 Verdict: action=%s quality=%.2f confidence=%.2f fallback=%v reason=%s\n",
		verdict.Action, verdict.Quality, verdict.Confidence, sig.IsFallback, verdict.Reason)
}

// attachContextPacket assembles the Context Broker packet (L1 pilot, #396)
// from the operator's local vault (memory.db), telemetry ledger, and the
// latest supervisor task state (SOM stand-in). Every source is
// independently failing — a broken source degrades the packet, never the
// triage.
func attachContextPacket(req *reflex.TriageRequest) {
	req.ContextPacket = assembleContextPacket(req.FilesModified, req.DiffSummary)
}

func assembleContextPacket(filesModified []string, query string) *g8scontext.ContextPacket {
	broker := g8scontext.NewBroker(g8scontext.Sources{
		VaultNotes:     vaultNotesSource(filesModified, query),
		RecentOutcomes: telemetryOutcomesSource(),
		WorldSnippets:  g8scontext.WorldSnippetsFromFile,
		SomPhase:       somPhaseSource(),
	}, func(event, detail string) {
		fmt.Fprintf(os.Stderr, "[warn] context broker: %s: %s\n", event, detail)
	})
	return broker.Assemble(context.Background())
}

func vaultNotesSource(filesModified []string, query string) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		dbPath, err := databasePath()
		if err != nil {
			return nil, err
		}
		baseDir := filepath.Dir(dbPath)
		var notes []string

		// 1. Spatial & Doctrine Knowledge Vault notes (#684)
		if v, vErr := vault.NewVault(filepath.Join(baseDir, "vault.db"), nil); vErr == nil {
			defer v.Close()
			seenRec := make(map[string]bool)
			var matchedRecs []vault.DistillationRecord
			for _, f := range filesModified {
				if recs, mErr := v.MatchSpatial(ctx, f, 5); mErr == nil {
					for _, r := range recs {
						if !seenRec[r.ID] {
							seenRec[r.ID] = true
							matchedRecs = append(matchedRecs, r)
						}
					}
				}
			}
			for _, st := range []string{"DOCTRINE", "APPLIED"} {
				status := st
				if recs, lErr := v.List(ctx, vault.VaultFilter{Status: &status, Limit: 5, IncludeShared: true}); lErr == nil {
					for _, r := range recs {
						if !seenRec[r.ID] && len(matchedRecs) < 5 {
							seenRec[r.ID] = true
							matchedRecs = append(matchedRecs, r)
						}
					}
				}
			}
			notes = append(notes, vault.FormatJITWhisper(matchedRecs)...)
		}

		// 2. Contextual RRF Memory entries (#684)
		adapter, err := memory.NewLocalSQLiteMemoryAdapter(memory.AdapterOptions{
			DBPath: filepath.Join(baseDir, "memory.db"),
		})
		if err != nil {
			return notes, nil
		}
		defer adapter.Close()
		scored, err := adapter.RecallHybridRRF(ctx, query, nil, nil, 5, 0)
		if err != nil {
			return notes, nil
		}
		for _, item := range scored {
			e := item.Entry
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
