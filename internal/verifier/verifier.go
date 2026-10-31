// Package verifier implements verifier-class registry and acceptance verification (issue #515, SCORECARD S-7).
//
// The verifier is the factory's self-evaluation organ: a completed round must
// be accepted (or refused) without human eyes, with trust earned per class and
// never assumed. Classes earn hard status by citing real catches. Deny-by-default:
// unregistered classes require human acceptance.
package verifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Registry stores registered verifier classes and handles fail-closed loading and matching.
type Registry struct {
	classes []Class
	err     error
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		classes: make([]Class, 0),
	}
}

// Classes returns a copy of the valid registered classes.
func (r *Registry) Classes() []Class {
	if r == nil || len(r.classes) == 0 {
		return nil
	}
	res := make([]Class, len(r.classes))
	copy(res, r.classes)
	return res
}

// Err returns any error encountered during loading.
func (r *Registry) Err() error {
	if r == nil {
		return nil
	}
	return r.err
}

// Resolve matches write-scope paths against registered classes deterministically.
// Rules:
//   - An entry matches when ALL paths fall inside its globs.
//   - Lowest-priority-number matching entry wins.
//   - Ties broken deterministically by name alphabetically.
//   - Empty paths or no matching classes -> (unregistered, false).
func (r *Registry) Resolve(paths []string) (Class, bool) {
	if r == nil || r.err != nil || len(r.classes) == 0 || len(paths) == 0 {
		return Class{Name: "unregistered", Status: StatusAdvisory}, false
	}

	var candidates []Class
	for _, c := range r.classes {
		if len(c.Paths) == 0 {
			continue
		}
		allMatch := true
		for _, p := range paths {
			matched := false
			for _, pattern := range c.Paths {
				if pathMatches(p, pattern) {
					matched = true
					break
				}
			}
			if !matched {
				allMatch = false
				break
			}
		}
		if allMatch {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return Class{Name: "unregistered", Status: StatusAdvisory}, false
	}

	// Deterministic selection: lowest priority number wins, tie-break by name
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].Name < candidates[j].Name
	})

	return candidates[0], true
}

// CommandRunner executes a command and returns output and status.
type CommandRunner func(ctx context.Context, name string, args ...string) (stdout []byte, stderr []byte, exitCode int, err error)

// defaultCommandRunner runs an external command via os/exec.
func defaultCommandRunner(dir string) CommandRunner {
	return func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		if dir != "" {
			cmd.Dir = dir
		}
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err := cmd.Run()
		stdout := stdoutBuf.Bytes()
		stderr := stderrBuf.Bytes()

		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return stdout, stderr, exitErr.ExitCode(), nil
			}
			// Infrastructure / runner error
			return stdout, stderr, -1, err
		}
		return stdout, stderr, 0, nil
	}
}

// TelemetrySink is an optional hook for recording emitted telemetry events in tests.
type TelemetrySink func(taskID string, verdict Verdict)

// Verifier coordinates class resolution, check execution, self-grade protection, and telemetry.
type Verifier struct {
	registry          *Registry
	runner            CommandRunner
	repoRoot          string
	telemetrySink     TelemetrySink
	lessonsLedgerPath string
}

// Option configures a Verifier instance.
type Option func(*Verifier)

// WithLessonsLedgerPath overrides the path to the ratified lessons JSONL ledger (#644).
func WithLessonsLedgerPath(path string) Option {
	return func(v *Verifier) {
		v.lessonsLedgerPath = path
	}
}

// WithRegistry sets a pre-configured Registry.
func WithRegistry(r *Registry) Option {
	return func(v *Verifier) {
		v.registry = r
	}
}

// WithRegistryPath sets the path to load the registry file from.
func WithRegistryPath(path string) Option {
	return func(v *Verifier) {
		r, err := LoadRegistry(path)
		if err != nil && r == nil {
			r = &Registry{err: err}
		}
		v.registry = r
	}
}

// WithRegistryData loads the registry directly from YAML bytes.
func WithRegistryData(data []byte) Option {
	return func(v *Verifier) {
		r, err := ParseRegistry(data)
		if err != nil && r == nil {
			r = &Registry{err: err}
		}
		v.registry = r
	}
}

// WithCommandRunner overrides the command execution engine.
func WithCommandRunner(runner CommandRunner) Option {
	return func(v *Verifier) {
		v.runner = runner
	}
}

// WithRepoRoot overrides the root working directory for script checks.
func WithRepoRoot(dir string) Option {
	return func(v *Verifier) {
		v.repoRoot = dir
	}
}

// WithTelemetrySink intercepts emitted telemetry events.
func WithTelemetrySink(sink TelemetrySink) Option {
	return func(v *Verifier) {
		v.telemetrySink = sink
	}
}

// NewVerifier creates a Verifier configured with the provided options.
func NewVerifier(opts ...Option) *Verifier {
	v := &Verifier{}
	for _, opt := range opts {
		opt(v)
	}

	if v.registry == nil {
		r, err := LoadRegistry(DefaultRegistryPath)
		if err != nil && r == nil {
			r = &Registry{err: err}
		}
		v.registry = r
	}

	if v.repoRoot == "" {
		if root, err := findRepoRoot(); err == nil {
			v.repoRoot = root
		}
	}

	if v.runner == nil {
		v.runner = defaultCommandRunner(v.repoRoot)
	}

	return v
}

// Verify evaluates acceptance checks for target on behalf of caller.
// Self-grade guard: caller set and equal to target => refused with typed error.
func (v *Verifier) Verify(target TaskRef, caller TaskRef) (Verdict, error) {
	return v.VerifyContext(context.Background(), target, caller)
}

// VerifyContext evaluates acceptance checks with context cancellation.
func (v *Verifier) VerifyContext(ctx context.Context, target TaskRef, caller TaskRef) (Verdict, error) {
	// Self-grade guard: caller set and equal to target (normalized for whitespace and case)
	callerID := strings.TrimSpace(caller.ID)
	targetID := strings.TrimSpace(target.ID)
	if callerID != "" && targetID != "" && strings.EqualFold(callerID, targetID) {
		return Verdict{}, &SelfGradeError{
			TargetID: target.ID,
			CallerID: caller.ID,
		}
	}

	now := time.Now().UTC()

	// Read-only or no receipt -> unregistered verdict (exit 0 / human acceptance floor)
	if (target.ReceiptID == "" || len(target.AllowedPaths) == 0) && target.WorktreeDir == "" && len(target.DoDItems) == 0 && target.FeatureID == "" && strings.TrimSpace(target.HoldoutDir) == "" && len(target.ExtraChecks) == 0 {
		verdict := Verdict{
			Class:       "unregistered",
			Registered:  false,
			Status:      StatusAdvisory,
			Outcome:     OutcomeUnregistered,
			Checks:      []CheckResult{},
			Scope:       false,
			RecordedAt:  now,
			WorktreeDir: target.WorktreeDir,
			F2Summary:   loadF2TelemetrySummary(target.ID),
			FeatureID:   target.FeatureID,
		}
		v.emitTelemetry(target.ID, verdict)
		return verdict, nil
	}

	// Class resolution
	class, registered := v.registry.Resolve(target.AllowedPaths)
	if !registered {
		if target.WorktreeDir != "" {
			pkgs := derivePackagesFromPaths(target.AllowedPaths)
			if len(pkgs) > 0 {
				class = Class{
					Name:   "worktree-go-test",
					Status: StatusAdvisory,
					Checks: []Check{
						{Type: CheckTypeGoTest, Packages: pkgs},
					},
				}
				registered = true
			}
		}
	}

	if len(target.ExtraChecks) > 0 {
		class.Checks = append(class.Checks, target.ExtraChecks...)
		if !registered {
			registered = true
			class.Name = "feature-verify"
			class.Status = StatusAdvisory
		}
	}

	if (target.FeatureID != "" || strings.TrimSpace(target.HoldoutDir) != "") && !registered {
		registered = true
		if class.Name == "" {
			class.Name = "feature-verify"
		}
		if class.Status == "" {
			class.Status = StatusAdvisory
		}
	}

	if !registered && target.WorktreeDir == "" && len(target.DoDItems) == 0 && target.FeatureID == "" && strings.TrimSpace(target.HoldoutDir) == "" && len(target.ExtraChecks) == 0 {
		verdict := Verdict{
			Class:       "unregistered",
			Registered:  false,
			Status:      StatusAdvisory,
			Outcome:     OutcomeUnregistered,
			Checks:      []CheckResult{},
			Scope:       false,
			RecordedAt:  now,
			WorktreeDir: target.WorktreeDir,
			F2Summary:   loadF2TelemetrySummary(target.ID),
			FeatureID:   target.FeatureID,
		}
		v.emitTelemetry(target.ID, verdict)
		return verdict, nil
	}

	// Registered class: execute checks
	var checkResults []CheckResult
	var allAdvisories []DiffAdvisory
	var allDormantReawakened []string
	var allDiagnostics []FailureDiagnostic
	var totalTestsRan, totalTestsFailed int
	var combinedLogs bytes.Buffer
	hasRunnerError := false
	hasCheckFailure := false

	repoRoot := v.repoRoot
	if repoRoot == "" && target.WorktreeDir != "" {
		repoRoot = target.WorktreeDir
	}

	runner := v.runner
	if runner == nil {
		runner = defaultCommandRunner(repoRoot)
	}

	for _, chk := range class.Checks {
		switch chk.Type {
		case CheckTypeScript:
			res := v.runScriptCheck(ctx, runner, chk.Run)
			checkResults = append(checkResults, res.checkResult)
			if len(res.stdout) > 0 {
				combinedLogs.Write(res.stdout)
				if !bytes.HasSuffix(res.stdout, []byte("\n")) {
					combinedLogs.WriteByte('\n')
				}
			}
			if len(res.stderr) > 0 {
				combinedLogs.Write(res.stderr)
				if !bytes.HasSuffix(res.stderr, []byte("\n")) {
					combinedLogs.WriteByte('\n')
				}
			}
			if res.diagnostic != nil {
				allDiagnostics = append(allDiagnostics, *res.diagnostic)
			}
			if strings.HasPrefix(res.checkResult.Detail, "runner error:") {
				hasRunnerError = true
			} else if !res.checkResult.OK {
				hasCheckFailure = true
			}

		case CheckTypeGoTest:
			pkgs := chk.Packages
			if len(pkgs) == 0 {
				pkgs = derivePackagesFromPaths(target.AllowedPaths)
			}

			if len(pkgs) == 0 {
				// No Go packages touched: vacuous pass
				checkResults = append(checkResults, CheckResult{
					Type:   string(CheckTypeGoTest),
					Target: "none",
					OK:     true,
					Detail: "no go packages touched",
				})
			} else {
				res := v.runGoTestCheck(ctx, runner, pkgs)
				checkResults = append(checkResults, res.checkResult)
				if len(res.stdout) > 0 {
					combinedLogs.Write(res.stdout)
					if !bytes.HasSuffix(res.stdout, []byte("\n")) {
						combinedLogs.WriteByte('\n')
					}
				}
				if len(res.stderr) > 0 {
					combinedLogs.Write(res.stderr)
					if !bytes.HasSuffix(res.stderr, []byte("\n")) {
						combinedLogs.WriteByte('\n')
					}
				}
				totalTestsRan += res.testsRan
				totalTestsFailed += res.testsFailed
				if len(res.diagnostics) > 0 {
					allDiagnostics = append(allDiagnostics, res.diagnostics...)
				}
				if strings.HasPrefix(res.checkResult.Detail, "runner error:") {
					hasRunnerError = true
				} else if !res.checkResult.OK {
					hasCheckFailure = true
				}
			}

		case CheckTypeDiffGuard:
			res, advs, reawakened := v.runDiffGuardCheck(ctx, runner, target)
			checkResults = append(checkResults, res)
			allAdvisories = append(allAdvisories, advs...)
			allDormantReawakened = append(allDormantReawakened, reawakened...)
			if strings.HasPrefix(res.Detail, "runner error:") {
				hasRunnerError = true
			} else if !res.OK {
				hasCheckFailure = true
			}
		}
	}

	// Holdout checks execution
	var holdoutChecksOK *bool
	if strings.TrimSpace(target.HoldoutDir) != "" {
		holdoutDir := strings.TrimSpace(target.HoldoutDir)
		if !filepath.IsAbs(holdoutDir) && repoRoot != "" {
			holdoutDir = filepath.Join(repoRoot, holdoutDir)
		}
		var holdoutChecks []CheckResult
		var holdoutFailed bool
		var scriptPaths []string
		var hasGoTest bool

		_ = filepath.Walk(holdoutDir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			low := strings.ToLower(info.Name())
			if strings.HasSuffix(low, ".sh") || strings.HasSuffix(low, ".js") || strings.HasSuffix(low, ".mjs") || strings.HasSuffix(low, ".py") {
				scriptPaths = append(scriptPaths, p)
			} else if strings.HasSuffix(low, "_test.go") {
				hasGoTest = true
			}
			return nil
		})
		sort.Strings(scriptPaths)

		for _, sp := range scriptPaths {
			res := v.runScriptCheck(ctx, runner, sp)
			res.checkResult.Type = "holdout"
			holdoutChecks = append(holdoutChecks, res.checkResult)
			checkResults = append(checkResults, res.checkResult)
			if len(res.stdout) > 0 {
				combinedLogs.Write(res.stdout)
				if !bytes.HasSuffix(res.stdout, []byte("\n")) {
					combinedLogs.WriteByte('\n')
				}
			}
			if len(res.stderr) > 0 {
				combinedLogs.Write(res.stderr)
				if !bytes.HasSuffix(res.stderr, []byte("\n")) {
					combinedLogs.WriteByte('\n')
				}
			}
			if res.diagnostic != nil {
				allDiagnostics = append(allDiagnostics, *res.diagnostic)
			}
			if strings.HasPrefix(res.checkResult.Detail, "runner error:") {
				hasRunnerError = true
			} else if !res.checkResult.OK {
				holdoutFailed = true
				hasCheckFailure = true
			}
		}

		if hasGoTest {
			res := v.runGoTestCheck(ctx, runner, []string{holdoutDir})
			res.checkResult.Type = "holdout"
			holdoutChecks = append(holdoutChecks, res.checkResult)
			checkResults = append(checkResults, res.checkResult)
			if len(res.stdout) > 0 {
				combinedLogs.Write(res.stdout)
				if !bytes.HasSuffix(res.stdout, []byte("\n")) {
					combinedLogs.WriteByte('\n')
				}
			}
			if len(res.stderr) > 0 {
				combinedLogs.Write(res.stderr)
				if !bytes.HasSuffix(res.stderr, []byte("\n")) {
					combinedLogs.WriteByte('\n')
				}
			}
			totalTestsRan += res.testsRan
			totalTestsFailed += res.testsFailed
			if len(res.diagnostics) > 0 {
				allDiagnostics = append(allDiagnostics, res.diagnostics...)
			}
			if strings.HasPrefix(res.checkResult.Detail, "runner error:") {
				hasRunnerError = true
			} else if !res.checkResult.OK {
				holdoutFailed = true
				hasCheckFailure = true
			}
		}

		okVal := len(holdoutChecks) > 0 && !holdoutFailed
		holdoutChecksOK = &okVal
		if !okVal {
			hasCheckFailure = true
		}
	}

	// Determine overall outcome:
	// Fail-open rule: check's runner error (infrastructure error) => outcome not-run
	var outcome Outcome
	switch {
	case !registered:
		outcome = OutcomeUnregistered
	case hasRunnerError:
		outcome = OutcomeNotRun
	case hasCheckFailure:
		outcome = OutcomeFail
	default:
		outcome = OutcomePass
	}

	// Canary Check
	var canaryResult *CanaryResult
	var vetoReason string
	if target.RunCanaryMutant {
		candidateFiles := target.AllowedPaths
		if len(candidateFiles) == 0 {
			candidateFiles = []string{"."}
		}
		probeRunner := func(probeCtx context.Context, targetDir string) (bool, string, error) {
			pRunner := v.runner
			if pRunner == nil {
				pRunner = defaultCommandRunner(targetDir)
			}
			if strings.TrimSpace(target.HoldoutDir) != "" {
				hDir := strings.TrimSpace(target.HoldoutDir)
				if !filepath.IsAbs(hDir) && targetDir != "" {
					hDir = filepath.Join(targetDir, hDir)
				}
				var sPaths []string
				var hGoTest bool
				_ = filepath.Walk(hDir, func(p string, info os.FileInfo, err error) error {
					if err != nil || info == nil || info.IsDir() {
						return nil
					}
					low := strings.ToLower(info.Name())
					if strings.HasSuffix(low, ".sh") || strings.HasSuffix(low, ".js") || strings.HasSuffix(low, ".mjs") || strings.HasSuffix(low, ".py") {
						sPaths = append(sPaths, p)
					} else if strings.HasSuffix(low, "_test.go") {
						hGoTest = true
					}
					return nil
				})
				sort.Strings(sPaths)
				for _, sp := range sPaths {
					res := v.runScriptCheck(probeCtx, pRunner, sp)
					if !res.checkResult.OK {
						return false, fmt.Sprintf("holdout script %s caught mutant: %s", filepath.Base(sp), res.checkResult.Detail), nil
					}
				}
				if hGoTest {
					res := v.runGoTestCheck(probeCtx, pRunner, []string{hDir})
					if !res.checkResult.OK {
						return false, fmt.Sprintf("holdout go test caught mutant: %s", res.checkResult.Detail), nil
					}
				}
				return true, "all holdout checks passed", nil
			}

			// If holdout not specified, re-run class.Checks
			for _, chk := range class.Checks {
				switch chk.Type {
				case CheckTypeScript:
					res := v.runScriptCheck(probeCtx, pRunner, chk.Run)
					if !res.checkResult.OK {
						return false, fmt.Sprintf("check %s caught mutant", chk.Run), nil
					}
				case CheckTypeGoTest:
					pkgs := chk.Packages
					if len(pkgs) == 0 {
						pkgs = derivePackagesFromPaths(target.AllowedPaths)
					}
					if len(pkgs) > 0 {
						res := v.runGoTestCheck(probeCtx, pRunner, pkgs)
						if !res.checkResult.OK {
							return false, fmt.Sprintf("go test caught mutant: %s", res.checkResult.Detail), nil
						}
					}
				}
			}
			return true, "all checks passed", nil
		}

		cRes := RunCanaryCheck(ctx, repoRoot, candidateFiles, probeRunner)
		canaryResult = &cRes
		if !cRes.CanaryCaught {
			outcome = OutcomeFail
			vetoReason = VetoCanarySurvived
		}
	}

	// Persist combined raw check logs
	var rawLogPath, rawLogSHA string
	if len(checkResults) > 0 || combinedLogs.Len() > 0 {
		logDir := os.Getenv("G8S_STATE_DIR")
		if strings.TrimSpace(logDir) != "" {
			logDir = filepath.Join(logDir, "logs")
		} else {
			logDir = filepath.Join(os.TempDir(), "g8s-logs")
		}

		taskIDSlug := strings.TrimSpace(target.ID)
		if taskIDSlug == "" {
			taskIDSlug = "unknown"
		}

		logPath := filepath.Join(logDir, fmt.Sprintf("verify-%s.log", taskIDSlug))
		if err := os.MkdirAll(logDir, 0o700); err == nil {
			rawBytes := combinedLogs.Bytes()
			if err := os.WriteFile(logPath, rawBytes, 0o600); err == nil {
				sum := sha256.Sum256(rawBytes)
				rawLogPath = logPath
				rawLogSHA = hex.EncodeToString(sum[:])
			}
		}
	}

	targetGitDir := repoRoot
	var treeSHA, headSHA string
	if targetGitDir != "" {
		cmdTree := exec.CommandContext(ctx, "git", "-C", targetGitDir, "rev-parse", "HEAD^{tree}")
		if out, err := cmdTree.Output(); err == nil {
			treeSHA = strings.TrimSpace(string(out))
		}
		cmdHead := exec.CommandContext(ctx, "git", "-C", targetGitDir, "rev-parse", "HEAD")
		if out, err := cmdHead.Output(); err == nil {
			headSHA = strings.TrimSpace(string(out))
		}
	}

	// Evaluate DoD matrix
	var dodMatrix []DoDItemStatus
	if len(target.DoDItems) > 0 || len(target.ReportedDoD) > 0 {
		checksOK := (outcome == OutcomePass)
		var dodOK bool
		trustedEv := TrustedEvidence{
			TaskID:      target.ID,
			Attempt:     os.Getenv("G8S_RUN_MARKER"),
			HeadSHA:     headSHA,
			TreeSHA:     treeSHA,
			RawLogSHA:   rawLogSHA,
			WorktreeDir: target.WorktreeDir,
			RepoRoot:    repoRoot,
			Checks:      checkResults,
		}
		dodMatrix, dodOK = EvaluateDoDMatrixWithEvidence(target.DoDItems, target.ReportedDoD, checksOK, trustedEv)
		if !dodOK {
			outcome = OutcomeFail
		}
	}

	var injectedLessons []InjectedLessonHint
	if outcome == OutcomeFail || len(allAdvisories) > 0 {
		injectedLessons = v.loadInjectedLessons(class.Name, allDiagnostics, allAdvisories, checkResults, target.AllowedPaths)
	}

	verdict := Verdict{
		Class:              class.Name,
		Registered:         registered,
		Status:             class.Status,
		Outcome:            outcome,
		Checks:             checkResults,
		Advisories:         allAdvisories,
		DormantReawakened:  allDormantReawakened,
		OverrideReason:     strings.TrimSpace(target.OverrideReason),
		Scope:              registered,
		RecordedAt:         now,
		WorktreeDir:        target.WorktreeDir,
		TreeSHA:            treeSHA,
		RawLogPath:         rawLogPath,
		RawLogSHA256:       rawLogSHA,
		TestsRan:           totalTestsRan,
		TestsFailed:        totalTestsFailed,
		FailureDiagnostics: allDiagnostics,
		DoDMatrix:          dodMatrix,
		F2Summary:          loadF2TelemetrySummary(target.ID),
		InjectedLessons:    injectedLessons,
		FeatureID:          target.FeatureID,
		HoldoutChecksOK:    holdoutChecksOK,
		CanaryResult:       canaryResult,
		VetoReason:         vetoReason,
	}

	if target.GenerateBlindPacket {
		pkt := BuildBlindReviewPacket(target, repoRoot, "", combinedLogs.String(), outcome == OutcomePass, verdict.HoldoutChecksOK, checkResults)
		verdict.BlindReviewPacket = &pkt
	}

	v.emitTelemetry(target.ID, verdict)
	return verdict, nil
}
