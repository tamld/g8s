package reflection

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

var ErrPacketUnscoped = errors.New("E_REFLEX_PACKET_UNSCOPED")

// PacketOptions bounds actuation and carries queue routing preferences.
type PacketOptions struct {
	MaxPackets int
	Provider   string
	Model      string
	Effort     string
	Role       string
}

// WorkPacketSpec is an evidence-bound queue proposal, not mutation authority.
type WorkPacketSpec struct {
	ReceiptID      string   `json:"receipt_id"`
	Artifact       string   `json:"artifact"`
	Verdict        string   `json:"verdict"`
	EvidenceRefs   []string `json:"evidence_refs"`
	Prompt         string   `json:"prompt"`
	IdempotencyKey string   `json:"idempotency_key"`
	AllowedPaths   []string `json:"allowed_paths"`
	Permission     string   `json:"permission"`
	Timeout        string   `json:"timeout"`
	MaxAttempts    int      `json:"max_attempts"`
	Provider       string   `json:"provider,omitempty"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	Role           string   `json:"role"`
}

// BuildWorkPackets performs no I/O. PruneList is ignored: S4 owns prune actuation.
func BuildWorkPackets(rcpt *ProcessReflectionReceipt, opts PacketOptions) ([]WorkPacketSpec, error) {
	if rcpt == nil {
		return nil, errors.New("reflection receipt is nil")
	}
	for i, artifact := range rcpt.AddList {
		if strings.TrimSpace(artifact) == "" {
			return nil, fmt.Errorf("%w: add_list[%d]", ErrPacketUnscoped, i)
		}
	}
	if err := rcpt.Validate(); err != nil {
		return nil, err
	}
	if opts.MaxPackets < 0 {
		return nil, errors.New("MaxPackets must be nonnegative")
	}
	if opts.MaxPackets == 0 {
		opts.MaxPackets = 5
	}
	if opts.Role == "" {
		opts.Role = "implementer"
	}
	packets := make([]WorkPacketSpec, 0, min(len(rcpt.AddList), opts.MaxPackets))
	for i, artifact := range rcpt.AddList {
		var judgments, refs []string
		for _, v := range rcpt.Verdicts {
			if v.ArtifactPath == artifact {
				judgments = append(judgments, v.Dimension+": "+v.Verdict)
				refs = append(refs, v.EvidenceRefs...)
			}
		}
		if len(refs) == 0 {
			return nil, fmt.Errorf("%w: add_list[%d] has no matching verdict", ErrUnevidencedVerdict, i)
		}
		// Validate all additions, including those beyond the actuation cap.
		if i >= opts.MaxPackets {
			continue
		}
		verdict := strings.Join(judgments, "; ")
		prompt := fmt.Sprintf("Review reflection receipt %s and re-verify artifact %s. Verdict: %s. Evidence refs: %s. Implement the evidenced addition within the allowed scope; re-verify the evidence before changing files.", rcpt.ReceiptID, artifact, verdict, strings.Join(refs, ", "))
		packets = append(packets, WorkPacketSpec{
			ReceiptID: rcpt.ReceiptID, Artifact: artifact, Verdict: verdict,
			EvidenceRefs: refs, Prompt: prompt,
			IdempotencyKey: fmt.Sprintf("reflex-%s-%d", rcpt.ReceiptID, i),
			AllowedPaths:   []string{artifact, path.Dir(artifact) + "/"},
			Permission:     "workspace_write", Timeout: "45m", MaxAttempts: 1,
			Provider: opts.Provider, Model: opts.Model, Effort: opts.Effort, Role: opts.Role,
		})
	}
	return packets, nil
}
