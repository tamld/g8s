// Package main — orchestrate_aic.go implements the AIC integration wrapper
// for automated GitHub PR reviews (T022/DELTA-18). It extracts the PR diff via `gh pr diff`,
// distills the diff with pure-Go AST/chunk intelligence (diffintel), and dispatches
// bounded verification tasks to `g8s orchestrate --from-intent` with role `verifier`.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/diffintel"
	"github.com/tamld/g8s/internal/review"
)

// ghDiffFetcher resolves the git diff for a GitHub PR. Placed behind a var seam
// so unit tests can stub it without invoking the real gh CLI or network.
var ghDiffFetcher = defaultGHDiffFetcher

func defaultGHDiffFetcher(pr int) (string, error) {
	cmd := exec.Command("gh", "pr", "diff", strconv.Itoa(pr))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr diff %d: %w (%s)", pr, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// runOrchestrateAIC executes the PR review orchestrator for AIC workflows.
func runOrchestrateAIC(args []string) {
	fs := flag.NewFlagSet("orchestrate-aic", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	pr := fs.Int("pr", 0, "GitHub PR number")
	intent := fs.String("intent", "", "review intent or guidance")
	model := fs.String("model", "", "target worker model (defaults to first ready provider's first model)")
	noPrune := fs.Bool("no-prune", false, "disable noise file pruning (e.g. review lockfiles)")
	rawDiff := fs.Bool("raw-diff", false, "legacy mode: pass raw diff without diffintel distillation")
	var addDirs pathFlags
	fs.Var(&addDirs, "add-dir", "additional allowed directory (repeatable, defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		exitUsage("orchestrate-aic", "", *traceID, err.Error(), "", *jsonl)
	}

	if *pr <= 0 || strings.TrimSpace(*intent) == "" {
		exitUsage("orchestrate-aic", "", *traceID, "usage: g8s orchestrate-aic --pr <number> --intent <text> [--json] [--model <model>] [--no-prune] [--add-dir <path> ...]", "Provide --pr <number> and --intent <text>", *jsonl)
	}

	diff, err := ghDiffFetcher(*pr)
	if err != nil {
		exitRuntime("orchestrate-aic", "", *traceID, cli.CodeRuntime, err, "Ensure gh CLI is authenticated and PR exists", *jsonl)
	}

	if *rawDiff {
		// Legacy behavior: concatenate raw diff directly
		combinedIntent := fmt.Sprintf("%s\n\nPR #%d Diff:\n%s", strings.TrimSpace(*intent), *pr, diff)
		orchArgs := []string{
			"--actor", *actor,
			"--trace-id", *traceID,
			"--from-intent", combinedIntent,
			"--model", *model,
			"--role", "verifier",
			"--permission", "read_only",
		}
		if *jsonMode {
			orchArgs = append(orchArgs, "--json")
		}
		if *jsonl {
			orchArgs = append(orchArgs, "--jsonl")
		}
		for _, dir := range addDirs {
			orchArgs = append(orchArgs, "--add-dir", dir)
		}
		runOrchestrate(orchArgs)
		return
	}

	// 1. Pure-Go unified diff parsing
	files, err := diffintel.ParseUnifiedDiff(diff)
	if err != nil {
		exitRuntime("orchestrate-aic", "", *traceID, cli.CodeRuntime, fmt.Errorf("parse diff: %w", err), "Malformed PR diff", *jsonl)
	}

	// 2. Noise pruning
	targetFiles := files
	noiseCount := 0
	if !*noPrune {
		targetFiles, noiseCount = diffintel.PruneNoise(files)
	}

	// 3. Context slicing
	chunks := diffintel.SliceChunks(targetFiles, 1500)

	// 4. Handle clean / zero-reviewable changes
	if len(chunks) == 0 {
		summary := review.ReviewSummary{
			PR:          *pr,
			Passed:      true,
			Verdict:     "APPROVED",
			TotalIssues: 0,
		}
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("review", "orchestrate-aic", "", summary)
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
			return
		}
		fmt.Printf("PR #%d: No reviewable code changes detected (%d noise/generated files skipped). Verdict: APPROVED\n", *pr, noiseCount)
		return
	}

	// 5. Generate distilled, bounded sub-tasks for FanOut
	var subtaskLines []string
	for _, chunk := range chunks {
		taskLine := fmt.Sprintf("Review %s (%s risk): %s", chunk.FilePath, chunk.RiskLevel, strings.TrimSpace(*intent))
		subtaskLines = append(subtaskLines, taskLine)
	}
	distilledIntent := strings.Join(subtaskLines, "\n")

	orchArgs := []string{
		"--actor", *actor,
		"--trace-id", *traceID,
		"--from-intent", distilledIntent,
		"--model", *model,
		"--role", "verifier",
		"--permission", "read_only",
	}
	if *jsonMode {
		orchArgs = append(orchArgs, "--json")
	}
	if *jsonl {
		orchArgs = append(orchArgs, "--jsonl")
	}
	for _, dir := range addDirs {
		orchArgs = append(orchArgs, "--add-dir", dir)
	}

	runOrchestrate(orchArgs)
}
