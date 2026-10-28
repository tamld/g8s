package verifier

// Aegis-Cross-Pollinated Mechanical Guards (#694, #696):
//   1. PlanDiffVerifier: compares [NEW|MODIFY|DELETE] markers in plan/spec text
//      against git diff --name-status (#694).
//   2. TestAssertionGuard (Catch-Intent Guard v2): detects net removal or
//      weakening of test assertions or injected t.Skip/it.skip in unified diffs (#694).
//   3. EvidenceProvenanceValidator: enforces [RUNTIME]/[CODE]/[TEST]/[CONFIG]
//      provenance tags and file:line citations on conclusion claims (#696).
//   4. DocConsumerFrontmatterValidator: enforces machine consumer: YAML
//      frontmatter on spec/governance documents (#696).

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PlannedFileAction represents an expected file operation parsed from a plan/brief.
type PlannedFileAction string

const (
	PlannedActionNew    PlannedFileAction = "NEW"
	PlannedActionModify PlannedFileAction = "MODIFY"
	PlannedActionDelete PlannedFileAction = "DELETE"
)

// PlanDiffViolationType categorizes discrepancies between a plan and git diff.
type PlanDiffViolationType string

const (
	ViolationMissingPlannedChange PlanDiffViolationType = "MISSING_PLANNED_CHANGE"
	ViolationActionMismatch       PlanDiffViolationType = "ACTION_MISMATCH"
	ViolationUnplannedChange      PlanDiffViolationType = "UNPLANNED_CHANGE"
)

// PlanDiffViolation describes a single file-level discrepancy between plan and diff.
type PlanDiffViolation struct {
	Type          PlanDiffViolationType `json:"type"`
	File          string                `json:"file"`
	PlannedAction PlannedFileAction     `json:"planned_action,omitempty"`
	ActualAction  PlannedFileAction     `json:"actual_action,omitempty"`
	Message       string                `json:"message"`
}

// PlanDiffReport summarizes the result of VerifyPlanDiff (#694).
type PlanDiffReport struct {
	OK           bool                         `json:"ok"`
	PlannedFiles map[string]PlannedFileAction `json:"planned_files"`
	ActualFiles  map[string]PlannedFileAction `json:"actual_files"`
	Violations   []PlanDiffViolation          `json:"violations,omitempty"`
	JITBaton     string                       `json:"jit_baton,omitempty"`
}

// Matches markers like:
//
//	[NEW] internal/foo/bar.go
//	- `[MODIFY]` `cmd/g8s/verify.go`
//	* [DELETE] `old/legacy.ts`
//	#### [CREATE] src/index.ts
var planActionMarkerRegex = regexp.MustCompile(`(?i)\[\s*(NEW|CREATE|ADD|MODIFY|UPDATE|EDIT|DELETE|REMOVE)\s*\]\s*` + "`?" + `([A-Za-z0-9_./-]+(?:\.[A-Za-z0-9_]+))` + "`?")

func normalizePlannedAction(raw string) PlannedFileAction {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "NEW", "CREATE", "ADD":
		return PlannedActionNew
	case "DELETE", "REMOVE":
		return PlannedActionDelete
	default:
		return PlannedActionModify
	}
}

func normalizeGitStatusAction(code string) PlannedFileAction {
	code = strings.ToUpper(strings.TrimSpace(code))
	if strings.HasPrefix(code, "A") {
		return PlannedActionNew
	}
	if strings.HasPrefix(code, "D") {
		return PlannedActionDelete
	}
	return PlannedActionModify
}

// ParsePlannedFileActions extracts [NEW|MODIFY|DELETE] <path> declarations from planText.
func ParsePlannedFileActions(planText string) map[string]PlannedFileAction {
	planned := make(map[string]PlannedFileAction)
	scanner := bufio.NewScanner(strings.NewReader(planText))
	for scanner.Scan() {
		line := scanner.Text()
		matches := planActionMarkerRegex.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			action := normalizePlannedAction(m[1])
			cleanPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(m[2])))
			cleanPath = strings.TrimPrefix(cleanPath, "./")
			if cleanPath == "" || cleanPath == "." {
				continue
			}
			planned[cleanPath] = action
		}
	}
	return planned
}

// ParseGitDiffNameStatus parses output of `git diff --name-status` into normalized file actions.
func ParseGitDiffNameStatus(diffNameStatus string) map[string]PlannedFileAction {
	actual := make(map[string]PlannedFileAction)
	scanner := bufio.NewScanner(strings.NewReader(diffNameStatus))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		targetPath := fields[len(fields)-1]
		cleanPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(targetPath)))
		cleanPath = strings.TrimPrefix(cleanPath, "./")
		if cleanPath == "" || cleanPath == "." {
			continue
		}
		actual[cleanPath] = normalizeGitStatusAction(status)
	}
	return actual
}

// VerifyPlanDiff mechanically compares [NEW|MODIFY|DELETE] declarations in planText
// against `git diff --name-status` output (#694).
func VerifyPlanDiff(planText string, diffNameStatus string) PlanDiffReport {
	planned := ParsePlannedFileActions(planText)
	actual := ParseGitDiffNameStatus(diffNameStatus)
	report := PlanDiffReport{
		OK:           true,
		PlannedFiles: planned,
		ActualFiles:  actual,
	}

	// If no explicit [NEW|MODIFY|DELETE] markers exist in planText, pass as no-op.
	if len(planned) == 0 {
		return report
	}

	plannedKeys := make([]string, 0, len(planned))
	for k := range planned {
		plannedKeys = append(plannedKeys, k)
	}
	sort.Strings(plannedKeys)

	for _, file := range plannedKeys {
		pAct := planned[file]
		aAct, exists := actual[file]
		if !exists {
			report.Violations = append(report.Violations, PlanDiffViolation{
				Type:          ViolationMissingPlannedChange,
				File:          file,
				PlannedAction: pAct,
				Message:       fmt.Sprintf("planned [%s] %s is missing from actual git diff", pAct, file),
			})
			continue
		}
		if pAct != aAct {
			report.Violations = append(report.Violations, PlanDiffViolation{
				Type:          ViolationActionMismatch,
				File:          file,
				PlannedAction: pAct,
				ActualAction:  aAct,
				Message:       fmt.Sprintf("planned [%s] %s but actual git diff action is [%s]", pAct, file, aAct),
			})
		}
	}

	actualKeys := make([]string, 0, len(actual))
	for k := range actual {
		actualKeys = append(actualKeys, k)
	}
	sort.Strings(actualKeys)

	for _, file := range actualKeys {
		aAct := actual[file]
		if _, plannedOK := planned[file]; !plannedOK {
			report.Violations = append(report.Violations, PlanDiffViolation{
				Type:         ViolationUnplannedChange,
				File:         file,
				ActualAction: aAct,
				Message:      fmt.Sprintf("unplanned [%s] on %s not declared in plan [NEW|MODIFY|DELETE] manifest", aAct, file),
			})
		}
	}

	if len(report.Violations) > 0 {
		report.OK = false
		first := report.Violations[0]
		report.JITBaton = fmt.Sprintf("[jit:baton:PLAN_DIFF_MISMATCH] %s (%s). Reconcile working tree with planned [NEW|MODIFY|DELETE] scope before re-running './bin/g8s verify'.", first.Type, first.File)
	}
	return report
}

// AssertionGuardFinding represents a detected test-weakening violation (#694).
type AssertionGuardFinding struct {
	RuleID            string `json:"rule_id"`
	File              string `json:"file"`
	RemovedAssertions int    `json:"removed_assertions"`
	AddedAssertions   int    `json:"added_assertions"`
	InjectedSkips     int    `json:"injected_skips"`
	Message           string `json:"message"`
}

// AssertionGuardReport summarizes the result of VerifyTestAssertionsNotWeakened (#694).
type AssertionGuardReport struct {
	OK       bool                    `json:"ok"`
	Findings []AssertionGuardFinding `json:"findings,omitempty"`
	JITBaton string                  `json:"jit_baton,omitempty"`
}

var (
	testAssertionTokenRegex = regexp.MustCompile(`(?:\bt\.(?:Fatal|Fatalf|Error|Errorf|FailNow)\s*\(|\b(?:require|assert)\.[A-Z][A-Za-z0-9_]*\s*\(|\bexpect\s*\(|\bassert_[a-z_]+\s*\(|\bpytest\.raises\s*\()`)
	testSkipTokenRegex      = regexp.MustCompile(`(?:\bt\.Skip(?:f|Now)?\s*\(|\b(?:it|test|describe)\.skip\s*\(|@pytest\.mark\.skip\b|\bskip\s*\(\s*["'])`)
)

func isRecognizedTestFile(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	return strings.HasSuffix(p, "_test.go") ||
		strings.HasSuffix(p, ".test.ts") ||
		strings.HasSuffix(p, ".spec.ts") ||
		strings.HasSuffix(p, ".test.js") ||
		strings.HasSuffix(p, ".spec.js") ||
		strings.HasPrefix(filepath.Base(p), "test_") && strings.HasSuffix(p, ".py")
}

// VerifyTestAssertionsNotWeakened inspects a unified git diff across test files
// to block Catch-Intent test weakening (deleting assertions or injecting t.Skip/it.skip
// to make failing tests pass) (#694).
func VerifyTestAssertionsNotWeakened(unifiedDiff string) AssertionGuardReport {
	type fileDelta struct {
		removed int
		added   int
		skips   int
	}
	deltas := make(map[string]*fileDelta)
	var fileOrder []string

	scanner := bufio.NewScanner(strings.NewReader(unifiedDiff))
	var currentFile string

	ensureFile := func(f string) *fileDelta {
		if d, ok := deltas[f]; ok {
			return d
		}
		d := &fileDelta{}
		deltas[f] = d
		fileOrder = append(fileOrder, f)
		return d
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++ ") {
			raw := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			if raw == "/dev/null" {
				continue
			}
			raw = strings.TrimPrefix(raw, "b/")
			currentFile = filepath.ToSlash(filepath.Clean(raw))
			continue
		}
		if strings.HasPrefix(line, "--- ") {
			raw := strings.TrimSpace(strings.TrimPrefix(line, "--- "))
			if raw != "/dev/null" {
				raw = strings.TrimPrefix(raw, "a/")
				currentFile = filepath.ToSlash(filepath.Clean(raw))
			}
			continue
		}
		if currentFile == "" || !isRecognizedTestFile(currentFile) {
			continue
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			body := strings.TrimSpace(line[1:])
			if strings.HasPrefix(body, "//") || strings.HasPrefix(body, "#") {
				continue
			}
			if testAssertionTokenRegex.MatchString(body) {
				ensureFile(currentFile).removed++
			}
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			body := strings.TrimSpace(line[1:])
			if strings.HasPrefix(body, "//") || strings.HasPrefix(body, "#") {
				continue
			}
			if testAssertionTokenRegex.MatchString(body) {
				ensureFile(currentFile).added++
			}
			if testSkipTokenRegex.MatchString(body) {
				ensureFile(currentFile).skips++
			}
		}
	}

	report := AssertionGuardReport{OK: true}
	for _, f := range fileOrder {
		d := deltas[f]
		if d.skips > 0 {
			report.Findings = append(report.Findings, AssertionGuardFinding{
				RuleID:            "TIER0-TEST-SKIP-INJECTED",
				File:              f,
				RemovedAssertions: d.removed,
				AddedAssertions:   d.added,
				InjectedSkips:     d.skips,
				Message:           fmt.Sprintf("injected %d test skip call(s) in %s to bypass verification", d.skips, f),
			})
		}
		if d.removed > d.added {
			report.Findings = append(report.Findings, AssertionGuardFinding{
				RuleID:            "TIER0-TEST-ASSERTION-WEAKENED",
				File:              f,
				RemovedAssertions: d.removed,
				AddedAssertions:   d.added,
				InjectedSkips:     d.skips,
				Message:           fmt.Sprintf("test assertions weakened in %s (removed %d assertion(s), added %d) — fix production code instead of deleting test assertions", f, d.removed, d.added),
			})
		}
	}

	if len(report.Findings) > 0 {
		report.OK = false
		first := report.Findings[0]
		report.JITBaton = fmt.Sprintf("[jit:baton:TEST_ASSERTION_GUARD] %s in %s. Restore deleted test assertions and fix the underlying production bug.", first.RuleID, first.File)
	}
	return report
}

// EvidenceProvenanceReport summarizes the result of VerifyEvidenceProvenance (#696).
type EvidenceProvenanceReport struct {
	OK               bool     `json:"ok"`
	HasConclusion    bool     `json:"has_conclusion"`
	ProvenanceTags   []string `json:"provenance_tags,omitempty"`
	FileLineCited    bool     `json:"file_line_cited"`
	ViolationMessage string   `json:"violation_message,omitempty"`
	JITBaton         string   `json:"jit_baton,omitempty"`
}

var (
	conclusionClaimRegex = regexp.MustCompile(`(?i)(?:status\s*:\s*(?:fixed|verified|resolved|complete|done)|result\s*:\s*(?:pass|passed|success)|root\s+cause\s*:|verdict\s*:\s*(?:pass|passed|approved))`)
	provenanceTagRegex   = regexp.MustCompile(`\[\s*(RUNTIME|CODE|TEST|CONFIG|LOG|DB)\s*\]`)
	fileLineCiteRegex    = regexp.MustCompile(`[A-Za-z0-9_./-]+\.[A-Za-z0-9_]+:\d+`)
)

// VerifyEvidenceProvenance checks that conclusion claims in verification reports
// carry both a provenance tag ([RUNTIME], [CODE], [TEST], [CONFIG], [LOG]) and
// a concrete file:line citation (#696).
func VerifyEvidenceProvenance(text string) EvidenceProvenanceReport {
	hasConclusion := conclusionClaimRegex.MatchString(text)
	tagMatches := provenanceTagRegex.FindAllStringSubmatch(text, -1)
	seenTags := make(map[string]bool)
	var tags []string
	for _, m := range tagMatches {
		if len(m) >= 2 {
			t := strings.ToUpper(strings.TrimSpace(m[1]))
			if !seenTags[t] {
				seenTags[t] = true
				tags = append(tags, t)
			}
		}
	}
	hasFileLine := fileLineCiteRegex.MatchString(text)

	report := EvidenceProvenanceReport{
		OK:             true,
		HasConclusion:  hasConclusion,
		ProvenanceTags: tags,
		FileLineCited:  hasFileLine,
	}

	if hasConclusion && (len(tags) == 0 || !hasFileLine) {
		report.OK = false
		var missing []string
		if len(tags) == 0 {
			missing = append(missing, "provenance tag ([RUNTIME], [CODE], [TEST], [CONFIG], [LOG])")
		}
		if !hasFileLine {
			missing = append(missing, "concrete file:line citation (e.g. pkg/file.go:42)")
		}
		report.ViolationMessage = fmt.Sprintf("conclusion claim lacks mandatory %s", strings.Join(missing, " and "))
		report.JITBaton = "[jit:baton:EVIDENCE_PROVENANCE_REQUIRED] Attach [RUNTIME]/[CODE]/[TEST] tag and exact file:line citation to every conclusion claim."
	}
	return report
}

// DocConsumerReport summarizes the result of VerifyDocConsumerFrontmatter (#696).
type DocConsumerReport struct {
	OK               bool     `json:"ok"`
	DeclaredConsumer []string `json:"declared_consumer,omitempty"`
	MissingConsumers []string `json:"missing_consumers,omitempty"`
	ViolationMessage string   `json:"violation_message,omitempty"`
}

// VerifyDocConsumerFrontmatter verifies that a specification/governance markdown file
// declares a `consumer:` field in YAML frontmatter and that every declared consumer
// file exists under repoRoot (#696 — No Dead Prose Without a Machine Consumer).
func VerifyDocConsumerFrontmatter(docText string, repoRoot string) DocConsumerReport {
	trimmed := strings.TrimSpace(docText)
	if !strings.HasPrefix(trimmed, "---") {
		return DocConsumerReport{
			OK:               false,
			ViolationMessage: "missing YAML frontmatter with 'consumer:' declaration (dead-prose guard)",
		}
	}
	rest := trimmed[3:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return DocConsumerReport{
			OK:               false,
			ViolationMessage: "unclosed YAML frontmatter block",
		}
	}
	frontmatter := rest[:endIdx]

	var consumers []string
	lines := strings.Split(frontmatter, "\n")
	inConsumerList := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "consumer:") || strings.HasPrefix(line, "consumers:") {
			val := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			val = strings.Trim(val, `"'[]`)
			if val != "" {
				for _, part := range strings.Split(val, ",") {
					c := strings.Trim(strings.TrimSpace(part), `"'`)
					if c != "" {
						consumers = append(consumers, c)
					}
				}
				inConsumerList = false
			} else {
				inConsumerList = true
			}
			continue
		}
		if inConsumerList {
			if strings.HasPrefix(line, "- ") {
				c := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "- ")), `"'`)
				if c != "" {
					consumers = append(consumers, c)
				}
			} else if strings.Contains(line, ":") {
				inConsumerList = false
			}
		}
	}

	if len(consumers) == 0 {
		return DocConsumerReport{
			OK:               false,
			ViolationMessage: "frontmatter lacks 'consumer:' field binding document to code or test",
		}
	}

	report := DocConsumerReport{
		OK:               true,
		DeclaredConsumer: consumers,
	}
	if strings.TrimSpace(repoRoot) != "" {
		for _, c := range consumers {
			full := filepath.Join(repoRoot, filepath.FromSlash(c))
			if _, err := os.Stat(full); err != nil {
				report.MissingConsumers = append(report.MissingConsumers, c)
			}
		}
		if len(report.MissingConsumers) > 0 {
			report.OK = false
			report.ViolationMessage = fmt.Sprintf("declared consumer file(s) do not exist on disk: %s", strings.Join(report.MissingConsumers, ", "))
		}
	}
	return report
}
