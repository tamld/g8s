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
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/vault"
)

func runMemory(args []string) {
	if len(args) == 0 {
		exitUsage("memory", "", "", "usage: g8s memory <list|revoke|propose|supersede|recall|promote|verify-anchors|export|import> [flags]", "Use 'g8s memory --help' for subcommand usage", false)
	}
	sub := args[0]
	switch sub {
	case "list":
		runMemoryList(args[1:])
	case "revoke":
		runMemoryRevoke(args[1:])
	case "propose":
		runMemoryPropose(args[1:])
	case "supersede":
		runMemorySupersede(args[1:])
	case "recall":
		runMemoryRecall(args[1:])
	case "promote":
		runMemoryPromote(args[1:])
	case "verify-anchors":
		runMemoryVerifyAnchors(args[1:])
	case "export":
		runMemoryExport(args[1:])
	case "import":
		runMemoryImport(args[1:])
	case "--help", "-h", "help":
		fmt.Println("Usage: g8s memory <list|revoke|propose|supersede|recall|promote|verify-anchors|export|import> [flags]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  list                     List memory entries with lifecycle/trust labels")
		fmt.Println("  revoke --session <id>    Bulk-revoke every entry promoted by a session (payload-hash tombstones, monotone)")
		fmt.Println("  propose                  Propose a contextual memory entry with optional --preconditions k=v,...")
		fmt.Println("  supersede                Supersede an older memory entry with a newer decision")
		fmt.Println("  recall                   Recall active non-superseded entries matching --context k=v,...")
		fmt.Println("  promote                  Promote a memory entry to the self-contained Knowledge Vault with mechanical anchors")
		fmt.Println("  verify-anchors           Verify Knowledge Vault file/symbol/test anchors against the workspace (Citation Rot check)")
		fmt.Println("  export                   Export portable Knowledge Vault records to JSONL (e.g. .g8s/knowledge-vault.jsonl)")
		fmt.Println("  import                   Import portable Knowledge Vault records from JSONL")
	default:
		exitUsage("memory", sub, "", fmt.Sprintf("unknown memory subcommand %q (valid: list, revoke, propose, supersede, recall, promote, verify-anchors, export, import)", sub), "", false)
	}
}

func openMemoryAdapter() *memory.LocalSQLiteMemoryAdapter {
	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("memory", "", "", cli.CodeIO, err, "", false)
	}
	baseDir := filepath.Dir(dbPath)
	v, _ := vault.NewVault(filepath.Join(baseDir, "vault.db"), nil)
	adapter, err := memory.NewLocalSQLiteMemoryAdapter(memory.AdapterOptions{
		DBPath: filepath.Join(baseDir, "memory.db"),
		Vault:  v,
	})
	if err != nil {
		if v != nil {
			v.Close()
		}
		exitRuntime("memory", "", "", cli.CodeRuntime, err, "", false)
	}
	// #411b / #685: the promotion gate carries a dedicated Jev memory sensor
	// that evaluates payloads both online and in deterministic offline fallback.
	gate := reflex.NewReflexGate()
	adapter.SetGateSensor(func(nakedPayload string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return gate.EvaluateMemoryEntry(ctx, nakedPayload)
	})
	return adapter
}

func parseContextKeyValuePairs(raw string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(strings.TrimSpace(raw), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			k := strings.TrimSpace(kv[0])
			v := strings.TrimSpace(kv[1])
			if k != "" {
				out[k] = v
			}
		}
	}
	return out
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

func runMemoryPropose(args []string) {
	fs := flag.NewFlagSet("memory propose", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	id := fs.String("id", "", "entry ID (required)")
	kind := fs.String("kind", "decision", "entry kind (fact|judgment|decision|procedure)")
	scope := fs.String("scope", "session", "entry scope (task|session|global)")
	session := fs.String("session", "f0", "session ID")
	payload := fs.String("payload", "", "entry payload (required)")
	preconds := fs.String("preconditions", "", "comma-separated key=value contextual preconditions")
	validUntil := fs.Float64("valid-until", 0, "optional unix timestamp expiration")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "propose", *traceID, err.Error(), "", *jsonl)
	}
	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*payload) == "" {
		exitUsage("memory", "propose", *traceID, "--id and --payload are required", "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}
	entry := &memory.MemoryEntry{
		ID:            strings.TrimSpace(*id),
		Kind:          memory.MemoryKind(*kind),
		Scope:         memory.MemoryScope(*scope),
		SessionID:     strings.TrimSpace(*session),
		PromotedBy:    "f0",
		Payload:       *payload,
		Preconditions: parseContextKeyValuePairs(*preconds),
		ValidUntil:    *validUntil,
	}
	if err := adapter.ProposeEntry(context.Background(), entry); err != nil {
		exitRuntime("memory", "propose", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_propose", "memory", "propose", entry)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("proposed %s (%s)\n", entry.ID, entry.Lifecycle)
}

func runMemorySupersede(args []string) {
	fs := flag.NewFlagSet("memory supersede", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	oldID := fs.String("old", "", "old entry ID to supersede (required)")
	newID := fs.String("new", "", "new entry ID (required)")
	kind := fs.String("kind", "decision", "entry kind (fact|judgment|decision|procedure)")
	scope := fs.String("scope", "session", "entry scope (task|session|global)")
	session := fs.String("session", "f0", "session ID")
	payload := fs.String("payload", "", "new entry payload (required)")
	preconds := fs.String("preconditions", "", "comma-separated key=value contextual preconditions")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "supersede", *traceID, err.Error(), "", *jsonl)
	}
	if strings.TrimSpace(*oldID) == "" || strings.TrimSpace(*newID) == "" || strings.TrimSpace(*payload) == "" {
		exitUsage("memory", "supersede", *traceID, "--old, --new, and --payload are required", "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}
	newEntry := &memory.MemoryEntry{
		ID:            strings.TrimSpace(*newID),
		Kind:          memory.MemoryKind(*kind),
		Scope:         memory.MemoryScope(*scope),
		SessionID:     strings.TrimSpace(*session),
		PromotedBy:    "f0",
		Payload:       *payload,
		Preconditions: parseContextKeyValuePairs(*preconds),
	}
	if err := adapter.SupersedeEntry(context.Background(), strings.TrimSpace(*oldID), newEntry); err != nil {
		exitRuntime("memory", "supersede", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_supersede", "memory", "supersede", map[string]any{"superseded": *oldID, "entry": newEntry})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("superseded %s -> %s\n", *oldID, newEntry.ID)
}

func runMemoryRecall(args []string) {
	fs := flag.NewFlagSet("memory recall", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	ctxStr := fs.String("context", "", "comma-separated key=value active operational context")
	queryStr := fs.String("query", "", "optional lexical query for RRF hybrid ranking (#684)")
	limitFlag := fs.Int("limit", 10, "maximum entries to return")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "recall", *traceID, err.Error(), "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}
	activeCtx := parseContextKeyValuePairs(*ctxStr)
	now := float64(time.Now().UnixNano()) / 1e9
	if strings.TrimSpace(*queryStr) != "" {
		scored, err := adapter.RecallHybridRRF(context.Background(), *queryStr, nil, activeCtx, *limitFlag, now)
		if err != nil {
			exitRuntime("memory", "recall", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		entries := make([]*memory.MemoryEntry, len(scored))
		for i, s := range scored {
			entries[i] = s.Entry
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("memory_recall", "memory", "recall", map[string]any{"entries": entries, "ranked": scored, "count": len(entries)})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
			return
		}
		for _, s := range scored {
			fmt.Printf("%s\trrf=%.4f\t%s\t%s\n", s.Entry.ID, s.RRFScore, s.Entry.Kind, s.Entry.Payload)
		}
		return
	}
	entries, err := adapter.RecallContextualEntries(context.Background(), activeCtx, now)
	if err != nil {
		exitRuntime("memory", "recall", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_recall", "memory", "recall", map[string]any{"entries": entries, "count": len(entries)})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	for _, e := range entries {
		fmt.Printf("%s\t%s\t%s\n", e.ID, e.Kind, e.Payload)
	}
}

func runMemoryPromote(args []string) {
	fs := flag.NewFlagSet("memory promote", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	entryID := fs.String("id", "", "memory entry ID to promote (required)")
	knowID := fs.String("knowledge-id", "", "knowledge vault record ID (defaults to entry ID)")
	title := fs.String("title", "", "knowledge record title (required)")
	problem := fs.String("problem", "", "causality problem description (required)")
	tradeOff := fs.String("trade-off", "", "causality trade-off / invariant (defaults to entry payload)")
	pkgName := fs.String("package", "", "spatial anchor package name")
	filePath := fs.String("file", "", "spatial anchor source file path (required)")
	symbol := fs.String("symbol", "", "spatial anchor symbol name")
	testFile := fs.String("test-file", "", "forensic anchor test file path (required)")
	testCase := fs.String("test-case", "", "forensic anchor test function name")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "promote", *traceID, err.Error(), "", *jsonl)
	}
	if strings.TrimSpace(*entryID) == "" || strings.TrimSpace(*title) == "" || strings.TrimSpace(*problem) == "" || strings.TrimSpace(*filePath) == "" || strings.TrimSpace(*testFile) == "" {
		exitUsage("memory", "promote", *traceID, "--id, --title, --problem, --file, and --test-file are required", "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}
	kID := strings.TrimSpace(*knowID)
	if kID == "" {
		kID = strings.TrimSpace(*entryID)
	}
	rec := vault.DistillationRecord{
		ID:     kID,
		Title:  strings.TrimSpace(*title),
		Status: "DOCTRINE",
		Causality: vault.CausalityAnchor{
			Problem:  strings.TrimSpace(*problem),
			TradeOff: strings.TrimSpace(*tradeOff),
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package: strings.TrimSpace(*pkgName),
			File:    strings.TrimSpace(*filePath),
			Symbol:  strings.TrimSpace(*symbol),
		},
		ForensicVerification: vault.ForensicAnchor{
			TestFile: strings.TrimSpace(*testFile),
			TestCase: strings.TrimSpace(*testCase),
		},
	}
	stored, err := adapter.PromoteToVault(context.Background(), strings.TrimSpace(*entryID), rec)
	if err != nil {
		exitRuntime("memory", "promote", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_promote", "memory", "promote", stored)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("promoted %s -> Knowledge Vault %s (DOCTRINE)\n", *entryID, stored.ID)
}

func runMemoryVerifyAnchors(args []string) {
	fs := flag.NewFlagSet("memory verify-anchors", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	rootDir := fs.String("root", ".", "workspace root directory to check file/symbol/test anchors against")
	markStale := fs.Bool("mark-stale", true, "transition records with broken anchors to STALE_ANCHOR")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "verify-anchors", *traceID, err.Error(), "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}
	stale, err := adapter.Vault().VerifyAnchors(context.Background(), *rootDir, *markStale)
	if err != nil {
		exitRuntime("memory", "verify-anchors", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_verify_anchors", "memory", "verify-anchors", map[string]any{"stale_ids": stale, "stale_count": len(stale)})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("verified anchors against %s: %d stale\n", *rootDir, len(stale))
}

func runMemoryExport(args []string) {
	fs := flag.NewFlagSet("memory export", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	filePath := fs.String("file", ".g8s/knowledge-vault.jsonl", "target JSONL file path")
	repoID := fs.String("repo-id", "", "optional repository filter")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "export", *traceID, err.Error(), "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}
	n, err := adapter.Vault().ExportPortableJSONL(context.Background(), *filePath, *repoID)
	if err != nil {
		exitRuntime("memory", "export", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_export", "memory", "export", map[string]any{"file": *filePath, "exported": n})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("exported %d knowledge records to %s\n", n, *filePath)
}

func runMemoryImport(args []string) {
	fs := flag.NewFlagSet("memory import", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	filePath := fs.String("file", ".g8s/knowledge-vault.jsonl", "source JSONL file path")
	if err := fs.Parse(args); err != nil {
		exitUsage("memory", "import", *traceID, err.Error(), "", *jsonl)
	}
	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}
	n, err := adapter.Vault().ImportPortableJSONL(context.Background(), *filePath)
	if err != nil {
		exitRuntime("memory", "import", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("memory_import", "memory", "import", map[string]any{"file": *filePath, "imported": n})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("imported %d knowledge records from %s\n", n, *filePath)
}
