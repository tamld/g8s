package main

// #395: `g8s memory list|revoke` — the operator-facing surface of the
// memory promotion gate (ADR-0023 v1). list shows entries with their
// lifecycle/trust labels; revoke bulk-revokes every entry promoted by a
// session and writes payload-hash tombstones (G1). Propose/promote is
// Brain-side only and deliberately has no CLI here.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/memory"
)

func runMemory(args []string) {
	if len(args) == 0 {
		exitUsage("memory", "", "", "usage: g8s memory <list|revoke> [flags]", "Use 'g8s memory list' or 'g8s memory revoke --session <id>'", false)
	}
	sub := args[0]
	fs := flag.NewFlagSet("memory "+sub, flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	switch sub {
	case "list":
		runMemoryList(fs, args[1:], *traceID, *jsonMode, *jsonl)
	case "revoke":
		runMemoryRevoke(fs, args[1:], *traceID, *jsonMode, *jsonl)
	case "--help", "-h", "help":
		fmt.Println("Usage: g8s memory <list|revoke> [flags]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  list                     List memory entries with lifecycle/trust labels")
		fmt.Println("  revoke --session <id>    Bulk-revoke every entry promoted by a session (tombstones by payload hash)")
	default:
		exitUsage("memory", sub, *traceID, fmt.Sprintf("unknown memory subcommand %q (valid: list, revoke)", sub), "", *jsonl)
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
	return adapter
}

func runMemoryList(fs *flag.FlagSet, args []string, traceID string, jsonMode, jsonl bool) {
	session := fs.String("session", "", "filter by promoting session id")
	state := fs.String("state", "", "filter by lifecycle state (working|scratch|active|distilled|archived|revoked|doctrine)")
	kind := fs.String("kind", "", "filter by kind (fact|judgment|decision|procedure)")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "list", traceID, err.Error(), "", jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	entries, err := adapter.ListEntries(context.Background(), memory.MemoryFilter{
		SessionID: *session,
		Lifecycle: memory.MemoryLifecycle(*state),
		Kind:      memory.MemoryKind(*kind),
	})
	if err != nil {
		exitRuntime("memory", "list", traceID, cli.CodeRuntime, err, "", jsonl)
	}
	if jsonMode || jsonl {
		env := cli.NewEnvelope("memory_list", "memory", "", map[string]any{"entries": entries, "count": len(entries)})
		env.TraceID = traceID
		_ = cli.WriteResponse(os.Stdout, env, jsonl)
		return
	}
	for _, e := range entries {
		fmt.Printf("%s\t%s\t%s\t%s\tsalience=%d\t%s\n", e.ID, e.Lifecycle, e.Trust, e.Kind, e.Salience, e.Scope)
	}
	fmt.Printf("%d entries\n", len(entries))
}

func runMemoryRevoke(fs *flag.FlagSet, args []string, traceID string, jsonMode, jsonl bool) {
	session := fs.String("session", "", "revoke every entry promoted by this session id (required)")
	keep := fs.Bool("keep-tombstones", true, "write payload-hash tombstones (G1, monotone)")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "revoke", traceID, err.Error(), "", jsonl)
	}
	if *session == "" {
		exitUsage("memory", "revoke", traceID, "--session <id> is required", "usage: g8s memory revoke --session <id>", jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	n, err := adapter.RevokeBySession(context.Background(), *session)
	if err != nil {
		exitRuntime("memory", "revoke", traceID, cli.CodeRuntime, err, "", jsonl)
	}
	_ = keep // v1: tombstones are always written — the flag exists to keep the surface stable; un-revoke is operator-only
	if jsonMode || jsonl {
		env := cli.NewEnvelope("memory_revoke", "memory", "", map[string]any{"session": *session, "revoked": n})
		env.TraceID = traceID
		_ = cli.WriteResponse(os.Stdout, env, jsonl)
		return
	}
	fmt.Printf("revoked %d entries from session %s (tombstones written)\n", n, *session)
}
