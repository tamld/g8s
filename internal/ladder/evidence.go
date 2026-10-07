package ladder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	// ErrMissingRequiredField indicates that a required field is missing in an EvidencePacket.
	ErrMissingRequiredField = errors.New("missing required field in evidence packet")

	// ErrInvalidDestinationPath indicates that the destination path failed security/safety validation.
	ErrInvalidDestinationPath = errors.New("invalid evidence packet destination path")
)

// MissingRequiredFieldError provides rich details when a required field is missing in an EvidencePacket.
type MissingRequiredFieldError struct {
	Field string
}

func (e *MissingRequiredFieldError) Error() string {
	return fmt.Sprintf("evidence packet missing required field: %s", e.Field)
}

func (e *MissingRequiredFieldError) Unwrap() error {
	return ErrMissingRequiredField
}

// PathValidationError provides rich details when a destination path fails safety validation.
type PathValidationError struct {
	Path   string
	Reason string
}

func (e *PathValidationError) Error() string {
	return fmt.Sprintf("invalid evidence packet destination path %s: %s", e.Path, e.Reason)
}

func (e *PathValidationError) Unwrap() error {
	return ErrInvalidDestinationPath
}

// RungRecord encapsulates execution and classification details for one rung in the quality ladder.
type RungRecord struct {
	RungIndex    int          `json:"rung_index"`
	TaskID       string       `json:"task_id"`
	Role         string       `json:"role,omitempty"`
	Permission   string       `json:"permission,omitempty"`
	Model        string       `json:"model,omitempty"`
	Effort       string       `json:"effort,omitempty"`
	Shape        FailureShape `json:"shape"`
	Verdict      string       `json:"verdict"`
	Tokens       int          `json:"tokens"`
	InputTokens  int          `json:"input_tokens,omitempty"`
	OutputTokens int          `json:"output_tokens,omitempty"`
	ChecksFailed []string     `json:"checks_failed,omitempty"`
}

// EvidencePacket is the comprehensive cost receipt and diagnostic trail emitted on HITL.
type EvidencePacket struct {
	RootTaskID         string       `json:"root_task_id"`
	Class              string       `json:"class"`
	FinalVerdict       string       `json:"final_verdict"`
	Reason             string       `json:"reason"`
	ExactFailingChecks []string     `json:"exact_failing_checks"`
	LadderHistory      []RungRecord `json:"ladder_history"`
	TotalTokens        int          `json:"total_tokens"`
	TokenBudget        int          `json:"token_budget,omitempty"`
	TotalRungs         int          `json:"total_rungs"`
	ScopeRoot          string       `json:"scope_root,omitempty"`
	Timestamp          time.Time    `json:"timestamp"`
	ValidationError    error        `json:"-"`
}

// Validate checks that required fields (RootTaskID, Class, FinalVerdict) are present and valid.
func (p *EvidencePacket) Validate() error {
	if p.ValidationError != nil {
		return p.ValidationError
	}
	if p.RootTaskID == "" {
		return &MissingRequiredFieldError{Field: "RootTaskID"}
	}
	if p.Class == "" {
		return &MissingRequiredFieldError{Field: "Class"}
	}
	if p.FinalVerdict == "" {
		return &MissingRequiredFieldError{Field: "FinalVerdict"}
	}
	return nil
}

// BuildEvidencePacket constructs a validated EvidencePacket from the ladder state and history.
// If any required field (RootTaskID, Class, FinalVerdict) is empty, ValidationError is set to a typed error.
func BuildEvidencePacket(
	rootTaskID string,
	class string,
	finalVerdict string,
	reason string,
	exactFailingChecks []string,
	history []RungRecord,
	tokenBudget int,
) *EvidencePacket {
	var validationErr error
	if rootTaskID == "" {
		validationErr = &MissingRequiredFieldError{Field: "RootTaskID"}
	} else if class == "" {
		validationErr = &MissingRequiredFieldError{Field: "Class"}
	} else if finalVerdict == "" {
		validationErr = &MissingRequiredFieldError{Field: "FinalVerdict"}
	}

	totalTokens := 0
	for _, r := range history {
		totalTokens += r.Tokens
	}

	checks := make([]string, len(exactFailingChecks))
	copy(checks, exactFailingChecks)

	hist := make([]RungRecord, len(history))
	copy(hist, history)

	return &EvidencePacket{
		RootTaskID:         rootTaskID,
		Class:              class,
		FinalVerdict:       finalVerdict,
		Reason:             reason,
		ExactFailingChecks: checks,
		LadderHistory:      hist,
		TotalTokens:        totalTokens,
		TokenBudget:        tokenBudget,
		TotalRungs:         len(history),
		Timestamp:          time.Now().UTC(),
		ValidationError:    validationErr,
	}
}

// NewEvidencePacket constructs an EvidencePacket and returns an error if validation fails.
func NewEvidencePacket(
	rootTaskID string,
	class string,
	finalVerdict string,
	reason string,
	exactFailingChecks []string,
	history []RungRecord,
	tokenBudget int,
) (*EvidencePacket, error) {
	p := BuildEvidencePacket(rootTaskID, class, finalVerdict, reason, exactFailingChecks, history, tokenBudget)
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, nil
}

// WriteJSON writes the EvidencePacket as indented JSON to w.
func (p *EvidencePacket) WriteJSON(w io.Writer) error {
	if err := p.Validate(); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

var deniedPathFragments = []string{
	"/.git",
	"/.env",
	"/.ssh",
	"/.gnupg",
	"/.aws",
	"/.config/gh",
	"/.npmrc",
	"/.pypirc",
	"master.key",
	"id_rsa",
	"id_ed25519",
}

func resolveExistingSymlinks(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err == nil {
		return filepath.Join(resolvedDir, base)
	}
	return path
}

func validateDestinationPath(path string, scopeRoot string) error {
	if path == "" {
		return nil
	}

	// 1. Check for directory traversal fragments
	normalizedSlash := filepath.ToSlash(path)
	for _, part := range strings.Split(normalizedSlash, "/") {
		if part == ".." {
			return &PathValidationError{
				Path:   path,
				Reason: "directory traversal ('..') is not permitted",
			}
		}
	}

	// 2. Resolve absolute and symlink paths for denied fragments & scope checks
	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		absPath = cleanPath
	}
	absPath = resolveExistingSymlinks(absPath)
	normalizedAbs := strings.ToLower(filepath.ToSlash(absPath))

	for _, fragment := range deniedPathFragments {
		if strings.Contains(normalizedAbs, fragment) {
			return &PathValidationError{
				Path:   path,
				Reason: fmt.Sprintf("denied path fragment detected: %s", fragment),
			}
		}
	}

	// Also check raw normalized path for relative denied fragments like .git/...
	rawLower := strings.ToLower(normalizedSlash)
	if strings.HasPrefix(rawLower, ".git/") || rawLower == ".git" {
		return &PathValidationError{
			Path:   path,
			Reason: "denied path fragment detected: .git",
		}
	}

	// 3. Check scope containment if ScopeRoot is declared
	if scopeRoot != "" {
		cleanRoot := filepath.Clean(scopeRoot)
		absRoot, err := filepath.Abs(cleanRoot)
		if err != nil {
			return fmt.Errorf("resolve scope root %s: %w", scopeRoot, err)
		}
		absRoot = resolveExistingSymlinks(absRoot)

		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.HasPrefix(filepath.ToSlash(rel), "../") {
			return &PathValidationError{
				Path:   path,
				Reason: fmt.Sprintf("path %s escapes declared scope root %s", path, scopeRoot),
			}
		}
	}

	return nil
}

// WriteToFile optionally writes the EvidencePacket as indented JSON to the target path.
// It validates the destination path against directory traversal, denied fragments,
// and containment within the declared scope root (if specified).
func (p *EvidencePacket) WriteToFile(path string) error {
	if path == "" {
		return nil
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if err := validateDestinationPath(path, p.ScopeRoot); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create evidence packet file %s: %w", path, err)
	}
	defer f.Close()

	return p.WriteJSON(f)
}
