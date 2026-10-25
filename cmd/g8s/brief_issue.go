package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/telemetry"
	"github.com/tamld/g8s/internal/vault"
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
	permission := fs.String("permission", "read_only", "dispatch permission class (read_only|workspace_write) — workspace_write requires a receipt reference")
	lane := fs.String("lane", "", "ALDC gate lane (0|D|R|F|S) — lanes F, R, S enforce Pre-Execution Research, CVE & Socratic Debate Gate (DELTA-35)")
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

	effectiveLane := *lane
	if effectiveLane == "" {
		effectiveLane = os.Getenv("G8S_GATE_LANE")
	}

	// L2 deterministic DoR floor (#398 + DELTA-35): mechanical, blocking, no Jev, no
	// broker dependency. A floor failure is a usage error naming every
	// failed check.
	if floor := brief.CheckDoRFloorForLane(*title, payload, dod, *permission, effectiveLane); !floor.OK {
		exitUsage("brief-issue", "dor-floor", *traceID, brief.FloorFailureError(floor).Error(), "Fix the listed DoR checks before issuing", *jsonl)
	}

	// L2 Jev-judged quality (#398, DELTA-22 r3): advisory only, gated on
	// broker availability. Never blocks issuance.
	if jevVerdict, jevErr := brief.ApplyJevQuality(*title, payload); jevErr != nil {
		fmt.Fprintf(os.Stderr, "[warn] brief-issue: %v\n", jevErr)
	} else if jevVerdict != "" {
		fmt.Fprintf(os.Stderr, "[info] brief-issue: Jev quality verdict: %s\n", jevVerdict)
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

	// #253 & #594 closed loop: when telemetry is enabled, surface spatially
	// relevant distilled negative patterns (with fallback to top patterns)
	// bounded by carrying capacity K_max (Hypothesis H1).
	if os.Getenv("G8S_TELEMETRY") == "1" {
		if section := preflightSection(title, payload); section != "" {
			payload += section
		}
	}

	b, err := brief.Issue(store, title, payload, dod, issuedBy, ttl)
	if err != nil {
		return brief.Brief{}, err
	}
	// L4 advisory skill routing (#398): deterministic keyword/manifest match
	// over the operator-local skill bank — display-only, zero enforcement,
	// silent degradation when the bank is absent. The envelope stays
	// flat-backward-compatible: brief fields at data.* plus one new
	// skill_suggestions key.
	data := map[string]any{}
	if raw, merr := json.Marshal(b); merr == nil {
		_ = json.Unmarshal(raw, &data)
	}
	if bank := brief.ScanSkillBank(skillBankDir()); bank != nil {
		data["skill_suggestions"] = brief.SuggestSkills(title, payload, bank, 3)
	}
	env := cli.NewEnvelope("brief", "brief-issue", "", data)
	env.TraceID = traceID
	if err := cli.WriteResponse(w, env, jsonl); err != nil {
		return brief.Brief{}, fmt.Errorf("format brief json: %w", err)
	}
	return b, nil
}

// skillBankDir resolves the operator-local skill bank (L4 advisory routing).
func skillBankDir() string {
	if p := os.Getenv("G8S_SKILL_BANK"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "skills")
}

// preflightSection builds the negative-pattern pre-flight section from the
// telemetry ledger (#253, #594, #633). Prioritizes path/prompt-scoped patterns via
// GetRelevantPatterns and falls back to QueryTopPatterns, capped at K_max
// (ResolveMaxActiveRules). Empty when telemetry is disabled, K_max == 0, or
// the ledger cannot be opened — brief issuance must never fail because of telemetry.
func preflightSection(title, payload string) string {
	maxK := vault.ResolveMaxActiveRules()
	if maxK <= 0 {
		return ""
	}
	cfg := telemetry.DefaultTelemetryConfig()
	if p := os.Getenv("G8S_TELEMETRY_DB"); p != "" {
		cfg.DBPath = p
	}
	eng, err := telemetry.NewTelemetryEngine(cfg)
	if err != nil {
		return ""
	}
	defer eng.Close()

	ctx := context.Background()
	patterns, err := eng.GetRelevantPatterns(ctx, "worker", title, payload)
	if err != nil || len(patterns) == 0 {
		patterns, err = eng.QueryTopPatterns(ctx, maxK)
		if err != nil || len(patterns) == 0 {
			return ""
		}
	}
	if len(patterns) > maxK {
		patterns = patterns[:maxK]
	}
	section, err := eng.InjectPreflightContext(ctx, &controlplane.BriefRow{}, patterns)
	if err != nil {
		return ""
	}
	return section
}
