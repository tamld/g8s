package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/telemetry"
	"github.com/tamld/g8s/internal/vault"
)

// runFeedback routes Sovereign HITL Ground Truth calibration and hypothesis
// falsification subcommands (issue #596).
func runFeedback(args []string) {
	code := runFeedbackWithIO(args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func runFeedbackWithIO(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		exitUsageWithWriter(stderr, "feedback", "", "", "usage: g8s feedback <record|override|report> [flags]", "Use 'g8s feedback record', 'g8s feedback override', or 'g8s feedback report'", false)
		return 2
	}

	sub := args[0]
	switch sub {
	case "record":
		return runFeedbackRecordWithIO(args[1:], stdout, stderr)
	case "override":
		return runFeedbackOverrideWithIO(args[1:], stdout, stderr)
	case "report":
		return runFeedbackReportWithIO(args[1:], stdout, stderr)
	case "--help", "-h", "help":
		fmt.Fprintln(stdout, "Usage: g8s feedback <record|override|report> [flags]")
		fmt.Fprintln(stdout, "\nSubcommands:")
		fmt.Fprintln(stdout, "  record     Record a Sovereign HITL Ground Truth verdict (tp|fp|veto) for a rule")
		fmt.Fprintln(stdout, "  override   Record an F0 Supervisor OverrideReceipt for an advisory signal")
		fmt.Fprintln(stdout, "  report     Evaluate falsifiable governance hypotheses (H1, H2, H3) and rule weights")
		return 0
	default:
		exitUsageWithWriter(stderr, "feedback", sub, "", fmt.Sprintf("unknown feedback subcommand %q (valid: record, override, report)", sub), "Run 'g8s feedback help' for usage", false)
		return 2
	}
}

func openGovernanceTelemetryEngine(dbFlag string) (*telemetry.TelemetryEngine, error) {
	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = resolveTelemetryDBPath(dbFlag)
	return telemetry.NewTelemetryEngine(cfg)
}

func runFeedbackRecordWithIO(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("feedback record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	actor, traceID, jsonl, _ := cli.AddCommonFlagsWithDefaults(fs, true)

	ruleFlag := fs.String("rule", "", "rule ID to calibrate (required)")
	verdictFlag := fs.String("verdict", "", "HITL verdict: tp, fp, or veto (required)")
	taskFlag := fs.String("task", "", "optional task ID")
	pkgFlag := fs.String("package", "", "optional package name")
	noteFlag := fs.String("note", "", "optional human operator rationale")
	dbFlag := fs.String("db", "", "optional telemetry database path override")

	if err := fs.Parse(args); err != nil {
		exitUsageWithWriter(stderr, "feedback", "record", *traceID, err.Error(), "", *jsonl)
		return 2
	}
	if strings.TrimSpace(*ruleFlag) == "" || strings.TrimSpace(*verdictFlag) == "" {
		exitUsageWithWriter(stderr, "feedback", "record", *traceID, "usage: g8s feedback record --rule <id> --verdict <tp|fp|veto> [--task <id>] [--package <pkg>] [--note <text>]", "Provide both --rule and --verdict", *jsonl)
		return 2
	}

	eng, err := openGovernanceTelemetryEngine(*dbFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "record", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}
	defer eng.Close()

	effectiveActor := strings.TrimSpace(*actor)
	if effectiveActor == "" {
		effectiveActor = "human-operator"
	}

	rw, err := eng.RecordHITLFeedback(context.Background(), telemetry.HITLFeedback{
		RuleID:  strings.TrimSpace(*ruleFlag),
		TaskID:  strings.TrimSpace(*taskFlag),
		Package: strings.TrimSpace(*pkgFlag),
		Verdict: telemetry.HITLVerdict(strings.ToLower(strings.TrimSpace(*verdictFlag))),
		Actor:   effectiveActor,
		Note:    strings.TrimSpace(*noteFlag),
	})
	if err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "record", *traceID, cli.CodeInvalid, err, "Verdict must be tp, fp, or veto", *jsonl)
		return 1
	}

	env := cli.NewEnvelope("rule_weight", "feedback", "record", rw)
	env.TraceID = *traceID
	if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "record", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}
	return 0
}

func runFeedbackOverrideWithIO(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("feedback override", flag.ContinueOnError)
	fs.SetOutput(stderr)
	actor, traceID, jsonl, _ := cli.AddCommonFlagsWithDefaults(fs, true)

	taskFlag := fs.String("task", "", "task ID for override receipt (required)")
	ruleFlag := fs.String("rule", "", "rule ID overridden (required)")
	reasonFlag := fs.String("reason", "", "override rationale (required)")
	dbFlag := fs.String("db", "", "optional telemetry database path override")

	if err := fs.Parse(args); err != nil {
		exitUsageWithWriter(stderr, "feedback", "override", *traceID, err.Error(), "", *jsonl)
		return 2
	}
	if strings.TrimSpace(*taskFlag) == "" || strings.TrimSpace(*ruleFlag) == "" || strings.TrimSpace(*reasonFlag) == "" {
		exitUsageWithWriter(stderr, "feedback", "override", *traceID, "usage: g8s feedback override --task <id> --rule <id> --reason <text>", "Provide --task, --rule, and --reason", *jsonl)
		return 2
	}

	eng, err := openGovernanceTelemetryEngine(*dbFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "override", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}
	defer eng.Close()

	effectiveActor := strings.TrimSpace(*actor)
	if effectiveActor == "" {
		effectiveActor = "f0-supervisor"
	}

	rec, err := eng.RecordOverrideReceipt(context.Background(), telemetry.OverrideReceipt{
		TaskID: strings.TrimSpace(*taskFlag),
		RuleID: strings.TrimSpace(*ruleFlag),
		Actor:  effectiveActor,
		Reason: strings.TrimSpace(*reasonFlag),
	})
	if err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "override", *traceID, cli.CodeInvalid, err, "", *jsonl)
		return 1
	}

	env := cli.NewEnvelope("override_receipt", "feedback", "override", rec)
	env.TraceID = *traceID
	if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "override", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}
	return 0
}

func runFeedbackReportWithIO(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("feedback report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, traceID, jsonl, _ := cli.AddCommonFlagsWithDefaults(fs, true)

	maxKFlag := fs.Int("max-k", vault.ResolveMaxActiveRules(), "carrying capacity K_max for Hypothesis H1 evaluation")
	dbFlag := fs.String("db", "", "optional telemetry database path override")

	if err := fs.Parse(args); err != nil {
		exitUsageWithWriter(stderr, "feedback", "report", *traceID, err.Error(), "", *jsonl)
		return 2
	}

	eng, err := openGovernanceTelemetryEngine(*dbFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "report", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}
	defer eng.Close()

	rep, err := eng.EvaluateHypotheses(context.Background(), *maxKFlag)
	if err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "report", *traceID, cli.CodeRuntime, err, "", *jsonl)
		return 1
	}

	env := cli.NewEnvelope("hypothesis_report", "feedback", "report", rep)
	env.TraceID = *traceID
	if err := cli.WriteResponse(stdout, env, *jsonl); err != nil {
		exitRuntimeWithWriter(stderr, "feedback", "report", *traceID, cli.CodeIO, err, "", *jsonl)
		return 1
	}
	return 0
}
