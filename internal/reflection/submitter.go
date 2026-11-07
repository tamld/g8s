package reflection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamld/g8s/internal/controlplane"
)

// PacketSubmitter is the queue insert boundary; it grants no execution authority.
type PacketSubmitter interface {
	SubmitTask(context.Context, controlplane.SubmitTaskRequest) (*controlplane.Task, error)
}

// SubmitPackets preserves order and returns successfully queued IDs on failure.
func SubmitPackets(ctx context.Context, submitter PacketSubmitter, packets []WorkPacketSpec) ([]string, error) {
	ids := make([]string, 0, len(packets))
	for _, p := range packets {
		// The extra queue fields are required by control-plane validation and
		// persistence. A reflection receipt is provenance, not a write receipt;
		// execution still requires the worker's normal capability validation.
		payload, err := json.Marshal(map[string]any{
			"receipt_id": p.ReceiptID, "artifact": p.Artifact, "verdict": p.Verdict,
			"evidence_refs": p.EvidenceRefs, "instruction": p.Prompt,
			"prompt": p.Prompt, "permission": p.Permission, "role": p.Role,
			"provider": p.Provider, "model": p.Model, "effort": p.Effort,
			"timeout": p.Timeout, "add_dirs": []string{"."},
		})
		if err != nil {
			return ids, err
		}
		task, err := submitter.SubmitTask(ctx, controlplane.SubmitTaskRequest{
			Payload: payload, Permission: p.Permission, AllowedPaths: p.AllowedPaths,
			Role: p.Role, IdempotencyKey: p.IdempotencyKey, Timeout: p.Timeout,
			MaxAttempts: p.MaxAttempts, Provider: p.Provider, Model: p.Model, Effort: p.Effort,
			AddDirs: []string{"."},
		})
		if err != nil {
			return ids, fmt.Errorf("submit packet %s: %w", p.IdempotencyKey, err)
		}
		if task == nil || task.TaskID == "" {
			return ids, fmt.Errorf("submit packet %s returned no task id", p.IdempotencyKey)
		}
		ids = append(ids, task.TaskID)
	}
	return ids, nil
}
