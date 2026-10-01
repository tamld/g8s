// Package doctor implements diagnostic sanity checks for g8s environment,
// security boundaries, permissions, and worker binary availability.
package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cleanup"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/heartbeat"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/registry"
	_ "modernc.org/sqlite"
)

// AttentionCheckQuestion defines a self-reflection question and its detected or unknown answer.
type AttentionCheckQuestion struct {
	Number   int    `json:"number"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// AttentionCheckReport wraps the 5 self-reflection attention questions per DEBT-47.
type AttentionCheckReport struct {
	Questions []AttentionCheckQuestion `json:"questions"`
}

// DiagnosticResult summarizes the outcome of one environmental check.
type DiagnosticResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // OK, WARN, FAIL
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// DoctorReport contains all executed health and sanity checks.
type DoctorReport struct {
	OverallStatus string             `json:"overall_status"` // HEALTHY, DEGRADED, UNHEALTHY
	Platform      string             `json:"platform"`
	GoRuntime     string             `json:"go_runtime"`
	ZeroCGO       bool               `json:"zero_cgo"`
	Scope         string             `json:"scope,omitempty"`
	ConfigDir     string             `json:"config_dir,omitempty"`
	DataDir       string             `json:"data_dir,omitempty"`
	CacheDir      string             `json:"cache_dir,omitempty"`
	DatabasePath  string             `json:"database_path,omitempty"`
	Checks        []DiagnosticResult `json:"checks"`
	AppliedFixes  []string           `json:"applied_fixes,omitempty"`
}

// Doctor provides diagnostic health and environment checks.
type Doctor struct {
	Scope       string
	DetectPaths bool
}

// New creates a new Doctor instance.
func New() *Doctor {
	return &Doctor{
		Scope: pathutil.ScopeUser,
	}
}

// detectInstallSource inspects the host registry to detect whether g8s was installed via MSI/NSIS or ZIP.
func (d *Doctor) detectInstallSource() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	// Check Uninstall registry key
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s`,
		registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		return "msi-or-nsis"
	}
	return "zip-or-manual"
}

// detectInstallPath determines the installation directory of g8s on Windows.
func (d *Doctor) detectInstallPath() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s`,
		registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		if loc, err := key.GetStringValue("InstallLocation"); err == nil && loc != "" {
			return loc
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return `C:\Program Files\g8s`
}

func diagnoseWindowsEnvironment(scope, userProfile, source, installPath string, onPath, detectPaths bool) []DiagnosticResult {
	var results []DiagnosticResult

	if scope == "" {
		scope = pathutil.ScopeUser
	}

	results = append(results, DiagnosticResult{
		Name:    "Windows User Profile",
		Status:  "OK",
		Message: fmt.Sprintf("User profile: %s", userProfile),
		Details: userProfile,
	})

	results = append(results, DiagnosticResult{
		Name:    "Windows Execution Scope",
		Status:  "OK",
		Message: fmt.Sprintf("Scope: %s", scope),
		Details: scope,
	})

	sourceMsg := "Install source: ZIP/Manual"
	if source == "msi-or-nsis" {
		sourceMsg = `Install source: MSI/NSIS (registry HKLM\...\Uninstall\g8s)`
	}

	results = append(results, DiagnosticResult{
		Name:    "Windows Install Source",
		Status:  "OK",
		Message: sourceMsg,
		Details: source,
	})

	results = append(results, DiagnosticResult{
		Name:    "Windows Install Path",
		Status:  "OK",
		Message: fmt.Sprintf("Install path: %s", installPath),
		Details: installPath,
	})

	configDir := pathutil.DefaultConfigDir()
	dataDir := pathutil.DataDirForScope(scope)
	cacheDir := pathutil.DefaultCacheDir()

	results = append(results, DiagnosticResult{
		Name:    "Config Directory",
		Status:  "OK",
		Message: fmt.Sprintf("Config: %s", configDir),
		Details: configDir,
	})
	results = append(results, DiagnosticResult{
		Name:    "Data Directory",
		Status:  "OK",
		Message: fmt.Sprintf("Data:   %s", dataDir),
		Details: dataDir,
	})
	results = append(results, DiagnosticResult{
		Name:    "Cache Directory",
		Status:  "OK",
		Message: fmt.Sprintf("Cache:  %s", cacheDir),
		Details: cacheDir,
	})

	if onPath {
		results = append(results, DiagnosticResult{
			Name:    "Windows PATH State",
			Status:  "OK",
			Message: "✓ INSTDIR is on system PATH",
			Details: installPath,
		})
	} else {
		results = append(results, DiagnosticResult{
			Name:    "Windows PATH State",
			Status:  "WARN",
			Message: fmt.Sprintf("INSTDIR %s is not in system PATH", installPath),
			Details: installPath,
		})
	}

	if detectPaths {
		profiles := pathutil.DetectUserProfiles()
		var foundProfiles []string
		for _, p := range profiles {
			if p.Exists {
				foundProfiles = append(foundProfiles, fmt.Sprintf("%s (%s)", p.Username, p.DataDir))
			}
		}
		if len(foundProfiles) > 0 {
			results = append(results, DiagnosticResult{
				Name:    "Multi-Profile Path Detection",
				Status:  "OK",
				Message: fmt.Sprintf("Found %d g8s profile(s) on system", len(foundProfiles)),
				Details: strings.Join(foundProfiles, ", "),
			})
		} else {
			results = append(results, DiagnosticResult{
				Name:    "Multi-Profile Path Detection",
				Status:  "OK",
				Message: "No secondary g8s user profiles found",
			})
		}
	}

	return results
}

// checkWindowsEnvironment executes Windows-specific diagnostics for install source and PATH.
func (d *Doctor) checkWindowsEnvironment() []DiagnosticResult {
	if runtime.GOOS != "windows" {
		return nil
	}

	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile, _ = os.UserHomeDir()
	}

	scope := d.Scope
	if scope == "" {
		scope = pathutil.ScopeUser
	}

	source := d.detectInstallSource()
	installPath := d.detectInstallPath()

	pathEnv := os.Getenv("PATH")
	onPath := false
	for _, p := range filepath.SplitList(pathEnv) {
		if strings.EqualFold(strings.TrimRight(p, `/\`), strings.TrimRight(installPath, `/\`)) {
			onPath = true
			break
		}
	}

	return diagnoseWindowsEnvironment(scope, userProfile, source, installPath, onPath, d.DetectPaths)
}

// RunDiagnostics executes the full diagnostic suite across the environment.
func RunDiagnostics(ctx context.Context, dbPath string) *DoctorReport {
	return New().RunDiagnosticsWithFix(ctx, dbPath, false)
}

// RunDiagnosticsWithFix executes the diagnostic suite and optionally applies automatic self-healing remediations.
func RunDiagnosticsWithFix(ctx context.Context, dbPath string, autoFix bool) *DoctorReport {
	return New().RunDiagnosticsWithFix(ctx, dbPath, autoFix)
}

// RunDiagnosticsWithFix executes the diagnostic suite on Doctor instance.
func (d *Doctor) RunDiagnosticsWithFix(ctx context.Context, dbPath string, autoFix bool) *DoctorReport {
	var appliedFixes []string

	if dbPath == "" {
		dbPath = pathutil.DefaultDatabasePath()
	}

	if autoFix {
		// 1. Ensure state and evidence directories exist with mode 0700
		stateDir := filepath.Dir(dbPath)
		evidenceDir := filepath.Join(stateDir, "evidence")
		for _, dir := range []string{stateDir, evidenceDir} {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				if err := os.MkdirAll(dir, 0o700); err == nil {
					appliedFixes = append(appliedFixes, fmt.Sprintf("Created directory %s (mode 0700)", dir))
				}
			} else if runtime.GOOS != "windows" {
				if info, err := os.Stat(dir); err == nil && info.Mode().Perm() != 0o700 {
					if err := os.Chmod(dir, 0o700); err == nil {
						appliedFixes = append(appliedFixes, fmt.Sprintf("Fixed permissions on %s (0700)", dir))
					}
				}
			}
		}

		// 2. Ensure database files have mode 0600 on POSIX
		if runtime.GOOS != "windows" {
			for _, file := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
				if info, err := os.Stat(file); err == nil && info.Mode().Perm() != 0o600 {
					if err := os.Chmod(file, 0o600); err == nil {
						appliedFixes = append(appliedFixes, fmt.Sprintf("Fixed permissions on %s (0600)", file))
					}
				}
			}
		}
	}

	scope := d.Scope
	if scope == "" {
		scope = pathutil.ScopeUser
	}

	report := &DoctorReport{
		OverallStatus: "HEALTHY",
		Platform:      fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoRuntime:     runtime.Version(),
		ZeroCGO:       true,
		Scope:         scope,
		ConfigDir:     pathutil.DefaultConfigDir(),
		DataDir:       pathutil.DataDirForScope(scope),
		CacheDir:      pathutil.DefaultCacheDir(),
		DatabasePath:  dbPath,
		AppliedFixes:  appliedFixes,
	}

	report.Checks = append(report.Checks, checkDatabase(dbPath))
	stateDir := filepath.Dir(dbPath)
	report.Checks = append(report.Checks, checkDiskSpace(stateDir))
	report.Checks = append(report.Checks, checkWorkspace())
	if runtime.GOOS == "windows" {
		report.Checks = append(report.Checks, d.checkWindowsEnvironment()...)
	}
	report.Checks = append(report.Checks, checkWorkerBinaries()...)
	report.Checks = append(report.Checks, checkProviders())
	report.Checks = append(report.Checks, checkProvidersFile())
	report.Checks = append(report.Checks, checkHarnessProfiles())
	report.Checks = append(report.Checks, checkStaleBinary())

	for _, check := range report.Checks {
		if check.Status == "FAIL" {
			report.OverallStatus = "UNHEALTHY"
			break
		}
		if check.Status == "WARN" && report.OverallStatus == "HEALTHY" {
			report.OverallStatus = "DEGRADED"
		}
	}

	return report
}

func checkDatabase(dbPath string) DiagnosticResult {
	if dbPath == "" {
		dbPath = pathutil.DefaultDatabasePath()
	}

	info, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		return DiagnosticResult{
			Name:    "Database State",
			Status:  "OK",
			Message: "Database does not exist yet (will initialize on first submit)",
			Details: dbPath,
		}
	}
	if err != nil {
		return DiagnosticResult{
			Name:    "Database State",
			Status:  "FAIL",
			Message: fmt.Sprintf("Failed to inspect database: %v", err),
			Details: dbPath,
		}
	}

	// Check POSIX permissions
	if runtime.GOOS != "windows" {
		mode := info.Mode().Perm()
		if mode != 0o600 {
			return DiagnosticResult{
				Name:    "Database Permissions",
				Status:  "WARN",
				Message: fmt.Sprintf("Database permissions are %#o, recommended 0600", mode),
				Details: dbPath,
			}
		}
	}

	// Test SQLite connectivity
	dsn := pathutil.SQLiteURI(dbPath, "_pragma=journal_mode(wal)")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return DiagnosticResult{
			Name:    "Database Connectivity",
			Status:  "FAIL",
			Message: fmt.Sprintf("Failed to connect: %v", err),
			Details: dbPath,
		}
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return DiagnosticResult{
			Name:    "Database Connectivity",
			Status:  "FAIL",
			Message: fmt.Sprintf("Ping failed: %v", err),
			Details: dbPath,
		}
	}

	return DiagnosticResult{
		Name:    "Database Connectivity",
		Status:  "OK",
		Message: "SQLite WAL database healthy and accessible",
		Details: dbPath,
	}
}

func checkWorkspace() DiagnosticResult {
	cwd, err := os.Getwd()
	if err != nil {
		return DiagnosticResult{
			Name:    "Workspace Integrity",
			Status:  "FAIL",
			Message: fmt.Sprintf("Unable to get current working directory: %v", err),
		}
	}

	// Check if in denied root
	for _, denied := range harness.DeniedPathFragments {
		if strings.Contains(cwd, denied) {
			return DiagnosticResult{
				Name:    "Workspace Integrity",
				Status:  "WARN",
				Message: fmt.Sprintf("Current directory matches denied path fragment %q", denied),
				Details: cwd,
			}
		}
	}

	return DiagnosticResult{
		Name:    "Workspace Integrity",
		Status:  "OK",
		Message: "Current workspace path valid and safe",
		Details: cwd,
	}
}

func checkWorkerBinaries() []DiagnosticResult {
	var results []DiagnosticResult
	binaries := []string{"agy", "claude", "gemini", "ollama"}

	if envAgy := os.Getenv("AGY_BIN"); envAgy != "" {
		if _, err := os.Stat(envAgy); err == nil {
			results = append(results, DiagnosticResult{
				Name:    "Worker Binary (AGY_BIN)",
				Status:  "OK",
				Message: "Custom worker binary found",
				Details: envAgy,
			})
		} else {
			results = append(results, DiagnosticResult{
				Name:    "Worker Binary (AGY_BIN)",
				Status:  "WARN",
				Message: "AGY_BIN set but file does not exist",
				Details: envAgy,
			})
		}
	}

	for _, bin := range binaries {
		path, err := exec.LookPath(bin)
		if err == nil {
			results = append(results, DiagnosticResult{
				Name:    fmt.Sprintf("Worker CLI (%s)", bin),
				Status:  "OK",
				Message: fmt.Sprintf("Available in PATH at %s", path),
				Details: path,
			})
		} else {
			results = append(results, DiagnosticResult{
				Name:    fmt.Sprintf("Worker CLI (%s)", bin),
				Status:  "OK",
				Message: "Optional worker CLI not found in PATH",
			})
		}
	}
	return results
}

func checkProviders() DiagnosticResult {
	configs := provider.DefaultConfigs()
	if len(configs) == 0 {
		return DiagnosticResult{
			Name:    "Provider Registry",
			Status:  "FAIL",
			Message: "No built-in providers registered",
		}
	}
	return DiagnosticResult{
		Name:    "Provider Registry",
		Status:  "OK",
		Message: fmt.Sprintf("%d built-in providers loaded successfully", len(configs)),
	}
}

func checkHarnessProfiles() DiagnosticResult {
	roles := harness.RoleNames()
	perms := harness.PermissionNames()

	if len(roles) == 0 || len(perms) == 0 {
		return DiagnosticResult{
			Name:    "Security Harness",
			Status:  "FAIL",
			Message: "Missing harness role or permission contracts",
		}
	}
	return DiagnosticResult{
		Name:    "Security Harness",
		Status:  "OK",
		Message: fmt.Sprintf("%d roles, %d permissions active and validated", len(roles), len(perms)),
	}
}

func resolveProvidersPath() string {
	if env := os.Getenv("G8S_PROVIDERS"); env != "" {
		return env
	}
	p := filepath.Join(pathutil.DefaultConfigDir(), "providers.json")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		legacy := filepath.Join(home, ".config", "g8s", "providers.json")
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return p
}

func checkProvidersFile(customPath ...string) DiagnosticResult {
	path := resolveProvidersPath()
	if len(customPath) > 0 && customPath[0] != "" {
		path = customPath[0]
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return DiagnosticResult{
			Name:    "Providers File",
			Status:  "OK",
			Message: "providers.json absent (legacy default is legal; built-in providers active)",
			Details: path,
		}
	}
	if err != nil {
		return DiagnosticResult{
			Name:    "Providers File",
			Status:  "FAIL",
			Message: fmt.Sprintf("%v (fix providers.json or remove it to fall back to built-ins)", err),
			Details: path,
		}
	}
	if info.IsDir() {
		return DiagnosticResult{
			Name:    "Providers File",
			Status:  "FAIL",
			Message: fmt.Sprintf("path %s is a directory, expected a file (fix providers.json or remove it to fall back to built-ins)", path),
			Details: path,
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		return DiagnosticResult{
			Name:    "Providers File",
			Status:  "FAIL",
			Message: fmt.Sprintf("%v (fix providers.json or remove it to fall back to built-ins)", err),
			Details: path,
		}
	}

	return DiagnosticResult{
		Name:    "Providers File",
		Status:  "OK",
		Message: fmt.Sprintf("%d provider(s) loaded cleanly from %s", len(cfg.Providers), path),
		Details: path,
	}
}

const minFreeDiskSpaceBytes uint64 = 2 * 1024 * 1024 * 1024 // 2 GiB

var diskFreeSpaceFunc = defaultDiskFreeSpace

func defaultDiskFreeSpace(path string) (uint64, error) {
	if runtime.GOOS == "windows" {
		return windowsDiskFreeSpace(path)
	}
	return unixDiskFreeSpace(path)
}

func unixDiskFreeSpace(path string) (uint64, error) {
	p := path
	for p != "" && p != "." && p != "/" {
		if _, err := os.Stat(p); err == nil {
			break
		}
		p = filepath.Dir(p)
	}
	if p == "" {
		p = "."
	}
	out, err := exec.Command("df", "-k", "-P", p).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output: %s", string(out))
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, fmt.Errorf("unexpected df output format: %v", fields)
	}
	availKB, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse available space from df: %w", err)
	}
	return availKB * 1024, nil
}

func windowsDiskFreeSpace(path string) (uint64, error) {
	vol := filepath.VolumeName(path)
	if vol == "" {
		vol = "C:"
	}
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("(Get-PSDrive %s).Free", strings.TrimSuffix(vol, ":")))
	out, err := cmd.Output()
	if err == nil {
		if b, perr := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); perr == nil {
			return b, nil
		}
	}
	cmdFs := exec.Command("fsutil", "volume", "diskfree", vol+"\\")
	outFs, errFs := cmdFs.Output()
	if errFs == nil {
		for _, line := range strings.Split(string(outFs), "\n") {
			if strings.Contains(strings.ToLower(line), "free bytes") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					valStr := strings.TrimSpace(parts[1])
					valStr = strings.ReplaceAll(valStr, ",", "")
					valStr = strings.ReplaceAll(valStr, " ", "")
					if b, err := strconv.ParseUint(valStr, 10, 64); err == nil {
						return b, nil
					}
				}
			}
		}
	}
	return 0, fmt.Errorf("unable to determine free disk space for %s", path)
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func checkDiskSpace(stateDir ...string) DiagnosticResult {
	dir := pathutil.DefaultStateDir()
	if len(stateDir) > 0 && stateDir[0] != "" {
		dir = stateDir[0]
	}

	freeBytes, err := diskFreeSpaceFunc(dir)
	if err != nil {
		return DiagnosticResult{
			Name:    "Disk Space",
			Status:  "WARN",
			Message: fmt.Sprintf("Failed to inspect disk space on %s: %v (disk exhaustion has masqueraded as random test/attempt failures twice)", dir, err),
			Details: dir,
		}
	}

	if freeBytes < minFreeDiskSpaceBytes {
		return DiagnosticResult{
			Name:    "Disk Space",
			Status:  "WARN",
			Message: fmt.Sprintf("Low free disk space on state volume (%s available < 2GiB threshold; disk exhaustion has masqueraded as random test/attempt failures twice)", formatBytes(freeBytes)),
			Details: fmt.Sprintf("path: %s, free: %d bytes", dir, freeBytes),
		}
	}

	return DiagnosticResult{
		Name:    "Disk Space",
		Status:  "OK",
		Message: fmt.Sprintf("Sufficient disk space available on state volume (%s free)", formatBytes(freeBytes)),
		Details: fmt.Sprintf("path: %s, free: %d bytes", dir, freeBytes),
	}
}

func checkStaleBinary(workspaceDir ...string) DiagnosticResult {
	dir, err := os.Getwd()
	if len(workspaceDir) > 0 && workspaceDir[0] != "" {
		dir = workspaceDir[0]
	} else if err != nil {
		return DiagnosticResult{
			Name:    "Stale Binary Check",
			Status:  "OK",
			Message: fmt.Sprintf("Unable to determine working directory: %v (check skipped)", err),
		}
	}

	// Check if bin/g8s exists
	binPath := filepath.Join(dir, "bin", "g8s")
	info, err := os.Stat(binPath)
	if os.IsNotExist(err) && runtime.GOOS == "windows" {
		binPath = filepath.Join(dir, "bin", "g8s.exe")
		info, err = os.Stat(binPath)
	}
	if os.IsNotExist(err) {
		return DiagnosticResult{
			Name:    "Stale Binary Check",
			Status:  "OK",
			Message: "bin/g8s absent (check skipped)",
			Details: binPath,
		}
	}
	if err != nil {
		return DiagnosticResult{
			Name:    "Stale Binary Check",
			Status:  "OK",
			Message: fmt.Sprintf("Unable to stat %s: %v (check skipped)", binPath, err),
			Details: binPath,
		}
	}

	// Check newest commit time via git log -1 --format=%ct
	cmd := exec.Command("git", "log", "-1", "--format=%ct")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return DiagnosticResult{
			Name:    "Stale Binary Check",
			Status:  "OK",
			Message: "Git repository absent or inaccessible (check skipped)",
			Details: binPath,
		}
	}

	commitUnix, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return DiagnosticResult{
			Name:    "Stale Binary Check",
			Status:  "OK",
			Message: fmt.Sprintf("Unable to parse commit timestamp %q (check skipped)", strings.TrimSpace(string(out))),
			Details: binPath,
		}
	}

	commitTime := time.Unix(commitUnix, 0)
	binModTime := info.ModTime()

	if binModTime.Before(commitTime) {
		return DiagnosticResult{
			Name:    "Stale Binary Check",
			Status:  "WARN",
			Message: fmt.Sprintf("bin/g8s predates latest commit (binary mtime: %s, commit: %s; stale-binary trap bit twice this campaign - rebuild with 'make local')", binModTime.UTC().Format(time.RFC3339), commitTime.UTC().Format(time.RFC3339)),
			Details: binPath,
		}
	}

	return DiagnosticResult{
		Name:    "Stale Binary Check",
		Status:  "OK",
		Message: fmt.Sprintf("bin/g8s is up-to-date with repository (binary mtime: %s)", binModTime.UTC().Format(time.RFC3339)),
		Details: binPath,
	}
}

// RunAttentionCheck evaluates the 5 self-reflection questions for the active session.
func (d *Doctor) RunAttentionCheck(ctx context.Context, actor string, heartbeatDir string) *AttentionCheckReport {
	questions := []AttentionCheckQuestion{
		{
			Number:   1,
			Question: "What 2-3 edge cases might your last task have missed?",
			Answer:   "unknown",
		},
		{
			Number:   2,
			Question: "Run 'g8s cleanup --target ghost-process --dry-run' — does it list your own session? (it should NOT)",
			Answer:   d.checkGhostProcesses(ctx, heartbeatDir),
		},
		{
			Number:   3,
			Question: "Are you running with --actor set to a unique identifier?",
			Answer:   d.checkActor(actor),
		},
		{
			Number:   4,
			Question: "When was your last heartbeat update?",
			Answer:   d.checkLastHeartbeat(heartbeatDir),
		},
		{
			Number:   5,
			Question: "What contract from your brief would you violate by accident?",
			Answer:   "unknown",
		},
	}

	return &AttentionCheckReport{
		Questions: questions,
	}
}

func (d *Doctor) checkGhostProcesses(ctx context.Context, heartbeatDir string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	hbDir := heartbeatDir
	if hbDir == "" {
		hbDir = filepath.Join(cwd, heartbeat.DefaultHeartbeatDir)
	}
	pm := &cleanup.DefaultProcessManager{RepoDir: cwd}
	ghosts, err := pm.FindGhostProcesses(ctx, hbDir, 5*time.Minute, time.Now)
	if err != nil {
		return "unknown"
	}
	if len(ghosts) == 0 {
		return "clean (0 ghost processes detected; session is not listed)"
	}
	return fmt.Sprintf("%d ghost process(es) detected", len(ghosts))
}

func (d *Doctor) checkActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" || actor == "default" {
		return "unknown (actor is empty or default; recommend setting --actor <unique-id>)"
	}
	return fmt.Sprintf("yes (actor=%s)", actor)
}

func (d *Doctor) checkLastHeartbeat(heartbeatDir string) string {
	hbStore := heartbeat.NewStore(heartbeatDir, time.Now)
	list, err := hbStore.List()
	if err != nil || len(list) == 0 {
		return "unknown (no active heartbeat found)"
	}
	mostRecent := list[0]
	ago := time.Since(mostRecent.LastUpdate).Truncate(time.Second)
	return fmt.Sprintf("%s (%s ago for session %s)",
		mostRecent.LastUpdate.UTC().Format(time.RFC3339),
		ago,
		mostRecent.SessionID,
	)
}
