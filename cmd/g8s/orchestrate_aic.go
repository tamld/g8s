// Package main — orchestrate_aic.go implements the thin AIC integration wrapper
// for automated GitHub PR reviews (T022/DELTA-18). It extracts the PR diff via `gh pr diff`
// and dispatches the review intent to `g8s orchestrate --from-intent`.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	pr := fs.Int("pr", 0, "GitHub PR number")
	intent := fs.String("intent", "", "review intent or guidance")
	model := fs.String("model", "gemini-3.7-flash-high", "target worker model")
	jsonMode := fs.Bool("json", true, "emit machine-readable JSON")
	var addDirs pathFlags
	fs.Var(&addDirs, "add-dir", "additional allowed directory (repeatable, defaults to cwd)")
	failIf(fs.Parse(args))

	if *pr <= 0 || strings.TrimSpace(*intent) == "" {
		fmt.Fprintln(os.Stderr, "usage: g8s orchestrate-aic --pr <number> --intent <text> [--json] [--model <model>] [--add-dir <path> ...]")
		os.Exit(2)
	}

	diff, err := ghDiffFetcher(*pr)
	failIf(err)

	combinedIntent := fmt.Sprintf("%s\n\nPR #%d Diff:\n%s", strings.TrimSpace(*intent), *pr, diff)

	orchArgs := []string{
		"--from-intent", combinedIntent,
		"--model", *model,
	}
	if *jsonMode {
		orchArgs = append(orchArgs, "--json")
	}
	for _, dir := range addDirs {
		orchArgs = append(orchArgs, "--add-dir", dir)
	}

	runOrchestrate(orchArgs)
}
