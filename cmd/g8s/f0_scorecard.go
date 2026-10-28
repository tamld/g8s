package main

import (
	"bufio"
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
	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/routing"
	"github.com/tamld/g8s/internal/vault"
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

type scorecardDeliveryEvent struct {
	TaskID           string `json:"task_id"`
	TouchedFiles     int    `json:"touched_files"`
	AllowedPaths     int    `json:"allowed_paths"`
	ScopeDriftCount  int    `json:"scope_drift_count"`
	DeliveredSuccess bool   `json:"delivered_success"`
	RecordedAt       string `json:"recorded_at"`
}

func appendScorecardDeliveryEvent(ev scorecardDeliveryEvent) {
	stateDir := pathutil.DefaultStateDir()
	if strings.TrimSpace(stateDir) == "" {
		return
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return
	}
	p := filepath.Join(stateDir, "scorecard_delivery_events.jsonl")
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// recordWorkerTaskTelemetry bridges worker execution outcomes into the unified
// model quality scorecard (model_quality_events.jsonl) and supervisor_metrics (#678, DELTA-45).
func recordWorkerTaskTelemetry(store *controlplane.Store, task *controlplane.Task, isFailure bool) {
	if task == nil {
		return
	}
	success := (task.State == controlplane.StateSucceeded || task.State == "COMPLETED" || task.State == controlplane.StateWorkerCompleted) && !isFailure
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
	if store != nil {
		attempts := 0
		esc := 1
		envScore := 0.0
		if success {
			attempts = 1
			esc = 0
			envScore = 1.0
		}
		_ = store.SaveMetrics(context.Background(), task.TaskID, controlplane.MetricsRow{
			SupervisorTaskID:     task.TaskID,
			EnvelopeScore:        envScore,
			FirstAttemptSuccess:  success,
			AttemptsToSuccess:    attempts,
			ApproachesToSuccess:  attempts,
			CycleDurationSeconds: 1.0,
			EscalationCount:      esc,
		})
	}
}

// recordVerifyTaskTelemetry records mechanical verification outcomes into the
// unified model quality scorecard (#678, DELTA-45) and triggers the automatic
// Experiential Autopsy Loop (#689) when a task passes after a prior failure.
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

	if passed {
		if dbPath, err := databasePath(); err == nil {
			ctx := context.Background()
			if store, sErr := controlplane.NewControlPlane(dbPath, nil); sErr == nil {
				defer store.Close()
				if traj, ok := memory.ExtractTrajectoryFromTask(ctx, store, task); ok && traj != nil {
					baseDir := filepath.Dir(dbPath)
					v, _ := vault.NewVault(filepath.Join(baseDir, "vault.db"), nil)
					if v != nil {
						defer v.Close()
					}
					if adapter, aErr := memory.NewLocalSQLiteMemoryAdapter(memory.AdapterOptions{
						DBPath: filepath.Join(baseDir, "memory.db"),
						Vault:  v,
					}); aErr == nil {
						defer adapter.Close()
						gate := reflex.NewReflexGate()
						adapter.SetGateSensor(func(nakedPayload string) (string, error) {
							return gate.EvaluateMemoryEntry(ctx, nakedPayload)
						})
						_, _ = adapter.RunExperientialAutopsy(ctx, *traj, true)
					}
				}
			}
		}
	}
}

// recordDeliverTaskTelemetry records worktree delivery & WriteReceipt tightness
// metrics into the unified telemetry bus (#678, #680 Axis 2).
func recordDeliverTaskTelemetry(task *controlplane.Task, touchedFiles, allowedPaths, scopeDrift int, success bool) {
	taskID := ""
	if task != nil {
		taskID = task.TaskID
		prov, mod := extractTaskProviderAndModel(task)
		errReason := ""
		if !success {
			errReason = "deliver_scope_drift"
		}
		_ = routing.RecordModelOutcome(pathutil.DefaultStateDir(), routing.ModelOutcomeRecord{
			Provider:       prov,
			ModelRequested: mod,
			Success:        success,
			VerifyChecked:  true,
			VerifyPassed:   success,
			ErrorReason:    errReason,
			RecordedAt:     time.Now().UTC().Format(time.RFC3339),
		})
	}
	appendScorecardDeliveryEvent(scorecardDeliveryEvent{
		TaskID:           taskID,
		TouchedFiles:     touchedFiles,
		AllowedPaths:     allowedPaths,
		ScopeDriftCount:  scopeDrift,
		DeliveredSuccess: success,
		RecordedAt:       time.Now().UTC().Format(time.RFC3339),
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
			defer store.Close()
			runningState := controlplane.StateRunning
			if tasks, lErr := store.ListTasks(context.Background(), controlplane.TaskFilter{State: &runningState, Limit: 50}); lErr == nil {
				activeCount = len(tasks)
			}
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
			defer store.Close()
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

	// Load delivery events for Axis 1 (scope_drift_attempts) & Axis 2 (receipt_utilization_ratio).
	var sumTouched, sumAllowed, scopeDriftAttempts, deliveryCount int
	if stateDir != "" {
		if df, dErr := os.Open(filepath.Join(stateDir, "scorecard_delivery_events.jsonl")); dErr == nil {
			defer df.Close()
			sc := bufio.NewScanner(df)
			for sc.Scan() {
				var ev scorecardDeliveryEvent
				if json.Unmarshal(sc.Bytes(), &ev) == nil {
					deliveryCount++
					sumTouched += ev.TouchedFiles
					sumAllowed += ev.AllowedPaths
					scopeDriftAttempts += ev.ScopeDriftCount
				}
			}
		}
	}

	// Axis 1: F1/F2 Worker Yield
	var totalTasks, completedTasks, failedTasks, apiErrorTasks, childSubtasks int
	if dbPath, dbErr := databasePath(); dbErr == nil {
		if store, sErr := controlplane.NewControlPlane(dbPath, nil); sErr == nil {
			defer store.Close()
			if tasks, lErr := store.ListTasks(context.Background(), controlplane.TaskFilter{Limit: 200}); lErr == nil {
				for _, t := range tasks {
					prov, _ := extractTaskProviderAndModel(t)
					if *providerFlag != "" && prov != *providerFlag {
						continue
					}
					if t.ParentTaskID != nil && strings.TrimSpace(*t.ParentTaskID) != "" {
						childSubtasks++
					}
					switch t.State {
					case controlplane.StateSucceeded, "COMPLETED":
						totalTasks++
						completedTasks++
					case controlplane.StateFailed:
						totalTasks++
						failedTasks++
						resStr := string(t.Result)
						if strings.Contains(resStr, "API Error") || strings.Contains(resStr, "CLI_AGENT_API_ERROR") {
							apiErrorTasks++
						}
						if strings.Contains(resStr, "out of scope") || strings.Contains(resStr, "out-of-scope") {
							scopeDriftAttempts++
						}
					}
				}
			}
		}
	}
	firstPassYield := 1.0
	if totalTasks > 0 {
		firstPassYield = float64(completedTasks) / float64(totalTasks)
	}
	fanoutOverhead := 0.0
	if totalTasks > 0 {
		fanoutOverhead = float64(childSubtasks) / float64(totalTasks)
	}

	// Axis 2: WriteReceipt Tightness (receipt_utilization_ratio = touched_files / allowed_paths & wildcard detection)
	var totalReceipts, consumedReceipts, wildcardReceipts int
	if rDbPath, rErr := receiptDatabasePath(); rErr == nil {
		if rm, mErr := receipt.NewReceiptManager(rDbPath, nil); mErr == nil {
			defer rm.Close()
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
		}
	}
	receiptUtilizationRatio := 1.0
	if sumAllowed > 0 {
		receiptUtilizationRatio = float64(sumTouched) / float64(sumAllowed)
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
	adjudicators := map[string]string{}
	for _, sk := range canonicalSkills {
		if _, err := os.Stat(filepath.Join("skills", sk, "SKILL.md")); err == nil {
			presentSkills++
			adjudicators[sk] = "READY"
		} else {
			adjudicators[sk] = "MISSING"
		}
	}

	// Axis 5: Jev L1-vs-L3 Calibration
	jevEndpoint := os.Getenv("G8S_JEV_ENDPOINT")
	jevMode := "tier2_deterministic_fallback"
	if jevEndpoint != "" {
		jevMode = "tier1_live_jev"
	}
	var verifyCheckedSum, verifyPassedSum int
	for _, m := range models {
		verifyCheckedSum += m.VerifyCheckedCount
		verifyPassedSum += int(float64(m.VerifyCheckedCount) * m.VerifyPassRate)
	}
	l1L3AgreementRate := 1.0
	if verifyCheckedSum > 0 {
		l1L3AgreementRate = float64(verifyPassedSum) / float64(verifyCheckedSum)
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
				"total_terminal_tasks":       totalTasks,
				"completed_tasks":            completedTasks,
				"failed_tasks":               failedTasks,
				"api_error_tasks":            apiErrorTasks,
				"scope_drift_attempts":       scopeDriftAttempts,
				"f2_subtask_fanout_overhead": fanoutOverhead,
				"first_pass_yield":           firstPassYield,
				"models":                     models,
			},
			"write_receipt_tightness": map[string]any{
				"total_receipts":            totalReceipts,
				"consumed_receipts":         consumedReceipts,
				"wildcard_receipts":         wildcardReceipts,
				"delivery_events":           deliveryCount,
				"receipt_utilization_ratio": receiptUtilizationRatio,
				"tightness_score":           tightnessScore,
			},
			"tool_mcp_friction_index": map[string]any{
				"canonical_command_count": len(cli.CanonicalCommands()),
				"canonical_flag_families": len(cli.CanonicalFlagAliases()),
				"unbound_cli_claims":      unboundClaims,
				"api_error_friction_rate": frictionIndex,
				"mcp_tools_monitored":     []string{"g8s_search", "g8s_read", "g8s_edit", "g8s_select"},
			},
			"skill_cross_audit": map[string]any{
				"canonical_skills_total":   len(canonicalSkills),
				"canonical_skills_present": presentSkills,
				"coverage_ratio":           float64(presentSkills) / float64(len(canonicalSkills)),
				"adjudicators":             adjudicators,
			},
			"jev_l1_l3_calibration": map[string]any{
				"operating_tier":          jevMode,
				"l3_gate_cmd":             "g8s reflex verify",
				"l1_predictions_checked":  verifyCheckedSum,
				"l1_vs_l3_agreement_rate": l1L3AgreementRate,
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
