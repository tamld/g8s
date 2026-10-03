package review

import (
	"strings"
	"testing"
)

func TestExtractJSON(t *testing.T) {
	rawWithMarkdown := "Here is my review:\n```json\n{\n  \"findings\": []\n}\n```\nHope it helps!"
	extracted := ExtractJSON(rawWithMarkdown)
	if extracted != "{\n  \"findings\": []\n}" {
		t.Errorf("unexpected extracted JSON: %s", extracted)
	}

	rawNaked := "prefix {\"findings\": [{\"line\": 10}]} suffix"
	extractedNaked := ExtractJSON(rawNaked)
	if extractedNaked != "{\"findings\": [{\"line\": 10}]}" {
		t.Errorf("unexpected naked extraction: %s", extractedNaked)
	}
}

func TestParseFindings(t *testing.T) {
	raw := `{
		"findings": [
			{
				"line": 42,
				"severity": "critical",
				"category": "CONCURRENCY",
				"message": "Data race on shared map",
				"suggestion": "Protect with sync.Mutex"
			}
		]
	}`

	findings, err := ParseFindings(raw, "main.go")
	if err != nil {
		t.Fatalf("ParseFindings failed: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.File != "main.go" {
		t.Errorf("file = %s, expected main.go", f.File)
	}
	if f.Line != 42 {
		t.Errorf("line = %d, expected 42", f.Line)
	}
	if f.Severity != SeverityCritical {
		t.Errorf("severity = %s, expected CRITICAL", f.Severity)
	}
}

func TestReduceStrictVsNonStrict(t *testing.T) {
	findings := []Finding{
		{
			File:     "auth.go",
			Line:     15,
			Severity: SeverityHigh,
			Message:  "Missing rate limit",
		},
		{
			File:     "db.go",
			Line:     100,
			Severity: SeverityLow,
			Message:  "Styling suggestion",
		},
	}

	// Normal mode (only CRITICAL fails) -> Should Pass
	summaryNormal := Reduce(42, findings, false)
	if !summaryNormal.Passed {
		t.Errorf("expected normal mode to pass with HIGH and LOW findings")
	}
	if summaryNormal.Verdict != "APPROVED" {
		t.Errorf("expected APPROVED, got %s", summaryNormal.Verdict)
	}

	// Strict mode (HIGH or CRITICAL fails) -> Should Fail
	summaryStrict := Reduce(42, findings, true)
	if summaryStrict.Passed {
		t.Errorf("expected strict mode to fail with HIGH finding")
	}
	if summaryStrict.Verdict != "CHANGES_REQUESTED" {
		t.Errorf("expected CHANGES_REQUESTED, got %s", summaryStrict.Verdict)
	}

	// Test sorting: HIGH must come before LOW
	if summaryStrict.Findings[0].Severity != SeverityHigh {
		t.Errorf("expected first finding to be HIGH")
	}
}

func TestFormatMarkdown(t *testing.T) {
	findings := []Finding{
		{
			File:       "auth.go",
			Line:       20,
			Severity:   SeverityCritical,
			Category:   "SECURITY",
			Message:    "Hardcoded credential",
			Suggestion: "Use environment variable",
		},
	}

	summary := Reduce(10, findings, false)
	md := FormatMarkdown(summary)

	if !strings.Contains(md, "CHANGES REQUESTED") {
		t.Errorf("expected CHANGES REQUESTED title in markdown")
	}
	if !strings.Contains(md, "🔴 CRITICAL") {
		t.Errorf("expected red icon for critical severity")
	}
	if !strings.Contains(md, "`auth.go`") {
		t.Errorf("expected file code formatting")
	}
}

func TestExtractJSON_NoBraces(t *testing.T) {
	raw := "plain text with no braces"
	if got := ExtractJSON(raw); got != raw {
		t.Errorf("expected %q, got %q", raw, got)
	}

	invertedBraces := "text } before {"
	if got := ExtractJSON(invertedBraces); got != invertedBraces {
		t.Errorf("expected %q, got %q", invertedBraces, got)
	}
}

func TestParseFindings_EdgeCases(t *testing.T) {
	// Empty string
	findings, err := ParseFindings("", "default.go")
	if err != nil || findings != nil {
		t.Errorf("expected nil findings and nil error for empty string, got: %v, %v", findings, err)
	}

	// Direct slice []Finding inside code fence
	sliceRaw := "```json\n[\n\t{\"file\": \"custom.go\", \"line\": 10, \"severity\": \"medium\", \"message\": \"msg\"}\n]\n```"
	findings, err = ParseFindings(sliceRaw, "default.go")
	if err != nil {
		t.Fatalf("unexpected error for slice input: %v", err)
	}
	if len(findings) != 1 || findings[0].File != "custom.go" || findings[0].Severity != SeverityMedium {
		t.Errorf("unexpected parsed findings: %+v", findings)
	}

	// Invalid JSON
	_, err = ParseFindings("not json { at all", "default.go")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReduce_SortingAndSeverities(t *testing.T) {
	findings := []Finding{
		{File: "b.go", Line: 20, Severity: SeverityMedium, Message: "m1"},
		{File: "a.go", Line: 5, Severity: SeverityMedium, Message: "m2"},
		{File: "a.go", Line: 2, Severity: SeverityMedium, Message: "m3"},
		{File: "z.go", Line: 1, Severity: SeverityLow, Message: "l1"},
		{File: "y.go", Line: 1, Severity: SeverityInfo, Message: "i1"},
		{File: "x.go", Line: 1, Severity: Severity("UNKNOWN"), Message: "u1"},
		{File: "c.go", Line: 1, Severity: SeverityCritical, Message: "c1"},
	}

	summary := Reduce(100, findings, false)
	if summary.CriticalCount != 1 {
		t.Errorf("expected 1 critical, got %d", summary.CriticalCount)
	}
	if summary.MediumCount != 3 {
		t.Errorf("expected 3 medium, got %d", summary.MediumCount)
	}
	if summary.LowCount != 2 { // Low and Info both counted in LowCount
		t.Errorf("expected 2 low, got %d", summary.LowCount)
	}
	if summary.Passed {
		t.Errorf("expected review with critical to fail")
	}

	// Order: Critical first, then Medium sorted by file then line
	if summary.Findings[0].Severity != SeverityCritical {
		t.Errorf("expected first finding to be Critical, got %s", summary.Findings[0].Severity)
	}
	// Among medium: a.go:2, then a.go:5, then b.go:20
	if summary.Findings[1].File != "a.go" || summary.Findings[1].Line != 2 {
		t.Errorf("expected a.go:2 second, got %s:%d", summary.Findings[1].File, summary.Findings[1].Line)
	}
	if summary.Findings[2].File != "a.go" || summary.Findings[2].Line != 5 {
		t.Errorf("expected a.go:5 third, got %s:%d", summary.Findings[2].File, summary.Findings[2].Line)
	}
	if summary.Findings[3].File != "b.go" || summary.Findings[3].Line != 20 {
		t.Errorf("expected b.go:20 fourth, got %s:%d", summary.Findings[3].File, summary.Findings[3].Line)
	}
}

func TestFormatMarkdown_ApprovedAndIcons(t *testing.T) {
	// Empty findings -> Approved markdown with empty message
	emptySummary := Reduce(1, nil, true)
	emptyMd := FormatMarkdown(emptySummary)
	if !strings.Contains(emptyMd, "APPROVED") {
		t.Errorf("expected APPROVED in markdown")
	}
	if !strings.Contains(emptyMd, "No defects or security vulnerabilities identified") {
		t.Errorf("expected no defects message")
	}

	// Markdown with multiple severities and pipe characters to escape
	findings := []Finding{
		{File: "auth.go", Line: 1, Severity: SeverityHigh, Category: "SEC", Message: "pipe | in message", Suggestion: "fix | now"},
		{File: "db.go", Line: 2, Severity: SeverityMedium, Category: "PERF", Message: "medium issue", Suggestion: "opt"},
		{File: "ui.go", Line: 3, Severity: SeverityLow, Category: "CORR", Message: "low issue", Suggestion: "clean"},
		{File: "doc.go", Line: 4, Severity: Severity("OTHER"), Category: "OTHER", Message: "other issue", Suggestion: "doc"},
	}
	summary := Reduce(2, findings, false)
	md := FormatMarkdown(summary)
	if !strings.Contains(md, "🟠 HIGH") {
		t.Errorf("expected high icon 🟠")
	}
	if !strings.Contains(md, "🟡 MEDIUM") {
		t.Errorf("expected medium icon 🟡")
	}
	if !strings.Contains(md, "🔵 LOW") {
		t.Errorf("expected low icon 🔵")
	}
	if !strings.Contains(md, "⚪ OTHER") {
		t.Errorf("expected default icon ⚪")
	}
	if !strings.Contains(md, "pipe \\| in message") {
		t.Errorf("expected escaped pipe in message")
	}
	if !strings.Contains(md, "fix \\| now") {
		t.Errorf("expected escaped pipe in suggestion")
	}
}
