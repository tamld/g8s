package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/contextpack"
)

func runContextPack(args []string) {
	fs := flag.NewFlagSet("context-pack", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	rootFlag := fs.String("root", ".", "workspace root directory to scan and compress")
	pathsFlag := fs.String("paths", "", "optional comma-separated file or directory paths")
	allowedFlag := fs.String("allowed-paths", "", "optional comma-separated pinned allowed_paths patterns")
	focusFlag := fs.String("focus", "", "focus query for BM25F and Personalized PageRank scoring")
	budgetFlag := fs.Int("budget", 8000, "token budget for Multiple-Choice Knapsack solver")
	outFlag := fs.String("out", "", "optional file path to write rendered Markdown bundle")

	fs.Usage = func() {
		fmt.Println("Usage: g8s context-pack [--root <dir>] [--budget 8000] [--focus \"...\"] [--paths <csv>] [--allowed-paths <csv>] [--out <file.md>] [--json]")
		fs.PrintDefaults()
	}
	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("context-pack", "", *traceID, err.Error(), "Run 'g8s context-pack --help' for usage", *jsonl)
	}

	var paths, allowed []string
	if strings.TrimSpace(*pathsFlag) != "" {
		for _, p := range strings.Split(*pathsFlag, ",") {
			if t := strings.TrimSpace(p); t != "" {
				paths = append(paths, t)
			}
		}
	}
	if strings.TrimSpace(*allowedFlag) != "" {
		for _, p := range strings.Split(*allowedFlag, ",") {
			if t := strings.TrimSpace(p); t != "" {
				allowed = append(allowed, t)
			}
		}
	}

	res, err := contextpack.Pack(contextpack.PackRequest{
		RootDir:      *rootFlag,
		Paths:        paths,
		AllowedPaths: allowed,
		FocusQuery:   *focusFlag,
		BudgetTokens: *budgetFlag,
	})
	if err != nil {
		exitRuntime("context-pack", "", *traceID, cli.CodeRuntime, err, "Verify --root and --paths exist", *jsonl)
	}

	if strings.TrimSpace(*outFlag) != "" {
		if wErr := os.WriteFile(*outFlag, []byte(res.RenderedMarkdown), 0o600); wErr != nil {
			exitRuntime("context-pack", "", *traceID, cli.CodeIO, wErr, "Ensure output path is writable", *jsonl)
		}
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("context-pack", "pack", *actor, res)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	fmt.Printf("ContextPack: %d/%d tokens (%.1fx compression, %.1f%% savings, %d files, %d secrets redacted)\n",
		res.TotalTokens, res.BudgetTokens, res.CompressionRatio, res.SavingsPercent, len(res.Files), res.SecretsRedacted)
	for _, f := range res.Files {
		fmt.Printf("  - [%s] %s (%d -> %d tokens, utility=%.3f, pinned=%v)\n",
			f.SelectedTier, f.Path, f.OriginalTokens, f.CompressedTokens, f.Utility, f.Pinned)
	}
}
