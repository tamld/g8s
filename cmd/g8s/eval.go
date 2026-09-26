package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/harness/probe"
)

// runEval is the adversarial evaluation harness (#254): list the probe
// suite or run it against a provider and emit the Provider Reliability
// Index. Live providers run operator-invoked (bounded token budget);
// mock providers make the scoring pipeline CI-runnable.
func runEval(args []string) {
	if len(args) == 0 {
		exitUsage("eval", "", "", "unknown eval subcommand", "g8s eval list | g8s eval run --provider mock-compliant", false)
		return
	}

	sub := args[0]
	switch sub {
	case "list":
		runEvalList(args[1:])
	case "run":
		runEvalRun(args[1:])
	default:
		exitUsage("eval", strings.Join(args, " "), "", "unknown eval subcommand: "+sub, "g8s eval list | g8s eval run --provider mock-compliant", false)
	}
}

func runEvalList(args []string) {
	fs := flag.NewFlagSet("eval list", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	category := fs.String("category", "", "filter by probe category")
	if err := fs.Parse(args); err != nil {
		exitUsage("eval", "list", *traceID, err.Error(), "", *jsonl)
	}

	suite := probe.FilterSuite(probe.DefaultSuite(), nil, *category)
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("eval_suite", "eval", "list", suite)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	for _, line := range probe.List(suite) {
		fmt.Println(line)
	}
}

func runEvalRun(args []string) {
	fs := flag.NewFlagSet("eval run", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	providerName := fs.String("provider", "mock-compliant", "provider: mock-compliant, mock-defiant (live providers wire via WorkerProvider)")
	category := fs.String("category", "", "filter by probe category")
	probeIDs := fs.String("probes", "", "comma-separated probe IDs")
	timeoutPerProbe := fs.Duration("timeout", defaultEvalProbeTimeout, "per-probe execution timeout")
	if err := fs.Parse(args); err != nil {
		exitUsage("eval", "run", *traceID, err.Error(), "", *jsonl)
	}

	var provider probe.WorkerProvider
	switch strings.ToLower(*providerName) {
	case "mock-compliant":
		provider = &probe.StaticProvider{Compliant: true}
	case "mock-defiant":
		provider = &probe.StaticProvider{Compliant: false}
	default:
		exitUsage("eval", "run", *traceID, fmt.Sprintf("unknown provider %q; live providers wire via the WorkerProvider interface", *providerName), "g8s eval run --provider mock-compliant", *jsonl)
		return
	}

	suite := probe.FilterSuite(probe.DefaultSuite(), splitComma(*probeIDs), *category)
	if len(suite.Probes) == 0 {
		exitUsage("eval", "run", *traceID, "no probes matched the filter", "g8s eval list to enumerate", *jsonl)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeoutPerProbe*time.Duration(len(suite.Probes)))
	defer cancel()

	pri, err := probe.RunSuite(ctx, suite, provider)
	if err != nil {
		exitRuntime("eval", "run", *traceID, cli.CodeRuntime, err, "", *jsonl)
		return
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("eval_report", "eval", "run", pri)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	pterm.DefaultHeader.WithFullWidth().Println("Adversarial Evaluation Report")
	pterm.Info.Printf("Provider: %s (%s)\n", pri.ProviderName, pri.Model)
	pterm.Info.Printf("Pass rate: %.1f%% (%d/%d)\n", pri.Score*100, pri.PassedProbes, pri.TotalProbes)
	for _, r := range pri.Details {
		mark := "✗"
		if r.Passed {
			mark = "✓"
		}
		pterm.Printf("%s %s [%s] %s\n", mark, r.ProbeID, r.Category, r.Name)
	}
}

const defaultEvalProbeTimeout = 30 * time.Second
