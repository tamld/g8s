//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
)

// WindowsServiceManager manages g8s as a Windows background service using sc.exe.
type WindowsServiceManager struct {
	Name        string
	DisplayName string
	Binary      string
	Args        []string
	cfg         Config
	guard       LifecycleGuard
	runner      Runner
}

var _ ServiceManager = (*WindowsServiceManager)(nil)

// NewWindowsServiceManager constructs a WindowsServiceManager.
func NewWindowsServiceManager(cfg Config, guard LifecycleGuard, runner Runner) (*WindowsServiceManager, error) {
	name := cfg.Label
	if name == "" {
		name = "g8s"
	}
	displayName := "g8s Orchestrator Service"

	binary := cfg.BinaryPath
	if binary == "" {
		binary = filepath.Join(pathutil.DefaultDataDir(), "bin", "g8s.exe")
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	if runner == nil {
		runner = execRunner{}
	}

	return &WindowsServiceManager{
		Name:        name,
		DisplayName: displayName,
		Binary:      binary,
		Args:        []string{"mcp"},
		cfg:         cfg,
		guard:       guard,
		runner:      runner,
	}, nil
}

func (w *WindowsServiceManager) refuseActiveWork() error {
	if w.guard == nil {
		return nil
	}
	for _, state := range []string{controlplane.StateLeased, controlplane.StateRunning} {
		filter := controlplane.TaskFilter{State: &state, Limit: 1}
		tasks, err := w.guard.ListTasks(context.Background(), filter)
		if err != nil {
			return fmt.Errorf("inspect active work: %w", err)
		}
		if len(tasks) > 0 {
			return errors.New("service lifecycle refused: tasks are leased or running")
		}
	}
	return nil
}

// Install registers the service via sc.exe with auto-start and failure recovery.
func (w *WindowsServiceManager) Install() error {
	return w.InstallContext(context.Background())
}

// InstallContext registers the service with context cancellation.
func (w *WindowsServiceManager) InstallContext(ctx context.Context) error {
	if err := w.refuseActiveWork(); err != nil {
		return err
	}

	binPathArg := fmt.Sprintf("\"%s\" %s", w.Binary, strings.Join(w.Args, " "))
	binPathArg = strings.TrimSpace(binPathArg)

	// sc create <name> binPath= "<path>" start= auto DisplayName= "<display>"
	createArgs := []string{
		"sc.exe", "create", w.Name,
		"binPath=", binPathArg,
		"start=", "auto",
		"DisplayName=", w.DisplayName,
	}
	if _, err := w.runner.Run(createArgs, w.cfg.Timeout); err != nil {
		return fmt.Errorf("sc create %s: %w", w.Name, err)
	}

	// sc failure <name> reset= 60 actions= restart/30000/restart/60000/restart/120000
	failureArgs := []string{
		"sc.exe", "failure", w.Name,
		"reset=", "60",
		"actions=", "restart/30000/restart/60000/restart/120000",
	}
	if _, err := w.runner.Run(failureArgs, w.cfg.Timeout); err != nil {
		return fmt.Errorf("sc failure %s: %w", w.Name, err)
	}

	// sc description <name> "Zero-CGO multi-agent orchestrator for AI CLI workers"
	descArgs := []string{
		"sc.exe", "description", w.Name,
		"Zero-CGO multi-agent orchestrator for AI CLI workers",
	}
	if _, err := w.runner.Run(descArgs, w.cfg.Timeout); err != nil {
		// Non-fatal description update
		return nil
	}

	return nil
}

// Start initiates the service via sc.exe start.
func (w *WindowsServiceManager) Start() error {
	return w.StartContext(context.Background())
}

// StartContext initiates the service with context cancellation.
func (w *WindowsServiceManager) StartContext(ctx context.Context) error {
	_, err := w.runner.Run([]string{"sc.exe", "start", w.Name}, w.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("sc start %s: %w", w.Name, err)
	}
	return nil
}

// Stop terminates the running service via sc.exe stop.
func (w *WindowsServiceManager) Stop() error {
	return w.StopContext(context.Background())
}

// StopContext terminates the service with context cancellation.
func (w *WindowsServiceManager) StopContext(ctx context.Context) error {
	_, err := w.runner.Run([]string{"sc.exe", "stop", w.Name}, w.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("sc stop %s: %w", w.Name, err)
	}
	return nil
}

// Uninstall stops and deletes the service entry via sc.exe delete.
func (w *WindowsServiceManager) Uninstall() error {
	return w.UninstallContext(context.Background())
}

// UninstallContext uninstalls the service with context cancellation.
func (w *WindowsServiceManager) UninstallContext(ctx context.Context) error {
	_, _ = w.runner.Run([]string{"sc.exe", "stop", w.Name}, w.cfg.Timeout)
	_, err := w.runner.Run([]string{"sc.exe", "delete", w.Name}, w.cfg.Timeout)
	if err != nil {
		return fmt.Errorf("sc delete %s: %w", w.Name, err)
	}
	return nil
}

// Status inspects the service state via sc.exe query.
func (w *WindowsServiceManager) Status() (*ServiceStatus, error) {
	return w.StatusContext(context.Background())
}

// StatusContext inspects service state with context cancellation.
func (w *WindowsServiceManager) StatusContext(ctx context.Context) (*ServiceStatus, error) {
	out, err := w.runner.Run([]string{"sc.exe", "query", w.Name}, w.cfg.Timeout)
	isLoaded := false
	if err == nil {
		outStr := strings.ToUpper(string(out))
		if strings.Contains(outStr, "STATE") && strings.Contains(outStr, "RUNNING") {
			isLoaded = true
		}
	}

	dbExists := false
	if w.cfg.DatabasePath != "" {
		info, err := os.Stat(w.cfg.DatabasePath)
		dbExists = err == nil && !info.IsDir()
		if dbExists {
			f, err := os.Open(w.cfg.DatabasePath)
			if err != nil {
				return nil, fmt.Errorf("cannot inspect control-plane state: %w", err)
			}
			defer f.Close()
			header := make([]byte, len(sqliteHeader))
			read, err := f.Read(header)
			if err != nil || read != len(sqliteHeader) || string(header[:read]) != sqliteHeader {
				return nil, errors.New("cannot inspect control-plane state: database header invalid")
			}
		}
	}

	return &ServiceStatus{
		Label:          w.Name,
		Loaded:         isLoaded,
		DatabaseExists: dbExists,
	}, nil
}
