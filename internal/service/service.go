// Package service manages the g8s background worker as an OS daemon. Per
// DELTA-06 Amendment A the MVP targets macOS launchd user LaunchAgents only;
// other platforms fail closed until dedicated backends land post-MVP. All
// init-system interaction flows through an injectable Runner so tests never
// touch a real launchd.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// sqliteHeader is the magic prefix every valid SQLite database starts with;
// status uses it to detect corrupt control-plane state fail-closed.
const sqliteHeader = "SQLite format 3\x00"

// DefaultTTL bounds the maintenance window held open across an install.
const DefaultTTL = 300.0

// ServiceError is a user-facing service management failure.
type ServiceError struct{ Message string }

// Error implements the error interface.
func (e *ServiceError) Error() string { return e.Message }

func serr(format string, args ...any) *ServiceError {
	return &ServiceError{Message: fmt.Sprintf(format, args...)}
}

// Runner executes one init-system command under a timeout budget. Production
// wires execRunner; tests inject recording fakes so launchd is never invoked.
type Runner interface {
	Run(argv []string, timeout time.Duration) ([]byte, error)
}

// execRunner shells out through exec.CommandContext and maps deadline
// overruns onto a fail-closed "timed out" ServiceError.
type execRunner struct{}

// Run executes argv under timeout, failing closed when the deadline lapses.
func (execRunner) Run(argv []string, timeout time.Duration) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, serr("launchctl %s timed out", strings.Join(argv[1:], " "))
	}
	if err != nil {
		return out, fmt.Errorf("%s failed: %w (output: %s)", argv[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// LifecycleGuard narrows the control-plane surface the manager needs:
// activity inspection plus the maintenance window held across installs.
// *controlplane.Store satisfies this interface directly.
type LifecycleGuard interface {
	ListTasks(ctx context.Context, filter controlplane.TaskFilter) ([]*controlplane.Task, error)
	BeginMaintenance(owner string, ttlSeconds float64) (int, error)
	EndMaintenance(owner string) (bool, error)
}

// ServiceStatus is the DELTA-06 status payload. The fixed key set guarantees
// no worker prompt material ever leaks through status output.
type ServiceStatus struct {
	Label          string `json:"label"`
	Loaded         bool   `json:"loaded"`
	DatabaseExists bool   `json:"database_exists"`
}

// Config carries every path the manager touches; zero-value fields fall back
// to macOS user-domain defaults resolved against Home.
type Config struct {
	Label         string
	BinaryPath    string
	DatabasePath  string
	PlistPath     string
	StdoutLogPath string
	StderrLogPath string
	PathEnv       string
	Home          string
	Platform      string
	Timeout       time.Duration
}

// Manager implements the DELTA-06 ServiceManager surface for launchd.
type Manager struct {
	cfg    Config
	runner Runner
	guard  LifecycleGuard
	uid    int
}

// ServiceManager is the normative DELTA-06 contract.
type ServiceManager interface {
	Install() error
	Start() error
	Stop() error
	Status() (*ServiceStatus, error)
	Uninstall() error
}

var _ ServiceManager = (*Manager)(nil)

// NewManager validates the platform decision and pins the canonical binary
// path before any unit content is generated.
func NewManager(cfg Config, guard LifecycleGuard) (*Manager, error) {
	platform := cfg.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform != "darwin" {
		return nil, serr("g8s service management is only supported on macOS (got %q); systemd and Windows backends are deferred post-MVP", platform)
	}
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		return nil, serr("binary path is required")
	}
	home := cfg.Home
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil {
			return nil, serr("resolve home directory: %v", err)
		}
		home = resolved
	}
	label := cfg.Label
	if label == "" {
		label = "com.g8s.worker"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	pathEnv := cfg.PathEnv
	if pathEnv == "" {
		pathEnv = os.Getenv("PATH")
	}
	canonical, err := filepath.EvalSymlinks(cfg.BinaryPath)
	if err != nil {
		return nil, serr("resolve binary %s: %v", cfg.BinaryPath, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, serr("stat binary %s: %v", canonical, err)
	}
	// Windows os.Stat mode bits carry no POSIX group/other write semantics
	// (Go reports 0666-style modes), so this rejection is POSIX-only.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return nil, serr("binary %s is group/world-writable; refusing to pin it into a service unit", canonical)
	}
	m := &Manager{
		cfg: Config{
			Label:         label,
			BinaryPath:    canonical,
			DatabasePath:  cfg.DatabasePath,
			PlistPath:     cfg.PlistPath,
			StdoutLogPath: cfg.StdoutLogPath,
			StderrLogPath: cfg.StderrLogPath,
			PathEnv:       pathEnv,
			Home:          home,
			Platform:      platform,
			Timeout:       timeout,
		},
		runner: execRunner{},
		guard:  guard,
		uid:    os.Getuid(),
	}
	if m.cfg.PlistPath == "" {
		m.cfg.PlistPath = filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	}
	if m.cfg.StdoutLogPath == "" {
		m.cfg.StdoutLogPath = filepath.Join(home, "Library", "Logs", label+".out.log")
	}
	if m.cfg.StderrLogPath == "" {
		m.cfg.StderrLogPath = filepath.Join(home, "Library", "Logs", label+".err.log")
	}
	return m, nil
}

// WithRunner overrides the init-system runner seam (tests only).
func (m *Manager) WithRunner(r Runner) *Manager {
	if r != nil {
		m.runner = r
	}
	return m
}

// xmlEscape renders arbitrary strings as safe XML character data.
func xmlEscape(v string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(v)
}

// sanitizedPathEnv drops the user-local bin directory so the daemon cannot
// pick up binaries an attacker may have planted there.
func (m *Manager) sanitizedPathEnv() string {
	localBin := filepath.Join(m.cfg.Home, ".local", "bin")
	kept := make([]string, 0, 8)
	for _, dir := range strings.Split(m.cfg.PathEnv, string(os.PathListSeparator)) {
		cleaned := strings.TrimSpace(dir)
		if cleaned == localBin || cleaned == "" {
			continue
		}
		kept = append(kept, cleaned)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// buildPlist renders the hardened LaunchAgent definition. It fails closed if
// any rendered value would embed secret-shaped material.
func (m *Manager) buildPlist() ([]byte, error) {
	sanitizedPath := m.sanitizedPathEnv()
	sections := []struct{ key, value string }{
		{"AGY_BIN", m.cfg.BinaryPath},
		{"PATH", sanitizedPath},
	}
	for _, s := range sections {
		upper := strings.ToUpper(s.value)
		if strings.Contains(upper, "API") || strings.Contains(upper, "TOKEN") {
			return nil, serr("environment value for %s would leak secret-shaped material into the service unit", s.key)
		}
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "  <key>Label</key>\n  <string>%s</string>\n", xmlEscape(m.cfg.Label))
	b.WriteString("  <key>KeepAlive</key>\n  <true/>\n")
	b.WriteString("  <key>ProcessType</key>\n  <string>Background</string>\n")
	b.WriteString("  <key>Umask</key>\n  <integer>63</integer>\n")
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEscape(m.cfg.BinaryPath))
	b.WriteString("    <string>--quiet</string>\n")
	b.WriteString("  </array>\n")
	fmt.Fprintf(&b, "  <key>StandardOutPath</key>\n  <string>%s</string>\n", xmlEscape(m.cfg.StdoutLogPath))
	fmt.Fprintf(&b, "  <key>StandardErrorPath</key>\n  <string>%s</string>\n", xmlEscape(m.cfg.StderrLogPath))
	b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	fmt.Fprintf(&b, "    <key>AGY_BIN</key>\n    <string>%s</string>\n", xmlEscape(m.cfg.BinaryPath))
	fmt.Fprintf(&b, "    <key>PATH</key>\n    <string>%s</string>\n", xmlEscape(sanitizedPath))
	b.WriteString("  </dict>\n")
	b.WriteString("</dict>\n</plist>\n")
	return []byte(b.String()), nil
}

func (m *Manager) domain() string { return fmt.Sprintf("gui/%d", m.uid) }

func (m *Manager) serviceTarget() string { return fmt.Sprintf("%s/%s", m.domain(), m.cfg.Label) }

func (m *Manager) run(argv []string) ([]byte, error) {
	return m.runner.Run(argv, m.cfg.Timeout)
}

// isLoaded probes launchd for the label; probe failures mean "not loaded".
func (m *Manager) isLoaded() bool {
	_, err := m.run([]string{"launchctl", "print", m.serviceTarget()})
	return err == nil
}

// refuseActiveWork blocks lifecycle mutations while tasks hold leases.
func (m *Manager) refuseActiveWork() error {
	if m.guard == nil {
		return nil
	}
	for _, state := range []string{controlplane.StateLeased, controlplane.StateRunning} {
		filter := controlplane.TaskFilter{State: &state, Limit: 1}
		tasks, err := m.guard.ListTasks(context.Background(), filter)
		if err != nil {
			return serr("inspect active work before service mutation: %v", err)
		}
		if len(tasks) > 0 {
			return serr("service lifecycle refused: tasks are leased or running")
		}
	}
	return nil
}

// preparePrivateLog creates (or appends to) a 0600 log, refusing symlinks so
// launchd output cannot be redirected onto a victim file.
func (m *Manager) preparePrivateLog(path, what string) error {
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return serr("cannot prepare private service log: %s is a symlink", what)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return serr("prepare %s log: %v", what, err)
	}
	return f.Close()
}

// Install writes the hardened unit and boots it into the user launchd domain
// under a maintenance window, rolling back the plist when bootstrap fails.
func (m *Manager) Install() error {
	if err := m.refuseActiveWork(); err != nil {
		return err
	}
	if info, err := os.Lstat(m.cfg.PlistPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return serr("symlinked LaunchAgent plist is not allowed: %s", m.cfg.PlistPath)
	}
	if err := m.preparePrivateLog(m.cfg.StdoutLogPath, "stdout"); err != nil {
		return err
	}
	if err := m.preparePrivateLog(m.cfg.StderrLogPath, "stderr"); err != nil {
		return err
	}
	payload, err := m.buildPlist()
	if err != nil {
		return err
	}
	previous, _ := os.ReadFile(m.cfg.PlistPath)
	restore := func() {
		if previous == nil {
			_ = os.Remove(m.cfg.PlistPath)
			return
		}
		_ = os.WriteFile(m.cfg.PlistPath, previous, 0o644)
	}
	if m.guard != nil {
		if _, err := m.guard.BeginMaintenance("g8s-service", DefaultTTL); err != nil {
			return serr("open maintenance window: %v", err)
		}
		defer func() { _, _ = m.guard.EndMaintenance("g8s-service") }()
	}
	wasLoaded := m.isLoaded()
	if wasLoaded {
		if _, err := m.run([]string{"launchctl", "bootout", m.serviceTarget()}); err != nil {
			return serr("bootout existing service before reinstall: %v", err)
		}
	}
	if err := os.WriteFile(m.cfg.PlistPath, payload, 0o644); err != nil {
		return serr("write LaunchAgent plist: %v", err)
	}
	if _, err := m.run([]string{"launchctl", "bootstrap", m.domain(), m.cfg.PlistPath}); err != nil {
		restore()
		return serr("bootstrap failed: %v", err)
	}
	if _, err := m.run([]string{"launchctl", "kickstart", "-p", m.serviceTarget()}); err != nil {
		return serr("kickstart failed: %v", err)
	}
	return nil
}

// Start kicks the installed service without touching the unit definition.
func (m *Manager) Start() error {
	if !m.isLoaded() {
		return serr("service is not installed and loaded")
	}
	if _, err := m.run([]string{"launchctl", "kickstart", "-p", m.serviceTarget()}); err != nil {
		return serr("kickstart failed: %v", err)
	}
	return nil
}

// Stop boots the service out of launchd while keeping the unit on disk.
func (m *Manager) Stop() error {
	if !m.isLoaded() {
		return serr("service is not installed and loaded")
	}
	if _, err := m.run([]string{"launchctl", "bootout", m.serviceTarget()}); err != nil {
		return serr("bootout failed: %v", err)
	}
	return nil
}

// Restart reloads the unit; it refuses when launchd has nothing loaded.
func (m *Manager) Restart() error {
	if !m.isLoaded() {
		return serr("service is not installed and loaded")
	}
	if _, err := m.run([]string{"launchctl", "bootout", m.serviceTarget()}); err != nil {
		return serr("bootout failed: %v", err)
	}
	if _, err := m.run([]string{"launchctl", "bootstrap", m.domain(), m.cfg.PlistPath}); err != nil {
		return serr("bootstrap failed: %v", err)
	}
	if _, err := m.run([]string{"launchctl", "kickstart", "-p", m.serviceTarget()}); err != nil {
		return serr("kickstart failed: %v", err)
	}
	return nil
}

// Status reports load state and database presence without mutating anything;
// it never creates the database and rejects corrupt databases fail-closed.
func (m *Manager) Status() (*ServiceStatus, error) {
	st := &ServiceStatus{Label: m.cfg.Label}
	st.Loaded = m.isLoaded()
	if m.cfg.DatabasePath != "" {
		info, err := os.Stat(m.cfg.DatabasePath)
		st.DatabaseExists = err == nil && !info.IsDir()
		if st.DatabaseExists {
			f, err := os.Open(m.cfg.DatabasePath)
			if err != nil {
				return nil, serr("cannot inspect control-plane state: %v", err)
			}
			header := make([]byte, len(sqliteHeader))
			read, _ := f.Read(header)
			_ = f.Close()
			if read != len(sqliteHeader) || string(header[:read]) != sqliteHeader {
				return nil, serr("cannot inspect control-plane state: database header invalid")
			}
		}
	}
	return st, nil
}

// Uninstall boots the service out and removes the unit, deliberately leaving
// the SQLite database and every receipt behind.
func (m *Manager) Uninstall() error {
	_, _ = m.run([]string{"launchctl", "bootout", m.serviceTarget()})
	if err := os.Remove(m.cfg.PlistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return serr("remove LaunchAgent plist: %v", err)
	}
	return nil
}
