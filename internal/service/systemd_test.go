package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSystemdUnitGeneration(t *testing.T) {
	home := t.TempDir()
	mgr, err := NewSystemdManager(Config{
		Label:        "g8s",
		Home:         home,
		BinaryPath:   "/usr/local/bin/g8s",
		DatabasePath: filepath.Join(home, "g8s.db"),
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewSystemdManager: %v", err)
	}

	unit := mgr.GenerateUnit()
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/g8s mcp") {
		t.Errorf("missing ExecStart line in unit: %s", unit)
	}
	if !strings.Contains(unit, "Environment=\"G8S_DB="+filepath.Join(home, "g8s.db")+"\"") {
		t.Errorf("missing Environment G8S_DB in unit: %s", unit)
	}
	if !strings.Contains(unit, "Restart=always") {
		t.Errorf("missing Restart=always in unit: %s", unit)
	}
}

type systemdTestRunner struct {
	cmds [][]string
}

func (r *systemdTestRunner) Run(argv []string, timeout time.Duration) ([]byte, error) {
	r.cmds = append(r.cmds, argv)
	cmdStr := strings.Join(argv, " ")
	if strings.Contains(cmdStr, "is-active") {
		return []byte("active"), nil
	}
	return []byte(""), nil
}

func TestSystemdInstallAndStartWithMockRunner(t *testing.T) {
	home := t.TempDir()
	runner := &systemdTestRunner{}

	mgr, err := NewSystemdManager(Config{
		Label:   "g8s",
		Home:    home,
		Timeout: 5 * time.Second,
	}, nil, runner)
	if err != nil {
		t.Fatalf("NewSystemdManager: %v", err)
	}

	if err := mgr.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status, err := mgr.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Loaded {
		t.Errorf("expected Loaded=true, got false")
	}

	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := mgr.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
}
