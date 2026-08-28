// Package supervisor — escalator.go assembles the human-actionable
// escalation payload from an exhausted approach×attempt loop.
package supervisor

import (
	"encoding/json"
	"fmt"

	"github.com/tamld/g8s/internal/orchestrator"
)

// Escalation is the JSON payload emitted to a human or a higher-tier agent
// when the supervisor gives up. JSON tags are part of the public schema (the
// brain ingests them verbatim); do not rename keys without an OpenSpec delta.
type Escalation struct {
	TaskID              string   `json:"task_id"`
	Trigger             string   `json:"trigger"`
	EnvelopeSummary     string   `json:"envelope_summary"`
	ApproachesTried     int      `json:"approaches_tried"`
	TotalAttempts       int      `json:"total_attempts"`
	FailedReceiptIDs    []string `json:"failed_receipt_ids"`
	RCASummary          string   `json:"rca_summary"`
	LastDiffSummary     string   `json:"last_diff_summary"`
	RecommendedAction   string   `json:"recommended_human_action"`
}

// BuildEscalation reduces the supervisor's run history into one Escalation
// record. taskID is the supervisor task; supTaskID is currently unused but
// kept in the signature for the WU3 supervisor_tables migration to wire in
// without breaking callers. lastReceipt may be nil for pre-attempt failures.
func BuildEscalation(
	taskID string,
	supTaskID string,
	trigger string,
	attempts []AttemptRecord,
	lastReceipt *orchestrator.Receipt,
	rca RCARecord,
) Escalation {
	_ = supTaskID // reserved for WU3 supervisor_tables FK; keep in signature

	approaches := 0
	totalAttempts := len(attempts)
	failedIDs := make([]string, 0, len(attempts))
	for _, a := range attempts {
		if a.ApproachIdx+1 > approaches {
			approaches = a.ApproachIdx + 1
		}
		if a.Receipt.TaskID != "" && !a.Receipt.OK {
			failedIDs = append(failedIDs, a.Receipt.TaskID)
		}
	}

	lastDiff := ""
	if lastReceipt != nil {
		lastDiff = fmt.Sprintf("commit=%s files=%d violations=%d",
			lastReceipt.CommitSHA, len(lastReceipt.FilesModified), len(lastReceipt.ScopeViolations))
	}

	recommended := "review evidence and re-scope"
	if rca.Confidence >= 0.6 {
		recommended = "inspect RCA summary, then either re-scope the task or escalate to a higher-tier Brain"
	} else {
		recommended = "human input required: RCA confidence below threshold"
	}

	return Escalation{
		TaskID:            taskID,
		Trigger:           trigger,
		EnvelopeSummary:   fmt.Sprintf("attempts=%d approaches=%d rca_confidence=%.2f", totalAttempts, approaches, rca.Confidence),
		ApproachesTried:   approaches,
		TotalAttempts:     totalAttempts,
		FailedReceiptIDs:  failedIDs,
		RCASummary:        fmt.Sprintf("symptom=%s root_cause=%s", rca.Symptom, rca.RootCause),
		LastDiffSummary:   lastDiff,
		RecommendedAction: recommended,
	}
}

// MarshalJSON re-exports encoding/json so callers don't need to import it.
func (e Escalation) MarshalJSON() ([]byte, error) {
	type alias Escalation
	return json.Marshal(alias(e))
}
