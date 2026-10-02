package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cli"
)

func TestAutopilotStatus_FreshProcess(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	// Plain text mode
	cmd := exec.Command(binPath, "autopilot", "status")
	cmd.Env = append(cmd.Environ(), "G8S_STATE_DIR="+tempDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("autopilot status failed with %v; output:\n%s", err, string(out))
	}

	raw := string(out)
	truthMsg := "autopilot runs only inside a `start` process; no cross-process scheduler exists in this build (staged path #485 — stage 3 will add OS-native scheduling)"
	if !strings.Contains(raw, truthMsg) {
		t.Errorf("expected status output to contain truthful message %q, got:\n%s", truthMsg, raw)
	}
	if !strings.Contains(strings.ToLower(raw), "none") && !strings.Contains(strings.ToLower(raw), "no start-process") {
		t.Errorf("expected status output to report no lockfile, got:\n%s", raw)
	}

	// JSON mode
	cmdJSON := exec.Command(binPath, "autopilot", "status", "--json")
	cmdJSON.Env = append(cmdJSON.Environ(), "G8S_STATE_DIR="+tempDir)
	outJSON, err := cmdJSON.CombinedOutput()
	if err != nil {
		t.Fatalf("autopilot status --json failed with %v; output:\n%s", err, string(outJSON))
	}

	var env testEnvelope
	if err := json.Unmarshal(outJSON, &env); err != nil {
		t.Fatalf("failed to unmarshal JSON envelope: %v\nOutput: %s", err, string(outJSON))
	}
	if env.Command != "autopilot" || env.Subcommand != "status" {
		t.Errorf("expected cmd=autopilot sub=status, got cmd=%s sub=%s", env.Command, env.Subcommand)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if data["running"] != false {
		t.Errorf("expected running=false, got %v", data["running"])
	}
	if data["lockfile_exists"] != false {
		t.Errorf("expected lockfile_exists=false, got %v", data["lockfile_exists"])
	}
	msgVal, ok := data["message"].(string)
	if !ok || !strings.Contains(msgVal, "staged path #485") {
		t.Errorf("expected message to mention staged path #485, got %v", data["message"])
	}
}

func TestAutopilotStop_FreshProcess(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	cmd := exec.Command(binPath, "autopilot", "stop")
	cmd.Env = append(cmd.Environ(), "G8S_STATE_DIR="+tempDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected autopilot stop on fresh process to exit non-zero, but succeeded with output:\n%s", string(out))
	}

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}

	raw := string(out)
	truthMsg := "no cross-process scheduler exists in this build (staged path #485 — stage 3 will add OS-native scheduling)"
	if !strings.Contains(raw, truthMsg) {
		t.Errorf("expected stop error to contain %q, got:\n%s", truthMsg, raw)
	}

	// Verify JSON error envelope matches sibling conventions
	cmdJSON := exec.Command(binPath, "autopilot", "stop", "--json")
	cmdJSON.Env = append(cmdJSON.Environ(), "G8S_STATE_DIR="+tempDir)
	outJSON, err := cmdJSON.CombinedOutput()
	if err == nil {
		t.Fatalf("expected autopilot stop --json to exit non-zero, but succeeded")
	}
	var env testEnvelope
	if err := json.Unmarshal(outJSON, &env); err != nil {
		t.Fatalf("failed to unmarshal error envelope: %v\nOutput: %s", err, string(outJSON))
	}
	if env.Kind != "error" || env.Command != "autopilot" || env.Subcommand != "stop" {
		t.Errorf("unexpected error envelope header: %+v", env)
	}
	if env.Error == nil || env.Error.Code != cli.CodeRuntime {
		t.Errorf("expected E_RUNTIME code, got: %+v", env.Error)
	}
	if !strings.Contains(env.Error.Message, truthMsg) {
		t.Errorf("expected Error.Message to contain truthful message, got %q", env.Error.Message)
	}
}

func TestAutopilotStop_LiveProcessLockfile(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "autopilot.lock")

	// Fabricate lockfile with current test PID (guaranteed alive)
	testPID := os.Getpid()
	lockContent, _ := json.Marshal(map[string]any{
		"pid":        testPID,
		"start_time": time.Now().UTC().Format(time.RFC3339),
	})
	if err := os.WriteFile(lockPath, lockContent, 0o644); err != nil {
		t.Fatalf("failed to write test lockfile: %v", err)
	}

	cmd := exec.Command(binPath, "autopilot", "stop")
	cmd.Env = append(cmd.Environ(), "G8S_STATE_DIR="+tempDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected stop to exit non-zero, got success")
	}

	raw := string(out)
	if !strings.Contains(raw, "kill") {
		t.Errorf("expected stop output to mention 'kill' to signal process, got:\n%s", raw)
	}
	if !strings.Contains(raw, strconv.Itoa(testPID)) {
		t.Errorf("expected stop output to offer PID %d, got:\n%s", testPID, raw)
	}
	// Verify current process was NOT killed
	proc, findErr := os.FindProcess(testPID)
	if findErr != nil || proc == nil {
		t.Fatalf("current process unexpectedly vanished!")
	}
}

func TestAutopilotTrigger_Honesty(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	cmd := exec.Command(binPath, "autopilot", "trigger")
	cmd.Env = append(cmd.Environ(), "G8S_STATE_DIR="+tempDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected trigger to exit non-zero, got success with output:\n%s", string(out))
	}

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}

	raw := string(out)
	expectedPhrase := "triggers are not persisted yet (staged path #485 — stage 3 will add persisted triggers and native scheduling)"
	if !strings.Contains(raw, expectedPhrase) {
		t.Errorf("expected trigger output to contain %q, got:\n%s", expectedPhrase, raw)
	}
}

func TestAutopilotStart_RefusesLiveLockfile(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "autopilot.lock")

	testPID := os.Getpid()
	lockContent, _ := json.Marshal(map[string]any{
		"pid":        testPID,
		"start_time": time.Now().UTC().Format(time.RFC3339),
	})
	if err := os.WriteFile(lockPath, lockContent, 0o644); err != nil {
		t.Fatalf("failed to write lockfile: %v", err)
	}

	cmd := exec.Command(binPath, "autopilot", "start")
	cmd.Env = append(cmd.Environ(), "G8S_STATE_DIR="+tempDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected start to refuse with live lockfile, but succeeded with output:\n%s", string(out))
	}

	raw := string(out)
	if !strings.Contains(raw, "already running") {
		t.Errorf("expected start to report already running, got:\n%s", raw)
	}
	if !strings.Contains(raw, strconv.Itoa(testPID)) {
		t.Errorf("expected start output to name PID %d, got:\n%s", testPID, raw)
	}
}

func TestAutopilotStart_DeadLockfileProceedsAndSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal tests skipped on windows")
	}

	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "autopilot.lock")

	// Delete-before-assert pattern: spawn a quick child command, wait for it to finish, then write its dead PID
	child := exec.Command(binPath, "version")
	if err := child.Run(); err != nil {
		t.Fatalf("failed to run short child command: %v", err)
	}
	deadPID := child.Process.Pid

	lockContent, _ := json.Marshal(map[string]any{
		"pid":        deadPID,
		"start_time": time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
	})
	if err := os.WriteFile(lockPath, lockContent, 0o644); err != nil {
		t.Fatalf("failed to write dead lockfile: %v", err)
	}

	// Start autopilot process. It should clean stale lock and proceed.
	startCmd := exec.Command(binPath, "autopilot", "start")
	startCmd.Env = append(startCmd.Environ(), "G8S_STATE_DIR="+tempDir)
	if err := startCmd.Start(); err != nil {
		t.Fatalf("failed to start autopilot process: %v", err)
	}
	defer func() {
		if startCmd.Process != nil {
			_ = startCmd.Process.Signal(syscall.SIGKILL)
		}
	}()

	// Wait for lockfile to be written with the new process PID
	var runningPID int
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		bytes, err := os.ReadFile(lockPath)
		if err == nil {
			var d struct {
				PID int `json:"pid"`
			}
			if err := json.Unmarshal(bytes, &d); err == nil && d.PID == startCmd.Process.Pid {
				runningPID = d.PID
				found = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Fatalf("expected lockfile to be updated with new PID %d, but timed out", startCmd.Process.Pid)
	}
	if runningPID != startCmd.Process.Pid {
		t.Errorf("lockfile PID = %d, want %d", runningPID, startCmd.Process.Pid)
	}

	// Now send SIGTERM to verify clean shutdown removes the lockfile
	if err := startCmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- startCmd.Wait()
	}()

	select {
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for process to exit after SIGTERM")
	case <-done:
	}

	// Verify lockfile is removed after clean shutdown
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("expected lockfile %s to be removed on clean shutdown, but it still exists", lockPath)
	}
}

func TestAutopilot_ProbeFuncUnit(t *testing.T) {
	// Verify probe func injection pattern
	origProbe := isProcessAlive
	defer func() { isProcessAlive = origProbe }()

	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "autopilot.lock")

	_ = writeAutopilotLock(lockPath, 99999, time.Now().UTC())

	isProcessAlive = func(pid int) bool {
		return pid == 99999
	}

	lockData, err := readAutopilotLock(lockPath)
	if err != nil || lockData == nil {
		t.Fatalf("failed to read lock: %v", err)
	}
	if !isProcessAlive(lockData.PID) {
		t.Errorf("expected mock probe to report alive")
	}

	isProcessAlive = func(pid int) bool {
		return false
	}
	if isProcessAlive(lockData.PID) {
		t.Errorf("expected mock probe to report dead")
	}

	removeAutopilotLock(lockPath)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("expected lockfile to be removed")
	}
}
