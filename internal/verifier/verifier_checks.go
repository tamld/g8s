package verifier

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/lessons"
	"github.com/tamld/g8s/internal/pathutil"
)

// Status indicates the enforcement mode of a verifier class.

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
		if len(tok) < 4 {
			return
		}
		switch tok {
		case "with", "from", "that", "this", "fail", "failed", "error", "test", "exit", "code", "file", "found", "such", "stat", "runner", "script", "docs", "under", "into", "when":
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

	interp := "bash"
	cleanScript := strings.TrimSpace(scriptPath)
	if strings.HasPrefix(cleanScript, "node ") {
		interp = "node"
		cleanScript = strings.TrimSpace(strings.TrimPrefix(cleanScript, "node "))
	} else if strings.HasPrefix(cleanScript, "python3 ") {
		interp = "python3"
		cleanScript = strings.TrimSpace(strings.TrimPrefix(cleanScript, "python3 "))
	} else if strings.HasPrefix(cleanScript, "python ") {
		interp = "python"
		cleanScript = strings.TrimSpace(strings.TrimPrefix(cleanScript, "python "))
	} else if strings.HasPrefix(cleanScript, "bash ") {
		interp = "bash"
		cleanScript = strings.TrimSpace(strings.TrimPrefix(cleanScript, "bash "))
	} else if strings.HasPrefix(cleanScript, "sh ") {
		interp = "sh"
		cleanScript = strings.TrimSpace(strings.TrimPrefix(cleanScript, "sh "))
	}

	if interp == "bash" {
		lower := strings.ToLower(cleanScript)
		if strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".cjs") {
			interp = "node"
		} else if strings.HasSuffix(lower, ".py") {
			interp = "python3"
		}
	}

	// Check if script exists if repoRoot is set
	resolvedPath := cleanScript
	if v.repoRoot != "" && !filepath.IsAbs(cleanScript) {
		resolvedPath = filepath.Join(v.repoRoot, cleanScript)
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

	stdout, stderr, exitCode, err := runner(timeoutCtx, interp, cleanScript)
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
