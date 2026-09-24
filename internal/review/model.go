package review

// Severity categorizes the impact of a code review finding.
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
	SeverityInfo     Severity = "INFO"
)

// Finding represents a single actionable code review issue anchored to an exact file and line.
type Finding struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Severity   Severity `json:"severity"`
	Category   string   `json:"category"` // e.g. CONCURRENCY, SECURITY, CORRECTNESS, PERFORMANCE
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}

// ReviewSummary is the aggregated output of all verifier subagents for a PR or diff.
type ReviewSummary struct {
	PR            int       `json:"pr,omitempty"`
	Passed        bool      `json:"passed"`
	Verdict       string    `json:"verdict"` // "APPROVED" or "CHANGES_REQUESTED"
	TotalIssues   int       `json:"total_issues"`
	CriticalCount int       `json:"critical_count"`
	HighCount     int       `json:"high_count"`
	MediumCount   int       `json:"medium_count"`
	LowCount      int       `json:"low_count"`
	Findings      []Finding `json:"findings"`
	TokensSaved   int       `json:"tokens_saved,omitempty"`
	DurationMs    int64     `json:"duration_ms,omitempty"`
}
