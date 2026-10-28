package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/featuremap"
	"github.com/tamld/g8s/internal/supervisor"
)

func runCoordinator(args []string) {
	if len(args) == 0 {
		exitUsage("coordinator", "", "", "usage: g8s coordinator <intake|mastery> [flags]", "Try: g8s coordinator intake --goal \"...\" --json", false)
	}
	sub := args[0]
	switch sub {
	case "intake":
		runCoordinatorIntake(args[1:])
	case "mastery":
		runCoordinatorMastery(args[1:])
	case "-h", "--help", "help":
		fmt.Println("Usage: g8s coordinator <intake|mastery> [flags]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  intake     Execute the 3-Pillar Coordinator Cognitive Intake, Root-Core Deconstruction & Routing pipeline (#688)")
		fmt.Println("  mastery    Compute per-domain Experiential Mastery Scorecard across .g8s/feature-map.yml (#690)")
	default:
		exitUsage("coordinator", sub, "", fmt.Sprintf("unknown coordinator subcommand %q (valid: intake, mastery)", sub), "", false)
	}
}

func runCoordinatorIntake(args []string) {
	fs := flag.NewFlagSet("coordinator intake", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	goalFlag := fs.String("goal", "", "objective or problem statement for coordinator intake (required)")
	filesFlag := fs.String("files", "", "comma-separated target files or path globs")
	fs.StringVar(filesFlag, "paths", "", "alias for --files")
	fs.StringVar(filesFlag, "path", "", "alias for --files")
	ctxFlag := fs.String("context", "", "comma-separated key=value operational context preconditions")
	repoFlag := fs.String("repo", ".", "repository root directory")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("coordinator", "intake", *traceID, err.Error(), "usage: g8s coordinator intake --goal \"...\" [--files \"...\"] [--context \"k=v\"] [--json]", *jsonl)
	}

	goal := strings.TrimSpace(*goalFlag)
	if goal == "" && fs.NArg() > 0 {
		goal = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if goal == "" {
		exitUsage("coordinator", "intake", *traceID, "--goal <objective> is required", "usage: g8s coordinator intake --goal \"...\" [--files \"...\"] [--json]", *jsonl)
	}

	var targetFiles []string
	for _, p := range strings.Split(*filesFlag, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			targetFiles = append(targetFiles, p)
		}
	}

	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}

	brief, err := supervisor.RunCoordinatorIntake(context.Background(), supervisor.CoordinatorIntakeRequest{
		Goal:          goal,
		TargetFiles:   targetFiles,
		ActiveContext: parseContextKeyValuePairs(*ctxFlag),
		RepoRoot:      *repoFlag,
		Memory:        adapter,
		Vault:         adapter.Vault(),
	})
	if err != nil {
		exitRuntime("coordinator", "intake", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("coordinator_intake", "coordinator", "intake", brief)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	fmt.Printf("Coordinator Intake [%s] lane=%s mastery=%s rigor=%s\n",
		strings.Join(brief.Orientation.MatchedFeatures, ","),
		brief.Orientation.GateLane,
		brief.Orientation.DomainMasteryTier,
		brief.Orientation.RecommendedRigor)
	fmt.Printf("  Primary Worker: %s\n", brief.RoutingPlan.PrimaryWorker)
	fmt.Printf("  Skill Chain:    %s\n", strings.Join(brief.RoutingPlan.SkillChain, " -> "))
	for _, probe := range brief.DialecticalQuestions.RootCoreProbes {
		fmt.Printf("  - %s\n", probe)
	}
}

func runCoordinatorMastery(args []string) {
	fs := flag.NewFlagSet("coordinator mastery", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	repoFlag := fs.String("repo", ".", "repository root directory")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("coordinator", "mastery", *traceID, err.Error(), "usage: g8s coordinator mastery [--repo .] [--json]", *jsonl)
	}

	fmPath := filepath.Join(*repoFlag, featuremap.DefaultFeatureMapPath)
	fm, err := featuremap.Load(fmPath)
	if err != nil {
		exitRuntime("coordinator", "mastery", *traceID, cli.CodeNotFound, err, "Ensure .g8s/feature-map.yml exists", *jsonl)
	}

	adapter := openMemoryAdapter()
	defer adapter.Close()
	if adapter.Vault() != nil {
		defer adapter.Vault().Close()
	}

	scorecard, err := supervisor.ComputeDomainMastery(context.Background(), fm, adapter, adapter.Vault(), *repoFlag)
	if err != nil {
		exitRuntime("coordinator", "mastery", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("coordinator_mastery", "coordinator", "mastery", scorecard)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	for _, d := range scorecard.Domains {
		fmt.Printf("%-26s %-14s doctrines=%d memories=%d applied=%d first_pass=%.0f%% rigor=%s\n",
			d.FeatureID, d.MasteryTier, d.ActiveDoctrineCount, d.ContextualMemoryCount, d.AppliedCount, d.FirstPassVerifyRate*100, d.RecommendedRigor)
	}
	fmt.Printf("\nMastery Summary: domains=%d master=%d expert=%d practitioner=%d novice=%d overall_first_pass=%.0f%%\n",
		scorecard.TotalDomains, scorecard.MasterCount, scorecard.ExpertCount, scorecard.PractitionerCnt, scorecard.NoviceCount, scorecard.OverallFirstPass*100)
}
