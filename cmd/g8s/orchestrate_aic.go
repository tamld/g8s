// Package main — orchestrate_aic.go implements the thin AIC integration wrapper
// for automated GitHub PR reviews (T022/DELTA-18). It extracts the PR diff via `gh pr diff`
// and dispatches the review intent to `g8s orchestrate --from-intent`.
package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tamld/g8s/internal/cli"
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
	var addDirs pathFlags
	fs.Var(&addDirs, "add-dir", "additional allowed directory (repeatable, defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		exitUsage("orchestrate-aic", "", *traceID, err.Error(), "", *jsonl)
	}

	if *pr <= 0 || strings.TrimSpace(*intent) == "" {
		exitUsage("orchestrate-aic", "", *traceID, "usage: g8s orchestrate-aic --pr <number> --intent <text> [--json] [--model <model>] [--add-dir <path> ...]", "Provide --pr <number> and --intent <text>", *jsonl)
	}

	diff, err := ghDiffFetcher(*pr)
	if err != nil {
		exitRuntime("orchestrate-aic", "", *traceID, cli.CodeRuntime, err, "Ensure gh CLI is authenticated and PR exists", *jsonl)
	}

	combinedIntent := fmt.Sprintf("%s\n\nPR #%d Diff:\n%s", strings.TrimSpace(*intent), *pr, diff)

	orchArgs := []string{
		"--actor", *actor,
		"--trace-id", *traceID,
		"--from-intent", combinedIntent,
		"--model", *model,
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
