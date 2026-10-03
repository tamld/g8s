package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWatchFailed_ImmediateOnFailedLine verifies:
// 1. File with a FAILED line (ts now) → exits immediately (code 0), envelope carries task_id/to.
func TestWatchFailed_ImmediateOnFailedLine(t *testing.T) {
	tempDir := t.TempDir()
	signalsDir := filepath.Join(tempDir, "signals")
	if err := os.MkdirAll(signalsDir, 0o755); err != nil {
		t.Fatalf("mkdir signals dir: %v", err)
	}
	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")

	nowStr := time.Now().UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"ts":%q,"task_id":"task-failed-1","from":"RUNNING","to":"FAILED"}`+"\n", nowStr)
	if err := os.WriteFile(signalsPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write signals file: %v", err)
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cfg := WatchFailedConfig{
		SignalsPath:  signalsPath,
		Timeout:      2 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Out:          &outBuf,
		ErrOut:       &errBuf,
	}

	start := time.Now()
	exitCode, err := watchFailed(context.Background(), cfg)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("watchFailed unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("watchFailed exitCode = %d, want 0", exitCode)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("watchFailed should exit immediately, took %v", elapsed)
	}

	var env struct {
		Kind string         `json:"kind"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (raw: %s)", err, outBuf.String())
	}
	if env.Kind != "task_signal" {
		t.Errorf("env.Kind = %q, want task_signal", env.Kind)
	}
	if env.Data["task_id"] != "task-failed-1" {
		t.Errorf("env.Data[task_id] = %v, want task-failed-1", env.Data["task_id"])
	}
	if env.Data["to"] != "FAILED" {
		t.Errorf("env.Data[to] = %v, want FAILED", env.Data["to"])
	}
}

// TestWatchFailed_TimeoutOnNonTerminalTransitions verifies:
// 2. File with only non-terminal transitions → keeps waiting until the timeout budget (100ms) → exit code 3.
func TestWatchFailed_TimeoutOnNonTerminalTransitions(t *testing.T) {
	tempDir := t.TempDir()
	signalsDir := filepath.Join(tempDir, "signals")
	if err := os.MkdirAll(signalsDir, 0o755); err != nil {
		t.Fatalf("mkdir signals dir: %v", err)
	}
	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")

	nowStr := time.Now().UTC().Format(time.RFC3339)
	lines := fmt.Sprintf(`{"ts":%q,"task_id":"task-1","from":"NONE","to":"QUEUED"}`+"\n"+
		`{"ts":%q,"task_id":"task-1","from":"QUEUED","to":"RUNNING"}`+"\n"+
		`{"ts":%q,"task_id":"task-2","from":"RUNNING","to":"LEASED"}`+"\n", nowStr, nowStr, nowStr)
	if err := os.WriteFile(signalsPath, []byte(lines), 0o644); err != nil {
		t.Fatalf("write signals file: %v", err)
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	timeoutBudget := 100 * time.Millisecond
	cfg := WatchFailedConfig{
		SignalsPath:  signalsPath,
		Timeout:      timeoutBudget,
		PollInterval: 10 * time.Millisecond,
		Out:          &outBuf,
		ErrOut:       &errBuf,
	}

	start := time.Now()
	exitCode, err := watchFailed(context.Background(), cfg)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("watchFailed unexpected error: %v", err)
	}
	if exitCode != 3 {
		t.Fatalf("watchFailed exitCode = %d, want 3", exitCode)
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("watchFailed should wait near timeout budget, waited %v", elapsed)
	}

	var env struct {
		Kind string         `json:"kind"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal timeout envelope: %v (raw: %s)", err, outBuf.String())
	}
	if env.Kind != "task_signal_timeout" {
		t.Errorf("env.Kind = %q, want task_signal_timeout", env.Kind)
	}
}

// TestWatchFailed_MidWaitFileAppearance verifies:
// 3. File appears MID-wait (written from goroutine after 100ms) → wakes and exits with event (proves tail).
func TestWatchFailed_MidWaitFileAppearance(t *testing.T) {
	tempDir := t.TempDir()
	signalsDir := filepath.Join(tempDir, "signals")
	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")

	// Ensure file does not exist at startup
	_ = os.Remove(signalsPath)

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.MkdirAll(signalsDir, 0o755)
		nowStr := time.Now().UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"ts":%q,"task_id":"task-mid-1","from":"RUNNING","to":"FAILED"}`+"\n", nowStr)
		_ = os.WriteFile(signalsPath, []byte(line), 0o644)
	}()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cfg := WatchFailedConfig{
		SignalsPath:  signalsPath,
		Timeout:      2 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Out:          &outBuf,
		ErrOut:       &errBuf,
	}

	start := time.Now()
	exitCode, err := watchFailed(context.Background(), cfg)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("watchFailed unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("watchFailed exitCode = %d, want 0", exitCode)
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("watchFailed should have waited for mid-wait file appearance, elapsed %v", elapsed)
	}

	var env struct {
		Kind string         `json:"kind"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (raw: %s)", err, outBuf.String())
	}
	if env.Kind != "task_signal" {
		t.Errorf("env.Kind = %q, want task_signal", env.Kind)
	}
	if env.Data["task_id"] != "task-mid-1" {
		t.Errorf("env.Data[task_id] = %v, want task-mid-1", env.Data["task_id"])
	}
	if env.Data["to"] != "FAILED" {
		t.Errorf("env.Data[to] = %v, want FAILED", env.Data["to"])
	}
}

// TestWatchFailed_SinceFilter verifies:
// 4. --since filters: an old FAILED line before --since → ignored.
func TestWatchFailed_SinceFilter(t *testing.T) {
	tempDir := t.TempDir()
	signalsDir := filepath.Join(tempDir, "signals")
	if err := os.MkdirAll(signalsDir, 0o755); err != nil {
		t.Fatalf("mkdir signals dir: %v", err)
	}
	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")

	now := time.Now().UTC()
	oldTime := now.Add(-1 * time.Hour).Format(time.RFC3339)
	oldLine := fmt.Sprintf(`{"ts":%q,"task_id":"task-old-1","from":"RUNNING","to":"FAILED"}`+"\n", oldTime)
	if err := os.WriteFile(signalsPath, []byte(oldLine), 0o644); err != nil {
		t.Fatalf("write signals file: %v", err)
	}

	sinceTime := now.Add(-5 * time.Minute)

	t.Run("old line ignored and times out", func(t *testing.T) {
		var outBuf bytes.Buffer
		var errBuf bytes.Buffer
		cfg := WatchFailedConfig{
			SignalsPath:  signalsPath,
			Since:        sinceTime,
			Timeout:      100 * time.Millisecond,
			PollInterval: 10 * time.Millisecond,
			Out:          &outBuf,
			ErrOut:       &errBuf,
		}

		exitCode, err := watchFailed(context.Background(), cfg)
		if err != nil {
			t.Fatalf("watchFailed unexpected error: %v", err)
		}
		if exitCode != 3 {
			t.Fatalf("watchFailed exitCode = %d, want 3 (timeout because old line ignored)", exitCode)
		}
	})

	t.Run("newer line matches", func(t *testing.T) {
		newTime := now.Add(1 * time.Second).Format(time.RFC3339)
		newLine := fmt.Sprintf(`{"ts":%q,"task_id":"task-new-1","from":"RUNNING","to":"FAILED"}`+"\n", newTime)
		f, err := os.OpenFile(signalsPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open signals file: %v", err)
		}
		if _, err := f.WriteString(newLine); err != nil {
			f.Close()
			t.Fatalf("append new line: %v", err)
		}
		f.Close()

		var outBuf bytes.Buffer
		var errBuf bytes.Buffer
		cfg := WatchFailedConfig{
			SignalsPath:  signalsPath,
			Since:        sinceTime,
			Timeout:      1 * time.Second,
			PollInterval: 10 * time.Millisecond,
			Out:          &outBuf,
			ErrOut:       &errBuf,
		}

		exitCode, err := watchFailed(context.Background(), cfg)
		if err != nil {
			t.Fatalf("watchFailed unexpected error: %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("watchFailed exitCode = %d, want 0", exitCode)
		}

		var env struct {
			Kind string         `json:"kind"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(outBuf.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.Data["task_id"] != "task-new-1" {
			t.Errorf("env.Data[task_id] = %v, want task-new-1", env.Data["task_id"])
		}
	})
}

// TestWatchFailed_MalformedLinesSkipped verifies:
// 5. Malformed lines interleaved with a valid FAILED line → skipped without error, valid line still reported.
func TestWatchFailed_MalformedLinesSkipped(t *testing.T) {
	tempDir := t.TempDir()
	signalsDir := filepath.Join(tempDir, "signals")
	if err := os.MkdirAll(signalsDir, 0o755); err != nil {
		t.Fatalf("mkdir signals dir: %v", err)
	}
	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")

	nowStr := time.Now().UTC().Format(time.RFC3339)
	content := "not a json string at all\n" +
		`{"ts": 12345, "task_id": "bad-ts", "to": "FAILED"}` + "\n" +
		`{"missing_fields": true}` + "\n" +
		fmt.Sprintf(`{"ts":%q,"task_id":"task-valid","from":"RUNNING","to":"FAILED"}`+"\n", nowStr) +
		`{"incomplete": "json` + "\n"
	if err := os.WriteFile(signalsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write signals file: %v", err)
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cfg := WatchFailedConfig{
		SignalsPath:  signalsPath,
		Timeout:      1 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Out:          &outBuf,
		ErrOut:       &errBuf,
	}

	exitCode, err := watchFailed(context.Background(), cfg)
	if err != nil {
		t.Fatalf("watchFailed unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("watchFailed exitCode = %d, want 0", exitCode)
	}

	var env struct {
		Kind string         `json:"kind"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Data["task_id"] != "task-valid" {
		t.Errorf("env.Data[task_id] = %v, want task-valid", env.Data["task_id"])
	}

	errOutput := errBuf.String()
	if !strings.Contains(errOutput, "warning") && !strings.Contains(errOutput, "malformed") {
		t.Errorf("expected warning in stderr about malformed lines, got: %q", errOutput)
	}
}

// TestWatchFailed_ContextInterrupt verifies:
// Exit 130 convention on context cancellation (SIGINT/SIGTERM).
func TestWatchFailed_ContextInterrupt(t *testing.T) {
	tempDir := t.TempDir()
	signalsPath := filepath.Join(tempDir, "signals", "tasks.jsonl")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 50ms
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cfg := WatchFailedConfig{
		SignalsPath:  signalsPath,
		Timeout:      2 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Out:          &outBuf,
		ErrOut:       &errBuf,
	}

	exitCode, err := watchFailed(ctx, cfg)
	if err != nil {
		t.Fatalf("watchFailed unexpected error: %v", err)
	}
	if exitCode != 130 {
		t.Fatalf("watchFailed exitCode on cancel = %d, want 130", exitCode)
	}
}

// TestWatchFailed_ParseSince verifies duration and RFC3339 parsing for --since.
func TestWatchFailed_ParseSince(t *testing.T) {
	// Empty string
	t1, err := parseSince("")
	if err != nil || !t1.IsZero() {
		t.Errorf("parseSince('') = (%v, %v), want zero time and no error", t1, err)
	}

	// Go duration "5m"
	before := time.Now().Add(-5 * time.Minute)
	t2, err := parseSince("5m")
	if err != nil {
		t.Errorf("parseSince('5m') error = %v", err)
	}
	if diff := t2.Sub(before); diff < -time.Second || diff > time.Second {
		t.Errorf("parseSince('5m') diff = %v, want near zero", diff)
	}

	// RFC3339
	rfc := "2026-10-02T12:00:00Z"
	t3, err := parseSince(rfc)
	if err != nil {
		t.Errorf("parseSince(%q) error = %v", rfc, err)
	}
	if t3.Format(time.RFC3339) != rfc {
		t.Errorf("parseSince(%q) = %v, want %s", rfc, t3, rfc)
	}

	// Invalid
	_, err = parseSince("not-a-valid-since")
	if err == nil {
		t.Errorf("expected error for invalid since, got nil")
	}
}

// TestWatchFailed_CLIIntegration tests g8s watch --failed via compiled binary.
func TestWatchFailed_CLIIntegration(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	signalsDir := filepath.Join(tempDir, "signals")
	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")

	t.Run("help text documents required flags, path, and exit codes", func(t *testing.T) {
		cmd := exec.Command(binPath, "watch", "--help")
		out, _ := cmd.CombinedOutput()
		helpText := string(out)

		requiredSubstrings := []string{
			"--failed",
			"--since",
			"--timeout",
			"signals/tasks.jsonl",
			"0",
			"3",
			"130",
		}
		for _, sub := range requiredSubstrings {
			if !strings.Contains(helpText, sub) {
				t.Errorf("help text missing required substring %q; output:\n%s", sub, helpText)
			}
		}
	})

	t.Run("timeout exits 3 with task_signal_timeout envelope", func(t *testing.T) {
		cmd := exec.Command(binPath, "watch", "--failed", "--timeout", "100ms", "--interval", "10ms")
		cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected nonzero exit code 3, got 0; output: %s", string(out))
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 3 {
			t.Fatalf("expected exit code 3, got %v (code %d), output: %s", err, exitErr.ExitCode(), string(out))
		}

		var env struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("unmarshal timeout envelope: %v; raw: %s", err, string(out))
		}
		if env.Kind != "task_signal_timeout" {
			t.Errorf("env.Kind = %q, want task_signal_timeout", env.Kind)
		}
	})

	t.Run("matching failed signal exits 0 with task_signal envelope", func(t *testing.T) {
		if err := os.MkdirAll(signalsDir, 0o755); err != nil {
			t.Fatalf("mkdir signals dir: %v", err)
		}
		nowStr := time.Now().UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"ts":%q,"task_id":"cli-task-1","from":"RUNNING","to":"FAILED"}`+"\n", nowStr)
		if err := os.WriteFile(signalsPath, []byte(line), 0o644); err != nil {
			t.Fatalf("write signals: %v", err)
		}

		cmd := exec.Command(binPath, "watch", "--failed", "--timeout", "2s", "--interval", "10ms")
		cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("expected exit code 0, got err %v; output: %s", err, string(out))
		}

		var env struct {
			Kind string         `json:"kind"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("unmarshal signal envelope: %v; raw: %s", err, string(out))
		}
		if env.Kind != "task_signal" {
			t.Errorf("env.Kind = %q, want task_signal", env.Kind)
		}
		if env.Data["task_id"] != "cli-task-1" {
			t.Errorf("data.task_id = %v, want cli-task-1", env.Data["task_id"])
		}
		if env.Data["to"] != "FAILED" {
			t.Errorf("data.to = %v, want FAILED", env.Data["to"])
		}
	})

	t.Run("cannot combine --failed with --task or --pr", func(t *testing.T) {
		cmd := exec.Command(binPath, "watch", "--failed", "--task", "t-123")
		cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected error combining --failed with --task, got exit 0")
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("expected exit code 2 (usage error), got %v, output: %s", err, string(out))
		}
	})
}

// TestWatchFailed_MidTailRotation verifies:
// Mid-tail rotation: write enough lines to trigger rotation WHILE a watch is tailing
// (in-process test of the watchFailed tail function), and verify the watch still
// reports a matching line written after the rotation (#510).
func TestWatchFailed_MidTailRotation(t *testing.T) {
	tempDir := t.TempDir()
	signalsDir := filepath.Join(tempDir, "signals")
	if err := os.MkdirAll(signalsDir, 0o755); err != nil {
		t.Fatalf("mkdir signals dir: %v", err)
	}
	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")

	now := time.Now().UTC()
	sinceTime := now.Add(-1 * time.Minute)

	// Startup state: pre-fill tasks.jsonl with lines whose ts <= sinceTime,
	// so startup read finds no matches and watchFailed enters the tail loop.
	const preFillCount = 20
	var preBuf bytes.Buffer
	for i := 1; i <= preFillCount; i++ {
		ts := now.Add(-5 * time.Minute).Format(time.RFC3339)
		fmt.Fprintf(&preBuf, `{"ts":%q,"task_id":"task-pre-%03d","from":"RUNNING","to":"WORKER_COMPLETED"}`+"\n", ts, i)
	}
	if err := os.WriteFile(signalsPath, preBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("write pre-fill signals file: %v", err)
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cfg := WatchFailedConfig{
		SignalsPath:  signalsPath,
		Since:        sinceTime,
		Timeout:      3 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Out:          &outBuf,
		ErrOut:       &errBuf,
	}

	// While watchFailed is tailing:
	// Goroutine simulates the writer: appends lines exceeding a rotation threshold,
	// triggers rotation (rename tasks.jsonl -> tasks.jsonl.1), and appends a matching
	// line to the fresh tasks.jsonl.
	const rotationCapBytes int64 = 2500
	appendWithRotation := func(line string) {
		fi, err := os.Stat(signalsPath)
		if err == nil && fi.Size() > rotationCapBytes {
			_ = os.Rename(signalsPath, signalsPath+".1")
		}
		f, err := os.OpenFile(signalsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Errorf("open signals file in test writer: %v", err)
			return
		}
		defer f.Close()
		_, _ = f.WriteString(line)
	}

	go func() {
		// Wait for watchFailed to finish startup and enter the tail loop.
		time.Sleep(50 * time.Millisecond)

		// 1. Write lines before rotation (all with ts before sinceTime so they don't match)
		for i := preFillCount + 1; ; i++ {
			ts := now.Add(-5 * time.Minute).Format(time.RFC3339)
			line := fmt.Sprintf(`{"ts":%q,"task_id":"task-mid-%03d","from":"RUNNING","to":"WORKER_COMPLETED"}`+"\n", ts, i)
			appendWithRotation(line)
			fi, err := os.Stat(signalsPath)
			if err == nil && fi.Size() > rotationCapBytes {
				break
			}
		}

		// Brief pause so the consumer tracks the growing offset in the pre-rotation file
		time.Sleep(30 * time.Millisecond)

		// 2. Next append triggers rotation and creates fresh tasks.jsonl!
		ts := now.Add(-5 * time.Minute).Format(time.RFC3339)
		line := fmt.Sprintf(`{"ts":%q,"task_id":"task-rot-first","from":"RUNNING","to":"WORKER_COMPLETED"}`+"\n", ts)
		appendWithRotation(line)

		// 3. Write matching line into the fresh tasks.jsonl (ts > sinceTime, to = FAILED)
		matchTS := now.Add(2 * time.Second).Format(time.RFC3339)
		matchingLine := fmt.Sprintf(`{"ts":%q,"task_id":"task-match-post-rotate","from":"RUNNING","to":"FAILED"}`+"\n", matchTS)
		appendWithRotation(matchingLine)
	}()

	start := time.Now()
	exitCode, err := watchFailed(context.Background(), cfg)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("watchFailed unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("watchFailed exitCode = %d, want 0", exitCode)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("watchFailed exited too fast (%v), expected it to wait for mid-tail rotation", elapsed)
	}

	var env struct {
		Kind string         `json:"kind"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v; raw: %s", err, outBuf.String())
	}
	if env.Kind != "task_signal" {
		t.Errorf("env.Kind = %q, want task_signal", env.Kind)
	}
	if env.Data["task_id"] != "task-match-post-rotate" {
		t.Errorf("env.Data[task_id] = %v, want task-match-post-rotate", env.Data["task_id"])
	}
	if env.Data["to"] != "FAILED" {
		t.Errorf("env.Data[to] = %v, want FAILED", env.Data["to"])
	}

	// Verify rotated file .1 exists
	if _, err := os.Stat(signalsPath + ".1"); err != nil {
		t.Errorf("expected rotated file tasks.jsonl.1 to exist: %v", err)
	}
}
