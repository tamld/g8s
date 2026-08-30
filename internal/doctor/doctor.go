// Package doctor implements diagnostic sanity checks for g8s environment,
// security boundaries, permissions, and worker binary availability.
package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/provider"
	"github.com/tamld/g8s/internal/registry"
	_ "modernc.org/sqlite"
)

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
		defer func() { _ = key.Close() }()
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
		defer func() { _ = key.Close() }()
		if loc, err := key.GetStringValue("InstallLocation"); err == nil && loc != "" {
			return loc
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return `C:\Program Files\g8s`
}

// checkWindowsEnvironment executes Windows-specific diagnostics for install source and PATH.
func (d *Doctor) checkWindowsEnvironment() []DiagnosticResult {
	if runtime.GOOS != "windows" {
		return nil
	}

	var results []DiagnosticResult
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile, _ = os.UserHomeDir()
	}

	scope := d.Scope
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

	source := d.detectInstallSource()
	installPath := d.detectInstallPath()

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

	// Resolved canonical paths
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

	// Check PATH registration
	pathEnv := os.Getenv("PATH")
	onPath := false
	for _, p := range filepath.SplitList(pathEnv) {
		if strings.EqualFold(strings.TrimRight(p, `/\`), strings.TrimRight(installPath, `/\`)) {
			onPath = true
			break
		}
	}

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

	if d.DetectPaths {
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
	report.Checks = append(report.Checks, checkWorkspace())
	if runtime.GOOS == "windows" {
		report.Checks = append(report.Checks, d.checkWindowsEnvironment()...)
	}
	report.Checks = append(report.Checks, checkWorkerBinaries()...)
	report.Checks = append(report.Checks, checkProviders())
	report.Checks = append(report.Checks, checkHarnessProfiles())

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
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)", url.PathEscape(dbPath))
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
