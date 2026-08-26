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
	"strings"

	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/provider"
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
	Checks        []DiagnosticResult `json:"checks"`
}

// RunDiagnostics executes the full diagnostic suite across the environment.
func RunDiagnostics(ctx context.Context, dbPath string) *DoctorReport {
	report := &DoctorReport{
		OverallStatus: "HEALTHY",
		Platform:      fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoRuntime:     runtime.Version(),
		ZeroCGO:       true,
	}

	report.Checks = append(report.Checks, checkDatabase(dbPath))
	report.Checks = append(report.Checks, checkWorkspace())
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
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".local", "state", "g8s", "g8s.db")
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
		if mode != 0600 && mode != 0644 {
			return DiagnosticResult{
				Name:    "Database Permissions",
				Status:  "WARN",
				Message: fmt.Sprintf("Database permissions are %#o, recommended 0600", mode),
				Details: dbPath,
			}
		}
	}

	// Test SQLite connectivity
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)", dbPath)
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
