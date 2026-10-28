package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/featuremap"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/verifier"
)

// runVerify runs verifier-class checks on a task (issue #515).
func runVerify(args []string) {
	code := runVerifyWithIO(args, os.Stdout, os.Stderr, "")
	if code != 0 {
		os.Exit(code)
	}
}

// runVerifyWithIO executes the verify command and writes output to stdout/stderr.
func runVerifyWithIO(args []string, stdout, stderr io.Writer, dbPathOverride string, verifierOpts ...verifier.Option) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	_ = jsonMode

	taskFlag := fs.String("task", "", "task ID to verify")
	fs.StringVar(taskFlag, "task-id", "", "alias for --task")
	asTaskFlag := fs.String("as-task", "", "caller task ID (self-grade guard)")
	overrideReasonFlag := fs.String("override-reason", "", "F0 override rationale for advisory diff-guard signals (#595)")
	briefFlag := fs.String("brief", "", "brief ID to load Definition of Done items from")
	featureFlag := fs.String("feature", "", "feature ID from .g8s/feature-map.yml to verify (includes blast_radius.direct_consumers)")
	repoFlag := fs.String("repo", "", "repository or worktree root directory")
	holdoutDirFlag := fs.String("holdout-dir", "", "out-of-worktree holdout test directory")
	blindPacketFlag := fs.Bool("blind-packet", false, "generate sanitized Double-Blind Review Packet")
	canaryMutantFlag := fs.Bool("canary-mutant", false, "run Seeded Mutant Canary sensitivity check")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		exitUsageWithWriter(stderr, "verify", "", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	taskID := *taskFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}

	isStandaloneFeature := false
	if taskID == "" {
		if strings.TrimSpace(*featureFlag) != "" {
			taskID = "feature-" + strings.TrimSpace(*featureFlag)
			isStandaloneFeature = true
		} else {
			exitUsageWithWriter(stderr, "verify", "", *traceID, "usage: g8s verify --task <id> [--as-task <id>] [--brief <brief-id>] [--override-reason <text>]", "Provide a task ID", *jsonl)
			return 2
		}
	}

	var target verifier.TaskRef
	var targetRepoDir string
	var verifiedTask *controlplane.Task

	if isStandaloneFeature {
		target = verifier.TaskRef{
			ID:             taskID,
			OverrideReason: strings.TrimSpace(*overrideReasonFlag),
		}
		if *briefFlag != "" {
			dbPath := dbPathOverride
			if dbPath == "" {
				var err error
				dbPath, err = databasePath()
				if err != nil {
					exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeIO, err, "", *jsonl)
					return 1
				}
			}

			store, err := controlplane.NewControlPlane(dbPath, nil)
			if err != nil {
				exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
				return 1
			}
			defer store.Close()

			ctx := context.Background()
			b, err := store.GetBrief(ctx, *briefFlag)
			if err != nil {
				exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeNotFound, fmt.Errorf("brief %q not found: %w", *briefFlag, err), "Check brief ID with 'g8s brief show'", *jsonl)
				return 1
			}
			target.DoDItems = verifier.ParseBriefDoDItems(b.DodMD)
		}
	} else {
		dbPath := dbPathOverride
		if dbPath == "" {
			var err error
			dbPath, err = databasePath()
			if err != nil {
				exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeIO, err, "", *jsonl)
				return 1
			}
		}

		store, err := controlplane.NewControlPlane(dbPath, nil)
		if err != nil {
			exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
			return 1
		}
		defer store.Close()

		ctx := context.Background()
		task, err := store.GetTask(ctx, taskID)
		if err != nil {
			exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
			return 1
		}
		if task == nil {
			exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeNotFound, fmt.Errorf("task %q not found", taskID), "Verify the task ID with 'g8s tasks'", *jsonl)
			return 1
		}
		verifiedTask = task

		target = verifier.TaskRef{
			ID:             taskID,
			OverrideReason: strings.TrimSpace(*overrideReasonFlag),
		}

		// Extract receipt_id and allowed_paths from task payload/request
		var reqPayload struct {
			ReceiptID    string   `json:"receipt_id"`
			AllowedPaths []string `json:"allowed_paths"`
			ScopeRoot    string   `json:"scope_root"`
			AddDirs      []string `json:"add_dirs"`
		}
		if len(task.Request) > 0 {
			_ = json.Unmarshal(task.Request, &reqPayload)
		}

		target.ReceiptID = reqPayload.ReceiptID
		target.AllowedPaths = reqPayload.AllowedPaths

		// If receipt_id is present and allowed_paths not embedded in request, resolve from
		// the sibling receipts.db — the canonical receipt ledger (the receipt CLI and the
		// controlplane both write receipts.db next to g8s.db; round-1 of issue #516 caught
		// verify reading g8s.db, where the table does not exist, and landing unregistered).
		if target.ReceiptID != "" && len(target.AllowedPaths) == 0 {
			rcDbPath := filepath.Join(filepath.Dir(dbPath), "receipts.db")
			db, err := sql.Open("sqlite", pathutil.SQLiteURI(rcDbPath, "mode=ro"))
			if err == nil {
				defer db.Close()
				var pathsJSON string
				if err := db.QueryRowContext(ctx, "SELECT allowed_paths_json FROM write_receipts WHERE receipt_id = ?", target.ReceiptID).Scan(&pathsJSON); err == nil {
					var paths []string
					if err := json.Unmarshal([]byte(pathsJSON), &paths); err == nil {
						target.AllowedPaths = paths
					}
				}
			}
		}

		targetRepoDir = strings.TrimSpace(reqPayload.ScopeRoot)
		if targetRepoDir == "" && len(reqPayload.AddDirs) > 0 {
			targetRepoDir = strings.TrimSpace(reqPayload.AddDirs[0])
		}

		// Fix L3: Inspect task.Result for deliverable pointer or dod_matrix
		var taskRes struct {
			Deliverable *struct {
				Mode string `json:"mode"`
				Dir  string `json:"dir"`
			} `json:"deliverable"`
			DoDMatrix []verifier.DoDItemStatus `json:"dod_matrix"`
		}
		if len(task.Result) > 0 {
			_ = json.Unmarshal(task.Result, &taskRes)
		}
		if len(taskRes.DoDMatrix) > 0 {
			target.ReportedDoD = taskRes.DoDMatrix
		}
		if taskRes.Deliverable != nil && taskRes.Deliverable.Mode == "worktree" && strings.TrimSpace(taskRes.Deliverable.Dir) != "" {
			wtDir := strings.TrimSpace(taskRes.Deliverable.Dir)
			if fi, err := os.Stat(wtDir); err == nil && fi.IsDir() {
				target.WorktreeDir = wtDir
				targetRepoDir = wtDir
			}
		}

		if *briefFlag != "" {
			b, err := store.GetBrief(ctx, *briefFlag)
			if err != nil {
				exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeNotFound, fmt.Errorf("brief %q not found: %w", *briefFlag, err), "Check brief ID with 'g8s brief show'", *jsonl)
				return 1
			}
			target.DoDItems = verifier.ParseBriefDoDItems(b.DodMD)
		}
	}

	caller := verifier.TaskRef{}
	if *asTaskFlag != "" {
		caller.ID = *asTaskFlag
	}

	// Self-grade guard normalization (issue #517 D-01)
	normCaller := strings.TrimSpace(caller.ID)
	normTarget := strings.TrimSpace(target.ID)
	if normCaller != "" && normTarget != "" && strings.EqualFold(normCaller, normTarget) {
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeInvalid, verifier.ErrSelfGrade, "", *jsonl)
		return 1
	}

	if strings.TrimSpace(*repoFlag) != "" && targetRepoDir == "" {
		targetRepoDir = strings.TrimSpace(*repoFlag)
		if target.WorktreeDir == "" {
			target.WorktreeDir = targetRepoDir
		}
	}

	if featureFlagVal := strings.TrimSpace(*featureFlag); featureFlagVal != "" {
		fmRoot := targetRepoDir
		if fmRoot == "" {
			fmRoot = "."
		}
		fmPath := filepath.Join(fmRoot, featuremap.DefaultFeatureMapPath)
		fm, err := featuremap.Load(fmPath)
		if err != nil {
			exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeNotFound, err, "Ensure .g8s/feature-map.yml exists", *jsonl)
			return 1
		}

		nodes, err := fm.ResolveTransitiveFeatures(featureFlagVal)
		if err != nil {
			exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeNotFound, err, "Check feature ID with 'g8s feature list'", *jsonl)
			return 1
		}

		target.FeatureID = featureFlagVal
		target.SpecContract = fmt.Sprintf("%s (%s) SRS:%s Delta:%s", nodes[0].Title, nodes[0].PersonaStory, strings.Join(nodes[0].SRSReqs, ","), nodes[0].OpenSpecDelta)

		if len(target.AllowedPaths) == 0 {
			if derivedPaths, err := fm.DeriveAllowedPaths(target.FeatureID); err == nil {
				target.AllowedPaths = derivedPaths
			}
		}

		var invariants []string
		seenInvariants := make(map[string]bool)
		var visibleTests []string
		seenVisibleTests := make(map[string]bool)

		for _, node := range nodes {
			for _, inv := range node.Invariants {
				inv = strings.TrimSpace(inv)
				if inv != "" && !seenInvariants[inv] {
					seenInvariants[inv] = true
					invariants = append(invariants, inv)
				}
			}
			for _, vt := range node.VisibleTests {
				vt = strings.TrimSpace(vt)
				if vt != "" && !seenVisibleTests[vt] {
					seenVisibleTests[vt] = true
					visibleTests = append(visibleTests, vt)
				}
			}
		}
		target.Invariants = invariants

		var extraChecks []verifier.Check
		for _, vt := range visibleTests {
			if strings.HasPrefix(vt, "go test") {
				fields := strings.Fields(vt)
				var pkgs []string
				if len(fields) > 2 {
					for _, f := range fields[2:] {
						if strings.HasPrefix(f, "-") {
							continue
						}
						pkgs = append(pkgs, f)
					}
				}
				if len(pkgs) > 0 {
					extraChecks = append(extraChecks, verifier.Check{
						Type:     verifier.CheckTypeGoTest,
						Packages: pkgs,
					})
				}
			} else if strings.HasSuffix(vt, "_test.go") {
				dir := filepath.ToSlash(filepath.Dir(vt))
				pkgDir := "./" + dir
				if dir == "." {
					pkgDir = "."
				} else if strings.HasPrefix(dir, "./") {
					pkgDir = dir
				}
				extraChecks = append(extraChecks, verifier.Check{
					Type:     verifier.CheckTypeGoTest,
					Packages: []string{pkgDir},
				})
			} else {
				extraChecks = append(extraChecks, verifier.Check{
					Type: verifier.CheckTypeScript,
					Run:  vt,
				})
			}
		}
		target.ExtraChecks = extraChecks

		if strings.TrimSpace(*holdoutDirFlag) == "" && os.Getenv("G8S_STATE_DIR") != "" {
			cand := filepath.Join(os.Getenv("G8S_STATE_DIR"), "holdout", target.FeatureID)
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				*holdoutDirFlag = cand
			}
		}
	}

	target.HoldoutDir = strings.TrimSpace(*holdoutDirFlag)
	target.GenerateBlindPacket = *blindPacketFlag
	target.RunCanaryMutant = *canaryMutantFlag

	// Run Verify
	finalOpts := make([]verifier.Option, 0, len(verifierOpts)+2)
	if targetRepoDir != "" && targetRepoDir != "." {
		finalOpts = append(finalOpts, verifier.WithRepoRoot(targetRepoDir))
		if regPath := filepath.Join(targetRepoDir, verifier.DefaultRegistryPath); fileExists(regPath) {
			finalOpts = append(finalOpts, verifier.WithRegistryPath(regPath))
		}
	}
	finalOpts = append(finalOpts, verifierOpts...)
	v := verifier.NewVerifier(finalOpts...)
	verdict, err := v.Verify(target, caller)
	if err != nil {
		// Self-grade refusal or typed error
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeInvalid, err, "", *jsonl)
		return 1
	}
	passedVerify := verdict.Outcome == verifier.OutcomePass && verdict.VetoReason == "" && (verdict.HoldoutChecksOK == nil || *verdict.HoldoutChecksOK)
	recordVerifyTaskTelemetry(verifiedTask, passedVerify, dbPathOverride, target.AllowedPaths)
	if os.Getenv("G8S_JIT_WHISPER") != "0" {
		if passedVerify {
			if isStandaloneFeature {
				verdict.JITBaton = "[jit:baton:VERIFIED] Next step: ./bin/g8s feature audit"
			} else {
				verdict.JITBaton = fmt.Sprintf("[jit:baton:VERIFIED] Next step: ./bin/g8s deliver %s --prune-worktree", taskID)
			}
		} else {
			verdict.JITBaton = fmt.Sprintf("[jit:baton:IMPASSE] Verification failed; experiential autopsy recorded. Next step: ./bin/g8s resubmit %s --prompt \"...\"", taskID)
		}
	}

	env := cli.NewEnvelope("verdict", "verify", taskID, verdict)
	env.TraceID = *traceID
	if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
		exitRuntimeWithWriter(stderr, "verify", "", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}

	// Hard failure exits 1
	if verdict.Status == verifier.StatusHard && verdict.Outcome == verifier.OutcomeFail {
		return 1
	}
	if verdict.VetoReason != "" || (verdict.HoldoutChecksOK != nil && !*verdict.HoldoutChecksOK) {
		return 1
	}

	return 0
}

func exitUsageWithWriter(w io.Writer, cmd, sub, traceID, msg, hint string, jsonl bool) {
	if traceID == "" {
		traceID = cli.GenerateTraceID()
	}
	env := cli.NewErrorEnvelope(cmd, sub, traceID, cli.CodeUsage, msg, hint, "")
	_ = cli.WriteResponse(w, env, jsonl)
}

func exitRuntimeWithWriter(w io.Writer, cmd, sub, traceID, code string, err error, hint string, jsonl bool) {
	if traceID == "" {
		traceID = cli.GenerateTraceID()
	}
	if code == "" {
		code = cli.CodeRuntime
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	env := cli.NewErrorEnvelope(cmd, sub, traceID, code, msg, hint, "")
	_ = cli.WriteResponse(w, env, jsonl)
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
