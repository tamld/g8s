//go:build windows

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

type mockWindowsRunner struct {
	calls   [][]string
	runFunc func(argv []string, timeout time.Duration) ([]byte, error)
}

func (m *mockWindowsRunner) Run(argv []string, timeout time.Duration) ([]byte, error) {
	m.calls = append(m.calls, argv)
	if m.runFunc != nil {
		return m.runFunc(argv, timeout)
	}
	return []byte("SUCCESS"), nil
}

type mockGuard struct {
	tasks []*controlplane.Task
	err   error
}

func (m *mockGuard) ListTasks(ctx context.Context, filter controlplane.TaskFilter) ([]*controlplane.Task, error) {
	return m.tasks, m.err
}

func (m *mockGuard) BeginMaintenance(owner string, ttlSeconds float64) (int, error) {
	return 1, nil
}

func (m *mockGuard) EndMaintenance(owner string) (bool, error) {
	return true, nil
}

func TestWindowsServiceManager_Install(t *testing.T) {
	runner := &mockWindowsRunner{}
	cfg := Config{
		Label:      "g8s-custom",
		BinaryPath: `C:\tools\g8s.exe`,
		Timeout:    5 * time.Second,
	}

	mgr, err := NewWindowsServiceManager(cfg, nil, runner)
	if err != nil {
		t.Fatalf("failed to create WindowsServiceManager: %v", err)
	}

	if err := mgr.Install(); err != nil {
		t.Fatalf("expected Install to succeed, got: %v", err)
	}

	if len(runner.calls) < 2 {
		t.Fatalf("expected at least 2 sc.exe calls, got %d", len(runner.calls))
	}

	// 1. Check create call
	createCall := strings.Join(runner.calls[0], " ")
	if !strings.Contains(createCall, "sc.exe create g8s-custom") {
		t.Errorf("expected create call in %q", createCall)
	}
	if !strings.Contains(createCall, `binPath= "C:\tools\g8s.exe" mcp`) {
		t.Errorf("expected binPath in %q", createCall)
	}
	if !strings.Contains(createCall, "start= auto") {
		t.Errorf("expected start= auto in %q", createCall)
	}

	// 2. Check failure recovery call
	failCall := strings.Join(runner.calls[1], " ")
	if !strings.Contains(failCall, "sc.exe failure g8s-custom") {
		t.Errorf("expected failure call in %q", failCall)
	}
	if !strings.Contains(failCall, "reset= 60 actions= restart/30000/restart/60000/restart/120000") {
		t.Errorf("expected actions in %q", failCall)
	}
}

func TestWindowsServiceManager_InstallRefuseActiveWork(t *testing.T) {
	task := &controlplane.Task{TaskID: "task-123"}
	guard := &mockGuard{tasks: []*controlplane.Task{task}}
	runner := &mockWindowsRunner{}
	cfg := Config{
		Label:      "g8s",
		BinaryPath: `C:\tools\g8s.exe`,
	}

	mgr, err := NewWindowsServiceManager(cfg, guard, runner)
	if err != nil {
		t.Fatalf("failed to create WindowsServiceManager: %v", err)
	}

	err = mgr.Install()
	if err == nil {
		t.Fatalf("expected error when active tasks exist, got nil")
	}
	if !strings.Contains(err.Error(), "service lifecycle refused") {
		t.Errorf("unexpected error message: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("expected no sc.exe calls when refused, got %d", len(runner.calls))
	}
}

func TestWindowsServiceManager_Start(t *testing.T) {
	runner := &mockWindowsRunner{}
	cfg := Config{Label: "g8s", BinaryPath: `C:\tools\g8s.exe`}
	mgr, err := NewWindowsServiceManager(cfg, nil, runner)
	if err != nil {
		t.Fatalf("NewWindowsServiceManager: %v", err)
	}

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	call := strings.Join(runner.calls[0], " ")
	if call != "sc.exe start g8s" {
		t.Errorf("unexpected start call: %q", call)
	}

	// Test failure
	runner.runFunc = func(argv []string, timeout time.Duration) ([]byte, error) {
		return nil, errors.New("service failed to start")
	}
	if err := mgr.Start(); err == nil {
		t.Fatalf("expected error when sc start fails, got nil")
	}
}

func TestWindowsServiceManager_Stop(t *testing.T) {
	runner := &mockWindowsRunner{}
	cfg := Config{Label: "g8s", BinaryPath: `C:\tools\g8s.exe`}
	mgr, err := NewWindowsServiceManager(cfg, nil, runner)
	if err != nil {
		t.Fatalf("NewWindowsServiceManager: %v", err)
	}

	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	call := strings.Join(runner.calls[0], " ")
	if call != "sc.exe stop g8s" {
		t.Errorf("unexpected stop call: %q", call)
	}
}

func TestWindowsServiceManager_Uninstall(t *testing.T) {
	runner := &mockWindowsRunner{}
	cfg := Config{Label: "g8s", BinaryPath: `C:\tools\g8s.exe`}
	mgr, err := NewWindowsServiceManager(cfg, nil, runner)
	if err != nil {
		t.Fatalf("NewWindowsServiceManager: %v", err)
	}

	if err := mgr.Uninstall(); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 calls (stop and delete), got %d", len(runner.calls))
	}
	stopCall := strings.Join(runner.calls[0], " ")
	deleteCall := strings.Join(runner.calls[1], " ")
	if stopCall != "sc.exe stop g8s" {
		t.Errorf("unexpected stop call: %q", stopCall)
	}
	if deleteCall != "sc.exe delete g8s" {
		t.Errorf("unexpected delete call: %q", deleteCall)
	}
}

func TestWindowsServiceManager_Status(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "g8s.db")
	if err := os.WriteFile(dbPath, []byte("SQLite format 3\x00extra data"), 0o600); err != nil {
		t.Fatalf("write mock db: %v", err)
	}

	runner := &mockWindowsRunner{
		runFunc: func(argv []string, timeout time.Duration) ([]byte, error) {
			return []byte(`
SERVICE_NAME: g8s
        TYPE               : 10  WIN32_OWN_PROCESS
        STATE              : 4  RUNNING
                                (STOPPABLE, NOT_PAUSABLE, ACCEPTS_SHUTDOWN)
        WIN32_EXIT_CODE    : 0  (0x0)
        SERVICE_EXIT_CODE  : 0  (0x0)
        CHECKPOINT         : 0x0
        WAIT_HINT          : 0x0
`), nil
		},
	}

	cfg := Config{
		Label:        "g8s",
		BinaryPath:   `C:\tools\g8s.exe`,
		DatabasePath: dbPath,
	}

	mgr, err := NewWindowsServiceManager(cfg, nil, runner)
	if err != nil {
		t.Fatalf("NewWindowsServiceManager: %v", err)
	}

	st, err := mgr.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if !st.Loaded {
		t.Errorf("expected Loaded=true, got false")
	}
	if !st.DatabaseExists {
		t.Errorf("expected DatabaseExists=true, got false")
	}

	// Corrupt DB test
	corruptDB := filepath.Join(tmpDir, "corrupt.db")
	if err := os.WriteFile(corruptDB, []byte("CORRUPT HEADER"), 0o600); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}
	mgr.cfg.DatabasePath = corruptDB
	_, err = mgr.Status()
	if err == nil {
		t.Fatalf("expected error on corrupt database, got nil")
	}
}
