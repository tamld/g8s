package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/verifier"
)

func runQAReport(args []string) {
	fs := flag.NewFlagSet("qa-report", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	taskFlag := fs.String("task", "", "optional task ID")
	specFlag := fs.String("spec", "", "path to BA/QA specification markdown file")
	holdoutFlag := fs.String("holdout-paths", "", "comma-separated holdout test paths")
	allowedFlag := fs.String("allowed-paths", "", "comma-separated worker allowed write paths")
	acBindingsFlag := fs.String("ac-bindings", "", "comma-separated AC=TestName pairs (e.g. AC-01=TestFoo,AC-02=TestBar)")
	checksPassedFlag := fs.Bool("checks-passed", true, "whether mechanical test execution passed")

	fs.Usage = func() {
		fmt.Println("Usage: g8s qa-report --spec <spec.md> [--holdout-paths <csv>] [--allowed-paths <csv>] [--ac-bindings <AC-01=TestFoo,...>] [--json]")
		fs.PrintDefaults()
	}
	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("qa-report", "", *traceID, err.Error(), "Run 'g8s qa-report --help' for usage", *jsonl)
	}

	if strings.TrimSpace(*specFlag) == "" {
		exitUsage("qa-report", "spec", *traceID, "--spec <path> is required", "Provide a path to the specification markdown file", *jsonl)
	}
	specBytes, err := os.ReadFile(*specFlag)
	if err != nil {
		exitRuntime("qa-report", "", *traceID, cli.CodeIO, fmt.Errorf("read spec %q: %w", *specFlag, err), "Ensure spec file exists", *jsonl)
	}

	var holdouts, allowed []string
	if strings.TrimSpace(*holdoutFlag) != "" {
		for _, p := range strings.Split(*holdoutFlag, ",") {
			if t := strings.TrimSpace(p); t != "" {
				holdouts = append(holdouts, t)
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

	acBindings := make(map[string]string)
	if strings.TrimSpace(*acBindingsFlag) != "" {
		for _, pair := range strings.Split(*acBindingsFlag, ",") {
			kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(kv) == 2 {
				acBindings[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	report := verifier.EvaluateQAPipeline(verifier.QAPipelineRequest{
		TaskID:            *taskFlag,
		SpecMarkdown:      string(specBytes),
		HoldoutPaths:      holdouts,
		AllowedWritePaths: allowed,
		ACTestBindings:    acBindings,
		ChecksPassed:      *checksPassedFlag,
	})

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("qa-report", "evaluate", *actor, report)
		env.TraceID = *traceID
		if !report.Passed {
			env.Kind = "error"
			env.Error = &cli.ErrPayload{
				Code:    cli.CodeInvalid,
				Message: strings.Join(report.FailureReasons, "; "),
			}
		}
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		if !report.Passed {
			os.Exit(1)
		}
		return
	}

	fmt.Printf("QA Pipeline Verdict: %s (MECHANICAL_VERIFIED=%d, OBSERVATIONAL=%d, UNVERIFIED_PROSE=%d)\n",
		report.Verdict, report.MechanicalVerified, report.ObservationalCount, report.UnverifiedProseCount)
	for _, st := range report.Stages {
		fmt.Printf("  [Stage %d] %s: passed=%v (%s)\n", st.Stage, st.Name, st.Passed, st.Summary)
	}
	if !report.Passed {
		os.Exit(1)
	}
}
