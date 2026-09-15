// Package main — autopilot.go implements the g8s autopilot command for
// managing the cron-based supervisor trigger.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tamld/g8s/internal/autopilot"
	"github.com/tamld/g8s/internal/cli"
)

func runAutopilot(args []string) {
	if len(args) == 0 {
		exitUsage("autopilot", "", "", "usage: g8s autopilot <start|stop|status|trigger|config> [options]", "Run 'g8s autopilot help' for available commands", false)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "start":
		runAutopilotStart(subArgs)
	case "stop":
		runAutopilotStop(subArgs)
	case "status":
		runAutopilotStatus(subArgs)
	case "trigger":
		runAutopilotTrigger(subArgs)
	case "config":
		runAutopilotConfig(subArgs)
	case "help", "-h", "--help":
		printAutopilotUsage()
	default:
		exitUsage("autopilot", subcmd, "", fmt.Sprintf("unknown autopilot subcommand %q", subcmd), "Run 'g8s autopilot help' for available commands", false)
	}
}

func printAutopilotUsage() {
	fmt.Println("Usage: g8s autopilot <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  start     Start the autopilot scheduler daemon")
	fmt.Println("  stop      Stop the autopilot scheduler")
	fmt.Println("  status    Show autopilot scheduler status")
	fmt.Println("  trigger   Manually trigger a scan cycle")
	fmt.Println("  config    Show or update autopilot configuration")
	fmt.Println("  help      Show this help message")
	fmt.Println()
	fmt.Println("Use 'g8s autopilot <command> --help' for more information about a command.")
	os.Exit(0)
}

var autopilotScheduler *autopilot.Scheduler

func runAutopilotStart(args []string) {
	fs := flag.NewFlagSet("autopilot start", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	configFile := fs.String("config", "", "path to autopilot config YAML file")
	daemon := fs.Bool("daemon", false, "run as daemon (block until stopped)")
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "start", *traceID, err.Error(), "", *jsonl)
	}

	cfg := autopilot.DefaultConfig()
	if *configFile != "" {
		loaded, err := autopilot.LoadConfig(*configFile)
		if err != nil {
			exitRuntime("autopilot", "start", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		cfg = loaded
	}

	// Default handler: submit to control plane queue
	handler := func(ctx context.Context, item *autopilot.WorkItem) error {
		// This would integrate with the control plane to submit tasks
		// For now, just log
		fmt.Printf("Autopilot: would submit task %s (score=%.3f)\n", item.ID, item.Score)
		return nil
	}

	scheduler, err := autopilot.NewScheduler(cfg, handler)
	if err != nil {
		exitRuntime("autopilot", "start", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	autopilotScheduler = scheduler

	if err := scheduler.Start(); err != nil {
		exitRuntime("autopilot", "start", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *daemon {
		// Block until signal
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nReceived shutdown signal, stopping...")
		if err := scheduler.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping scheduler: %v\n", err)
		}
	} else {
		// Just start and exit (background)
		fmt.Println("Autopilot scheduler started in background")
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_start", "autopilot", "start", map[string]any{
			"status": "started",
			"cron":   cfg.Cron,
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	}
}

func runAutopilotStop(args []string) {
	fs := flag.NewFlagSet("autopilot stop", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "stop", *traceID, err.Error(), "", *jsonl)
	}

	if autopilotScheduler == nil {
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("autopilot_stop", "autopilot", "stop", map[string]any{
				"status": "not_running",
			})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Println("Autopilot scheduler is not running")
		}
		return
	}

	if err := autopilotScheduler.Stop(); err != nil {
		exitRuntime("autopilot", "stop", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	autopilotScheduler = nil

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_stop", "autopilot", "stop", map[string]any{
			"status": "stopped",
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		fmt.Println("Autopilot scheduler stopped")
	}
}

func runAutopilotStatus(args []string) {
	fs := flag.NewFlagSet("autopilot status", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "status", *traceID, err.Error(), "", *jsonl)
	}

	if autopilotScheduler == nil {
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("autopilot_status", "autopilot", "status", map[string]any{
				"running": false,
			})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Println("Autopilot scheduler: stopped")
		}
		return
	}

	queue := autopilotScheduler.GetQueue()
	cfg := autopilotScheduler.GetConfig()

	status := map[string]any{
		"running":             autopilotScheduler.IsRunning(),
		"cron":                cfg.Cron,
		"queue_size":          queue.Len(),
		"max_items_per_tick":  cfg.MaxItemsPerTick,
		"min_score_threshold": cfg.MinScoreThreshold,
		"scan_sources":        cfg.ScanSources,
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_status", "autopilot", "status", status)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		fmt.Println("Autopilot scheduler: running")
		fmt.Printf("  Cron: %s\n", cfg.Cron)
		fmt.Printf("  Queue size: %d\n", queue.Len())
		fmt.Printf("  Max items per tick: %d\n", cfg.MaxItemsPerTick)
		fmt.Printf("  Min score threshold: %.2f\n", cfg.MinScoreThreshold)
		fmt.Printf("  Scan sources: github_issues=%v failing_ci=%v static_analysis=%v stale_todos=%v\n",
			cfg.ScanSources.GitHubIssues, cfg.ScanSources.FailingCI,
			cfg.ScanSources.StaticAnalysis, cfg.ScanSources.StaleTodos)

		// Show top items
		topItems := queue.TopN(5)
		if len(topItems) > 0 {
			fmt.Println("\nTop queued items:")
			for i, item := range topItems {
				fmt.Printf("  %d. %s (score=%.3f, source=%s)\n", i+1, item.Title, item.Score, item.Source)
			}
		}
	}
}

func runAutopilotTrigger(args []string) {
	fs := flag.NewFlagSet("autopilot trigger", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "trigger", *traceID, err.Error(), "", *jsonl)
	}

	if autopilotScheduler == nil {
		exitRuntime("autopilot", "trigger", *traceID, cli.CodeRuntime, fmt.Errorf("scheduler not running"), "", *jsonl)
	}

	ctx := context.Background()
	if err := autopilotScheduler.TriggerScan(ctx); err != nil {
		exitRuntime("autopilot", "trigger", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_trigger", "autopilot", "trigger", map[string]any{
			"status": "triggered",
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		fmt.Println("Scan triggered successfully")
	}
}

func runAutopilotConfig(args []string) {
	fs := flag.NewFlagSet("autopilot config", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	configFile := fs.String("file", "", "config file to read/write")
	show := fs.Bool("show", false, "show current config")
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "config", *traceID, err.Error(), "", *jsonl)
	}

	if *show || (fs.NArg() == 0 && *configFile == "") {
		// Show current config
		var cfg autopilot.Config
		if autopilotScheduler != nil {
			cfg = autopilotScheduler.GetConfig()
		} else {
			cfg = autopilot.DefaultConfig()
			if *configFile != "" {
				loaded, err := autopilot.LoadConfig(*configFile)
				if err == nil {
					cfg = loaded
				}
			}
		}

		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("autopilot_config", "autopilot", "config", cfg)
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Printf("Cron: %s\n", cfg.Cron)
			fmt.Printf("Repository: %s\n", cfg.Repository)
			fmt.Printf("Max items per tick: %d\n", cfg.MaxItemsPerTick)
			fmt.Printf("Min score threshold: %.2f\n", cfg.MinScoreThreshold)
			fmt.Printf("Lookback window: %v\n", cfg.LookbackWindow)
			fmt.Printf("Codebase path: %s\n", cfg.CodebasePath)
			fmt.Printf("Priority weights: severity=%.2f confidence=%.2f cost_inverse=%.2f\n",
				cfg.PriorityWeights.Severity, cfg.PriorityWeights.Confidence, cfg.PriorityWeights.CostInverse)
			fmt.Printf("Scan sources: github_issues=%v failing_ci=%v static_analysis=%v stale_todos=%v\n",
				cfg.ScanSources.GitHubIssues, cfg.ScanSources.FailingCI,
				cfg.ScanSources.StaticAnalysis, cfg.ScanSources.StaleTodos)
		}
		return
	}

	// Update config
	if autopilotScheduler == nil {
		exitRuntime("autopilot", "config", *traceID, cli.CodeRuntime, fmt.Errorf("scheduler not running"), "", *jsonl)
	}

	cfg := autopilot.DefaultConfig()
	if *configFile != "" {
		loaded, err := autopilot.LoadConfig(*configFile)
		if err != nil {
			exitRuntime("autopilot", "config", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		cfg = loaded
	}

	if err := autopilotScheduler.UpdateConfig(cfg); err != nil {
		exitRuntime("autopilot", "config", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_config", "autopilot", "config", map[string]any{
			"status": "updated",
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		fmt.Println("Autopilot configuration updated")
	}
}
