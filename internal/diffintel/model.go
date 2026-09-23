// Package diffintel implements pure-Go unified diff parsing, noise pruning,
// and context slicing for high-precision, token-efficient subagent code reviews.
package diffintel

// LineType classifies a diff line.
type LineType int

const (
	LineContext LineType = iota // ' '
	LineAdd                     // '+'
	LineDelete                  // '-'
)

// DiffLine represents a single line inside a hunk with its exact file line number.
type DiffLine struct {
	Type    LineType `json:"type"`
	Content string   `json:"content"`
	OldLine int      `json:"old_line,omitempty"` // 0 if added
	NewLine int      `json:"new_line,omitempty"` // 0 if deleted
}

// Hunk represents a contiguous block of modified lines in unified diff format.
type Hunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Header   string     `json:"header,omitempty"` // Optional function / context header
	Lines    []DiffLine `json:"lines"`
}

// FileDiff represents all changes applied to a single file.
type FileDiff struct {
	OldPath   string `json:"old_path"`
	NewPath   string `json:"new_path"`
	IsNew     bool   `json:"is_new"`
	IsDeleted bool   `json:"is_deleted"`
	IsBinary  bool   `json:"is_binary"`
	Hunks     []Hunk `json:"hunks"`
}

// ReviewChunk is a bounded, context-isolated slice of diff assigned to a verifier worker.
type ReviewChunk struct {
	ID            string `json:"id"`
	FilePath      string `json:"file_path"`
	RiskLevel     string `json:"risk_level"` // "CRITICAL", "HIGH", "MEDIUM", "LOW"
	Hunks         []Hunk `json:"hunks"`
	TokenEstimate int    `json:"token_estimate"`
	FormattedBody string `json:"formatted_body"`
}
