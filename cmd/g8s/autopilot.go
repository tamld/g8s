// Package main — autopilot.go implements the g8s autopilot command for
// managing the cron-based supervisor trigger.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tamld/g8s/internal/autopilot"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/pathutil"
)

type autopilotLockData struct {
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
}

func autopilotLockPath() string {
	return filepath.Join(pathutil.DefaultStateDir(), "autopilot.lock")
}

func readAutopilotLock(path string) (*autopilotLockData, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lockData autopilotLockData
	if err := json.Unmarshal(bytes, &lockData); err == nil && lockData.PID > 0 {
		return &lockData, nil
	}
	// Fallback to simple format: "<pid>" or "<pid> <start_time>"
	fields := strings.Fields(string(bytes))
	if len(fields) > 0 {
		var pid int
		if _, err := fmt.Sscanf(fields[0], "%d", &pid); err == nil && pid > 0 {
			lockData.PID = pid
			if len(fields) > 1 {
				if t, err := time.Parse(time.RFC3339, fields[1]); err == nil {
					lockData.StartTime = t
				}
			}
			return &lockData, nil
		}
	}
	return nil, fmt.Errorf("invalid lockfile format")
}

func writeAutopilotLock(path string, pid int, startTime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(autopilotLockData{
		PID:       pid,
		StartTime: startTime.UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func removeAutopilotLock(path string) {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[warn] autopilot: remove lockfile %s: %v\n", path, err)
	}
}

var isProcessAlive = defaultIsProcessAlive

// runAutopilot dispatches autopilot subcommands (start/stop/status/
// trigger/config). Honesty semantics per #485 stage 1.
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
	fmt.Println("  start     Start the autopilot scheduler (in-process)")
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
	daemon := fs.Bool("daemon", true, "run until stopped (default true)")
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "start", *traceID, err.Error(), "", *jsonl)
	}

	lockPath := autopilotLockPath()
	if existingLock, err := readAutopilotLock(lockPath); err == nil && existingLock != nil {
		if isProcessAlive(existingLock.PID) {
			exitRuntime("autopilot", "start", *traceID, cli.CodeRuntime,
				fmt.Errorf("autopilot is already running (PID %d); refuse to start duplicate process", existingLock.PID),
				fmt.Sprintf("To restart, stop PID %d or remove %s if stale", existingLock.PID, lockPath),
				*jsonl)
		}
		// Stale lockfile -> remove and continue
		removeAutopilotLock(lockPath)
	}

	cfg := autopilot.DefaultConfig()
	if *configFile != "" {
		loaded, err := autopilot.LoadConfig(*configFile)
		if err != nil {
			exitRuntime("autopilot", "start", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		cfg = loaded
	}
	if len(strings.Fields(cfg.Cron)) == 5 {
		cfg.Cron = "0 " + cfg.Cron
	}

	// LOUD NOTICE (#485 stages 2-3):
	// The due-work handler remains an intentional logged no-op in stage 1.
	// Task submission and execution are deferred to stage 2 (throttle & admission control)
	// and stage 3 (tick handler integration with control plane and OS-native scheduling).
	// DO NOT submit actual tasks here until stage 2 rate limiting is in place.
	handler := func(ctx context.Context, item *autopilot.WorkItem) error {
		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("autopilot_item", "autopilot", "item", map[string]any{
				"item_id": item.ID,
				"score":   item.Score,
			})
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Printf("Autopilot: would submit task %s (score=%.3f)\n", item.ID, item.Score)
		}
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

	startTime := time.Now().UTC()
	if err := writeAutopilotLock(lockPath, os.Getpid(), startTime); err != nil {
		_ = scheduler.Stop()
		exitRuntime("autopilot", "start", *traceID, cli.CodeIO, err, "failed to write autopilot lockfile", *jsonl)
	}
	defer removeAutopilotLock(lockPath)

	banner := "[EXPERIMENTAL] g8s autopilot is running in-process only (staged path #485). Cross-process daemonization and OS-native scheduling will be added in stage 3."
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_start", "autopilot", "start", map[string]any{
			"status": "started",
			"cron":   cfg.Cron,
			"pid":    os.Getpid(),
			"banner": banner,
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		fmt.Println(banner)
		fmt.Printf("Autopilot scheduler started (PID %d, cron %s)\n", os.Getpid(), cfg.Cron)
	}

	if *daemon {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		if !*jsonMode && !*jsonl {
			fmt.Println("\nReceived shutdown signal, stopping...")
		}
		if err := scheduler.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping scheduler: %v\n", err)
		}
	}
}

func runAutopilotStop(args []string) {
	fs := flag.NewFlagSet("autopilot stop", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = jsonMode
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "stop", *traceID, err.Error(), "", *jsonl)
	}

	lockPath := autopilotLockPath()
	lockData, err := readAutopilotLock(lockPath)
	if err == nil && lockData != nil && isProcessAlive(lockData.PID) {
		msg := fmt.Sprintf("cannot stop autopilot scheduler in another process: no cross-process scheduler exists in this build (staged path #485 — stage 3 will add OS-native scheduling). Autopilot is running under PID %d", lockData.PID)
		hint := fmt.Sprintf("To stop autopilot, signal PID %d directly: kill %d (or kill -TERM %d)", lockData.PID, lockData.PID, lockData.PID)
		exitRuntime("autopilot", "stop", *traceID, cli.CodeRuntime, fmt.Errorf("%s", msg), hint, *jsonl)
		return
	}

	msg := "cannot stop autopilot scheduler: no cross-process scheduler exists in this build (staged path #485 — stage 3 will add OS-native scheduling)"
	hint := "No active autopilot start process found to stop"
	exitRuntime("autopilot", "stop", *traceID, cli.CodeRuntime, fmt.Errorf("%s", msg), hint, *jsonl)
}

func runAutopilotStatus(args []string) {
	fs := flag.NewFlagSet("autopilot status", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "status", *traceID, err.Error(), "", *jsonl)
	}

	lockPath := autopilotLockPath()
	lockData, err := readAutopilotLock(lockPath)
	lockfileExisted := err == nil && lockData != nil
	alive := false
	if lockfileExisted {
		alive = isProcessAlive(lockData.PID)
	}

	msg := "autopilot runs only inside a `start` process; no cross-process scheduler exists in this build (staged path #485 — stage 3 will add OS-native scheduling)"

	if *jsonMode || *jsonl {
		status := map[string]any{
			"running":         alive,
			"message":         msg,
			"lockfile_exists": lockfileExisted,
		}
		if lockfileExisted {
			status["lockfile_pid"] = lockData.PID
			status["process_alive"] = alive
			if !lockData.StartTime.IsZero() {
				status["lockfile_start_time"] = lockData.StartTime.Format(time.RFC3339)
			}
		}
		env := cli.NewEnvelope("autopilot_status", "autopilot", "status", status)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	fmt.Println(msg)
	if lockfileExisted {
		if alive {
			fmt.Printf("Start-process lockfile exists: PID %d (running, started %s)\n",
				lockData.PID, lockData.StartTime.Format(time.RFC3339))
		} else {
			fmt.Printf("Start-process lockfile exists: PID %d (not running / stale lockfile, started %s)\n",
				lockData.PID, lockData.StartTime.Format(time.RFC3339))
		}
	} else {
		fmt.Println("Start-process lockfile: none")
	}
}

func runAutopilotTrigger(args []string) {
	fs := flag.NewFlagSet("autopilot trigger", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = jsonMode
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "trigger", *traceID, err.Error(), "", *jsonl)
	}

	exitRuntime("autopilot", "trigger", *traceID, cli.CodeRuntime,
		fmt.Errorf("triggers are not persisted yet (staged path #485 — stage 3 will add persisted triggers and native scheduling)"),
		"Cross-process trigger capability is scheduled for stage 3",
		*jsonl)
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
