// Package verifier implements verifier-class registry and acceptance verification (issue #515, SCORECARD S-7).
//
// The verifier is the factory's self-evaluation organ: a completed round must
// be accepted (or refused) without human eyes, with trust earned per class and
// never assumed. Classes earn hard status by citing real catches. Deny-by-default:
// unregistered classes require human acceptance.
package verifier

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tamld/g8s/internal/lessons"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/telemetry"
)

// Status indicates the enforcement mode of a verifier class.
type Status string

const (
	// StatusAdvisory indicates checks are recorded for observability but do not hard-block.
	StatusAdvisory Status = "advisory"
	// StatusHard indicates checks must pass to grant automated acceptance.
	StatusHard Status = "hard"
)

// String returns the string representation of Status.
func (s Status) String() string {
	return string(s)
}

// Outcome represents the overall evaluation result of a verification run.
type Outcome string

const (
	// OutcomePass indicates all checks passed successfully.
	OutcomePass Outcome = "pass"
	// OutcomeFail indicates one or more checks failed.
	OutcomeFail Outcome = "fail"
	// OutcomeNotRun indicates an infrastructure runner error occurred (fail-open rule).
	OutcomeNotRun Outcome = "not-run"
	// OutcomeUnregistered indicates no registered class matched the task paths.
	OutcomeUnregistered Outcome = "unregistered"
)

// String returns the string representation of Outcome.
func (o Outcome) String() string {
	return string(o)
}

// CheckType represents the type of check to execute.
type CheckType string

const (
	// CheckTypeScript runs a tracked repository script.
	CheckTypeScript CheckType = "script"
	// CheckTypeGoTest runs 'go test -count=1' on target packages.
	CheckTypeGoTest CheckType = "go-test"
)

// String returns the string representation of CheckType.
func (c CheckType) String() string {
	return string(c)
}

// Check defines an individual check within a verifier class.
type Check struct {
	Type     CheckType `json:"type" yaml:"type"`
	Run      string    `json:"run,omitempty" yaml:"run,omitempty"`
	Packages []string  `json:"packages,omitempty" yaml:"packages,omitempty"`
}

// Class represents a registered verifier class entry.
type Class struct {
	Name          string   `json:"name" yaml:"name"`
	Status        Status   `json:"status" yaml:"status"`
	FoundingCatch string   `json:"founding_catch" yaml:"founding_catch"`
	Priority      int      `json:"priority" yaml:"priority"`
	Paths         []string `json:"paths" yaml:"paths"`
	Checks        []Check  `json:"checks" yaml:"checks"`
}

// BriefDoDItem represents a single Definition of Done requirement from BriefSchema.
type BriefDoDItem struct {
	ID           string `json:"id"`
	Description  string `json:"description"`
	VerifierHint string `json:"verifier_hint,omitempty"`
}

// DoDItemStatus represents the evaluation result of a DoD item in RoundReceiptSchema.
type DoDItemStatus struct {
	ID       string `json:"id"`
	Status   string `json:"status"` // "MET" | "UNMET" | "PARTIAL"
	Evidence string `json:"evidence"`
}

// FailureDiagnostic captures lossless, untruncated failure details for a failed test/check.
type FailureDiagnostic struct {
	Target  string `json:"target"`
	Test    string `json:"test"`
	Summary string `json:"summary"`
}

// TaskRef references a task and its write delegation scope.
type TaskRef struct {
	ID             string          `json:"id"`
	ReceiptID      string          `json:"receipt_id,omitempty"`
	AllowedPaths   []string        `json:"allowed_paths,omitempty"`
	OverrideReason string          `json:"override_reason,omitempty"`
	WorktreeDir    string          `json:"worktree_dir,omitempty"`
	DoDItems       []BriefDoDItem  `json:"dod_items,omitempty"`
	ReportedDoD    []DoDItemStatus `json:"reported_dod,omitempty"`
}

// CheckResult represents the outcome of a single check execution.
type CheckResult struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// F2TelemetrySummary aggregates ADR-0032 depth-2 (F1 -> F2) subagent invocations
// into an observational telemetry digest without polluting F0's context window (#643).
// Note: This is observational runtime telemetry (with SHA-256 over the local JSONL log
// for drill-down auditability), not a cryptographic attestation of upstream model execution.
type F2TelemetrySummary struct {
	TotalCalls      int      `json:"total_calls"`
	ModelsUsed      []string `json:"models_used,omitempty"`
	TotalTokens     int      `json:"total_tokens"`
	TotalDurationMs int64    `json:"total_duration_ms"`
	TelemetrySHA256 string   `json:"telemetry_sha256,omitempty"`
}

// InjectedLessonHint surfaces a ratified retrospective lesson at the point of
// verification failure or advisory trigger when its failure context matches (#644).
type InjectedLessonHint struct {
	ID              string `json:"id"`
	AuthorClass     string `json:"author_class,omitempty"`
	MatchedSignal   string `json:"matched_signal,omitempty"`
	WrongApproach   string `json:"wrong_approach"`
	CorrectApproach string `json:"correct_approach"`
}

// Verdict encapsulates the complete machine acceptance decision.
type Verdict struct {
	Class              string               `json:"class"`
	Registered         bool                 `json:"registered"`
	Status             Status               `json:"status"`
	Outcome            Outcome              `json:"outcome"`
	Checks             []CheckResult        `json:"checks"`
	Advisories         []DiffAdvisory       `json:"advisories,omitempty"`
	DormantReawakened  []string             `json:"dormant_reawakened,omitempty"`
	OverrideReason     string               `json:"override_reason,omitempty"`
	Scope              bool                 `json:"scope"`
	RecordedAt         time.Time            `json:"recorded_at"`
	WorktreeDir        string               `json:"worktree_dir,omitempty"`
	TreeSHA            string               `json:"tree_sha,omitempty"`
	RawLogPath         string               `json:"raw_log_path,omitempty"`
	RawLogSHA256       string               `json:"raw_log_sha256,omitempty"`
	TestsRan           int                  `json:"tests_ran"`
	TestsFailed        int                  `json:"tests_failed"`
	FailureDiagnostics []FailureDiagnostic  `json:"failure_diagnostics,omitempty"`
	DoDMatrix          []DoDItemStatus      `json:"dod_matrix,omitempty"`
	F2Summary          *F2TelemetrySummary  `json:"f2_summary,omitempty"`
	InjectedLessons    []InjectedLessonHint `json:"injected_lessons,omitempty"`
}

// DefaultRegistryPath is the default location for the verifier classes configuration.
const DefaultRegistryPath = ".g8s/verifier-classes.yml"

// ErrSelfGrade is returned when a caller attempts to grade its own task class.
var ErrSelfGrade = errors.New("no verifier grades its own class")

// SelfGradeError provides typed error details for self-grade guard violations.
type SelfGradeError struct {
	TargetID string
	CallerID string
}

func (e *SelfGradeError) Error() string {
	return "no verifier grades its own class"
}

func (e *SelfGradeError) Is(target error) bool {
	return target == ErrSelfGrade
}

// ⚡ Bolt Optimization: Hoist dynamic regexp.MustCompile calls to package level for zero-allocation processing.
var (
	// Citation regex patterns: exactly #<digits> or incident:<slug>
	citationIssueTokenRegex    = regexp.MustCompile(`^#\d+$`)
	citationIncidentTokenRegex = regexp.MustCompile(`^incident:[a-z0-9][a-z0-9_-]*$`)

	// Go test failure extraction patterns
	goTestPassRegex = regexp.MustCompile(`^(?:\s*)--- PASS:\s*(\S+)`)
	goTestFailRegex = regexp.MustCompile(`^(?:\s*)--- FAIL:\s*(\S+)`)
	goTestRunRegex  = regexp.MustCompile(`^=== RUN\s+(\S+)`)

	// Brief DoD item extraction patterns
	dodItemRegex     = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+(?:\[[ xX]\]\s+)?)?\*{0,2}(D\d+|DoD-\d+)\*{0,2}(?:[:\-–—\s]+(.*))?$`)
	dodFallbackRegex = regexp.MustCompile(`(?i)\b(D\d+|DoD-\d+)\b[:\-–—\s]*(.*)`)
)

// isValidCitationToken reports whether a single token is a valid citation.
func isValidCitationToken(tok string) bool {
	return citationIssueTokenRegex.MatchString(tok) || citationIncidentTokenRegex.MatchString(tok)
}

// isValidCitation reports whether a founding_catch citation string is valid.
// After trimming, the string must be exactly one citation token (^#\d+$ or
// ^incident:[a-z0-9][a-z0-9_-]*$), optionally followed by ONE space and
// free-text rationale. Two citation tokens, leading/trailing junk, or "#"
// alone are refused.
func isValidCitation(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}

	idx := strings.IndexByte(trimmed, ' ')
	if idx == -1 {
		return isValidCitationToken(trimmed)
	}

	citation := trimmed[:idx]
	if !isValidCitationToken(citation) {
		return false
	}

	// Must be followed by ONE space and non-empty free-text rationale
	rest := trimmed[idx+1:]
	if len(rest) == 0 || rest[0] == ' ' {
		return false
	}

	// Rationale cannot start with another citation or citation prefix
	if strings.HasPrefix(rest, "#") || (len(rest) >= 9 && strings.EqualFold(rest[:9], "incident:")) {
		return false
	}

	// Two citation tokens: rationale cannot contain any bare citation token
	for _, word := range strings.Fields(rest) {
		if isValidCitationToken(word) {
			return false
		}
	}

	return true
}

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
	if (target.ReceiptID == "" || len(target.AllowedPaths) == 0) && target.WorktreeDir == "" && len(target.DoDItems) == 0 {
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

	if !registered && target.WorktreeDir == "" && len(target.DoDItems) == 0 {
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

	// Evaluate DoD matrix
	var dodMatrix []DoDItemStatus
	if len(target.DoDItems) > 0 || len(target.ReportedDoD) > 0 {
		checksOK := (outcome == OutcomePass)
		var dodOK bool
		dodMatrix, dodOK = EvaluateDoDMatrix(target.DoDItems, target.ReportedDoD, checksOK, rawLogSHA)
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
		RawLogPath:         rawLogPath,
		RawLogSHA256:       rawLogSHA,
		TestsRan:           totalTestsRan,
		TestsFailed:        totalTestsFailed,
		FailureDiagnostics: allDiagnostics,
		DoDMatrix:          dodMatrix,
		F2Summary:          loadF2TelemetrySummary(target.ID),
		InjectedLessons:    injectedLessons,
	}

	targetGitDir := repoRoot
	if targetGitDir != "" {
		cmd := exec.CommandContext(ctx, "git", "-C", targetGitDir, "rev-parse", "HEAD^{tree}")
		if out, err := cmd.Output(); err == nil {
			verdict.TreeSHA = strings.TrimSpace(string(out))
		}
	}

	v.emitTelemetry(target.ID, verdict)
	return verdict, nil
}

func loadF2TelemetrySummary(taskID string) *F2TelemetrySummary {
	safeTaskID := filepath.Base(strings.TrimSpace(taskID))
	if safeTaskID == "" || safeTaskID == "." || safeTaskID == ".." || strings.ContainsAny(safeTaskID, `/\`) {
		return nil
	}
	stateDir := pathutil.DefaultStateDir()
	fPath := filepath.Join(stateDir, "f2_telemetry", safeTaskID+".jsonl")
	data, err := os.ReadFile(fPath)
	if err != nil {
		return nil
	}

	var summary F2TelemetrySummary
	modelSet := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec struct {
			ModelRequested string `json:"model_requested"`
			ModelResolved  string `json:"model_resolved"`
			DurationMs     int64  `json:"duration_ms"`
			TotalTokens    int    `json:"total_tokens"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		summary.TotalCalls++
		summary.TotalTokens += rec.TotalTokens
		summary.TotalDurationMs += rec.DurationMs
		m := strings.TrimSpace(rec.ModelResolved)
		if m == "" {
			m = strings.TrimSpace(rec.ModelRequested)
		}
		if m != "" {
			modelSet[m] = struct{}{}
		}
	}
	if summary.TotalCalls == 0 {
		return nil
	}
	for m := range modelSet {
		summary.ModelsUsed = append(summary.ModelsUsed, m)
	}
	sort.Strings(summary.ModelsUsed)
	sum := sha256.Sum256(data)
	summary.TelemetrySHA256 = hex.EncodeToString(sum[:])
	return &summary
}

func extractFailureSignals(diagnostics []FailureDiagnostic, advisories []DiffAdvisory, checks []CheckResult, allowedPaths []string) []string {
	seen := make(map[string]struct{})
	var signals []string
	addToken := func(tok string) {
		tok = strings.ToLower(strings.Trim(tok, " \t\r\n.,:;()[]{}\"'`"))
		if len(tok) < 3 {
			return
		}
		switch tok {
		case "the", "and", "for", "with", "from", "that", "this", "fail", "failed", "error", "test", "exit", "code":
			return
		}
		if _, ok := seen[tok]; !ok {
			seen[tok] = struct{}{}
			signals = append(signals, tok)
		}
	}

	for _, adv := range advisories {
		addToken(adv.RuleID)
		addToken(filepath.Base(adv.File))
		for _, w := range strings.Fields(adv.Message) {
			addToken(w)
		}
	}
	for _, diag := range diagnostics {
		addToken(diag.Test)
		addToken(diag.Target)
		for _, w := range strings.Fields(diag.Summary) {
			addToken(w)
		}
	}
	for _, chk := range checks {
		if !chk.OK {
			addToken(chk.Type)
			addToken(chk.Target)
			for _, w := range strings.Fields(chk.Detail) {
				addToken(w)
			}
		}
	}
	for _, p := range allowedPaths {
		addToken(filepath.Base(p))
		addToken(filepath.Base(filepath.Dir(p)))
	}
	return signals
}

func (v *Verifier) loadInjectedLessons(className string, diagnostics []FailureDiagnostic, advisories []DiffAdvisory, checks []CheckResult, allowedPaths []string) []InjectedLessonHint {
	ledgerPath := strings.TrimSpace(v.lessonsLedgerPath)
	if ledgerPath == "" {
		ledgerPath = strings.TrimSpace(os.Getenv("G8S_LESSONS_LEDGER"))
	}
	if ledgerPath == "" {
		if v.repoRoot != "" {
			ledgerPath = filepath.Join(v.repoRoot, "docs", "lessons", "ledger.jsonl")
		} else {
			ledgerPath = filepath.Join("docs", "lessons", "ledger.jsonl")
		}
	}

	items, err := lessons.NewLedger(ledgerPath).Load()
	if err != nil || len(items) == 0 {
		return nil
	}

	signals := extractFailureSignals(diagnostics, advisories, checks, allowedPaths)
	if len(signals) == 0 {
		return nil
	}

	type scoredHint struct {
		hint  InjectedLessonHint
		score int
	}
	var scored []scoredHint

	for _, item := range items {
		if item.Status != lessons.StatusRatified {
			continue
		}
		matched := len(item.ReviewedClasses) == 0 || strings.EqualFold(item.AuthorClass, className)
		if !matched {
			for _, rc := range item.ReviewedClasses {
				if rc == "*" || strings.EqualFold(rc, className) {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		wrong := strings.TrimSpace(item.Observation.Text)
		if wrong == "" && len(item.Observation.CitedEvents) > 0 {
			wrong = strings.TrimSpace(item.Observation.CitedEvents[0].Claim)
		}
		correct := strings.TrimSpace(item.Recommendation)
		if wrong == "" || correct == "" {
			continue
		}

		corpus := strings.ToLower(wrong + " " + correct)
		for _, ce := range item.Observation.CitedEvents {
			corpus += " " + strings.ToLower(ce.Claim) + " " + strings.ToLower(ce.EventType)
		}

		score := 0
		var firstSignal string
		for _, sig := range signals {
			if strings.Contains(corpus, sig) {
				score++
				if firstSignal == "" {
					firstSignal = sig
				}
			}
		}
		if score == 0 {
			continue
		}

		scored = append(scored, scoredHint{
			score: score,
			hint: InjectedLessonHint{
				ID:              item.ID,
				AuthorClass:     item.AuthorClass,
				MatchedSignal:   firstSignal,
				WrongApproach:   wrong,
				CorrectApproach: correct,
			},
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	var hints []InjectedLessonHint
	for _, s := range scored {
		hints = append(hints, s.hint)
		if len(hints) >= lessons.DefaultRoundBudget {
			break
		}
	}
	return hints
}

type scriptCheckResult struct {
	checkResult CheckResult
	stdout      []byte
	stderr      []byte
	diagnostic  *FailureDiagnostic
}

func (v *Verifier) runScriptCheck(ctx context.Context, runner CommandRunner, scriptPath string) scriptCheckResult {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Check if script exists if repoRoot is set
	resolvedPath := scriptPath
	if v.repoRoot != "" && !filepath.IsAbs(scriptPath) {
		resolvedPath = filepath.Join(v.repoRoot, scriptPath)
	}

	if _, err := os.Stat(resolvedPath); err != nil {
		return scriptCheckResult{
			checkResult: CheckResult{
				Type:   string(CheckTypeScript),
				Target: scriptPath,
				OK:     false,
				Detail: fmt.Sprintf("runner error: script file not found: %v", err),
			},
		}
	}

	stdout, stderr, exitCode, err := runner(timeoutCtx, "bash", scriptPath)
	if err != nil {
		return scriptCheckResult{
			checkResult: CheckResult{
				Type:   string(CheckTypeScript),
				Target: scriptPath,
				OK:     false,
				Detail: fmt.Sprintf("runner error: %v", err),
			},
			stdout: stdout,
			stderr: stderr,
		}
	}

	if exitCode != 0 {
		detail := fmt.Sprintf("exit code %d", exitCode)
		if trimmed := strings.TrimSpace(string(stderr)); trimmed != "" {
			detail += ": " + trimmed
		} else if trimmed := strings.TrimSpace(string(stdout)); trimmed != "" {
			detail += ": " + trimmed
		}
		var diag *FailureDiagnostic
		summary := strings.TrimSpace(string(stdout) + "\n" + string(stderr))
		if summary != "" {
			diag = &FailureDiagnostic{
				Target:  scriptPath,
				Test:    filepath.Base(scriptPath),
				Summary: summary,
			}
		}
		return scriptCheckResult{
			checkResult: CheckResult{
				Type:   string(CheckTypeScript),
				Target: scriptPath,
				OK:     false,
				Detail: detail,
			},
			stdout:     stdout,
			stderr:     stderr,
			diagnostic: diag,
		}
	}

	return scriptCheckResult{
		checkResult: CheckResult{
			Type:   string(CheckTypeScript),
			Target: scriptPath,
			OK:     true,
			Detail: "ok",
		},
		stdout: stdout,
		stderr: stderr,
	}
}

type goTestCheckResult struct {
	checkResult CheckResult
	stdout      []byte
	stderr      []byte
	testsRan    int
	testsFailed int
	diagnostics []FailureDiagnostic
}

func (v *Verifier) runGoTestCheck(ctx context.Context, runner CommandRunner, pkgs []string) goTestCheckResult {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	targetStr := strings.Join(pkgs, " ")
	args := append([]string{"test", "-count=1"}, pkgs...)
	stdout, stderr, exitCode, err := runner(timeoutCtx, "go", args...)
	if err != nil {
		return goTestCheckResult{
			checkResult: CheckResult{
				Type:   string(CheckTypeGoTest),
				Target: targetStr,
				OK:     false,
				Detail: fmt.Sprintf("runner error: %v", err),
			},
			stdout: stdout,
			stderr: stderr,
		}
	}

	testsRan, testsFailed, isVacuous := countGoTests(stdout, exitCode)
	if isVacuous {
		return goTestCheckResult{
			checkResult: CheckResult{
				Type:   string(CheckTypeGoTest),
				Target: targetStr,
				OK:     false,
				Detail: "vacuous pass: 0 tests ran",
			},
			stdout:      stdout,
			stderr:      stderr,
			testsRan:    0,
			testsFailed: 0,
		}
	}

	if exitCode != 0 {
		diagnostics := extractGoTestFailures(targetStr, stdout, stderr)
		detail := fmt.Sprintf("exit code %d", exitCode)
		if len(diagnostics) > 0 {
			var names []string
			for _, d := range diagnostics {
				names = append(names, d.Test)
			}
			detail = fmt.Sprintf("exit code %d: %d test(s) failed (%s)", exitCode, len(diagnostics), strings.Join(names, ", "))
		} else if trimmed := strings.TrimSpace(string(stderr)); trimmed != "" {
			detail += ": " + trimmed
		} else if trimmed := strings.TrimSpace(string(stdout)); trimmed != "" {
			detail += ": " + trimmed
		}

		if testsFailed == 0 && len(diagnostics) > 0 {
			testsFailed = len(diagnostics)
		}
		if testsRan < testsFailed {
			testsRan = testsFailed
		}

		return goTestCheckResult{
			checkResult: CheckResult{
				Type:   string(CheckTypeGoTest),
				Target: targetStr,
				OK:     false,
				Detail: detail,
			},
			stdout:      stdout,
			stderr:      stderr,
			testsRan:    testsRan,
			testsFailed: testsFailed,
			diagnostics: diagnostics,
		}
	}

	return goTestCheckResult{
		checkResult: CheckResult{
			Type:   string(CheckTypeGoTest),
			Target: targetStr,
			OK:     true,
			Detail: "ok",
		},
		stdout:      stdout,
		stderr:      stderr,
		testsRan:    testsRan,
		testsFailed: 0,
	}
}

// countGoTests parses stdout of go test to determine tests ran, failed, and whether execution was vacuous.
func countGoTests(stdout []byte, exitCode int) (ran int, failed int, isVacuous bool) {
	var passCount, failCount, okCount int
	var hasVacuousMarker bool

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Check for JSON go test event
		if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
			var event struct {
				Action string `json:"Action"`
				Test   string `json:"Test"`
			}
			if err := json.Unmarshal([]byte(trimmed), &event); err == nil && event.Test != "" {
				switch event.Action {
				case "pass":
					passCount++
				case "fail":
					failCount++
				}
				continue
			}
		}

		if goTestPassRegex.MatchString(trimmed) {
			passCount++
		} else if goTestFailRegex.MatchString(trimmed) {
			failCount++
		} else {
			if strings.HasPrefix(trimmed, "ok\t") || strings.HasPrefix(trimmed, "ok ") || trimmed == "ok" {
				if strings.Contains(trimmed, "[no tests to run]") || strings.Contains(trimmed, "[no test files]") {
					hasVacuousMarker = true
				} else {
					okCount++
				}
			}
		}

		if strings.Contains(trimmed, "[no tests to run]") ||
			strings.Contains(trimmed, "[no test files]") ||
			strings.Contains(trimmed, "testing: warning: no tests to run") {
			hasVacuousMarker = true
		}
	}

	if passCount > 0 || failCount > 0 {
		ran = passCount + failCount
		failed = failCount
	} else if okCount > 0 {
		ran = okCount
		failed = 0
	}

	if (hasVacuousMarker && (passCount == 0 && okCount == 0)) || (exitCode == 0 && ran == 0) {
		ran = 0
		failed = 0
		isVacuous = true
	}

	return ran, failed, isVacuous
}

// extractGoTestFailures extracts lossless failure diagnostics from go test stdout/stderr.
// Strips === RUN and --- PASS mechanical noise lines, preserving file:line diagnostics.
func extractGoTestFailures(targetStr string, stdout, stderr []byte) []FailureDiagnostic {
	var diagnostics []FailureDiagnostic
	diagMap := make(map[string]*FailureDiagnostic)

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	var currentRunTest string
	var currentRunLines []string
	var activeFailTest string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if m := goTestRunRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			currentRunTest = m[1]
			currentRunLines = nil
			activeFailTest = ""
			continue
		}

		if m := goTestPassRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			currentRunTest = ""
			currentRunLines = nil
			activeFailTest = ""
			continue
		}

		if m := goTestFailRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			testName := m[1]
			activeFailTest = testName
			var lines []string
			lines = append(lines, line)
			if currentRunTest == testName && len(currentRunLines) > 0 {
				lines = append(lines, currentRunLines...)
			}

			if existing, ok := diagMap[testName]; ok {
				existing.Summary += "\n" + strings.Join(lines, "\n")
			} else {
				diag := &FailureDiagnostic{
					Target:  targetStr,
					Test:    testName,
					Summary: strings.Join(lines, "\n"),
				}
				diagMap[testName] = diag
				diagnostics = append(diagnostics, *diag)
			}
			currentRunTest = ""
			currentRunLines = nil
			continue
		}

		// Indented lines
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, " ") {
			if activeFailTest != "" {
				for i := range diagnostics {
					if diagnostics[i].Test == activeFailTest {
						diagnostics[i].Summary += "\n" + line
						break
					}
				}
			} else if currentRunTest != "" {
				currentRunLines = append(currentRunLines, line)
			}
			continue
		}

		// Non-indented lines
		if activeFailTest != "" {
			if strings.HasPrefix(line, "panic:") || strings.HasPrefix(line, "goroutine ") {
				for i := range diagnostics {
					if diagnostics[i].Test == activeFailTest {
						diagnostics[i].Summary += "\n" + line
						break
					}
				}
				continue
			}
			activeFailTest = ""
		}
	}

	var stderrLines []string
	if len(stderr) > 0 {
		errScanner := bufio.NewScanner(bytes.NewReader(stderr))
		for errScanner.Scan() {
			t := strings.TrimSpace(errScanner.Text())
			if t != "" {
				stderrLines = append(stderrLines, errScanner.Text())
			}
		}
	}

	if len(stderrLines) > 0 {
		diagnostics = append(diagnostics, FailureDiagnostic{
			Target:  targetStr,
			Test:    "build",
			Summary: strings.Join(stderrLines, "\n"),
		})
	} else if len(diagnostics) == 0 {
		var errLines []string
		outScanner := bufio.NewScanner(bytes.NewReader(stdout))
		for outScanner.Scan() {
			l := outScanner.Text()
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "=== RUN") || strings.HasPrefix(t, "--- PASS") {
				continue
			}
			errLines = append(errLines, l)
		}
		if len(errLines) > 0 {
			diagnostics = append(diagnostics, FailureDiagnostic{
				Target:  targetStr,
				Test:    "build",
				Summary: strings.Join(errLines, "\n"),
			})
		}
	}

	return diagnostics
}

// ParseBriefDoDItems extracts BriefDoDItems from json or markdown DoD content.
func ParseBriefDoDItems(dodMD string) []BriefDoDItem {
	trimmed := strings.TrimSpace(dodMD)
	if trimmed == "" {
		return nil
	}

	// Try JSON first
	if strings.HasPrefix(trimmed, "[") {
		var jsonItems []BriefDoDItem
		if err := json.Unmarshal([]byte(trimmed), &jsonItems); err == nil && len(jsonItems) > 0 {
			return jsonItems
		}
	}

	var items []BriefDoDItem
	seen := make(map[string]bool)

	// Markdown patterns matching (D\d+|DoD-\d+)

	scanner := bufio.NewScanner(strings.NewReader(dodMD))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var id, desc string
		if m := dodItemRegex.FindStringSubmatch(line); len(m) > 1 {
			id = strings.TrimSpace(m[1])
			if len(m) > 2 {
				desc = strings.TrimSpace(m[2])
			}
		} else if m := dodFallbackRegex.FindStringSubmatch(line); len(m) > 1 {
			id = strings.TrimSpace(m[1])
			if len(m) > 2 {
				desc = strings.TrimSpace(m[2])
			}
		}
		if id != "" {
			normID := strings.ToUpper(id)
			if !seen[normID] {
				seen[normID] = true
				desc = strings.Trim(desc, "*_` \t")
				items = append(items, BriefDoDItem{
					ID:          id,
					Description: desc,
				})
			}
		}
	}
	return items
}

// EvaluateDoDMatrix evaluates 1-to-1 coverage of items against reported statuses.
func EvaluateDoDMatrix(items []BriefDoDItem, reported []DoDItemStatus, checksOK bool, rawLogSHA string) ([]DoDItemStatus, bool) {
	if len(items) == 0 {
		if len(reported) == 0 {
			return nil, true
		}
		allMET := true
		for _, r := range reported {
			if r.Status != "MET" || strings.TrimSpace(r.Evidence) == "" {
				allMET = false
			}
		}
		return reported, allMET
	}

	if len(reported) == 0 {
		matrix := make([]DoDItemStatus, 0, len(items))
		for _, item := range items {
			if checksOK {
				matrix = append(matrix, DoDItemStatus{
					ID:       item.ID,
					Status:   "MET",
					Evidence: fmt.Sprintf("log sha256: %s", rawLogSHA),
				})
			} else {
				matrix = append(matrix, DoDItemStatus{
					ID:       item.ID,
					Status:   "UNMET",
					Evidence: "checks failed",
				})
			}
		}
		return matrix, checksOK
	}

	// Reported is non-empty: verify 1-to-1 coverage of every item in items
	repMap := make(map[string]DoDItemStatus, len(reported))
	for _, r := range reported {
		repMap[strings.ToUpper(strings.TrimSpace(r.ID))] = r
	}

	allOK := true
	matrix := make([]DoDItemStatus, 0, len(items))
	for _, item := range items {
		normID := strings.ToUpper(strings.TrimSpace(item.ID))
		if r, ok := repMap[normID]; ok {
			if r.Status != "MET" || strings.TrimSpace(r.Evidence) == "" {
				allOK = false
			}
			matrix = append(matrix, r)
		} else {
			allOK = false
			matrix = append(matrix, DoDItemStatus{
				ID:       item.ID,
				Status:   "UNMET",
				Evidence: "missing from reported DoD matrix",
			})
		}
	}

	return matrix, allOK
}

func (v *Verifier) emitTelemetry(taskID string, verdict Verdict) {
	if v.telemetrySink != nil {
		v.telemetrySink(taskID, verdict)
		return
	}

	cfg := telemetry.DefaultTelemetryConfig()
	if p := os.Getenv("G8S_TELEMETRY_DB"); p != "" {
		cfg.DBPath = p
	}
	eng, err := telemetry.NewTelemetryEngine(cfg)
	if err != nil {
		return
	}
	defer eng.Close()

	ev := telemetry.TraceEvent{
		TaskID:    taskID,
		EventType: telemetry.TraceEventVerifierVerdict,
		Timestamp: verdict.RecordedAt,
		Payload: map[string]any{
			"class":      verdict.Class,
			"status":     string(verdict.Status),
			"outcome":    string(verdict.Outcome),
			"checks_run": len(verdict.Checks),
		},
	}
	if err := eng.IngestEvent(context.Background(), ev); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] verifier telemetry ingest: %v\n", err)
	}
}

// derivePackagesFromPaths extracts Go package import paths from target paths.
// Rule: prefers non-test Go source files when present; falls back to test files
// if only test files were modified.
func derivePackagesFromPaths(paths []string) []string {
	var nonTestGoFiles []string
	var testGoFiles []string

	for _, p := range paths {
		clean := filepath.ToSlash(filepath.Clean(p))
		if !strings.HasSuffix(clean, ".go") {
			continue
		}
		if strings.HasSuffix(clean, "_test.go") {
			testGoFiles = append(testGoFiles, clean)
		} else {
			nonTestGoFiles = append(nonTestGoFiles, clean)
		}
	}

	selected := nonTestGoFiles
	if len(selected) == 0 {
		selected = testGoFiles
	}
	if len(selected) == 0 {
		return nil
	}

	pkgMap := make(map[string]bool)
	for _, f := range selected {
		dir := filepath.Dir(f)
		dir = filepath.ToSlash(filepath.Clean(dir))
		var pkg string
		if dir == "." || dir == "" {
			pkg = "."
		} else if !strings.HasPrefix(dir, "./") {
			pkg = "./" + dir
		} else {
			pkg = dir
		}
		pkgMap[pkg] = true
	}

	var res []string
	for pkg := range pkgMap {
		res = append(res, pkg)
	}
	sort.Strings(res)
	return res
}

// Package-level default verifier.
var defaultVerifier = NewVerifier()

// Verify evaluates acceptance checks using the default verifier.
func Verify(target TaskRef, caller TaskRef) (Verdict, error) {
	return defaultVerifier.Verify(target, caller)
}

// Resolve matches write-scope paths against the default registry.
func Resolve(paths []string) (Class, bool) {
	if defaultVerifier == nil || defaultVerifier.registry == nil {
		return Class{Name: "unregistered", Status: StatusAdvisory}, false
	}
	return defaultVerifier.registry.Resolve(paths)
}

// LoadRegistry parses and validates the verifier classes configuration file.
// If the file is missing or invalid, an empty/error Registry is returned.
func LoadRegistry(relPath string) (*Registry, error) {
	path, err := findFile(relPath)
	if err != nil {
		return &Registry{err: err}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &Registry{err: err}, err
	}
	return ParseRegistry(data)
}

// ParseRegistry parses verifier classes YAML data using pure Go stdlib.
// Enforces fail-closed rules:
//   - status: hard without a valid citation (#<digits> or incident:<slug>) -> entry treated as UNREGISTERED
//   - duplicate names -> refused
//   - unknown check type -> entry unregistered
//   - malformed YAML -> error returned, classes unregistered, never panics
func ParseRegistry(data []byte) (*Registry, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	reg := &Registry{
		classes: make([]Class, 0),
	}

	type rawCheck struct {
		typ      string
		run      string
		packages []string
		hasPkgs  bool
	}

	type rawClass struct {
		name          string
		status        string
		foundingCatch string
		priority      int
		paths         []string
		checks        []rawCheck
	}

	var rawClasses []rawClass
	var currentClass *rawClass
	var currentCheck *rawCheck

	inClassesSection := false
	inPathsSection := false
	inChecksSection := false
	inPackagesSection := false

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		rawLine := scanner.Text()
		line := stripYAMLComment(rawLine)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		leadingSpaces := len(line) - len(strings.TrimLeft(line, " "))

		// Top-level key: "classes:"
		if leadingSpaces == 0 {
			if trimmed == "classes:" {
				inClassesSection = true
				inPathsSection = false
				inChecksSection = false
				inPackagesSection = false
				continue
			}
			// Another top-level section: end classes
			inClassesSection = false
			inPathsSection = false
			inChecksSection = false
			inPackagesSection = false
			continue
		}

		if !inClassesSection {
			continue
		}

		// A class item starts with "- " at indent 2 (or 1..3 spaces)
		if leadingSpaces >= 1 && leadingSpaces <= 3 && strings.HasPrefix(trimmed, "- ") {
			if currentCheck != nil && currentClass != nil {
				currentClass.checks = append(currentClass.checks, *currentCheck)
				currentCheck = nil
			}
			if currentClass != nil {
				rawClasses = append(rawClasses, *currentClass)
			}
			currentClass = &rawClass{
				priority: 100, // default priority
			}
			inPathsSection = false
			inChecksSection = false
			inPackagesSection = false

			itemContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if strings.HasPrefix(itemContent, "name:") {
				val := strings.TrimPrefix(itemContent, "name:")
				currentClass.name = cleanYAMLValue(val)
			}
			continue
		}

		if currentClass == nil {
			return &Registry{err: fmt.Errorf("unexpected content outside class at line %d: %q", lineNum, trimmed)}, fmt.Errorf("unexpected content outside class at line %d", lineNum)
		}

		// Inside paths list
		if inPathsSection {
			if strings.HasPrefix(trimmed, "- ") {
				p := cleanYAMLValue(strings.TrimPrefix(trimmed, "- "))
				currentClass.paths = append(currentClass.paths, p)
				continue
			}
			inPathsSection = false
		}

		// Inside packages list for go-test check
		if inPackagesSection {
			if strings.HasPrefix(trimmed, "- ") {
				pkg := cleanYAMLValue(strings.TrimPrefix(trimmed, "- "))
				if currentCheck != nil {
					currentCheck.packages = append(currentCheck.packages, pkg)
				}
				continue
			}
			inPackagesSection = false
		}

		// Check item starts with "- " under checks:
		if inChecksSection {
			if strings.HasPrefix(trimmed, "- ") {
				if currentCheck != nil {
					currentClass.checks = append(currentClass.checks, *currentCheck)
				}
				currentCheck = &rawCheck{}
				inPackagesSection = false

				itemContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if strings.HasPrefix(itemContent, "type:") {
					val := strings.TrimPrefix(itemContent, "type:")
					currentCheck.typ = cleanYAMLValue(val)
				}
				continue
			}

			if currentCheck != nil {
				if strings.HasPrefix(trimmed, "type:") {
					val := strings.TrimPrefix(trimmed, "type:")
					currentCheck.typ = cleanYAMLValue(val)
					continue
				}
				if strings.HasPrefix(trimmed, "run:") {
					val := strings.TrimPrefix(trimmed, "run:")
					currentCheck.run = cleanYAMLValue(val)
					continue
				}
				if strings.HasPrefix(trimmed, "packages:") {
					val := strings.TrimSpace(strings.TrimPrefix(trimmed, "packages:"))
					currentCheck.hasPkgs = true
					if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
						inner := strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
						for _, it := range strings.Split(inner, ",") {
							c := cleanYAMLValue(it)
							if c != "" {
								currentCheck.packages = append(currentCheck.packages, c)
							}
						}
						inPackagesSection = false
					} else {
						inPackagesSection = true
					}
					continue
				}
			}

			// If line doesn't match check fields and doesn't start with "- ", exit checks section
			if !strings.HasPrefix(trimmed, "type:") && !strings.HasPrefix(trimmed, "run:") && !strings.HasPrefix(trimmed, "packages:") {
				inChecksSection = false
				if currentCheck != nil {
					currentClass.checks = append(currentClass.checks, *currentCheck)
					currentCheck = nil
				}
			}
		}

		// Class scalar fields
		if strings.HasPrefix(trimmed, "paths:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "paths:"))
			if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				inner := strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
				for _, it := range strings.Split(inner, ",") {
					c := cleanYAMLValue(it)
					if c != "" {
						currentClass.paths = append(currentClass.paths, c)
					}
				}
				inPathsSection = false
			} else {
				inPathsSection = true
			}
			inChecksSection = false
			inPackagesSection = false
			continue
		}

		if strings.HasPrefix(trimmed, "checks:") {
			inChecksSection = true
			inPathsSection = false
			inPackagesSection = false
			continue
		}

		if strings.HasPrefix(trimmed, "name:") {
			val := strings.TrimPrefix(trimmed, "name:")
			currentClass.name = cleanYAMLValue(val)
			continue
		}

		if strings.HasPrefix(trimmed, "status:") {
			val := strings.TrimPrefix(trimmed, "status:")
			currentClass.status = cleanYAMLValue(val)
			continue
		}

		if strings.HasPrefix(trimmed, "founding_catch:") {
			val := strings.TrimPrefix(trimmed, "founding_catch:")
			currentClass.foundingCatch = cleanYAMLValue(val)
			continue
		}

		if strings.HasPrefix(trimmed, "priority:") {
			val := strings.TrimPrefix(trimmed, "priority:")
			p, err := strconv.Atoi(cleanYAMLValue(val))
			if err == nil {
				currentClass.priority = p
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		reg.err = err
		return reg, err
	}

	if currentCheck != nil && currentClass != nil {
		currentClass.checks = append(currentClass.checks, *currentCheck)
	}
	if currentClass != nil {
		rawClasses = append(rawClasses, *currentClass)
	}

	// Validate classes and enforce fail-closed rules
	nameCounts := make(map[string]int)
	for _, rc := range rawClasses {
		if rc.name != "" {
			nameCounts[rc.name]++
		}
	}

	for _, rc := range rawClasses {
		// 1. Must have a valid name
		if rc.name == "" {
			continue
		}
		// 2. Duplicate names -> refused
		if nameCounts[rc.name] > 1 {
			continue
		}
		// 3. Status must be advisory or hard
		st := Status(rc.status)
		if st != StatusAdvisory && st != StatusHard {
			continue
		}
		// 4. Hard without valid citation -> load error for entry -> unregistered
		if st == StatusHard && !isValidCitation(rc.foundingCatch) {
			continue
		}

		// 5. Unknown check type -> entry unregistered
		unknownCheckFound := false
		var checks []Check
		for _, chk := range rc.checks {
			cType := CheckType(chk.typ)
			if cType != CheckTypeScript && cType != CheckTypeGoTest && cType != CheckTypeDiffGuard {
				unknownCheckFound = true
				break
			}
			checks = append(checks, Check{
				Type:     cType,
				Run:      chk.run,
				Packages: chk.packages,
			})
		}
		if unknownCheckFound {
			continue
		}

		reg.classes = append(reg.classes, Class{
			Name:          rc.name,
			Status:        st,
			FoundingCatch: rc.foundingCatch,
			Priority:      rc.priority,
			Paths:         rc.paths,
			Checks:        checks,
		})
	}

	return reg, nil
}

func cleanYAMLValue(val string) string {
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			return val[1 : len(val)-1]
		}
	}
	return val
}

func stripYAMLComment(line string) string {
	inDouble := false
	inSingle := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' && !inSingle {
			inDouble = !inDouble
		} else if ch == '\'' && !inDouble {
			inSingle = !inSingle
		} else if ch == '#' && !inDouble && !inSingle {
			return line[:i]
		}
	}
	return line
}

// pathMatches tests whether cleanFile matches pattern.
// Handles exact match, globstar ("**"), directory boundaries ("/*"), and filepath.Match globs.
func pathMatches(file, pattern string) bool {
	cleanFile := filepath.ToSlash(filepath.Clean(file))
	cleanFile = strings.TrimPrefix(cleanFile, "./")
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))
	cleanPattern = strings.TrimPrefix(cleanPattern, "./")

	// Confusable-character guard: a path containing non-ASCII characters never
	// matches a registry pattern (deny-by-default direction — an unregistered
	// verdict means human acceptance, never over-matching). Windows filesystem
	// normalization can fold look-alike Unicode (e.g. Greek omicron in "dοcs")
	// onto ASCII names, which would silently widen the docs class on one
	// platform only; this guard keeps class resolution identical everywhere
	// (release red cell, issue #517).
	if strings.ContainsFunc(cleanFile, func(r rune) bool { return r > unicode.MaxASCII }) {
		return false
	}

	// 1. Exact path match
	if cleanFile == cleanPattern {
		return true
	}
	// 1a. Globstar
	if strings.Contains(cleanPattern, "**") {
		return globstarMatch(
			strings.Split(cleanFile, "/"),
			strings.Split(cleanPattern, "/"),
		)
	}
	// 2. Directory boundary match: pattern ends in /* or / or is a bare directory name
	if strings.HasSuffix(pattern, "/*") || strings.HasSuffix(pattern, "/") {
		dirPrefix := strings.TrimSuffix(cleanPattern, "*")
		if !strings.HasSuffix(dirPrefix, "/") {
			dirPrefix += "/"
		}
		if strings.HasPrefix(cleanFile, dirPrefix) {
			return true
		}
	} else if !strings.Contains(cleanPattern, "*") {
		if strings.HasPrefix(cleanFile, cleanPattern+"/") {
			return true
		}
	}
	// 3. Glob matching (e.g. *.md, *_test.go). path.Match (not filepath.Match)
	// is required here: paths are already slash-normalized, and filepath.Match
	// is case-insensitive on Windows, which would let "DOCS/x.md" match the
	// lowercase "docs/**" registry patterns and silently widen the class
	// (release red cell, issue #517 — Windows-oracle catch).
	m, _ := path.Match(cleanPattern, cleanFile)
	return m
}

func globstarMatch(fileSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(fileSegs) == 0
	}
	if patSegs[0] == "**" {
		for i := 0; i <= len(fileSegs); i++ {
			if globstarMatch(fileSegs[i:], patSegs[1:]) {
				return true
			}
		}
		return false
	}
	if len(fileSegs) == 0 {
		return false
	}
	m, _ := path.Match(patSegs[0], fileSegs[0])
	if !m {
		return false
	}
	return globstarMatch(fileSegs[1:], patSegs[1:])
}

// findFile attempts to locate a file by path, searching upward from current working directory.
func findFile(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		if _, err := os.Stat(relPath); err == nil {
			return relPath, nil
		}
		return "", os.ErrNotExist
	}
	if _, err := os.Stat(relPath); err == nil {
		return relPath, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	dir := cwd
	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", os.ErrNotExist
}

// findRepoRoot finds the repository root directory by walking up looking for go.mod or .g8s.
func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd, nil
}
