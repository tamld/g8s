package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SystemdManager implements service management for Linux user systemd units.
type SystemdManager struct {
	cfg    Config
	guard  LifecycleGuard
	runner Runner
}

// NewSystemdManager creates a systemd service manager.
func NewSystemdManager(cfg Config, guard LifecycleGuard, runner Runner) (*SystemdManager, error) {
	if cfg.Label == "" {
		cfg.Label = "g8s"
	}
	if cfg.Home == "" {
		var err error
		cfg.Home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home: %w", err)
		}
	}
	if cfg.BinaryPath == "" {
		cfg.BinaryPath = filepath.Join(cfg.Home, ".local", "bin", "g8s")
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = filepath.Join(cfg.Home, ".local", "state", "g8s", "g8s.db")
	}
	if cfg.StdoutLogPath == "" {
		cfg.StdoutLogPath = filepath.Join(cfg.Home, ".local", "state", "g8s", "service.stdout.log")
	}
	if cfg.StderrLogPath == "" {
		cfg.StderrLogPath = filepath.Join(cfg.Home, ".local", "state", "g8s", "service.stderr.log")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if runner == nil {
		runner = execRunner{}
	}

	return &SystemdManager{
		cfg:    cfg,
		guard:  guard,
		runner: runner,
	}, nil
}

func (s *SystemdManager) unitPath() string {
	return filepath.Join(s.cfg.Home, ".config", "systemd", "user", s.cfg.Label+".service")
}

// GenerateUnit returns the systemd unit file content.
func (s *SystemdManager) GenerateUnit() string {
	pathEnv := s.cfg.PathEnv
	if pathEnv == "" {
		pathEnv = "/usr/local/bin:/usr/bin:/bin:" + filepath.Join(s.cfg.Home, ".local", "bin")
	}

	return fmt.Sprintf(`[Unit]
Description=g8s (The Gatekeepers) AI Worker Daemon
After=network.target

[Service]
Type=simple
ExecStart=%s mcp
Restart=always
RestartSec=5s
Environment="G8S_DB=%s"
Environment="PATH=%s"
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, s.cfg.BinaryPath, s.cfg.DatabasePath, pathEnv, s.cfg.StdoutLogPath, s.cfg.StderrLogPath)
}

// Install writes the systemd unit and reloads systemd user daemon.
func (s *SystemdManager) Install() error {
	unitFile := s.unitPath()
	if err := os.MkdirAll(filepath.Dir(unitFile), 0755); err != nil {
		return fmt.Errorf("create systemd dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.StdoutLogPath), 0700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	content := s.GenerateUnit()
	if err := os.WriteFile(unitFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	_, err := s.runner.Run([]string{"systemctl", "--user", "daemon-reload"}, s.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	return nil
}

// Start enables and starts the systemd service unit.
func (s *SystemdManager) Start() error {
	_, err := s.runner.Run([]string{"systemctl", "--user", "enable", "--now", s.cfg.Label}, s.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("systemctl enable --now: %w", err)
	}
	return nil
}

// Stop stops the systemd service unit.
func (s *SystemdManager) Stop() error {
	_, err := s.runner.Run([]string{"systemctl", "--user", "stop", s.cfg.Label}, s.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("systemctl stop: %w", err)
	}
	return nil
}

// Uninstall stops, disables and deletes the systemd unit file.
func (s *SystemdManager) Uninstall() error {
	_, _ = s.runner.Run([]string{"systemctl", "--user", "disable", "--now", s.cfg.Label}, s.cfg.Timeout)
	_ = os.Remove(s.unitPath())
	_, _ = s.runner.Run([]string{"systemctl", "--user", "daemon-reload"}, s.cfg.Timeout)
	return nil
}

// Status inspects the systemd unit state.
func (s *SystemdManager) Status() (*ServiceStatus, error) {
	out, err := s.runner.Run([]string{"systemctl", "--user", "is-active", s.cfg.Label}, s.cfg.Timeout)
	isActive := err == nil && strings.TrimSpace(string(out)) == "active"

	dbExists := false
	if _, err := os.Stat(s.cfg.DatabasePath); err == nil {
		dbExists = true
	}

	return &ServiceStatus{
		Label:          s.cfg.Label,
		Loaded:         isActive,
		DatabaseExists: dbExists,
	}, nil
}
