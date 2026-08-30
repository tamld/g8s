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
	"github.com/tamld/g8s/internal/sleep"
)

// runSleep records operator away/sleep status and defers non-critical notifications per DEBT-50.
func runSleep(args []string) {
	fs := flag.NewFlagSet("sleep", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	untilFlag := fs.String("until", "", "target wakeup time (e.g. 09:00 or ISO timestamp)")
	criticalOnly := fs.Bool("critical-only", true, "defer all non-critical notifications until wake")
	reportFormat := fs.String("report-format", "voice", "summary report format on wake (voice or json)")

	if err := fs.Parse(args); err != nil {
		exitUsage("sleep", "", *traceID, err.Error(), "", *jsonl)
	}

	store := sleep.NewFileStore("")
	state := &sleep.SleepState{
		ID:           cli.GenerateTraceID(),
		SleepStart:   time.Now().UTC(),
		Until:        *untilFlag,
		Operator:     *actor,
		CriticalOnly: *criticalOnly,
		ReportFormat: *reportFormat,
	}

	if err := store.RecordSleep(context.Background(), state); err != nil {
		exitRuntime("sleep", "", *traceID, cli.CodeIO, err, "failed to record sleep state", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("sleep_state", "sleep", "", state)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	untilMsg := ""
	if *untilFlag != "" {
		untilMsg = fmt.Sprintf(" (until %s)", *untilFlag)
	}
	pterm.Success.Printf("🌙 Sleep cycle active for operator '%s'%s. Non-critical notifications deferred. Run 'g8s wake' upon return.\n", *actor, untilMsg)
}

// runWake ends sleep cycle and emits voice or json summary report per DEBT-50.
func runWake(args []string) {
	fs := flag.NewFlagSet("wake", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	formatFlag := fs.String("format", "voice", "summary format (voice or json)")
	_ = actor

	if err := fs.Parse(args); err != nil {
		exitUsage("wake", "", *traceID, err.Error(), "", *jsonl)
	}

	ctx := context.Background()
	store := sleep.NewFileStore("")
	collector := sleep.NewFileCollector("")

	state, err := store.RecordWake(ctx)
	if err != nil {
		exitRuntime("wake", "", *traceID, cli.CodeIO, err, "failed to record wake state", *jsonl)
	}

	var since time.Time
	if state != nil {
		since = state.SleepStart
	}
	events, _ := collector.ListEventsSince(ctx, since)
	summary := sleep.GenerateVoiceSummary(state, events, time.Now().UTC())

	if *jsonMode || *jsonl || strings.ToLower(*formatFlag) == "json" {
		env := cli.NewEnvelope("wake_summary", "wake", "", summary)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	pterm.DefaultHeader.WithFullWidth().Println("🌅 g8s Wake Summary")
	fmt.Printf("\n%s\n\n", summary.VoiceText)

	if len(summary.Bullets) > 0 {
		pterm.Info.Println("Recorded Events:")
		for _, b := range summary.Bullets {
			fmt.Printf("  • %s\n", b)
		}
		fmt.Println()
	}
}
