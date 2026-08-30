package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/conv"
)

// runConverge takes N solution.md files and produces a synthesized converged.md.
func runConverge(args []string) {
	fs := flag.NewFlagSet("converge", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	outPath := fs.String("out", "converged.md", "path to write synthesized converged markdown output")

	// Separate flags from positional arguments so flags can appear before or after file paths
	var flagArgs []string
	var fileArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			if (arg == "-out" || arg == "--out" || arg == "-actor" || arg == "--actor" || arg == "-trace-id" || arg == "--trace-id") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
		} else {
			fileArgs = append(fileArgs, arg)
		}
	}

	if err := fs.Parse(flagArgs); err != nil {
		exitUsage("converge", "", *traceID, err.Error(), "", *jsonl)
	}

	files := append(fs.Args(), fileArgs...)
	if len(files) < 1 {
		exitUsage("converge", "", *traceID, "usage: g8s converge <solution-1.md> <solution-2.md> ... [--out <converged.md>] [--json]", "Specify at least one solution.md file to synthesize", *jsonl)
	}

	report, err := conv.ConvergeFiles(files, *outPath)
	if err != nil {
		exitRuntime("converge", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("converged_report", "converge", "", report)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("converge", "", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	fmt.Fprintf(os.Stdout, "Synthesized %d solutions into %s\n", len(files), *outPath)
	fmt.Fprintf(os.Stdout, "Common ground sections: %d\n", len(report.CommonGround))
	fmt.Fprintf(os.Stdout, "Resolved divergences: %d\n", len(report.Divergences))
	if len(report.SpotCheckWarnings) > 0 {
		fmt.Fprintf(os.Stdout, "Spot-check warnings: %d\n", len(report.SpotCheckWarnings))
		for _, w := range report.SpotCheckWarnings {
			fmt.Fprintf(os.Stdout, "  - [%s] %s\n", w.Category, w.Message)
		}
	}
}
