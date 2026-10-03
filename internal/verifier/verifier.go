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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

// TaskRef references a task and its write delegation scope.
type TaskRef struct {
	ID           string   `json:"id"`
	ReceiptID    string   `json:"receipt_id,omitempty"`
	AllowedPaths []string `json:"allowed_paths,omitempty"`
}

// CheckResult represents the outcome of a single check execution.
type CheckResult struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Verdict encapsulates the complete machine acceptance decision.
type Verdict struct {
	Class      string        `json:"class"`
	Registered bool          `json:"registered"`
	Status     Status        `json:"status"`
	Outcome    Outcome       `json:"outcome"`
	Checks     []CheckResult `json:"checks"`
	Scope      bool          `json:"scope"`
	RecordedAt time.Time     `json:"recorded_at"`
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

// Citation regex patterns: exactly #<digits> or incident:<slug>
var (
	citationIssueTokenRegex    = regexp.MustCompile(`^#\d+$`)
	citationIncidentTokenRegex = regexp.MustCompile(`^incident:[a-z0-9][a-z0-9_-]*$`)
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
	if strings.HasPrefix(rest, "#") || strings.HasPrefix(strings.ToLower(rest), "incident:") {
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
	registry      *Registry
	runner        CommandRunner
	repoRoot      string
	telemetrySink TelemetrySink
}

// Option configures a Verifier instance.
type Option func(*Verifier)

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
	if target.ReceiptID == "" || len(target.AllowedPaths) == 0 {
		verdict := Verdict{
			Class:      "unregistered",
			Registered: false,
			Status:     StatusAdvisory,
			Outcome:    OutcomeUnregistered,
			Checks:     []CheckResult{},
			Scope:      false,
			RecordedAt: now,
		}
		v.emitTelemetry(target.ID, verdict)
		return verdict, nil
	}

	// Class resolution
	class, registered := v.registry.Resolve(target.AllowedPaths)
	if !registered {
		verdict := Verdict{
			Class:      "unregistered",
			Registered: false,
			Status:     StatusAdvisory,
			Outcome:    OutcomeUnregistered,
			Checks:     []CheckResult{},
			Scope:      false,
			RecordedAt: now,
		}
		v.emitTelemetry(target.ID, verdict)
		return verdict, nil
	}

	// Registered class: execute checks
	var checkResults []CheckResult
	hasRunnerError := false
	hasCheckFailure := false

	runner := v.runner
	if runner == nil {
		runner = defaultCommandRunner(v.repoRoot)
	}

	for _, chk := range class.Checks {
		switch chk.Type {
		case CheckTypeScript:
			res := v.runScriptCheck(ctx, runner, chk.Run)
			checkResults = append(checkResults, res)
			if strings.HasPrefix(res.Detail, "runner error:") {
				hasRunnerError = true
			} else if !res.OK {
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
				checkResults = append(checkResults, res)
				if strings.HasPrefix(res.Detail, "runner error:") {
					hasRunnerError = true
				} else if !res.OK {
					hasCheckFailure = true
				}
			}
		}
	}

	// Determine overall outcome:
	// Fail-open rule: check's runner error (infrastructure error) => outcome not-run
	var outcome Outcome
	switch {
	case hasRunnerError:
		outcome = OutcomeNotRun
	case hasCheckFailure:
		outcome = OutcomeFail
	default:
		outcome = OutcomePass
	}

	verdict := Verdict{
		Class:      class.Name,
		Registered: true,
		Status:     class.Status,
		Outcome:    outcome,
		Checks:     checkResults,
		Scope:      true,
		RecordedAt: now,
	}

	v.emitTelemetry(target.ID, verdict)
	return verdict, nil
}

func (v *Verifier) runScriptCheck(ctx context.Context, runner CommandRunner, scriptPath string) CheckResult {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Check if script exists if repoRoot is set
	resolvedPath := scriptPath
	if v.repoRoot != "" && !filepath.IsAbs(scriptPath) {
		resolvedPath = filepath.Join(v.repoRoot, scriptPath)
	}

	if _, err := os.Stat(resolvedPath); err != nil {
		return CheckResult{
			Type:   string(CheckTypeScript),
			Target: scriptPath,
			OK:     false,
			Detail: fmt.Sprintf("runner error: script file not found: %v", err),
		}
	}

	_, stderr, exitCode, err := runner(timeoutCtx, "bash", scriptPath)
	if err != nil {
		return CheckResult{
			Type:   string(CheckTypeScript),
			Target: scriptPath,
			OK:     false,
			Detail: fmt.Sprintf("runner error: %v", err),
		}
	}

	if exitCode != 0 {
		detail := fmt.Sprintf("exit code %d", exitCode)
		if trimmed := strings.TrimSpace(string(stderr)); trimmed != "" {
			detail += ": " + trimmed
		}
		return CheckResult{
			Type:   string(CheckTypeScript),
			Target: scriptPath,
			OK:     false,
			Detail: detail,
		}
	}

	return CheckResult{
		Type:   string(CheckTypeScript),
		Target: scriptPath,
		OK:     true,
		Detail: "ok",
	}
}

func (v *Verifier) runGoTestCheck(ctx context.Context, runner CommandRunner, pkgs []string) CheckResult {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	targetStr := strings.Join(pkgs, " ")
	args := append([]string{"test", "-count=1"}, pkgs...)
	_, stderr, exitCode, err := runner(timeoutCtx, "go", args...)
	if err != nil {
		return CheckResult{
			Type:   string(CheckTypeGoTest),
			Target: targetStr,
			OK:     false,
			Detail: fmt.Sprintf("runner error: %v", err),
		}
	}

	if exitCode != 0 {
		detail := fmt.Sprintf("exit code %d", exitCode)
		if trimmed := strings.TrimSpace(string(stderr)); trimmed != "" {
			detail += ": " + trimmed
		}
		return CheckResult{
			Type:   string(CheckTypeGoTest),
			Target: targetStr,
			OK:     false,
			Detail: detail,
		}
	}

	return CheckResult{
		Type:   string(CheckTypeGoTest),
		Target: targetStr,
		OK:     true,
		Detail: "ok",
	}
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
			if cType != CheckTypeScript && cType != CheckTypeGoTest {
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
	// 3. Glob matching (e.g. *.md, *_test.go)
	m, _ := filepath.Match(cleanPattern, cleanFile)
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
	m, _ := filepath.Match(patSegs[0], fileSegs[0])
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
