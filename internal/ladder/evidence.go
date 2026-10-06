package ladder

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

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
	Timestamp          time.Time    `json:"timestamp"`
}

// BuildEvidencePacket constructs a validated EvidencePacket from the ladder state and history.
func BuildEvidencePacket(
	rootTaskID string,
	class string,
	finalVerdict string,
	reason string,
	exactFailingChecks []string,
	history []RungRecord,
	tokenBudget int,
) *EvidencePacket {
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
	}
}

// WriteJSON writes the EvidencePacket as indented JSON to w.
func (p *EvidencePacket) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// WriteToFile optionally writes the EvidencePacket as indented JSON to the target path.
func (p *EvidencePacket) WriteToFile(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create evidence packet file %s: %w", path, err)
	}
	defer f.Close()

	return p.WriteJSON(f)
}
