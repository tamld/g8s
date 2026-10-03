package service

import (
	"errors"
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

type errorSystemdRunner struct {
	failOn string
}

func (r *errorSystemdRunner) Run(argv []string, timeout time.Duration) ([]byte, error) {
	cmdStr := strings.Join(argv, " ")
	if strings.Contains(cmdStr, r.failOn) {
		return nil, errors.New("simulated error on " + r.failOn)
	}
	return []byte(""), nil
}

func TestSystemd_ErrorBranches(t *testing.T) {
	home := t.TempDir()

	// 1. Install daemon-reload fails
	mgr, _ := NewSystemdManager(Config{Label: "g8s", Home: home}, nil, &errorSystemdRunner{failOn: "daemon-reload"})
	if err := mgr.Install(); err == nil || !strings.Contains(err.Error(), "daemon-reload") {
		t.Fatalf("expected daemon-reload error, got %v", err)
	}

	// 2. Start fails
	mgrStart, _ := NewSystemdManager(Config{Label: "g8s", Home: home}, nil, &errorSystemdRunner{failOn: "enable"})
	if err := mgrStart.Start(); err == nil || !strings.Contains(err.Error(), "enable --now") {
		t.Fatalf("expected enable error, got %v", err)
	}

	// 3. Stop fails
	mgrStop, _ := NewSystemdManager(Config{Label: "g8s", Home: home}, nil, &errorSystemdRunner{failOn: "stop"})
	if err := mgrStop.Stop(); err == nil || !strings.Contains(err.Error(), "systemctl stop") {
		t.Fatalf("expected stop error, got %v", err)
	}

	// 4. Default configuration options
	mgrDef, err := NewSystemdManager(Config{}, nil, nil)
	if err != nil {
		t.Fatalf("NewSystemdManager default failed: %v", err)
	}
	if mgrDef.cfg.Label != "g8s" {
		t.Errorf("expected default label g8s, got %s", mgrDef.cfg.Label)
	}
}
