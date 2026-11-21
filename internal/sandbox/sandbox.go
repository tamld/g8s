// Package sandbox enforces OS-level subprocess containment and environment secret scrubbing for worker execution.
package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ContainmentMode describes the strength of process isolation achieved at launch.
type ContainmentMode string

const (
	ModeKernelEnforced       ContainmentMode = "kernel_enforced"
	ModeJobObjectEnvScrubbed ContainmentMode = "job_object_env_scrubbed"
	ModePolicyOnly           ContainmentMode = "policy_only"
)

// ErrKernelSandboxRequired is returned when RequireKernelEnforced is set and
// the host cannot provide ModeKernelEnforced containment (e.g., policy_only or
// Windows JobObject without kernel filesystem ACLs).
var ErrKernelSandboxRequired = errors.New("E_SANDBOX_KERNEL_ENFORCED_REQUIRED")

// Config parameterizes per-attempt subprocess containment and environment scrubbing.
type Config struct {
	Argv                   []string
	WorktreeDir            string
	RunDir                 string
	StateDir               string
	Permission             string
	Role                   string
	ReceiptID              string
	AllowedPaths           []string
	ProviderName           string
	AddDirs                []string
	ExtraWriteDirs         []string
	DenyDirs               []string
	BaseEnv                []string
	TaskEnv                []string
	NoSandbox              bool
	RequireKernelEnforced  bool
	RequireNetworkEnforced bool
	// AllowDegradedSandbox (#971 WP1): explicit operator opt-in that lets a
	// workspace_write attempt proceed below kernel_enforced containment with
	// sandbox_degraded recorded in the attempt envelope. Without it, hosts
	// lacking a kernel sandbox backend refuse workspace_write (fail-closed).
	AllowDegradedSandbox bool
}

// PreparedLaunch holds the wrapped command vector, scrubbed environment, containment
// telemetry, policy enforcement report, and a cleanup hook that reaps surviving sandboxed grandchildren.
type PreparedLaunch struct {
	Argv         []string
	Env          []string
	Mode         ContainmentMode
	Backend      string
	TempDir      string
	SandboxID    string
	PolicyReport *PolicyEnforcementReport
	Cleanup      func()
	// Degraded (#971 WP1) mirrors PolicyReport.Effective.SandboxDegraded: true
	// when containment is below kernel_enforced with the sandbox requested.
	Degraded       bool
	DegradedReason string
	proof          EnforcementProof
}

// CanonicalPath cleans p and resolves symlinks on the longest existing prefix,
// preserving any trailing non-existent segments. This ensures macOS symlinked
// roots (/tmp -> /private/tmp, /var -> /private/var) match XNU Seatbelt rules.
func CanonicalPath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}

	var tail []string
	cur := cleaned
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			parts := append([]string{resolved}, tail...)
			return filepath.Clean(filepath.Join(parts...))
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
	return cleaned
}

// ResolveGitWorktreeAdminDir returns the canonical git admin directory when
// worktreeDir uses a `.git` gitdir pointer file (so git status/index.lock
// works inside an isolated worktree without exposing the main checkout).
// Regular `.git` directories already sit inside worktreeDir and return "".
func ResolveGitWorktreeAdminDir(worktreeDir string) string {
	trimmed := strings.TrimSpace(worktreeDir)
	if trimmed == "" {
		return ""
	}
	gitEntry := filepath.Join(trimmed, ".git")
	fi, err := os.Stat(gitEntry)
	if err != nil || fi.IsDir() {
		return ""
	}
	data, err := os.ReadFile(gitEntry)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmedLine), "gitdir:") {
			target := strings.TrimSpace(trimmedLine[len("gitdir:"):])
			if target == "" {
				return ""
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(trimmed, target)
			}
			return CanonicalPath(target)
		}
	}
	return ""
}

// Prepare builds a scrubbed environment, isolated temporary/cache directories,
// and an OS-enforced launch wrapper for cfg.
func Prepare(cfg Config) (*PreparedLaunch, error) {
	if len(cfg.Argv) == 0 || strings.TrimSpace(cfg.Argv[0]) == "" {
		return nil, errors.New("sandbox: empty argv")
	}
	if err := validateConfigPaths(cfg); err != nil {
		return nil, err
	}

	tmpDir, ownTmp, err := prepareIsolatedTempDir(cfg.RunDir)
	if err != nil {
		return nil, err
	}
	gocacheDir, err := prepareGoCacheDir(cfg.Permission, cfg.StateDir, tmpDir)
	if err != nil {
		if ownTmp {
			removeAllLogged(tmpDir)
		}
		return nil, err
	}

	sandboxID := "sbx-" + randomHex(8)
	baseEnv := cfg.BaseEnv
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	env := ScrubEnv(baseEnv, cfg.TaskEnv, cfg.ProviderName, tmpDir, gocacheDir, sandboxID)

	reqPolicy := requestedPolicyFromConfig(cfg)

	if cfg.NoSandbox {
		report := EvaluatePolicyContract(reqPolicy, newSandboxDisabledProof())
		if report.RejectedBeforeSpawn {
			if ownTmp {
				removeAllLogged(tmpDir)
			}
			sentinel := ErrKernelSandboxRequired
			if !reqPolicy.RequireKernelEnforced && reqPolicy.RequireNetworkEnforced {
				sentinel = ErrNetworkEnforcementRequired
			}
			return nil, &PolicyRejectionError{Err: sentinel, Report: report}
		}
		launch := &PreparedLaunch{
			Argv:         append([]string{}, cfg.Argv...),
			Env:          env,
			Mode:         ModePolicyOnly,
			Backend:      "disabled",
			TempDir:      tmpDir,
			SandboxID:    sandboxID,
			PolicyReport: &report,
			Cleanup: func() {
				if ownTmp {
					removeAllLogged(tmpDir)
				}
			},
		}
		launch.Degraded = report.Effective.SandboxDegraded
		launch.DegradedReason = report.Effective.SandboxDegradedReason
		attachSandboxMarker(cfg.StateDir, cfg, launch)
		return launch, nil
	}

	launch, osErr := prepareOSFn(cfg, env, tmpDir, gocacheDir, sandboxID, ownTmp)
	if osErr != nil {
		return nil, osErr
	}
	enforced, err := enforcePolicyRequirement(launch, reqPolicy)
	if err != nil {
		return nil, err
	}
	attachSandboxMarker(cfg.StateDir, cfg, enforced)
	return enforced, nil
}

func requestedPolicyFromConfig(cfg Config) RequestedPolicy {
	return RequestedPolicy{
		Permission:             cfg.Permission,
		Role:                   cfg.Role,
		ReceiptID:              cfg.ReceiptID,
		AllowedPaths:           append([]string(nil), cfg.AllowedPaths...),
		HoldoutDenyDirs:        MergeDenyDirs(nil, cfg.DenyDirs),
		RequireKernelEnforced:  cfg.RequireKernelEnforced,
		NoSandbox:              cfg.NoSandbox,
		RequireNetworkEnforced: cfg.RequireNetworkEnforced,
		AllowDegradedSandbox:   cfg.AllowDegradedSandbox,
		TransportKind:          "process",
	}
}

// prepareOSFn is the per-OS launch-builder seam (build-tag selected); tests
// stub it to exercise the central degraded-sandbox gate on any host (#971 WP1).
var prepareOSFn = prepareOS

func attachSandboxMarker(stateDir string, cfg Config, launch *PreparedLaunch) {
	if launch == nil || strings.TrimSpace(stateDir) == "" || strings.TrimSpace(launch.SandboxID) == "" {
		return
	}
	if err := WriteSandboxMarker(stateDir, SandboxMarker{
		SandboxID:   launch.SandboxID,
		WorkerPID:   os.Getpid(),
		WorktreeDir: cfg.WorktreeDir,
		RunDir:      cfg.RunDir,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: persist marker %s: %v\n", launch.SandboxID, err)
	}
	origCleanup := launch.Cleanup
	sbxID := launch.SandboxID
	launch.Cleanup = func() {
		if origCleanup != nil {
			origCleanup()
		}
		RemoveSandboxMarker(stateDir, sbxID)
	}
}

func enforceContainmentRequirement(launch *PreparedLaunch, requireKernel bool) (*PreparedLaunch, error) {
	return enforcePolicyRequirement(launch, RequestedPolicy{
		RequireKernelEnforced: requireKernel,
		TransportKind:         "process",
	})
}

func enforcePolicyRequirement(launch *PreparedLaunch, reqPolicy RequestedPolicy) (*PreparedLaunch, error) {
	if launch == nil {
		return nil, nil
	}
	proof := launch.proof
	if !proof.sealed {
		proof = newSandboxKernelProof(launch.Mode, launch.Backend, false, "")
	}
	report := EvaluatePolicyContract(reqPolicy, proof)
	if report.RejectedBeforeSpawn {
		if launch.Cleanup != nil {
			launch.Cleanup()
		}
		sentinel := ErrKernelSandboxRequired
		switch report.Evaluated.RejectionCode {
		case ErrNetworkEnforcementRequired.Error():
			sentinel = ErrNetworkEnforcementRequired
		case ErrDegradedSandboxNotAllowed.Error():
			sentinel = ErrDegradedSandboxNotAllowed
		}
		return nil, &PolicyRejectionError{Err: sentinel, Report: report}
	}
	launch.PolicyReport = &report
	launch.Degraded = report.Effective.SandboxDegraded
	launch.DegradedReason = report.Effective.SandboxDegradedReason
	return launch, nil
}

func validatePathString(label, val string) error {
	if strings.ContainsAny(val, "\x00\r\n") {
		return fmt.Errorf("sandbox: %s contains disallowed control character", label)
	}
	return nil
}

func validateConfigPaths(cfg Config) error {
	checks := []struct {
		label string
		val   string
	}{
		{"worktree_dir", cfg.WorktreeDir},
		{"run_dir", cfg.RunDir},
		{"state_dir", cfg.StateDir},
	}
	for _, c := range checks {
		if err := validatePathString(c.label, c.val); err != nil {
			return err
		}
	}
	for _, d := range cfg.AddDirs {
		if err := validatePathString("add_dir", d); err != nil {
			return err
		}
	}
	for _, d := range cfg.ExtraWriteDirs {
		if err := validatePathString("extra_write_dir", d); err != nil {
			return err
		}
	}
	for _, d := range cfg.DenyDirs {
		if err := validatePathString("deny_dir", d); err != nil {
			return err
		}
	}
	return nil
}

func prepareIsolatedTempDir(runDir string) (tmpDir string, ownTmp bool, err error) {
	if strings.TrimSpace(runDir) != "" {
		raw := filepath.Join(strings.TrimSpace(runDir), "tmp")
		if mkErr := os.MkdirAll(raw, 0o700); mkErr != nil {
			return "", false, fmt.Errorf("sandbox: create isolated tmpdir %s: %w", raw, mkErr)
		}
		tightenDir0700(raw)
		return CanonicalPath(raw), false, nil
	}
	raw, mkErr := os.MkdirTemp("", "g8s-sbx-tmp-*")
	if mkErr != nil {
		return "", false, fmt.Errorf("sandbox: create temp dir: %w", mkErr)
	}
	tightenDir0700(raw)
	return CanonicalPath(raw), true, nil
}

func prepareGoCacheDir(permission, stateDir, tmpDir string) (string, error) {
	var raw string
	if strings.EqualFold(strings.TrimSpace(permission), "workspace_write") && strings.TrimSpace(stateDir) != "" {
		raw = filepath.Join(strings.TrimSpace(stateDir), "sandbox-gocache")
	} else {
		raw = filepath.Join(tmpDir, "gocache")
	}
	if err := os.MkdirAll(raw, 0o700); err != nil {
		return "", fmt.Errorf("sandbox: create gocache dir %s: %w", raw, err)
	}
	tightenDir0700(raw)
	return CanonicalPath(raw), nil
}

func tightenDir0700(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: chmod 0700 %s: %v\n", dir, err)
	}
}

func removeAllLogged(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: remove temp dir %s: %v\n", dir, err)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}
