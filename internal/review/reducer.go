package review

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type workerOutputWrapper struct {
	Findings []Finding `json:"findings"`
}

// ExtractJSON finds and extracts the JSON object from raw LLM output,
// stripping any markdown code fences if present.
func ExtractJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	// Check for ```json ... ```
	if strings.Contains(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		var jsonLines []string
		inside := false
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "```") {
				inside = !inside
				continue
			}
			if inside {
				jsonLines = append(jsonLines, l)
			}
		}
		if len(jsonLines) > 0 {
			return strings.TrimSpace(strings.Join(jsonLines, "\n"))
		}
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start != -1 && end != -1 && end > start {
		return trimmed[start : end+1]
	}
	return trimmed
}

// ParseFindings parses the raw JSON string returned by a verifier subagent.
func ParseFindings(raw string, defaultFile string) ([]Finding, error) {
	extracted := ExtractJSON(raw)
	if extracted == "" {
		return nil, nil
	}

	var wrapper workerOutputWrapper
	if err := json.Unmarshal([]byte(extracted), &wrapper); err != nil {
		// Try unmarshaling as direct slice []Finding
		var list []Finding
		if errList := json.Unmarshal([]byte(extracted), &list); errList == nil {
			wrapper.Findings = list
		} else {
			return nil, fmt.Errorf("unmarshal worker findings: %w (raw: %s)", err, extracted)
		}
	}

	for i := range wrapper.Findings {
		if wrapper.Findings[i].File == "" {
			wrapper.Findings[i].File = defaultFile
		}
		wrapper.Findings[i].Severity = Severity(strings.ToUpper(string(wrapper.Findings[i].Severity)))
	}

	return wrapper.Findings, nil
}

// severityRank maps Severity to an integer for sorting.
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

// Reduce synthesizes all worker findings into an authoritative ReviewSummary.
// If strict is true, any HIGH or CRITICAL finding fails the review.
// If strict is false, only CRITICAL findings fail the review.
func Reduce(pr int, findings []Finding, strict bool) ReviewSummary {
	var summary ReviewSummary
	summary.PR = pr
	summary.TotalIssues = len(findings)

	// Sort findings by severity desc, then file, then line
	sort.Slice(findings, func(i, j int) bool {
		rI := severityRank(findings[i].Severity)
		rJ := severityRank(findings[j].Severity)
		if rI != rJ {
			return rI > rJ
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})

	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			summary.CriticalCount++
		case SeverityHigh:
			summary.HighCount++
		case SeverityMedium:
			summary.MediumCount++
		case SeverityLow, SeverityInfo:
			summary.LowCount++
		}
	}

	summary.Findings = findings

	if strict {
		summary.Passed = summary.CriticalCount == 0 && summary.HighCount == 0
	} else {
		summary.Passed = summary.CriticalCount == 0
	}

	if summary.Passed {
		summary.Verdict = "APPROVED"
	} else {
		summary.Verdict = "CHANGES_REQUESTED"
	}

	return summary
}

// FormatMarkdown renders a formatted markdown report suitable for GitHub PR comments.
func FormatMarkdown(s ReviewSummary) string {
	var b strings.Builder
	title := "### ✅ g8s Automated Code Review: APPROVED"
	if !s.Passed {
		title = "### ❌ g8s Automated Code Review: CHANGES REQUESTED"
	}
	b.WriteString(title + "\n\n")

	fmt.Fprintf(&b, "**Summary**: %d issue(s) detected (Critical: %d, High: %d, Medium: %d, Low: %d)\n\n",
		s.TotalIssues, s.CriticalCount, s.HighCount, s.MediumCount, s.LowCount)

	if len(s.Findings) == 0 {
		b.WriteString("No defects or security vulnerabilities identified in the inspected changes.\n")
		return b.String()
	}

	b.WriteString("| Severity | File | Line | Category | Issue | Suggestion |\n")
	b.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, f := range s.Findings {
		icon := "⚪"
		switch f.Severity {
		case SeverityCritical:
			icon = "🔴"
		case SeverityHigh:
			icon = "🟠"
		case SeverityMedium:
			icon = "🟡"
		case SeverityLow:
			icon = "🔵"
		}
		cleanMsg := strings.ReplaceAll(f.Message, "|", "\\|")
		cleanSug := strings.ReplaceAll(f.Suggestion, "|", "\\|")
		fmt.Fprintf(&b, "| %s %s | `%s` | %d | %s | %s | %s |\n",
			icon, f.Severity, f.File, f.Line, f.Category, cleanMsg, cleanSug)
	}

	return b.String()
}
