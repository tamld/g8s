// Package lane implements ALDC Layer 1 gate-lane routing and Layer 2 Jev-assisted
// ambiguity resolution (issue #514, ADR-0024 S6-3, SCORECARD S-6).
//
// Gate intensity scales with change class:
//   - Lane 0 (hotfix):   Emergency hotfix with supervisor approval (targeted gates + retro-gate)
//   - Lane D (docs):     Documentation only changes (docs/* or *.md)
//   - Lane R (refactor): Refactoring without behavior change, mechanical edits
//   - Lane F (feature):  Default for features and fixes (>200 lines or behavior change)
//   - Lane S (security): Changes touching declared trust boundaries (P0 machine match)
//
// Composability note: lanes constrain WHERE (paths, trust); the context router
// (internal/routing) decides WHO.
package lane

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Lane represents a PR gate lane.
type Lane string

const (
	// LaneHotfix (0) is for emergency hotfixes with supervisor approval.
	LaneHotfix Lane = "0"
	// LaneDocs (D) is for documentation only changes.
	LaneDocs Lane = "D"
	// LaneRefactor (R) is for refactoring without behavior change.
	LaneRefactor Lane = "R"
	// LaneFeature (F) is the default lane for features and bug fixes.
	LaneFeature Lane = "F"
	// LaneSecurity (S) is the P0 security lane for changes touching trust boundaries.
	LaneSecurity Lane = "S"
)

// String returns the string representation of the Lane.
func (l Lane) String() string {
	return string(l)
}

// Name returns the human-readable canonical name of the Lane.
func (l Lane) Name() string {
	switch l {
	case LaneHotfix:
		return "hotfix"
	case LaneDocs:
		return "docs"
	case LaneRefactor:
		return "refactor"
	case LaneFeature:
		return "feature"
	case LaneSecurity:
		return "security"
	default:
		return string(l)
	}
}

// IsValid reports whether the lane is one of the 5 canonical lanes: 0, D, R, F, S.
func (l Lane) IsValid() bool {
	switch l {
	case LaneHotfix, LaneDocs, LaneRefactor, LaneFeature, LaneSecurity:
		return true
	default:
		return false
	}
}

// ParseLane parses a string into a Lane, supporting lane keys ("0", "D", "R", "F", "S")
// and descriptive aliases ("hotfix", "docs", "refactor", "feature", "security").
func ParseLane(s string) (Lane, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "hotfix":
		return LaneHotfix, nil
	case "d", "docs", "doc":
		return LaneDocs, nil
	case "r", "refactor":
		return LaneRefactor, nil
	case "f", "feature", "feat", "fix":
		return LaneFeature, nil
	case "s", "security", "sec", "p0":
		return LaneSecurity, nil
	default:
		return "", fmt.Errorf("unknown lane: %q", s)
	}
}

// Bundle represents the gate bundle associated with a Lane.
type Bundle struct {
	ID          Lane     `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Gates       []string `json:"gates" yaml:"gates"`
}

// BundlesConfig holds the mapping of lanes to their gate bundles loaded from .g8s/lane-bundles.yml.
type BundlesConfig struct {
	Lanes map[Lane]Bundle `json:"lanes"`
}

// TrustBoundariesConfig holds the pattern registry loaded from .g8s/trust-boundaries.yml.
type TrustBoundariesConfig struct {
	TrustBoundaries []string `json:"trust_boundaries"`
}

// Default paths for configuration files.
const (
	DefaultLaneBundlesPath     = ".g8s/lane-bundles.yml"
	DefaultTrustBoundariesPath = ".g8s/trust-boundaries.yml"
)

// DefaultTrustBoundaries provides the safety floor patterns from ADR-0024
// in case .g8s/trust-boundaries.yml is missing or inaccessible.
var DefaultTrustBoundaries = []string{
	"internal/receipt/*",
	"internal/worker/proc_*",
	"internal/server/*",
	"internal/harness/*",
	"internal/memory/*",
	"internal/dispatch/*",
}

// Input represents a planned change submitted to the router.
type Input struct {
	Paths   []string `json:"paths"`
	Summary string   `json:"summary"`
}

// Router evaluates planned changes against lane bundles and trust boundaries.
type Router struct {
	bundles         *BundlesConfig
	bundlesErr      error
	bundlesPath     string
	trustBoundaries []string
	trustExplicit   bool
	trustErr        error
	trustPath       string
}

// RouterOption configures a Router instance.
type RouterOption func(*Router)

// WithBundlesPath sets an explicit path for lane-bundles.yml.
func WithBundlesPath(path string) RouterOption {
	return func(r *Router) {
		r.bundlesPath = path
	}
}

// WithTrustBoundariesPath sets an explicit path for trust-boundaries.yml.
func WithTrustBoundariesPath(path string) RouterOption {
	return func(r *Router) {
		r.trustPath = path
	}
}

// WithBundlesData loads bundle configuration directly from YAML bytes.
func WithBundlesData(data []byte) RouterOption {
	return func(r *Router) {
		cfg, err := parseLaneBundles(data)
		r.bundles = cfg
		r.bundlesErr = err
	}
}

// WithTrustBoundariesData loads trust boundaries directly from YAML bytes.
func WithTrustBoundariesData(data []byte) RouterOption {
	return func(r *Router) {
		r.trustExplicit = true
		cfg, err := parseTrustBoundaries(data)
		if err != nil {
			r.trustBoundaries = append([]string(nil), DefaultTrustBoundaries...)
			r.trustErr = err
		} else {
			r.trustBoundaries = cfg.TrustBoundaries
			r.trustErr = nil
		}
	}
}

// WithTrustBoundaries sets explicit trust boundary patterns directly.
func WithTrustBoundaries(patterns []string) RouterOption {
	return func(r *Router) {
		r.trustExplicit = true
		r.trustBoundaries = append([]string(nil), patterns...)
		r.trustErr = nil
	}
}

// NewRouter constructs a new Router configured with the provided options.
// If options are omitted, it discovers default configuration files starting
// from the working directory and searching upward.
func NewRouter(opts ...RouterOption) *Router {
	r := &Router{
		bundlesPath:     DefaultLaneBundlesPath,
		trustPath:       DefaultTrustBoundariesPath,
		trustBoundaries: append([]string(nil), DefaultTrustBoundaries...),
	}

	for _, opt := range opts {
		opt(r)
	}

	// Load bundles if not already provided via WithBundlesData.
	if r.bundles == nil && r.bundlesErr == nil {
		path, err := findFile(r.bundlesPath)
		if err != nil {
			r.bundlesErr = err
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				r.bundlesErr = err
			} else {
				cfg, err := parseLaneBundles(data)
				r.bundles = cfg
				r.bundlesErr = err
			}
		}
	}

	// Load trust boundaries if not already explicitly provided.
	if !r.trustExplicit && r.trustErr == nil && r.trustPath != "" {
		path, err := findFile(r.trustPath)
		if err == nil {
			data, err := os.ReadFile(path)
			if err == nil {
				cfg, err := parseTrustBoundaries(data)
				if err == nil && len(cfg.TrustBoundaries) > 0 {
					r.trustBoundaries = cfg.TrustBoundaries
				}
			}
		}
	}

	return r
}

// Route classifies a planned change deterministically into a Lane.
//
// Classification precedence:
//  1. Trust boundaries (P0 machine match in .g8s/trust-boundaries.yml) -> Lane S.
//     Safety floor invariant: this check runs first and ALWAYS holds even if
//     lane-bundles.yml is missing or unparseable.
//  2. Deny-by-default: if bundles configuration is broken or missing, all non-trust
//     changes fall back to Lane F (feature).
//  3. Empty paths -> Lane F (plan.md:36 contract).
//  4. Explicit hotfix marker in summary -> Lane 0 (hotfix).
//  5. Docs-only paths (docs/* or *.md) -> Lane D (docs).
//  6. Pure refactor declared in summary/paths -> Lane R (refactor).
//  7. Default (feature, fix, mixed docs+code, unknown) -> Lane F (feature).
func (r *Router) Route(input Input) Lane {
	// Step 1: Safety floor (ADR-0024 Layer 3 / brief item 4).
	// Trust-boundary paths ALWAYS route to Lane S (P0).
	if r.matchesTrustBoundary(input.Paths) {
		return LaneSecurity
	}

	// Step 2: Deny-by-default (brief item 4).
	// Unparseable bundles or missing configuration falls back to Lane F.
	if r.bundlesErr != nil || r.bundles == nil {
		return LaneFeature
	}

	// Step 3: Empty paths contract (plan.md:36 contract: empty -> F).
	if len(input.Paths) == 0 {
		return LaneFeature
	}

	// Step 4: Explicit hotfix marker -> Lane 0 (hotfix).
	if isHotfixSummary(input.Summary) {
		return LaneHotfix
	}

	// Step 5: Docs-only paths -> Lane D (docs).
	if isDocsOnly(input.Paths) {
		return LaneDocs
	}

	// Step 6: Pure refactor declared -> Lane R (refactor).
	if isRefactor(input.Paths, input.Summary) {
		return LaneRefactor
	}

	// Step 7: Default for features, fixes, mixed docs+code, unknown -> Lane F.
	return LaneFeature
}

// Suggest evaluates an advisory suggestion (e.g. from Jev / LLM assist) against
// deterministic bundles policy (Layer 2 ambiguity resolution).
//
// Invariants enforced:
//  1. Lane S from a suggestion is ALWAYS refused (design line 43).
//  2. If deterministic policy identified a trust-boundary change (Lane S), machine-matched
//     P0 can never be downgraded by an advisory suggestion (ADR-0024 Layer 3).
//  3. Deny-by-default: if bundles configuration is missing or invalid, suggestions cannot
//     be validated and are refused.
//  4. The suggested lane must exist in the loaded bundles configuration.
//  5. A suggestion of Lane D (docs) for changes containing non-documentation code paths
//     is refused.
//
// Returns (Lane, bool) where bool indicates whether the suggestion was accepted (true)
// or refused (false). On refusal, the deterministically computed Lane is returned.
// The caller records (suggestion, source, decision) in telemetry.
func (r *Router) Suggest(input Input, suggestion Lane, source string) (Lane, bool) {
	_ = source // Passed for caller telemetry context
	deterministicLane := r.Route(input)

	// Invariant 1: Lane S from a suggestion is ALWAYS refused (design :43).
	if suggestion == LaneSecurity {
		return deterministicLane, false
	}

	// Invariant 2: Machine-matched P0 (Lane S) is never downgraded by suggestion.
	if deterministicLane == LaneSecurity {
		return LaneSecurity, false
	}

	// Invariant 3: Deny-by-default if bundles configuration is unavailable.
	if r.bundlesErr != nil || r.bundles == nil {
		return deterministicLane, false
	}

	// Invariant 4: Suggested lane must exist in bundles configuration.
	if _, exists := r.bundles.Lanes[suggestion]; !exists {
		return deterministicLane, false
	}

	// Invariant 5: Suggestion of Lane D requires docs-only paths.
	if suggestion == LaneDocs && !isDocsOnly(input.Paths) {
		return deterministicLane, false
	}

	return suggestion, true
}

// GetBundle returns the bundle configuration for a given lane.
func (r *Router) GetBundle(l Lane) (Bundle, bool) {
	if r.bundles == nil {
		return Bundle{}, false
	}
	b, ok := r.bundles.Lanes[l]
	return b, ok
}

// Bundles returns a copy of all loaded lane bundles.
func (r *Router) Bundles() map[Lane]Bundle {
	res := make(map[Lane]Bundle)
	if r.bundles == nil {
		return res
	}
	for k, v := range r.bundles.Lanes {
		res[k] = v
	}
	return res
}

// TrustBoundaries returns the active trust boundary patterns.
func (r *Router) TrustBoundaries() []string {
	return append([]string(nil), r.trustBoundaries...)
}

func (r *Router) matchesTrustBoundary(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	boundaries := r.trustBoundaries
	if len(boundaries) == 0 {
		boundaries = DefaultTrustBoundaries
	}
	for _, p := range paths {
		clean := filepath.ToSlash(filepath.Clean(p))
		for _, pattern := range boundaries {
			if pathMatches(clean, pattern) {
				return true
			}
		}
	}
	return false
}

// Package-level default router instance.
var defaultRouter = NewRouter()

// Route deterministically classifies a planned change into a Lane using the default router.
func Route(input Input) Lane {
	return defaultRouter.Route(input)
}

// Suggest evaluates an advisory suggestion using the default router.
func Suggest(input Input, suggestion Lane, source string) (Lane, bool) {
	return defaultRouter.Suggest(input, suggestion, source)
}

// isDocsPath reports whether a single file path is a documentation file.
// Matches paths starting with "docs/" or files with ".md" extension.
func isDocsPath(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == "." || clean == "" {
		return false
	}
	if strings.HasPrefix(clean, "docs/") || clean == "docs" {
		return true
	}
	if strings.HasSuffix(strings.ToLower(clean), ".md") {
		return true
	}
	return false
}

// isDocsOnly reports whether all paths in the set are documentation paths.
func isDocsOnly(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !isDocsPath(p) {
			return false
		}
	}
	return true
}

// isHotfixSummary reports whether the summary declares an explicit hotfix marker.
func isHotfixSummary(summary string) bool {
	s := strings.TrimSpace(summary)
	lower := strings.ToLower(s)
	if lower == "0" || lower == "hotfix" || lower == "lane:0" || lower == "lane: 0" || lower == "lane:hotfix" {
		return true
	}
	prefixes := []string{
		"hotfix:",
		"hotfix/",
		"[hotfix]",
		"(hotfix)",
		"hotfix -",
		"hotfix ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// isRefactor reports whether the change declares a pure refactoring without behavior change.
// Rules:
//  1. Summary conventional commit prefix "refactor:" or scoped "refactor(...):"
//  2. Summary markers "[refactor]", "(refactor)", "refactor/", "refactor -", "refactor "
//  3. Paths explicitly located under a refactor/ directory
func isRefactor(paths []string, summary string) bool {
	s := strings.TrimSpace(summary)
	lower := strings.ToLower(s)

	if lower == "r" || lower == "refactor" || lower == "lane:r" || lower == "lane: r" || lower == "lane:refactor" {
		return true
	}

	refactorPrefixes := []string{
		"refactor:",
		"refactor/",
		"[refactor]",
		"(refactor)",
		"refactor -",
		"refactor ",
		"refactor-",
		"chore(refactor):",
	}
	for _, prefix := range refactorPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	// Conventional commit scope: refactor(component): ...
	if strings.HasPrefix(lower, "refactor(") && strings.Contains(lower, "):") {
		return true
	}

	// Paths check: all paths within refactor directory
	if len(paths) > 0 {
		allRefactorPaths := true
		for _, p := range paths {
			clean := filepath.ToSlash(filepath.Clean(p))
			if !strings.HasPrefix(clean, "refactor/") && !strings.Contains(clean, "/refactor/") {
				allRefactorPaths = false
				break
			}
		}
		if allRefactorPaths {
			return true
		}
	}

	return false
}

// pathMatches tests whether cleanFile matches pattern.
// Handles exact match, globstar ("**"), directory boundaries ("/*"), and filepath.Match globs.
func pathMatches(cleanFile, pattern string) bool {
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))
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
	// 3. Glob matching (e.g. *.md, internal/worker/proc_*)
	// path.Match (POSIX) instead of filepath.Match: the separator must be '/'
	// on every platform — filepath.Match's separator is os.PathSeparator, so
	// on Windows '*' crosses '/' and patterns like *.md match whole nested
	// paths (issue #570, red-cell R1-1 + Windows E2E oracle).
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

// parseLaneBundles parses YAML data from .g8s/lane-bundles.yml using pure Go stdlib.
func parseLaneBundles(data []byte) (*BundlesConfig, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	cfg := &BundlesConfig{
		Lanes: make(map[Lane]Bundle),
	}

	var currentLane Lane
	var currentBundle *Bundle
	inLanesSection := false
	inGatesSection := false

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

		if leadingSpaces == 0 {
			if trimmed == "lanes:" {
				inLanesSection = true
				inGatesSection = false
				continue
			}
			inLanesSection = false
			inGatesSection = false
			continue
		}

		if !inLanesSection {
			continue
		}

		// Lane key at indent level 2 (or 1-3 spaces): "  0:", "  D:", etc.
		if leadingSpaces >= 1 && leadingSpaces <= 3 && strings.HasSuffix(trimmed, ":") {
			inGatesSection = false
			if currentBundle != nil && currentLane != "" {
				cfg.Lanes[currentLane] = *currentBundle
			}
			rawKey := strings.TrimSuffix(trimmed, ":")
			cleanKey := cleanYAMLValue(rawKey)
			lane := Lane(cleanKey)
			if !lane.IsValid() {
				return nil, fmt.Errorf("invalid lane key %q at line %d", cleanKey, lineNum)
			}
			currentLane = lane
			currentBundle = &Bundle{
				ID:    lane,
				Gates: []string{},
			}
			continue
		}

		if currentBundle == nil {
			return nil, fmt.Errorf("unexpected content outside lane definition at line %d: %q", lineNum, trimmed)
		}

		if inGatesSection {
			if strings.HasPrefix(trimmed, "- ") {
				gate := cleanYAMLValue(strings.TrimPrefix(trimmed, "- "))
				currentBundle.Gates = append(currentBundle.Gates, gate)
				continue
			}
			inGatesSection = false
		}

		if strings.HasPrefix(trimmed, "gates:") {
			inGatesSection = true
			continue
		}

		if strings.HasPrefix(trimmed, "name:") {
			val := strings.TrimPrefix(trimmed, "name:")
			currentBundle.Name = cleanYAMLValue(val)
			continue
		}

		if strings.HasPrefix(trimmed, "description:") {
			val := strings.TrimPrefix(trimmed, "description:")
			currentBundle.Description = cleanYAMLValue(val)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if currentBundle != nil && currentLane != "" {
		cfg.Lanes[currentLane] = *currentBundle
	}

	if len(cfg.Lanes) == 0 {
		return nil, errors.New("no valid lanes found in configuration")
	}

	return cfg, nil
}

// parseTrustBoundaries parses YAML data from .g8s/trust-boundaries.yml using pure Go stdlib.
func parseTrustBoundaries(data []byte) (*TrustBoundariesConfig, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	cfg := &TrustBoundariesConfig{
		TrustBoundaries: []string{},
	}

	inSection := false
	for scanner.Scan() {
		line := stripYAMLComment(scanner.Text())
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "trust_boundaries:") {
			inSection = true
			continue
		}

		if inSection {
			if strings.HasPrefix(trimmed, "- ") {
				val := cleanYAMLValue(strings.TrimPrefix(trimmed, "- "))
				cfg.TrustBoundaries = append(cfg.TrustBoundaries, val)
				continue
			}
			if !strings.HasPrefix(trimmed, "-") {
				inSection = false
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(cfg.TrustBoundaries) == 0 {
		return nil, errors.New("no trust boundaries found in configuration")
	}

	return cfg, nil
}

// findFile attempts to locate a file by path, falling back to searching parent directories.
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
