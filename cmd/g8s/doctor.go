package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/doctor"
	"github.com/tamld/g8s/internal/pathutil"
)

// runDoctor executes diagnostic sanity checks for environment, permissions, and tools,
// or runs attention self-reflection checks when --attention-check is set per DEBT-47.
func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	fixMode := fs.Bool("fix", false, "apply automatic self-healing remediations")
	scopeFlag := fs.String("scope", pathutil.ScopeUser, "installation and execution scope (user or system)")
	detectPathsFlag := fs.Bool("detect-paths", false, "detect and enumerate all g8s profile paths on host")
	attentionCheck := fs.Bool("attention-check", false, "run self-reflection prompts against the worker")
	if err := fs.Parse(args); err != nil {
		exitUsage("doctor", "", *traceID, err.Error(), "", *jsonl)
	}

	doc := &doctor.Doctor{
		Scope:       *scopeFlag,
		DetectPaths: *detectPathsFlag,
	}

	if *attentionCheck {
		attReport := doc.RunAttentionCheck(context.Background(), *actor, "")
		env := cli.NewEnvelope("attention_check", "doctor", "attention-check", attReport)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	dbPath, _ := databasePath()
	report := doc.RunDiagnosticsWithFix(context.Background(), dbPath, *fixMode)

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("doctor_report", "doctor", "", report)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		if report.OverallStatus == "UNHEALTHY" {
			os.Exit(1)
		}
		return
	}

	pterm.DefaultHeader.WithFullWidth().Println("g8s Doctor Diagnostics")
	fmt.Printf("Platform: %s | Runtime: %s | Zero-CGO: %t | Status: %s\n\n",
		report.Platform, report.GoRuntime, report.ZeroCGO, report.OverallStatus)

	if len(report.AppliedFixes) > 0 {
		pterm.Info.Println("Applied Self-Healing Fixes:")
		for _, fix := range report.AppliedFixes {
			pterm.Success.Printf("  • %s\n", fix)
		}
		fmt.Println()
	}

	var td pterm.TableData
	td = append(td, []string{"Check", "Status", "Message", "Details"})
	for _, chk := range report.Checks {
		var statusStr string
		switch chk.Status {
		case "OK":
			statusStr = pterm.Green(chk.Status)
		case "WARN":
			statusStr = pterm.Yellow(chk.Status)
		default:
			statusStr = pterm.Red(chk.Status)
		}
		td = append(td, []string{chk.Name, statusStr, chk.Message, chk.Details})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()

	if report.OverallStatus == "UNHEALTHY" {
		os.Exit(1)
	}
}
