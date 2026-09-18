// Package shared contains shared types used across worker, supervisor, and orchestrator packages.
package shared

// ResultAcceptance tracks the acceptance decision for a worker result
type ResultAcceptance struct {
	Accepted      bool          `json:"accepted"`
	Reason        string        `json:"reason"`
	ErrorTracking ErrorTracking `json:"error_tracking"`
	ContractCheck ContractCheck `json:"contract_check"`
}

// ErrorTracking captures structured error information
type ErrorTracking struct {
	ErrorCode    string            `json:"error_code"`
	ErrorMessage string            `json:"error_message"`
	Context      map[string]string `json:"context,omitempty"`
	Retryable    bool              `json:"retryable"`
	Severity     string            `json:"severity"` // "low", "medium", "high", "critical"
}

// ContractCheck validates worker output against the task contract
type ContractCheck struct {
	Passed          bool     `json:"passed"`
	Violations      []string `json:"violations,omitempty"`
	OutputSizeBytes int64    `json:"output_size_bytes"`
	DurationSecs    int      `json:"duration_secs"`
	ExitCode        int      `json:"exit_code"`
}

// DenialReason represents the reason a task was denied/blocked by the worker
type DenialReason string

const (
	DenialReasonScopeViolation    DenialReason = "scope_violation"
	DenialReasonPolicyViolation   DenialReason = "policy_violation"
	DenialReasonResourceExhausted DenialReason = "resource_exhausted"
	DenialReasonInvalidInput      DenialReason = "invalid_input"
	DenialReasonDependencyFailed  DenialReason = "dependency_failed"
)
