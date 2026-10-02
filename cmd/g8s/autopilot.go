// Package main — autopilot.go implements the g8s autopilot command for
// managing the cron-based supervisor trigger.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tamld/g8s/internal/autopilot"
	"github.com/tamld/g8s/internal/cleanup"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/doctor"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/settings"
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
// trigger/config/tick/install-schedule/uninstall-schedule). Honesty semantics per #485 stage 1-3.
func runAutopilot(args []string) {
	if len(args) == 0 {
		exitUsage("autopilot", "", "", "usage: g8s autopilot <start|stop|status|trigger|config|tick|install-schedule|uninstall-schedule> [options]", "Run 'g8s autopilot help' for available commands", false)
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
	case "tick":
		runAutopilotTick(subArgs)
	case "install-schedule":
		runAutopilotInstallSchedule(subArgs)
	case "uninstall-schedule":
		runAutopilotUninstallSchedule(subArgs)
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
	fmt.Println("  start               Start the autopilot scheduler (in-process)")
	fmt.Println("  stop                Stop the autopilot scheduler")
	fmt.Println("  status              Show autopilot scheduler status")
	fmt.Println("  trigger             Manually trigger a scan cycle")
	fmt.Println("  config              Show or update autopilot configuration")
	fmt.Println("  tick                Run one tick of background jobs (doctor, retention, hygiene)")
	fmt.Println("  install-schedule    Install OS-native scheduler for periodic ticks")
	fmt.Println("  uninstall-schedule  Uninstall OS-native scheduler")
	fmt.Println("  help                Show this help message")
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

// runAutopilotTick runs stateless background maintenance jobs and exits.
func runAutopilotTick(args []string) {
	fs := flag.NewFlagSet("autopilot tick", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	jobsFlag := fs.String("jobs", "", "comma-separated list of jobs to run (default: doctor,retention,hygiene)")
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "tick", *traceID, err.Error(), "", *jsonl)
	}

	var jobs []string
	if *jobsFlag != "" {
		for _, j := range strings.Split(*jobsFlag, ",") {
			j = strings.TrimSpace(j)
			if j == "" {
				continue
			}
			switch j {
			case "doctor", "retention", "hygiene":
				jobs = append(jobs, j)
			default:
				exitUsage("autopilot", "tick", *traceID, fmt.Sprintf("invalid job %q: allowed jobs are doctor, retention, hygiene", j), "Choose from: doctor, retention, hygiene", *jsonl)
			}
		}
	} else {
		jobs = []string{"doctor", "retention", "hygiene"}
	}

	ctx := context.Background()
	jsonlMode := *jsonl || !*jsonMode

	for _, job := range jobs {
		var status string
		var detail any

		switch job {
		case "doctor":
			status, detail = runTickDoctor(ctx)
		case "retention":
			status, detail = runTickRetention(ctx)
		case "hygiene":
			status, detail = runTickHygiene(ctx)
		}

		data := map[string]any{
			"job":    job,
			"status": status,
			"detail": detail,
		}
		env := cli.NewEnvelope("autopilot_tick", "autopilot", "tick", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, jsonlMode)
	}
}

func runTickDoctor(ctx context.Context) (string, any) {
	doc := doctor.New()
	dbPath, _ := databasePath()
	report := doc.RunDiagnosticsWithFix(ctx, dbPath, false)
	if report == nil {
		return "error", map[string]any{"error": "nil report returned from doctor"}
	}
	status := "ok"
	if report.OverallStatus == "UNHEALTHY" {
		status = "unhealthy"
	}
	detail := map[string]any{
		"overall_status": report.OverallStatus,
		"checks_count":   len(report.Checks),
		"summary":        fmt.Sprintf("overall: %s (%d checks evaluated)", report.OverallStatus, len(report.Checks)),
		"checks":         report.Checks,
	}
	return status, detail
}

func runTickRetention(ctx context.Context) (string, any) {
	retentionDays := 0
	if mgr, err := settings.NewManager(""); err == nil {
		if v, ok := mgr.Get("evidence_retention_days"); ok && v != nil {
			if s, ok := v.(string); ok && s != "" {
				if n, err := strconv.Atoi(s); err == nil && n >= 0 {
					retentionDays = n
				}
			}
		}
	}
	if env := os.Getenv("G8S_EVIDENCE_RETENTION_DAYS"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n >= 0 {
			retentionDays = n
		}
	}

	evidenceDir := pathutil.DefaultEvidenceDir()
	if mgr, err := settings.NewManager(""); err == nil {
		if v, ok := mgr.Get("evidence_dir"); ok && v != nil {
			if s, ok := v.(string); ok && s != "" {
				evidenceDir = s
			}
		}
	}
	if env := os.Getenv("G8S_EVIDENCE_DIR"); env != "" {
		evidenceDir = env
	}

	cfg := cleanup.CleanupConfig{
		Targets:               []string{cleanup.TargetEvidence},
		EvidenceDir:           evidenceDir,
		EvidenceRetentionDays: retentionDays,
		DryRun:                false,
		Clock:                 time.Now,
		Writer:                io.Discard,
	}
	report, err := cleanup.RunCleanupSweep(ctx, cfg)
	if err != nil {
		return "error", map[string]any{
			"error":          err.Error(),
			"evidence_dir":   evidenceDir,
			"retention_days": retentionDays,
		}
	}
	detail := map[string]any{
		"evidence_dir":   evidenceDir,
		"retention_days": retentionDays,
		"removed_count":  len(report.Items),
		"items":          report.Items,
	}
	return "ok", detail
}

func runTickHygiene(ctx context.Context) (string, any) {
	dbPath, _ := databasePath()
	stateDir := pathutil.DefaultStateDir()

	hbDir := filepath.Join(stateDir, "heartbeat")
	if _, err := os.Stat(hbDir); os.IsNotExist(err) {
		if _, err := os.Stat(".heartbeat"); err == nil {
			hbDir = ".heartbeat"
		}
	}

	cfg := cleanup.CleanupConfig{
		Targets: []string{
			cleanup.TargetGhostProcess,
			cleanup.TargetOrphanWT,
			cleanup.TargetOrphanDir,
			cleanup.TargetOrphanSession,
		},
		DBPath:             dbPath,
		WorktreeBaseDir:    filepath.Join(stateDir, "worktrees"),
		HeartbeatDir:       hbDir,
		DryRun:             false,
		Clock:              time.Now,
		Writer:             io.Discard,
		SessionGracePeriod: cleanup.DefaultSessionGrace,
	}
	report, err := cleanup.RunCleanupSweep(ctx, cfg)
	if err != nil {
		return "error", map[string]any{"error": err.Error()}
	}
	detail := map[string]any{
		"items":   report.Items,
		"summary": report.Summary,
	}
	return "ok", detail
}

func resolveG8sBinaryPath() (string, error) {
	if env := os.Getenv("G8S_BIN_PATH"); env != "" {
		abs, err := filepath.Abs(env)
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs, nil
			}
		}
		return "", fmt.Errorf("configured G8S_BIN_PATH does not exist: %s", env)
	}

	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot resolve executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		resolved = execPath
	}
	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot determine absolute path for %s: %w", resolved, err)
	}
	fi, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("executable binary does not exist at %s: %w", absPath, err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("resolved binary path %s is a directory", absPath)
	}
	return absPath, nil
}

const crontabMarker = "# g8s-autopilot"

func generateCrontabLine(binPath string, dur time.Duration) string {
	mins := int(dur.Minutes())
	if mins < 1 {
		mins = 1
	}
	var cronSpec string
	if mins < 60 {
		cronSpec = fmt.Sprintf("*/%d * * * *", mins)
	} else if mins == 60 {
		cronSpec = "0 * * * *"
	} else if mins%60 == 0 {
		cronSpec = fmt.Sprintf("0 */%d * * *", mins/60)
	} else {
		cronSpec = fmt.Sprintf("*/%d * * * *", mins)
	}
	return fmt.Sprintf("%s %s autopilot tick %s", cronSpec, binPath, crontabMarker)
}

func updateCrontabContent(existing string, newLine string) string {
	lines := strings.Split(existing, "\n")
	var kept []string
	for _, l := range lines {
		if strings.Contains(l, crontabMarker) {
			continue
		}
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	if newLine != "" {
		kept = append(kept, newLine)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

func installCrontabSchedule(binPath string, dur time.Duration) error {
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	var current string
	if err == nil {
		current = string(out)
	}
	newLine := generateCrontabLine(binPath, dur)
	updated := updateCrontabContent(current, newLine)

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(updated)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab update failed: %w (output: %s)", err, strings.TrimSpace(string(combined)))
	}
	return nil
}

func uninstallCrontabSchedule() error {
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		return nil
	}
	updated := updateCrontabContent(string(out), "")
	if strings.TrimSpace(updated) == "" {
		_ = exec.Command("crontab", "-r").Run()
		return nil
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(updated)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab remove failed: %w (output: %s)", err, strings.TrimSpace(string(combined)))
	}
	return nil
}

const launchAgentLabel = "g8s.autopilot"

func launchAgentPlistPath() (string, error) {
	if env := os.Getenv("G8S_LAUNCH_AGENTS_DIR"); env != "" {
		return filepath.Join(env, launchAgentLabel+".plist"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func generateLaunchAgentPlist(binPath string, dur time.Duration) string {
	seconds := int(dur.Seconds())
	if seconds < 1 {
		seconds = 60
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "  <key>Label</key>\n  <string>%s</string>\n", launchAgentLabel)
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	fmt.Fprintf(&b, "    <string>%s</string>\n", binPath)
	b.WriteString("    <string>autopilot</string>\n")
	b.WriteString("    <string>tick</string>\n")
	b.WriteString("  </array>\n")
	b.WriteString("  <key>StartInterval</key>\n")
	fmt.Fprintf(&b, "  <integer>%d</integer>\n", seconds)
	b.WriteString("  <key>ProcessType</key>\n  <string>Background</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func installLaunchAgentSchedule(binPath string, dur time.Duration) error {
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if _, err := os.Stat(plistPath); err == nil {
		if os.Getenv("G8S_LAUNCH_AGENTS_NO_LOAD") != "1" {
			_ = exec.Command("launchctl", "unload", plistPath).Run()
		}
	}

	content := generateLaunchAgentPlist(binPath, dur)
	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write LaunchAgent plist: %w", err)
	}

	if os.Getenv("G8S_LAUNCH_AGENTS_NO_LOAD") != "1" {
		if out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl load failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func uninstallLaunchAgentSchedule() error {
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	if os.Getenv("G8S_LAUNCH_AGENTS_NO_LOAD") != "1" {
		_ = exec.Command("launchctl", "unload", plistPath).Run()
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove LaunchAgent plist: %w", err)
	}
	return nil
}

const windowsTaskName = "g8s-autopilot"

// windowsSchtasksCreateArgs builds the argument list for schtasks /Create.
// Documented format:
// schtasks /Create /F /SC MINUTE /MO <n> /TN g8s-autopilot /TR "<absolute g8s.exe path> autopilot tick"
func windowsSchtasksCreateArgs(binPath string, dur time.Duration) []string {
	mins := int(dur.Minutes())
	if mins < 1 {
		mins = 1
	}
	taskRun := fmt.Sprintf("\"%s\" autopilot tick", binPath)
	return []string{
		"/Create",
		"/F",
		"/SC", "MINUTE",
		"/MO", strconv.Itoa(mins),
		"/TN", windowsTaskName,
		"/TR", taskRun,
	}
}

// windowsSchtasksDeleteArgs builds the argument list for schtasks /Delete.
func windowsSchtasksDeleteArgs() []string {
	return []string{
		"/Delete",
		"/F",
		"/TN", windowsTaskName,
	}
}

func installWindowsSchedule(binPath string, dur time.Duration) error {
	args := windowsSchtasksCreateArgs(binPath, dur)
	cmd := exec.Command("schtasks", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Create failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstallWindowsSchedule() error {
	args := windowsSchtasksDeleteArgs()
	cmd := exec.Command("schtasks", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		outStr := strings.ToLower(string(out))
		if strings.Contains(outStr, "cannot find") || strings.Contains(outStr, "does not exist") {
			return nil
		}
		return fmt.Errorf("schtasks /Delete failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runAutopilotInstallSchedule(args []string) {
	fs := flag.NewFlagSet("autopilot install-schedule", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	everyFlag := fs.String("every", "", "schedule interval (e.g. 10m, 1h, 30m)")
	dryRunFlag := fs.Bool("dry-run", false, "generate schedule configuration without installing")
	generateOnlyFlag := fs.Bool("generate-only", false, "alias for --dry-run")
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "install-schedule", *traceID, err.Error(), "", *jsonl)
	}

	if *everyFlag == "" {
		exitUsage("autopilot", "install-schedule", *traceID, "--every flag is required", "Provide a schedule interval such as --every 10m or --every 1h", *jsonl)
	}

	dur, err := time.ParseDuration(*everyFlag)
	if err != nil || dur <= 0 {
		exitUsage("autopilot", "install-schedule", *traceID, fmt.Sprintf("invalid duration %q: %v", *everyFlag, err), "Provide a valid duration such as 10m, 1h", *jsonl)
	}
	if dur < time.Minute {
		exitUsage("autopilot", "install-schedule", *traceID, fmt.Sprintf("interval %v is too short: minimum schedule interval is 1m", dur), "Use --every 1m or greater", *jsonl)
	}

	binPath, err := resolveG8sBinaryPath()
	if err != nil {
		exitRuntime("autopilot", "install-schedule", *traceID, cli.CodeRuntime,
			fmt.Errorf("cannot resolve real g8s binary path: %w", err),
			"Schedule must point at an existing executable on disk",
			*jsonl)
		return
	}

	dryRun := *dryRunFlag || *generateOnlyFlag
	targetOS := runtime.GOOS

	if dryRun {
		var generated string
		switch targetOS {
		case "darwin":
			generated = generateLaunchAgentPlist(binPath, dur)
		case "windows":
			args := windowsSchtasksCreateArgs(binPath, dur)
			generated = "schtasks " + strings.Join(args, " ")
		default: // linux and others
			generated = generateCrontabLine(binPath, dur)
		}

		if *jsonMode || *jsonl {
			env := cli.NewEnvelope("autopilot_schedule", "autopilot", "install-schedule", map[string]any{
				"status":    "dry_run",
				"every":     *everyFlag,
				"duration":  dur.String(),
				"target_os": targetOS,
				"binary":    binPath,
				"generated": generated,
			})
			env.TraceID = *traceID
			_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		} else {
			fmt.Println(generated)
		}
		return
	}

	var installErr error
	switch targetOS {
	case "darwin":
		installErr = installLaunchAgentSchedule(binPath, dur)
	case "windows":
		installErr = installWindowsSchedule(binPath, dur)
	case "linux":
		installErr = installCrontabSchedule(binPath, dur)
	default:
		installErr = fmt.Errorf("unsupported OS %q for native autopilot scheduling", targetOS)
	}

	if installErr != nil {
		exitRuntime("autopilot", "install-schedule", *traceID, cli.CodeRuntime, installErr, "Check permissions and system scheduler availability", *jsonl)
		return
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_schedule", "autopilot", "install-schedule", map[string]any{
			"status":    "installed",
			"every":     *everyFlag,
			"duration":  dur.String(),
			"target_os": targetOS,
			"binary":    binPath,
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		fmt.Printf("Installed native autopilot schedule (every %s, OS: %s)\n", *everyFlag, targetOS)
	}
}

func runAutopilotUninstallSchedule(args []string) {
	fs := flag.NewFlagSet("autopilot uninstall-schedule", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("autopilot", "uninstall-schedule", *traceID, err.Error(), "", *jsonl)
	}

	targetOS := runtime.GOOS
	var uninstallErr error
	switch targetOS {
	case "darwin":
		uninstallErr = uninstallLaunchAgentSchedule()
	case "windows":
		uninstallErr = uninstallWindowsSchedule()
	case "linux":
		uninstallErr = uninstallCrontabSchedule()
	default:
		uninstallErr = fmt.Errorf("unsupported OS %q for native autopilot scheduling", targetOS)
	}

	if uninstallErr != nil {
		exitRuntime("autopilot", "uninstall-schedule", *traceID, cli.CodeRuntime, uninstallErr, "", *jsonl)
		return
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("autopilot_schedule", "autopilot", "uninstall-schedule", map[string]any{
			"status":    "uninstalled",
			"target_os": targetOS,
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		fmt.Printf("Uninstalled native autopilot schedule (OS: %s)\n", targetOS)
	}
}
