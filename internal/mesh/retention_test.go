package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func retentionSetupPair(t *testing.T, s *Store) {
	t.Helper()
	msgMustExec(t, s, "register", map[string]any{"node_id": "sender", "incarnation": "s1"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "rec", "incarnation": "r1"})
}

func retentionSend(t *testing.T, s *Store, msgID string) {
	t.Helper()
	msgMustExec(t, s, "send", map[string]any{
		"node_id": "sender", "incarnation": "s1", "message_id": msgID,
		"to_node": "rec", "to_incarnation": "r1", "kind": "k",
		"payload": map[string]any{"id": msgID},
	})
}

func retentionAck(t *testing.T, s *Store, msgID string) {
	t.Helper()
	msgMustExec(t, s, "ack", map[string]any{"node_id": "rec", "incarnation": "r1", "message_id": msgID})
}

func retentionMessageCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

func retentionClaimCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM claims`).Scan(&n); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	return n
}

func TestMeshRetentionPrune(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	retentionSetupPair(t, s)

	// ACKed, older than 168h cutoff (uses created_at; no ack_at column).
	s.now = func() time.Time { return now.Add(-200 * time.Hour) }
	retentionSend(t, s, "old_acked")
	retentionAck(t, s, "old_acked")

	// ACKed, recent.
	s.now = func() time.Time { return now.Add(-24 * time.Hour) }
	retentionSend(t, s, "recent_acked")
	retentionAck(t, s, "recent_acked")

	// UnACKed but old.
	s.now = func() time.Time { return now.Add(-200 * time.Hour) }
	retentionSend(t, s, "old_pending")

	s.now = func() time.Time { return now }
	pct := 50.0
	msgMustExec(t, s, "quota-publish", map[string]any{
		"node_id": "sender", "incarnation": "s1",
		"observation": QuotaObservation{
			Source: "src", ObservedAt: now.Format(time.RFC3339),
			Windows: []QuotaWindow{{Name: "w", WindowMinutes: 60, UsedPercent: &pct, RemainingPercent: &pct, Status: "observed"}},
		},
	})
	msgMustExec(t, s, "claim", map[string]any{
		"node_id": "sender", "incarnation": "s1", "task_id": "task_fence",
		"resource": "r1", "paths": []string{"a.go"},
		"context_revision": 0, "lease_seconds": 120,
	})
	if retentionClaimCount(t, s) != 1 {
		t.Fatal("expected one claim before prune")
	}

	dryRaw := msgMustExec(t, s, "prune", map[string]any{"older_than": "168h", "dry_run": true})
	var dry msgPruneResp
	if err := json.Unmarshal(dryRaw, &dry); err != nil || !dry.DryRun || dry.Pruned != 1 {
		t.Fatalf("dry_run: %+v err=%v", dry, err)
	}
	if retentionMessageCount(t, s) != 3 {
		t.Fatalf("dry_run must not delete rows, got %d messages", retentionMessageCount(t, s))
	}

	pruneRaw := msgMustExec(t, s, "prune", map[string]any{"older_than": "168h", "dry_run": false})
	var pr msgPruneResp
	if err := json.Unmarshal(pruneRaw, &pr); err != nil || pr.Pruned != 1 || pr.DryRun {
		t.Fatalf("prune: %+v err=%v", pr, err)
	}
	if _, err := time.Parse(time.RFC3339, pr.Cutoff); err != nil {
		t.Fatalf("cutoff RFC3339: %v", err)
	}
	if retentionMessageCount(t, s) != 2 {
		t.Fatalf("expected 2 messages after prune, got %d", retentionMessageCount(t, s))
	}

	var snap msgSnapResp
	if err := json.Unmarshal(msgMustExec(t, s, "snapshot", map[string]any{}), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Nodes) != 2 || len(snap.Quotas) != 1 || len(snap.Claims) != 1 {
		t.Fatalf("nodes/quotas/claims must survive prune: %+v", snap)
	}

	var ib struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(msgMustExec(t, s, "inbox", map[string]any{"node_id": "rec", "incarnation": "r1"}), &ib); err != nil || len(ib.Messages) != 1 || ib.Messages[0].MessageID != "old_pending" {
		t.Fatalf("unACKed old message kept: %+v", ib)
	}

	secondRaw := msgMustExec(t, s, "prune", map[string]any{"older_than": "168h"})
	var second msgPruneResp
	if err := json.Unmarshal(secondRaw, &second); err != nil || second.Pruned != 0 {
		t.Fatalf("second prune idempotent: %+v err=%v", second, err)
	}

	if err := msgExecErr(t, s, "prune", map[string]any{"older_than": "30m"}); !errors.Is(err, ErrInvalidPruneOlderThan) {
		t.Fatalf("expected ErrInvalidPruneOlderThan for <1h, got %v", err)
	}
	if err := msgExecErr(t, s, "prune", map[string]any{"older_than": "not-a-duration"}); !errors.Is(err, ErrInvalidPruneOlderThan) {
		t.Fatalf("expected ErrInvalidPruneOlderThan for bad duration, got %v", err)
	}
}

func TestMeshRetentionPruneDefaultOlderThan(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	retentionSetupPair(t, s)
	s.now = func() time.Time { return now.Add(-200 * time.Hour) }
	retentionSend(t, s, "old_acked")
	retentionAck(t, s, "old_acked")
	s.now = func() time.Time { return now }

	raw, err := s.Execute(context.Background(), "prune", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out msgPruneResp
	if err := json.Unmarshal(raw, &out); err != nil || out.Pruned != 1 {
		t.Fatalf("default 168h prune: %+v err=%v", out, err)
	}
}
