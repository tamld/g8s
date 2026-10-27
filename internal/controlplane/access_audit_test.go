package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAccessAuditTransactionalAndRedacted(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	// 1. Submit a task and verify transactional task.submit audit row
	task, err := store.SubmitTask(ctx, SubmitTaskRequest{
		Payload:        json.RawMessage(`{"prompt":"audit test prompt","permission":"read_only","actor":"f0-supervisor"}`),
		IdempotencyKey: "audit-test-idem-1",
		Role:           "collector",
		Permission:     "read_only",
		Model:          "cu/composer-2.5",
		AddDirs:        []string{t.TempDir()},
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	rows, err := store.ListAccessAudit(ctx, AccessAuditFilter{
		SubjectID: task.TaskID,
	})
	if err != nil {
		t.Fatalf("ListAccessAudit after submit: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least 1 access_audit row for submitted task %s, got 0", task.TaskID)
	}
	if rows[0].Action != "task.submit" {
		t.Errorf("audit action = %q, want %q", rows[0].Action, "task.submit")
	}

	// 2. Claim & Start task and verify task.claim and task.start audit rows
	claimed, err := store.ClaimTask(ctx, "worker-audit-1", 60)
	if err != nil || claimed == nil || claimed.LeaseToken == nil {
		t.Fatalf("ClaimTask: err=%v claimed=%v", err, claimed)
	}
	if !store.StartTask(task.TaskID, "worker-audit-1", *claimed.LeaseToken) {
		t.Fatalf("StartTask returned false")
	}

	allTaskRows, err := store.ListAccessAudit(ctx, AccessAuditFilter{
		SubjectID: task.TaskID,
	})
	if err != nil {
		t.Fatalf("ListAccessAudit after claim/start: %v", err)
	}
	if len(allTaskRows) < 3 {
		t.Fatalf("expected >= 3 access_audit rows (submit, claim, start), got %d", len(allTaskRows))
	}

	// 3. Record direct access-audit entry with sensitive bearer token and verify redaction
	err = store.RecordAccessAudit(ctx, AccessAuditRecord{
		Actor:     "sec-auditor",
		Action:    "receipt.issue",
		SubjectID: "rcpt-123",
		TraceID:   "trace-abc",
		Detail:    "Authorization: Bearer sk-secret1234567890abcdef1234567890",
	})
	if err != nil {
		t.Fatalf("RecordAccessAudit: %v", err)
	}

	secRows, err := store.ListAccessAudit(ctx, AccessAuditFilter{
		Actor:  "sec-auditor",
		Action: "receipt.issue",
	})
	if err != nil {
		t.Fatalf("ListAccessAudit(sec-auditor): %v", err)
	}
	if len(secRows) != 1 {
		t.Fatalf("expected 1 receipt.issue audit row, got %d", len(secRows))
	}
	if strings.Contains(secRows[0].Detail, "sk-secret1234567890abcdef1234567890") {
		t.Errorf("expected secret token to be redacted in access_audit.detail, got %q", secRows[0].Detail)
	}
	if secRows[0].Instance == "" {
		t.Errorf("expected non-empty instance ID on access_audit record")
	}
}
