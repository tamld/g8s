// Package dispatch implements the bounded AGY CLI dispatch wrapper ported
// from reference/python/scripts/agy_dispatch.py. It resolves the AGY
// executable without host assumptions, builds sandboxed commands behind the
// harness gate, executes them through an injectable runner, detects read-only
// contract violations, sanitizes sensitive output, and returns a structured
// result envelope.
package dispatch

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Defaults mirrored from the Python baseline.
const (
	// DefaultModel is used when RunOptions.Model is empty.
	DefaultModel = "Gemini 3.7 Flash (High)"
	// ReadOnlyContractExit is the process exit code reported when a worker
	// that was supposed to stay read-only produced side effects anyway.
	ReadOnlyContractExit = 3
	// MaxCaptureBytes bounds captured subprocess output per stream.
	MaxCaptureBytes = 2 * 1024 * 1024
	// TruncationMarker separates retained head and tail halves.
	TruncationMarker = "\n<OUTPUT_TRUNCATED>\n"
	// ReplacementRune substitutes invalid UTF-8 bytes during decode.
	ReplacementRune = "\uFFFD"
)

var windowsExecutableSuffixes = []string{".exe", ".cmd", ".bat"}

// sensitivePatterns redact credentials from captured output, applied in order.
var sensitivePatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`postgresql://[^\s"\x60]+`), "postgresql://<REDACTED>"},
	{regexp.MustCompile(`://[^\s"\x60/:]+:[^\s"\x60/@]+@`), "://<REDACTED>:<REDACTED>@"},
	{regexp.MustCompile(`specifically \x60[^\x60]+\x60`), "specifically `<REDACTED>`"},
	{regexp.MustCompile(`(?i)(password|credential|secret)[^.\n]{0,160}`), "${1} <REDACTED>"},
}

// violationPattern pairs a read-only contract detector with its class name.
type violationPattern struct {
	pattern       *regexp.Regexp
	violationType string
}

var readOnlyViolationPatterns = []violationPattern{
	{
		regexp.MustCompile(`(?im)^\s*(?:[$>]\s*)?(?:uv\s+run\s+python\s+)?wiki\.py\s+(?:reflect|write|rename|ingest|promote|claim|bypass)\b`),
		"wiki_mutation_command",
	},
	{
		regexp.MustCompile(`(?i)\b(?:i\s+)?(?:ran|executed|used)\s+\x60?(?:uv\s+run\s+python\s+)?wiki\.py\s+(?:reflect|write|rename|ingest|promote|claim|bypass)\b`),
		"wiki_mutation_report",
	},
	{regexp.MustCompile(`(?i)\bsession logged to log\.md\b`), "wiki_reflect_side_effect"},
	{regexp.MustCompile(`(?i)\bnote written:\b`), "wiki_write_side_effect"},
	{
		regexp.MustCompile(`(?im)^\s*(?:[$>]\s*)?git\s+(?:add|commit|checkout|reset|merge|rebase|push|rm|mv)\b`),
		"git_mutation_command",
	},
	{regexp.MustCompile(`(?m)^\[[^\]\n]+ [0-9a-f]{7,}\] .+`), "git_commit_side_effect"},
}

// ResolveOptions carries injectable seams for ResolveBinary so tests never
// touch the real host environment. Zero-value function fields fall back to
// their operating-system defaults.
type ResolveOptions struct {
	// EnvLookup resolves environment variables; defaults to os.LookupEnv.
	EnvLookup func(key string) (string, bool)
	// Platform selects Windows suffix expansion; defaults to runtime GOOS.
	Platform string
	// Home is the user home directory used by ~ expansion and fallbacks;
	// defaults to os.UserHomeDir().
	Home string
	// Which performs PATH lookups; defaults to exec.LookPath.
	Which func(name string) (string, error)
	// Exists reports path existence; defaults to os.Stat.
	Exists func(path string) bool
}

func (o ResolveOptions) envLookup(key string) (string, bool) {
	if o.EnvLookup != nil {
		return o.EnvLookup(key)
	}
	return os.LookupEnv(key)
}

func (o ResolveOptions) platform() string {
	if o.Platform != "" {
		return o.Platform
	}
	return runtimeGOOS()
}

func (o ResolveOptions) home() string {
	if o.Home != "" {
		return o.Home
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return home
}

func (o ResolveOptions) which(name string) (string, error) {
	if o.Which != nil {
		return o.Which(name)
	}
	return exec.LookPath(name)
}

func (o ResolveOptions) exists(path string) bool {
	if o.Exists != nil {
		return o.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

// expandUser expands a leading ~ using the injected home directory.
func expandUser(reference, home string) string {
	if home == "" {
		return reference
	}
	switch {
	case reference == "~":
		return home
	case strings.HasPrefix(reference, "~/"):
		return home + reference[1:]
	case strings.HasPrefix(reference, `~\`):
		return home + reference[1:]
	default:
		return reference
	}
}

// hasSuffix reports whether the reference already ends with a known Windows
// executable suffix (case-insensitive).
func hasSuffix(reference string) bool {
	lowerRef := strings.ToLower(reference)
	for _, suffix := range windowsExecutableSuffixes {
		if strings.HasSuffix(lowerRef, suffix) {
			return true
		}
	}
	return false
}

// suffixCandidates expands a suffix-less reference with Windows executable
// suffixes on Windows platforms only.
func suffixCandidates(reference string, platform string) []string {
	if platform != "windows" || hasSuffix(reference) {
		return []string{reference}
	}
	candidates := make([]string, 0, len(windowsExecutableSuffixes)+1)
	candidates = append(candidates, reference)
	for _, suffix := range windowsExecutableSuffixes {
		candidates = append(candidates, reference+suffix)
	}
	return candidates
}

// resolveReference tries direct existence (with Windows suffix expansion)
// before falling back to a PATH lookup of the reference itself.
func resolveReference(reference string, opts ResolveOptions) (string, bool) {
	expanded := expandUser(reference, opts.home())
	for _, candidate := range suffixCandidates(expanded, opts.platform()) {
		if opts.exists(candidate) {
			return candidate, true
		}
	}
	match, err := opts.which(reference)
	if err == nil && match != "" {
		return match, true
	}
	return "", false
}

// homeFallbacks lists safe per-user install locations probed last.
func homeFallbacks(home string) []string {
	return []string{
		home + "/.local/bin/agy",
		home + "/AppData/Local/Programs/agy/agy",
		home + "/AppData/Roaming/npm/agy",
	}
}

// ResolveBinary locates the AGY executable with the precedence documented in
// spec/openspec/08-dispatch-wrapper-spec.md: explicit reference, then the
// AGY_BIN environment override, then PATH, then home fallbacks.
func ResolveBinary(explicit string, opts ResolveOptions) (string, error) {
	for _, reference := range []string{explicit, envValue(opts, "AGY_BIN")} {
		if reference == "" {
			continue
		}
		if resolved, ok := resolveReference(reference, opts); ok {
			return resolved, nil
		}
	}
	if match, err := opts.which("agy"); err == nil && match != "" {
		return match, nil
	}
	home := opts.home()
	for _, fallback := range homeFallbacks(home) {
		for _, candidate := range suffixCandidates(fallback, opts.platform()) {
			if candidate != "" && opts.exists(candidate) {
				return candidate, nil
			}
		}
	}
	return "", errors.New("AGY binary not found: set --agy-bin, set AGY_BIN, or add agy to PATH")
}

func envValue(opts ResolveOptions, key string) string {
	value, _ := opts.envLookup(key)
	return value
}

// CommandOptions describes one AGY invocation.
type CommandOptions struct {
	Binary          string
	Prompt          string
	Model           string
	Timeout         string
	AddDirs         []string
	SkipPermissions bool
	NoSandbox       bool
	// Home overrides ~ expansion; empty uses os.UserHomeDir().
	Home string
}

// BuildCommand constructs the AGY argv. The sandbox stays enabled unless the
// caller explicitly disables it; skip-permissions adds
// --dangerously-skip-permissions without ever removing --sandbox here.
func BuildCommand(opts CommandOptions) []string {
	command := []string{
		opts.Binary,
		"--prompt", opts.Prompt,
		"--model", opts.Model,
		"--print-timeout", opts.Timeout,
	}
	if opts.SkipPermissions {
		command = append(command, "--dangerously-skip-permissions")
	}
	if !opts.NoSandbox {
		command = append(command, "--sandbox")
	}
	home := opts.Home
	if home == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			home = homeDir
		}
	}
	for _, dir := range opts.AddDirs {
		command = append(command, "--add-dir", expandUser(dir, home))
	}
	return command
}

// Violation is one detected read-only contract breach with evidence.
type Violation struct {
	Type    string `json:"type"`
	Snippet string `json:"snippet"`
}

// DetectReadOnlyContractViolations scans combined output for likely side
// effects from a worker that was supposed to stay read-only. At most one hit
// is recorded per violation class, in declaration order.
func DetectReadOnlyContractViolations(stdout, stderr string) []Violation {
	var parts []string
	for _, part := range []string{stdout, stderr} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	combined := strings.Join(parts, "\n")

	var violations []Violation
	for _, vp := range readOnlyViolationPatterns {
		loc := vp.pattern.FindStringIndex(combined)
		if loc == nil {
			continue
		}
		violations = append(violations, Violation{
			Type:    vp.violationType,
			Snippet: matchSnippet(combined, loc[0], loc[1]),
		})
	}
	return violations
}

// matchSnippet extracts a +/-96 rune window around the match, escapes
// newlines, and passes the evidence through output sanitization.
func matchSnippet(text string, start, end int) string {
	const radius = 96
	runes := []rune(text)
	startRune := utf8.RuneCountInString(text[:start])
	endRune := startRune + utf8.RuneCountInString(text[start:end])
	begin := startRune - radius
	if begin < 0 {
		begin = 0
	}
	finish := endRune + radius
	if finish > len(runes) {
		finish = len(runes)
	}
	snippet := string(runes[begin:finish])
	snippet = strings.ReplaceAll(snippet, "\n", "\\n")
	return SanitizeOutput(snippet)
}

// SanitizeOutput redacts credentials and secret-bearing fragments.
func SanitizeOutput(value string) string {
	sanitized := value
	for _, sp := range sensitivePatterns {
		sanitized = sp.pattern.ReplaceAllString(sanitized, sp.replacement)
	}
	return sanitized
}

// CaptureBounded decodes raw stream bytes, replacing invalid UTF-8 with
// U+FFFD. Oversize payloads keep only the head and tail halves of the budget
// joined by TruncationMarker instead of failing.
func CaptureBounded(raw []byte, maxBytes int) string {
	if len(raw) <= maxBytes {
		return strings.ToValidUTF8(string(raw), ReplacementRune)
	}
	half := maxBytes / 2
	payload := make([]byte, 0, maxBytes+len(TruncationMarker))
	payload = append(payload, raw[:half]...)
	payload = append(payload, TruncationMarker...)
	payload = append(payload, raw[len(raw)-half:]...)
	return strings.ToValidUTF8(string(payload), ReplacementRune)
}

// ExecResult carries one completed subprocess execution.
type ExecResult struct {
	ReturnCode int
	Stdout     []byte
	Stderr     []byte
}

// Runner executes a fully built command; tests substitute fakes.
type Runner func(command []string) (ExecResult, error)

// RunOptions parameterizes one dispatch envelope invocation.
type RunOptions struct {
	Prompt          string
	Model           string
	Role            string
	Permission      string
	Timeout         string
	AddDirs         []string
	NoSandbox       bool
	SkipPermissions bool
	ReceiptID       string
	BinaryOverride  string

	// Resolver overrides resolution seams; zero functions use host defaults.
	Resolver ResolveOptions
	// Runner executes the command; nil uses the real exec-based runner.
	Runner Runner
	// Clock measures duration; nil uses time.Now (constitution: injectable).
	Clock func() time.Time
}

// ContractViolationReport summarizes read-only policy enforcement results.
type ContractViolationReport struct {
	Policy     string      `json:"policy"`
	ExitCode   int         `json:"exit_code"`
	Violations []Violation `json:"violations"`
}

// Result is the structured dispatch outcome written by the CLI envelope.
type Result struct {
	OK                bool                     `json:"ok"`
	ReturnCode        int                      `json:"returncode"`
	HarnessReturnCode int                      `json:"harness_returncode"`
	DurationSeconds   float64                  `json:"duration_seconds"`
	Model             string                   `json:"model"`
	Role              string                   `json:"role"`
	Permission        string                   `json:"permission"`
	AGYBin            string                   `json:"agy_bin"`
	AddDirs           []string                 `json:"add_dirs"`
	CommandPreview    string                   `json:"command_preview"`
	Stdout            string                   `json:"stdout"`
	Stderr            string                   `json:"stderr"`
	ContractViolation *ContractViolationReport `json:"contract_violation,omitempty"`
}

// Run executes one bounded dispatch: resolve, gate, execute, detect,
// sanitize, and summarize. Gate or resolution failures return an error
// before any process spawns.
func Run(opts RunOptions) (Result, error) {
	model := opts.Model
	if model == "" {
		model = DefaultModel
	}
	timeout := opts.Timeout
	if timeout == "" {
		timeout = "5m0s"
	}
	role, permission := opts.normalize()

	binary, err := ResolveBinary(opts.BinaryOverride, opts.Resolver)
	if err != nil {
		return Result{}, err
	}
	if err := validateGate(opts); err != nil {
		return Result{}, err
	}
	contractPrompt, err := buildPrompt(opts)
	if err != nil {
		return Result{}, err
	}

	command := BuildCommand(CommandOptions{
		Binary:          binary,
		Prompt:          contractPrompt,
		Model:           model,
		Timeout:         timeout,
		AddDirs:         opts.AddDirs,
		SkipPermissions: opts.SkipPermissions,
		NoSandbox:       opts.NoSandbox,
	})

	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	started := clock()

	execResult, err := runCommand(opts, command)
	if err != nil {
		return Result{}, err
	}

	duration := clock().Sub(started).Seconds()

	stdout := CaptureBounded(execResult.Stdout, MaxCaptureBytes)
	stderr := CaptureBounded(execResult.Stderr, MaxCaptureBytes)

	profile, permErr := permissionProfile(permission)
	if permErr != nil {
		return Result{}, permErr
	}
	var violations []Violation
	if !profile.MutationAllowed {
		violations = DetectReadOnlyContractViolations(stdout, stderr)
	}

	harnessReturnCode := execResult.ReturnCode
	if execResult.ReturnCode == 0 && len(violations) > 0 {
		harnessReturnCode = ReadOnlyContractExit
	}

	result := Result{
		OK:                harnessReturnCode == 0,
		ReturnCode:        execResult.ReturnCode,
		HarnessReturnCode: harnessReturnCode,
		DurationSeconds:   duration,
		Model:             model,
		Role:              role,
		Permission:        permission,
		AGYBin:            binary,
		AddDirs:           opts.AddDirs,
		CommandPreview:    commandPreview(binary, model, timeout),
		Stdout:            SanitizeOutput(stdout),
		Stderr:            SanitizeOutput(stderr),
	}
	if len(violations) > 0 {
		result.ContractViolation = &ContractViolationReport{
			Policy:     "read_only",
			ExitCode:   ReadOnlyContractExit,
			Violations: violations,
		}
	}
	return result, nil
}

// runCommand dispatches through the injected runner or the real exec runner.
func runCommand(opts RunOptions, command []string) (ExecResult, error) {
	if opts.Runner != nil {
		return opts.Runner(command)
	}
	return execRunner(command)
}

// execRunner captures separate streams through bounded byte slices and maps
// non-zero exits onto ReturnCode instead of an error.
func execRunner(command []string) (ExecResult, error) {
	cmd := exec.Command(command[0], command[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	result := ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ReturnCode = exitErr.ExitCode()
		return result, nil
	}
	if runErr != nil {
		return result, runErr
	}
	result.ReturnCode = 0
	return result, nil
}
