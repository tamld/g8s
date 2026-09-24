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
