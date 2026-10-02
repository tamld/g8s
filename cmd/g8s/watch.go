package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/watch"
)

// Supported milestone modes for watch (#436).
const (
	MilestoneAccept         = "accept"
	MilestoneWorkerComplete = "worker-complete"

	// StateWorkerComplete indicates worker finished; awaiting supervisor acceptance (#436).
	StateWorkerComplete watch.VerdictState = "worker-complete"

	// DetailWorkerComplete is the detail message when WORKER_COMPLETED milestone is reached.
	DetailWorkerComplete = "worker finished; awaiting supervisor acceptance"
)

// TaskVerdict represents the mapped resolution of a task observation under a milestone.
type TaskVerdict struct {
	Done     bool
	State    watch.VerdictState
	Failed   []string
	Detail   string
	ExitCode int
}

// resolveTaskVerdict maps a CheckResult and milestone into a TaskVerdict.
func resolveTaskVerdict(res watch.CheckResult, milestone string) TaskVerdict {
	if !res.Done {
		return TaskVerdict{
			Done:     false,
			Detail:   res.Detail,
			ExitCode: -1,
		}
	}
	if !res.Passed {
		return TaskVerdict{
			Done:     true,
			State:    watch.StateFailed,
			Failed:   res.Failed,
			Detail:   res.Detail,
			ExitCode: 1,
		}
	}
	if milestone == MilestoneWorkerComplete && res.Detail == DetailWorkerComplete {
		return TaskVerdict{
			Done:     true,
			State:    StateWorkerComplete,
			Detail:   DetailWorkerComplete,
			ExitCode: 0,
		}
	}
	return TaskVerdict{
		Done:     true,
		State:    watch.StatePassed,
		Detail:   res.Detail,
		ExitCode: 0,
	}
}

// runWatch blocks until a watched condition reaches a terminal state (#371).
// Run it as a background process: the process-exit notification is the push
// channel that wakes the supervisor without any sleep-polling.
//
//	g8s watch --pr 356 [--interval 60s] [--timeout 30m]
//	g8s watch --task <task-id> [--interval 10s] [--milestone <accept|worker-complete>]
//	g8s watch --failed [--since <RFC3339|duration>] [--timeout <dur>]
func runWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	prFlag := fs.Int("pr", 0, "GitHub PR number: watch its CI checks until terminal")
	taskFlag := fs.String("task", "", "g8s task id: watch its state until terminal")
	interval := fs.Duration("interval", 60*time.Second, "poll interval (default 60s for task/pr watch; 500ms for --failed)")
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after this long (default 30m for task/pr watch; default 0 = block forever for --failed; exit 3)")
	milestone := fs.String("milestone", MilestoneAccept, "watch milestone: accept (default) or worker-complete")
	failed := fs.Bool("failed", false, "watch signals file (<db-dir>/signals/tasks.jsonl) for worker terminal/failed events")
	since := fs.String("since", "", "filter signals after RFC3339 timestamp or Go duration (e.g. 5m, 1h)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Usage:
  g8s watch --task <task-id> [--interval 10s] [--timeout 30m] [--milestone <accept|worker-complete>]
  g8s watch --pr <pr-number> [--interval 60s] [--timeout 30m]
  g8s watch --failed [--since <RFC3339|duration>] [--timeout <dur>]

Long-polls for task/PR completion, or watches signals file (<db-dir>/signals/tasks.jsonl)
for worker terminal/failed events.

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), `
Exit codes (--failed mode):
  0   Signal matched
  3   Timeout elapsed
  130 Interrupt (SIGINT/SIGTERM)
`)
	}

	if err := fs.Parse(args); err != nil {
		exitUsage("watch", "", *traceID, err.Error(), "", *jsonl)
	}

	if *failed {
		if *prFlag != 0 || *taskFlag != "" {
			exitUsage("watch", "", *traceID, "--failed cannot be used with --pr or --task", "g8s watch --failed [--since <RFC3339|duration>] [--timeout <dur>]", *jsonl)
			return
		}
		var timeoutProvided bool
		var intervalProvided bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "timeout" {
				timeoutProvided = true
			}
			if f.Name == "interval" {
				intervalProvided = true
			}
		})

		failedTimeout := time.Duration(0)
		if timeoutProvided {
			failedTimeout = *timeout
		}
		pollInterval := 500 * time.Millisecond
		if intervalProvided {
			pollInterval = *interval
		}

		var sinceTime time.Time
		if *since != "" {
			st, serr := parseSince(*since)
			if serr != nil {
				exitUsage("watch", "", *traceID, serr.Error(), "g8s watch --failed --since 5m", *jsonl)
				return
			}
			sinceTime = st
		}

		dbPath, perr := databasePath()
		if perr != nil {
			exitRuntime("watch", "", *traceID, cli.CodeIO, perr, "", *jsonl)
			return
		}
		signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		cfg := WatchFailedConfig{
			SignalsPath:  signalsPath,
			Since:        sinceTime,
			Timeout:      failedTimeout,
			PollInterval: pollInterval,
			TraceID:      *traceID,
			JSONL:        *jsonl,
			Out:          os.Stdout,
			ErrOut:       os.Stderr,
		}

		code, err := watchFailed(ctx, cfg)
		if err != nil {
			exitRuntime("watch", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
			return
		}
		os.Exit(code)
	}

	if (*prFlag == 0) == (*taskFlag == "") {
		exitUsage("watch", "", *traceID, "exactly one of --pr or --task is required", "g8s watch --pr 356 --interval 60s", *jsonl)
		return
	}
	if *milestone != MilestoneAccept && *milestone != MilestoneWorkerComplete {
		exitUsage("watch", "", *traceID, fmt.Sprintf("invalid --milestone %q (must be %s or %s)", *milestone, MilestoneAccept, MilestoneWorkerComplete), "g8s watch --task <task-id> --milestone worker-complete", *jsonl)
		return
	}

	var target string
	var checker watch.Checker
	switch {
	case *prFlag != 0:
		target = fmt.Sprintf("pr/%d", *prFlag)
		pr := *prFlag
		checker = func(ctx context.Context) (watch.CheckResult, error) {
			return checkPR(ctx, pr)
		}
	default:
		target = "task/" + *taskFlag
		taskID := *taskFlag
		dbPath, perr := databasePath()
		if perr != nil {
			exitRuntime("watch", "", *traceID, cli.CodeIO, perr, "", *jsonl)
			return
		}
		store, serr := controlplane.NewControlPlane(dbPath, nil)
		if serr != nil {
			exitRuntime("watch", "", *traceID, cli.CodeRuntime, serr, "", *jsonl)
			return
		}
		defer store.Close()
		checker = func(ctx context.Context) (watch.CheckResult, error) {
			return checkTask(ctx, store, taskID, *milestone)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := watch.Watcher{Checker: checker, Interval: *interval, Timeout: *timeout}
	verdict, err := w.Run(ctx)
	if err != nil {
		exitRuntime("watch", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		return
	}

	if *milestone == MilestoneWorkerComplete && verdict.Detail == DetailWorkerComplete {
		verdict.State = StateWorkerComplete
	}

	data := map[string]any{
		"target":  target,
		"state":   string(verdict.State),
		"failed":  verdict.Failed,
		"detail":  verdict.Detail,
		"elapsed": verdict.Elapsed.String(),
		"polls":   verdict.Polls,
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("watch_verdict", "watch", "", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		pterm.Info.Printf("watch %s: %s (%s, %d polls)\n", target, verdict.State, verdict.Elapsed.Round(time.Second), verdict.Polls)
		if len(verdict.Failed) > 0 {
			pterm.Warning.Println("failed: " + strings.Join(verdict.Failed, ", "))
		}
	}

	switch verdict.State {
	case watch.StatePassed, StateWorkerComplete:
		return
	case watch.StateFailed:
		os.Exit(1)
	default:
		os.Exit(2)
	}
}

// checkPR observes one GitHub PR's CI checks via gh.
func checkPR(ctx context.Context, pr int) (watch.CheckResult, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "checks", fmt.Sprint(pr),
		"--repo", "tamld/g8s", "--json", "name,state")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// gh exits non-zero while checks are pending — treat as not-done.
		if strings.Contains(string(out), "no checks") || strings.Contains(err.Error(), "exit status 8") {
			return watch.CheckResult{Done: false}, nil
		}
		return watch.CheckResult{}, fmt.Errorf("gh pr checks: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var checks []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if jerr := json.Unmarshal(out, &checks); jerr != nil {
		return watch.CheckResult{}, fmt.Errorf("decode gh pr checks: %w", jerr)
	}
	if len(checks) == 0 {
		return watch.CheckResult{Done: false}, nil
	}
	res := watch.CheckResult{Done: true, Passed: true}
	for _, c := range checks {
		switch c.State {
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			// healthy
		case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			res.Failed = append(res.Failed, c.Name+" ("+c.State+")")
			res.Passed = false
		default:
			res.Done = false // still pending
		}
	}
	res.Detail = fmt.Sprintf("%d checks observed", len(checks))
	return res, nil
}

// checkTask observes one g8s task's state via the control plane (#436).
func checkTask(ctx context.Context, store *controlplane.Store, taskID string, milestone ...string) (watch.CheckResult, error) {
	m := MilestoneAccept
	if len(milestone) > 0 && milestone[0] != "" {
		m = milestone[0]
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return watch.CheckResult{}, fmt.Errorf("get task: %w", err)
	}
	if task == nil {
		return watch.CheckResult{}, fmt.Errorf("task %s not found", taskID)
	}
	switch task.State {
	case "SUCCEEDED":
		return watch.CheckResult{Done: true, Passed: true, Detail: "task " + taskID + " succeeded"}, nil
	case "WORKER_COMPLETED":
		if m == MilestoneWorkerComplete {
			return watch.CheckResult{
				Done:   true,
				Passed: true,
				Detail: DetailWorkerComplete,
			}, nil
		}
		return watch.CheckResult{Done: false, Detail: "state " + task.State}, nil
	case "FAILED", "CANCELLED", "TIMED_OUT":
		return watch.CheckResult{Done: true, Passed: false, Failed: []string{task.State}, Detail: "task " + taskID + " ended " + task.State}, nil
	default:
		return watch.CheckResult{Done: false, Detail: "state " + task.State}, nil
	}
}

// WatchFailedConfig holds options for the watch --failed mode.
type WatchFailedConfig struct {
	SignalsPath  string
	Since        time.Time
	Timeout      time.Duration
	PollInterval time.Duration
	TraceID      string
	JSONL        bool
	Out          io.Writer
	ErrOut       io.Writer
}

const (
	ExitCodeSignalMatched = 0
	ExitCodeTimeout       = 3
	ExitCodeInterrupt     = 130
)

// isTerminalState reports whether state is a recognized terminal state.
func isTerminalState(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "FAILED", "SUCCEEDED", "CANCELLED", "WORKER_COMPLETED", "NEEDS_INFO",
		"TIMED_OUT", "SUPERVISOR_ACCEPTED", "SUPERVISOR_REJECTED":
		return true
	default:
		return false
	}
}

// parseSince parses a --since argument, which can be an RFC3339 timestamp
// or a Go duration string (e.g., "5m", "1h") meaning "now minus duration".
func parseSince(sinceStr string) (time.Time, error) {
	sinceStr = strings.TrimSpace(sinceStr)
	if sinceStr == "" {
		return time.Time{}, nil
	}
	dur, err := time.ParseDuration(sinceStr)
	if err == nil {
		if dur < 0 {
			dur = -dur
		}
		return time.Now().UTC().Add(-dur), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q: must be RFC3339 timestamp or Go duration (e.g. 5m, 1h)", sinceStr)
}

// parseSignalTime parses an RFC3339 or RFC3339Nano timestamp.
func parseSignalTime(ts string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// parseAndMatchSignalLine parses a raw JSON line. If it is malformed, it prints a stderr
// warning and returns (nil, false). If valid, it returns the record and whether it matches
// the terminal state and since criteria.
func parseAndMatchSignalLine(line []byte, since time.Time, errOut io.Writer) (map[string]any, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil, false
	}
	var record map[string]any
	if err := json.Unmarshal(trimmed, &record); err != nil {
		fmt.Fprintf(errOut, "watch: warning: malformed signal line: %s\n", string(trimmed))
		return nil, false
	}
	tsVal, ok1 := record["ts"].(string)
	taskIDVal, ok2 := record["task_id"].(string)
	toVal, ok3 := record["to"].(string)
	if !ok1 || !ok2 || !ok3 || tsVal == "" || taskIDVal == "" || toVal == "" {
		fmt.Fprintf(errOut, "watch: warning: malformed signal line: %s\n", string(trimmed))
		return nil, false
	}
	t, err := parseSignalTime(tsVal)
	if err != nil {
		fmt.Fprintf(errOut, "watch: warning: malformed signal line: %s\n", string(trimmed))
		return nil, false
	}
	if !isTerminalState(toVal) {
		return record, false
	}
	if !since.IsZero() && !t.After(since) {
		return record, false
	}
	return record, true
}

// watchFailed long-polls the signals file for terminal transitions.
func watchFailed(ctx context.Context, cfg WatchFailedConfig) (int, error) {
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	if cfg.ErrOut == nil {
		cfg.ErrOut = os.Stderr
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.TraceID == "" {
		cfg.TraceID = cli.GenerateTraceID()
	}

	var timeoutCh <-chan time.Time
	if cfg.Timeout > 0 {
		timer := time.NewTimer(cfg.Timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	var currentOffset int64

	// 1. Startup: read whole file if it exists.
	// Print every line whose `to` is a terminal state AND ts > --since as JSON envelope.
	// If a matching line exists at startup -> print and EXIT 0 immediately.
	data, err := os.ReadFile(cfg.SignalsPath)
	if err == nil {
		lastNL := bytes.LastIndexByte(data, '\n')
		if lastNL != -1 {
			currentOffset = int64(lastNL + 1)
			lines := bytes.Split(data[:lastNL+1], []byte{'\n'})
			matchedAtStartup := 0
			for _, line := range lines {
				rec, match := parseAndMatchSignalLine(line, cfg.Since, cfg.ErrOut)
				if match {
					env := cli.NewEnvelope("task_signal", "watch", "", rec)
					env.TraceID = cfg.TraceID
					_ = cli.WriteResponse(cfg.Out, env, cfg.JSONL)
					matchedAtStartup++
				}
			}
			if matchedAtStartup > 0 {
				return ExitCodeSignalMatched, nil
			}
		}
	}

	// 2. Tail loop: check for appended bytes every cfg.PollInterval.
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ExitCodeInterrupt, nil
		case <-timeoutCh:
			env := cli.NewEnvelope("task_signal_timeout", "watch", "", map[string]any{
				"timeout": cfg.Timeout.String(),
			})
			env.TraceID = cfg.TraceID
			_ = cli.WriteResponse(cfg.Out, env, cfg.JSONL)
			return ExitCodeTimeout, nil
		case <-ticker.C:
			fi, err := os.Stat(cfg.SignalsPath)
			if err != nil {
				// File does not exist yet (or transient error); keep waiting.
				continue
			}
			size := fi.Size()
			if size < currentOffset {
				// File truncated or replaced.
				currentOffset = 0
			}
			if size > currentOffset {
				f, err := os.Open(cfg.SignalsPath)
				if err != nil {
					continue
				}
				_, err = f.Seek(currentOffset, io.SeekStart)
				if err != nil {
					f.Close()
					continue
				}
				buf := make([]byte, size-currentOffset)
				n, err := io.ReadFull(f, buf)
				f.Close()
				if err != nil && err != io.ErrUnexpectedEOF {
					continue
				}
				buf = buf[:n]
				lastNL := bytes.LastIndexByte(buf, '\n')
				if lastNL == -1 {
					// Incomplete line; wait for newline to be appended.
					continue
				}
				currentOffset += int64(lastNL + 1)
				lines := bytes.Split(buf[:lastNL+1], []byte{'\n'})
				for _, line := range lines {
					rec, match := parseAndMatchSignalLine(line, cfg.Since, cfg.ErrOut)
					if match {
						env := cli.NewEnvelope("task_signal", "watch", "", rec)
						env.TraceID = cfg.TraceID
						_ = cli.WriteResponse(cfg.Out, env, cfg.JSONL)
						return ExitCodeSignalMatched, nil
					}
				}
			}
		}
	}
}
