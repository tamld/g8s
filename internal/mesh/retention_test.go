package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func retentionTombstoneCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM message_tombstones`).Scan(&n); err != nil {
		t.Fatalf("count tombstones: %v", err)
	}
	return n
}

func retentionSendSeq(t *testing.T, s *Store, msgID string) int64 {
	t.Helper()
	raw := msgMustExec(t, s, "send", map[string]any{
		"node_id": "sender", "incarnation": "s1", "message_id": msgID,
		"to_node": "rec", "to_incarnation": "r1", "kind": "k",
		"payload": map[string]any{"id": msgID},
	})
	var out struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("send seq: %v", err)
	}
	return out.Seq
}

func retentionPendingInbox(t *testing.T, s *Store) []Message {
	t.Helper()
	var ib struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(msgMustExec(t, s, "inbox", map[string]any{"node_id": "rec", "incarnation": "r1"}), &ib); err != nil {
		t.Fatalf("inbox: %v", err)
	}
	return ib.Messages
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
	if retentionTombstoneCount(t, s) != 0 {
		t.Fatalf("dry_run must not create tombstones, got %d", retentionTombstoneCount(t, s))
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
	if retentionTombstoneCount(t, s) != 1 {
		t.Fatalf("expected 1 tombstone after prune, got %d", retentionTombstoneCount(t, s))
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

func TestMeshRetentionPruneTombstoneDedup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	retentionSetupPair(t, s)
	s.now = func() time.Time { return now.Add(-200 * time.Hour) }
	firstSeq := retentionSendSeq(t, s, "pruned_msg")
	retentionAck(t, s, "pruned_msg")

	s.now = func() time.Time { return now }
	msgMustExec(t, s, "prune", map[string]any{"older_than": "168h", "dry_run": false})
	if retentionMessageCount(t, s) != 0 {
		t.Fatalf("expected no message rows after prune, got %d", retentionMessageCount(t, s))
	}
	if retentionTombstoneCount(t, s) != 1 {
		t.Fatalf("expected one tombstone, got %d", retentionTombstoneCount(t, s))
	}

	reSeq := retentionSendSeq(t, s, "pruned_msg")
	if reSeq != firstSeq {
		t.Fatalf("idempotent resend after prune: seq %d want %d", reSeq, firstSeq)
	}
	if len(retentionPendingInbox(t, s)) != 0 {
		t.Fatal("tombstone idempotent resend must not redeliver to inbox")
	}
	// Inbox now pumps idle keepalive probes. They are independent of replay;
	// the pruned delivery must still have no physical message row.
	var replayRows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE kind NOT IN ('ping','pong')").Scan(&replayRows); err != nil {
		t.Fatal(err)
	}
	if replayRows != 0 {
		t.Fatalf("idempotent resend inserted delivery rows: %d", replayRows)
	}

	if err := msgExecErr(t, s, "send", map[string]any{
		"node_id": "sender", "incarnation": "s1", "message_id": "pruned_msg",
		"to_node": "rec", "to_incarnation": "r1", "kind": "k",
		"payload": map[string]any{"id": "pruned_msg", "different": true},
	}); err == nil || !strings.Contains(err.Error(), "ID_CONFLICT") {
		t.Fatalf("expected ID_CONFLICT after prune for different digest, got %v", err)
	}
}

func TestMeshMessageCreatedAtUTCFormat(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	retentionSetupPair(t, s)
	retentionSend(t, s, "utc_fmt")
	var createdAt string
	if err := s.db.QueryRow(`SELECT created_at FROM messages WHERE message_id = ?`, "utc_fmt").Scan(&createdAt); err != nil {
		t.Fatalf("read created_at: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		t.Fatalf("created_at %q is not RFC3339: %v", createdAt, err)
	}
	if parsed.Location() != time.UTC {
		t.Fatalf("created_at %q must be UTC, got location %v", createdAt, parsed.Location())
	}
	if createdAt != parsed.UTC().Format(time.RFC3339) {
		t.Fatalf("created_at %q must equal canonical UTC RFC3339 %q", createdAt, parsed.UTC().Format(time.RFC3339))
	}
	if !strings.HasSuffix(createdAt, "Z") {
		t.Fatalf("created_at %q must use Zulu suffix for UTC RFC3339", createdAt)
	}
}

func TestMeshRetentionPruneTombstoneDurability(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	retentionSetupPair(t, s)
	s.now = func() time.Time { return now.Add(-200 * time.Hour) }
	wantSeq := retentionSendSeq(t, s, "dur_msg")
	retentionAck(t, s, "dur_msg")
	s.now = func() time.Time { return now }
	msgMustExec(t, s, "prune", map[string]any{"older_than": "168h"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.now = func() time.Time { return now }

	gotSeq := retentionSendSeq(t, s2, "dur_msg")
	if gotSeq != wantSeq {
		t.Fatalf("after reopen: seq %d want %d", gotSeq, wantSeq)
	}
	if len(retentionPendingInbox(t, s2)) != 0 {
		t.Fatal("reopened store: tombstone must block redelivery")
	}
}
