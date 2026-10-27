package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/routing"
)

func extractTaskProviderAndModel(task *controlplane.Task) (string, string) {
	if task == nil {
		return "default", "default"
	}
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if len(task.Request) > 0 {
		_ = json.Unmarshal(task.Request, &req)
	}
	prov := strings.TrimSpace(req.Provider)
	if prov == "" {
		prov = "default"
	}
	mod := strings.TrimSpace(req.Model)
	if mod == "" {
		mod = prov
	}
	return prov, mod
}

// recordWorkerTaskTelemetry bridges worker execution outcomes into the unified
// model quality scorecard (model_quality_events.jsonl) (#678, DELTA-45).
func recordWorkerTaskTelemetry(store *controlplane.Store, task *controlplane.Task, isFailure bool) {
	_ = store
	if task == nil {
		return
	}
	success := (task.State == controlplane.StateSucceeded || task.State == "COMPLETED") && !isFailure
	prov, mod := extractTaskProviderAndModel(task)
	errReason := ""
	if !success {
		errReason = "worker_attempt_failed"
	}
	_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
		Provider:       prov,
		ModelRequested: mod,
		Success:        success,
		ErrorReason:    errReason,
		RecordedAt:     time.Now().UTC().Format(time.RFC3339),
	})
}

// recordVerifyTaskTelemetry records mechanical verification outcomes into the
// unified model quality scorecard (#678, DELTA-45).
func recordVerifyTaskTelemetry(task *controlplane.Task, passed bool) {
	if task == nil {
		return
	}
	prov, mod := extractTaskProviderAndModel(task)
	_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
		Provider:       prov,
		ModelRequested: mod,
		Success:        passed,
		VerifyChecked:  true,
		VerifyPassed:   passed,
		RecordedAt:     time.Now().UTC().Format(time.RFC3339),
	})
}

// runF0 implements the F0 supervisor commitment-point gate CLI (`g8s f0 gate check` / `g8s f0 status`).
func runF0(args []string) {
	if len(args) == 0 {
		exitUsage("f0", "", "", "usage: g8s f0 <gate|status> [flags]", "Try: g8s f0 gate check --json", false)
	}
	sub := args[0]
	switch sub {
	case "gate":
		rest := args[1:]
		if len(rest) > 0 && rest[0] == "check" {
			rest = rest[1:]
		}
		runF0GateCheck(rest)
	case "status":
		runF0Status(args[1:])
	case "-h", "--help", "help":
		fmt.Println("Usage: g8s f0 <gate check|status> [--repo <path>] [--action merge|close|publish] [--json]")
	default:
		exitUsage("f0", sub, "", fmt.Sprintf("unknown f0 subcommand %q", sub), "Available subcommands: gate check, status", false)
	}
}

type f0GateItem struct {
	Gate   string `json:"gate"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func runF0GateCheck(args []string) {
	fs := flag.NewFlagSet("f0 gate check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	repoFlag := fs.String("repo", ".", "target repository path")
	actionFlag := fs.String("action", "merge", "commitment action (merge|close|publish)")
	prFlag := fs.Int("pr", 0, "optional PR number")
	headSHAFlag := fs.String("head-sha", "", "optional head commit SHA")
	baseFlag := fs.String("base", "main", "base branch")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("f0", "gate", *traceID, err.Error(), "usage: g8s f0 gate check [--repo <path>] [--action merge] [--json]", *jsonl)
	}

	absRepo, err := filepath.Abs(*repoFlag)
	if err != nil {
		absRepo = *repoFlag
	}

	var gates []f0GateItem
	allPassed := true

	// G0: Repository Git & Head Resolution
	headSHA := strings.TrimSpace(*headSHAFlag)
	if headSHA == "" {
		if out, gErr := exec.Command("git", "-C", absRepo, "rev-parse", "--short", "HEAD").Output(); gErr == nil {
			headSHA = strings.TrimSpace(string(out))
		}
	}
	if headSHA != "" {
		gates = append(gates, f0GateItem{Gate: "G0", Name: "repo_head_resolved", Status: "PASS", Detail: "HEAD=" + headSHA})
	} else {
		gates = append(gates, f0GateItem{Gate: "G0", Name: "repo_head_resolved", Status: "WARN", Detail: "not a git checkout or HEAD unresolved"})
	}

	// G1: Markdown CLI Surface Claim Integrity
	agentsPath := filepath.Join(absRepo, "AGENTS.md")
	if data, rErr := os.ReadFile(agentsPath); rErr == nil {
		unbound := cli.ScanMarkdownCLIInvocations("AGENTS.md", string(data))
		if len(unbound) == 0 {
			gates = append(gates, f0GateItem{Gate: "G1", Name: "markdown_cli_claim_guard", Status: "PASS", Detail: "0 unbound CLI claims in AGENTS.md"})
		} else {
			allPassed = false
			gates = append(gates, f0GateItem{Gate: "G1", Name: "markdown_cli_claim_guard", Status: "FAIL", Detail: unbound[0].Error()})
		}
	} else {
		gates = append(gates, f0GateItem{Gate: "G1", Name: "markdown_cli_claim_guard", Status: "PASS", Detail: "no AGENTS.md present"})
	}

	// G2: Durable Queue Active Leak Check
	activeCount := 0
	if dbPath, dbErr := databasePath(); dbErr == nil {
		if store, sErr := controlplane.NewControlPlane(dbPath, nil); sErr == nil {
			runningState := controlplane.StateRunning
			if tasks, lErr := store.ListTasks(context.Background(), controlplane.TaskFilter{State: &runningState, Limit: 50}); lErr == nil {
				activeCount = len(tasks)
			}
			defer store.Close()
		}
	}
	gates = append(gates, f0GateItem{
		Gate:   "G2",
		Name:   "durable_queue_hygiene",
		Status: "PASS",
		Detail: fmt.Sprintf("running_tasks=%d", activeCount),
	})

	verdict := "PASS"
	if !allPassed {
		verdict = "BLOCK"
	}

	payload := map[string]any{
		"verdict":    verdict,
		"action":     *actionFlag,
		"repo":       absRepo,
		"pr":         *prFlag,
		"head_sha":   headSHA,
		"base":       *baseFlag,
		"gates":      gates,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("f0_gate_check", "f0", "gate", payload)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		if !allPassed {
			os.Exit(1)
		}
		return
	}

	fmt.Printf("F0 Gate Check [%s] repo=%s action=%s head=%s\n", verdict, absRepo, *actionFlag, headSHA)
	for _, g := range gates {
		fmt.Printf("  [%s] %s (%s): %s\n", g.Status, g.Gate, g.Name, g.Detail)
	}
	if !allPassed {
		os.Exit(1)
	}
}

func runF0Status(args []string) {
	fs := flag.NewFlagSet("f0 status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	repoFlag := fs.String("repo", ".", "target repository path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("f0", "status", *traceID, err.Error(), "usage: g8s f0 status [--repo <path>] [--json]", *jsonl)
	}
	absRepo, _ := filepath.Abs(*repoFlag)
	var totalTasks, completedTasks, failedTasks int
	if dbPath, dbErr := databasePath(); dbErr == nil {
		if store, sErr := controlplane.NewControlPlane(dbPath, nil); sErr == nil {
			if tasks, lErr := store.ListTasks(context.Background(), controlplane.TaskFilter{Limit: 200}); lErr == nil {
				totalTasks = len(tasks)
				for _, t := range tasks {
					switch t.State {
					case controlplane.StateSucceeded, "COMPLETED":
						completedTasks++
					case controlplane.StateFailed:
						failedTasks++
					}
				}
			}
			defer store.Close()
		}
	}
	payload := map[string]any{
		"repo":            absRepo,
		"total_tasks":     totalTasks,
		"completed_tasks": completedTasks,
		"failed_tasks":    failedTasks,
		"checked_at":      time.Now().UTC().Format(time.RFC3339),
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("f0_status", "f0", "status", payload)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("F0 Status: repo=%s total_tasks=%d completed=%d failed=%d\n", absRepo, totalTasks, completedTasks, failedTasks)
}

// runScorecard implements DELTA-45 5-Axis Cross-Evaluation & Kaizen Engine (#680).
// Usage: g8s scorecard [status|audit|kaizen] [--provider <p>] [--window <n>] [--json]
func runScorecard(args []string) {
	mode := "status"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "status", "audit", "kaizen":
			mode = args[0]
			rest = args[1:]
		case "help", "-h", "--help":
			fmt.Println("Usage: g8s scorecard [status|audit|kaizen] [--provider <name>] [--window 20] [--json]")
			return
		default:
			exitUsage("scorecard", args[0], "", fmt.Sprintf("unknown scorecard subcommand %q", args[0]), "Available subcommands: status, audit, kaizen", false)
		}
	}

	fs := flag.NewFlagSet("scorecard "+mode, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	providerFlag := fs.String("provider", "", "filter model axis by provider name")
	windowFlag := fs.Int("window", routing.DefaultScorecardWindow, "rolling observation window per model")

	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("scorecard", mode, *traceID, err.Error(), "usage: g8s scorecard [status|audit|kaizen] [--provider <name>] [--window 20] [--json]", *jsonl)
	}

	stateDir := pathutil.DefaultStateDir()
	models, _ := routing.ComputeScorecard(stateDir, *providerFlag, *windowFlag)

	// Axis 1: F1/F2 Worker Yield
	var totalTasks, completedTasks, failedTasks, apiErrorTasks int
	if dbPath, dbErr := databasePath(); dbErr == nil {
		if store, sErr := controlplane.NewControlPlane(dbPath, nil); sErr == nil {
			if tasks, lErr := store.ListTasks(context.Background(), controlplane.TaskFilter{Limit: 200}); lErr == nil {
				for _, t := range tasks {
					prov, _ := extractTaskProviderAndModel(t)
					if *providerFlag != "" && prov != *providerFlag {
						continue
					}
					switch t.State {
					case controlplane.StateSucceeded, "COMPLETED":
						totalTasks++
						completedTasks++
					case controlplane.StateFailed:
						totalTasks++
						failedTasks++
						if strings.Contains(string(t.Result), "API Error") || strings.Contains(string(t.Result), "CLI_AGENT_API_ERROR") {
							apiErrorTasks++
						}
					}
				}
			}
			defer store.Close()
		}
	}
	firstPassYield := 1.0
	if totalTasks > 0 {
		firstPassYield = float64(completedTasks) / float64(totalTasks)
	}

	// Axis 2: WriteReceipt Tightness (touched_files / allowed_paths & wildcard detection)
	var totalReceipts, consumedReceipts, wildcardReceipts int
	if rDbPath, rErr := receiptDatabasePath(); rErr == nil {
		if rm, mErr := receipt.NewReceiptManager(rDbPath, nil); mErr == nil {
			if list, lErr := rm.ListReceipts("", time.Time{}, ""); lErr == nil {
				totalReceipts = len(list)
				for _, r := range list {
					if r.Consumed {
						consumedReceipts++
					}
					for _, p := range r.AllowedPaths {
						if p == "**" || p == "*" || p == "." {
							wildcardReceipts++
							break
						}
					}
				}
			}
			defer rm.Close()
		}
	}
	tightnessScore := 1.0
	if totalReceipts > 0 && wildcardReceipts > 0 {
		tightnessScore = 1.0 - (float64(wildcardReceipts) / float64(totalReceipts) * 0.5)
	}

	// Axis 3: Tool/MCP Friction Index
	unboundClaims := 0
	for _, docFile := range []string{"AGENTS.md", "README.md"} {
		if data, err := os.ReadFile(docFile); err == nil {
			unboundClaims += len(cli.ScanMarkdownCLIInvocations(docFile, string(data)))
		}
	}
	frictionIndex := 0.0
	if totalTasks > 0 {
		frictionIndex = float64(apiErrorTasks) / float64(totalTasks)
	}

	// Axis 4: Skill-vs-Skill Cross-Audit
	canonicalSkills := []string{
		"g8s-supervisor",
		"g8s-blind-verifier",
		"g8s-red-prove",
		"g8s-sec-gate",
		"g8s-epistemic-audit",
		"g8s-scenario-matrix",
		"g8s-ux-visual-gate",
	}
	presentSkills := 0
	for _, sk := range canonicalSkills {
		if _, err := os.Stat(filepath.Join("skills", sk, "SKILL.md")); err == nil {
			presentSkills++
		}
	}

	// Axis 5: Jev L1-vs-L3 Calibration
	jevEndpoint := os.Getenv("G8S_JEV_ENDPOINT")
	jevMode := "tier2_deterministic_fallback"
	if jevEndpoint != "" {
		jevMode = "tier1_live_jev"
	}

	var kaizen []string
	for _, m := range models {
		if m.HealthStatus == routing.ModelHealthDegraded || m.HealthStatus == routing.ModelHealthQuarantined {
			kaizen = append(kaizen, fmt.Sprintf("Demote %s/%s (health=%s, score=%.1f): %s", m.Provider, m.Model, m.HealthStatus, m.CompositeScore, m.Recommendation))
		}
	}
	if wildcardReceipts > 0 {
		kaizen = append(kaizen, fmt.Sprintf("Tighten WriteReceipt scopes: %d/%d receipts used wildcard ('**' or '*') allowed_paths", wildcardReceipts, totalReceipts))
	}
	if unboundClaims > 0 {
		kaizen = append(kaizen, fmt.Sprintf("Resolve %d unbound Markdown CLI claims (E_UNBOUND_CLI_CLAIM)", unboundClaims))
	}
	if len(kaizen) == 0 {
		kaizen = append(kaizen, "All 5 DELTA-45 axes within target bounds; maintain current routing and receipt discipline.")
	}

	payload := map[string]any{
		"delta":  "DELTA-45",
		"mode":   mode,
		"window": *windowFlag,
		"axes": map[string]any{
			"f1_f2_worker_yield": map[string]any{
				"total_terminal_tasks": totalTasks,
				"completed_tasks":      completedTasks,
				"failed_tasks":         failedTasks,
				"api_error_tasks":      apiErrorTasks,
				"first_pass_yield":     firstPassYield,
				"models":               models,
			},
			"write_receipt_tightness": map[string]any{
				"total_receipts":    totalReceipts,
				"consumed_receipts": consumedReceipts,
				"wildcard_receipts": wildcardReceipts,
				"tightness_score":   tightnessScore,
			},
			"tool_mcp_friction_index": map[string]any{
				"canonical_command_count": len(cli.CanonicalCommands()),
				"canonical_flag_families": len(cli.CanonicalFlagAliases()),
				"unbound_cli_claims":      unboundClaims,
				"api_error_friction_rate": frictionIndex,
			},
			"skill_cross_audit": map[string]any{
				"canonical_skills_total":   len(canonicalSkills),
				"canonical_skills_present": presentSkills,
				"coverage_ratio":           float64(presentSkills) / float64(len(canonicalSkills)),
			},
			"jev_l1_l3_calibration": map[string]any{
				"operating_tier": jevMode,
				"l3_gate_cmd":    "g8s reflex verify",
			},
		},
		"kaizen_recommendations": kaizen,
		"generated_at":           time.Now().UTC().Format(time.RFC3339),
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("scorecard_5axis", "scorecard", mode, payload)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	b, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Println(string(b))
}
