package main

// #395: `g8s memory list|revoke` — the operator-facing surface of the
// memory promotion gate (ADR-0023 v1). list shows entries with their
// lifecycle/trust labels; revoke bulk-revokes every entry promoted by a
// session and writes payload-hash tombstones (G1, monotone). Propose/promote
// is Brain-side only and deliberately has no CLI here.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/reflex"
)

func runMemory(args []string) {
	if len(args) == 0 {
		exitUsage("memory", "", "", "usage: g8s memory <list|revoke> [flags]", "Use 'g8s memory list' or 'g8s memory revoke --session <id>'", false)
	}
	sub := args[0]
	switch sub {
	case "list":
		runMemoryList(args[1:])
	case "revoke":
		runMemoryRevoke(args[1:])
	case "--help", "-h", "help":
		fmt.Println("Usage: g8s memory <list|revoke> [flags]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  list                     List memory entries with lifecycle/trust labels")
		fmt.Println("  revoke --session <id>    Bulk-revoke every entry promoted by a session (payload-hash tombstones, monotone)")
	default:
		exitUsage("memory", sub, "", fmt.Sprintf("unknown memory subcommand %q (valid: list, revoke)", sub), "", false)
	}
}

func openMemoryAdapter() *memory.LocalSQLiteMemoryAdapter {
	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("memory", "", "", cli.CodeIO, err, "", false)
	}
	adapter, err := memory.NewLocalSQLiteMemoryAdapter(memory.AdapterOptions{
		DBPath: filepath.Join(filepath.Dir(dbPath), "memory.db"),
	})
	if err != nil {
		exitRuntime("memory", "", "", cli.CodeRuntime, err, "", false)
	}
	// #411b: the promotion gate carries a real Jev sensor — promotion is
	// Brain-side only, and the gate must not be a pass-through.
	adapter.SetGateSensor(memorySensorAdapter(reflex.NewReflexGate().TriageMutation))
	return adapter
}

func runMemoryList(args []string) {
	fs := flag.NewFlagSet("memory list", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	session := fs.String("session", "", "filter by promoting session id")
	state := fs.String("state", "", "filter by lifecycle state (working|scratch|active|distilled|archived|revoked|doctrine)")
	kind := fs.String("kind", "", "filter by kind (fact|judgment|decision|procedure)")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "list", *traceID, err.Error(), "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	entries, err := adapter.ListEntries(context.Background(), memory.MemoryFilter{
		SessionID: *session,
		Lifecycle: memory.MemoryLifecycle(*state),
		Kind:      memory.MemoryKind(*kind),
	})
	if err != nil {
		exitRuntime("memory", "list", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_list", "memory", "list", map[string]any{"entries": entries, "count": len(entries)})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	for _, e := range entries {
		fmt.Printf("%s\t%s\t%s\t%s\tsalience=%d\t%s\n", e.ID, e.Lifecycle, e.Trust, e.Kind, e.Salience, e.Scope)
	}
	fmt.Printf("%d entries\n", len(entries))
}

func runMemoryRevoke(args []string) {
	fs := flag.NewFlagSet("memory revoke", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	session := fs.String("session", "", "revoke every entry promoted by this session id (required)")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "revoke", *traceID, err.Error(), "", *jsonl)
	}
	if *session == "" {
		exitUsage("memory", "revoke", *traceID, "--session <id> is required", "usage: g8s memory revoke --session <id>", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	n, err := adapter.RevokeBySession(context.Background(), *session)
	if err != nil {
		exitRuntime("memory", "revoke", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_revoke", "memory", "revoke", map[string]any{"session": *session, "revoked": n})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("revoked %d entries from session %s (tombstones written)\n", n, *session)
}

// memorySensorAdapter translates the reflex gate (Jev) into the memory
// promotion gate's sensor contract (#411b): naked payload in, allowlist
// verdict out. instant_kill → blocked; grant_receipt → clean; anything else
// (including escalate_hitl) is an unrecognized verdict — the promotion gate
// fails CLOSED on those. Transport errors fail OPEN unverified upstream
// (ADR-0023 DEGRADED) with a broker_failure sink event.
func memorySensorAdapter(triage func(ctx context.Context, req reflex.TriageRequest) (reflex.TriageVerdict, error)) memory.GateSensor {
	return func(nakedPayload string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		verdict, err := triage(ctx, reflex.TriageRequest{
			TaskID:      "memory-promotion-gate",
			DiffSummary: nakedPayload,
		})
		if err != nil {
			return "", err
		}
		switch verdict.Action {
		case reflex.ActionInstantKill:
			return "blocked", nil
		case reflex.ActionGrantReceipt:
			return "clean", nil
		default:
			return "", fmt.Errorf("unrecognized sensor action %q", verdict.Action)
		}
	}
}
