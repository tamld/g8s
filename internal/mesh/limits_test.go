package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func limitsStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	msgMustExec(t, s, "register", map[string]any{"node_id": "s", "incarnation": "i"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i"})
	return s
}

func limitSend(id string) map[string]any {
	return map[string]any{
		"node_id": "s", "incarnation": "i", "message_id": id,
		"to_node": "r", "to_incarnation": "i", "kind": "result", "payload": map[string]any{},
	}
}

func TestMeshPayloadAndInputSizeLimits(t *testing.T) {
	s := limitsStore(t)
	// The public seam caps the whole request, including JSON framing.
	base, err := json.Marshal(limitSend("envelope"))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{16384, 16385} {
		raw := append(append([]byte{}, base...), []byte(strings.Repeat(" ", size-len(base)))...)
		_, err := s.Execute(context.Background(), "send", raw)
		if size == 16384 && err != nil {
			t.Fatalf("exact input cap rejected: %v", err)
		}
		if size == 16385 && (err == nil || !strings.Contains(err.Error(), "at most 16384")) {
			t.Fatalf("oversized input accepted or wrong error: %v", err)
		}
	}
	// Exercise the payload validator directly because a 16 KiB payload plus
	// its request envelope exceeds Execute's independent input cap.
	for _, size := range []int{16383, 16384, 16385} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			req := limitSend(fmt.Sprintf("payload-%d", size))
			req["payload"] = map[string]any{"x": strings.Repeat("x", size-len(`{"x":""}`))}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			_, err = msgSend(context.Background(), s, raw)
			if size <= 16384 && err != nil {
				t.Fatalf("payload at/below cap rejected: %v", err)
			}
			if size > 16384 && (err == nil || !strings.Contains(err.Error(), "payload size")) {
				t.Fatalf("oversized payload accepted or wrong error: %v", err)
			}
		})
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 3 {
		t.Fatalf("rejected inputs changed messages: count=%d err=%v", count, err)
	}
}

// Seed boundary-adjacent rows in one transaction; the boundary crossing itself
// always goes through Execute, including its actor checks and capacity query.
func seedLimitMessages(t *testing.T, s *Store, count int, distributed bool) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO messages(message_id,sender,sender_inc,recipient,recipient_inc,kind,payload,digest,created_at)
		VALUES(?, 's', 'i', ?, 'i', 'result', '{}', 'seed', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := range count {
		recipient := "r"
		if distributed {
			recipient = fmt.Sprintf("r%d", i%10)
		}
		if _, err := stmt.Exec(fmt.Sprintf("seed-%05d", i), recipient); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestMeshPendingInboxCapacity(t *testing.T) {
	s := limitsStore(t)
	seedLimitMessages(t, s, 999, false)
	msgMustExec(t, s, "send", limitSend("last"))
	if err := msgExecErr(t, s, "send", limitSend("over")); err == nil || !strings.Contains(err.Error(), "max 1000 pending") {
		t.Fatalf("expected recipient cap: %v", err)
	}
	var pending int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE recipient='r' AND acked=0").Scan(&pending); err != nil || pending != 1000 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
	// One recipient at capacity does not block another inbox.
	other := limitSend("other")
	other["to_node"] = "s"
	msgMustExec(t, s, "send", other)
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "message_id": "last"})
	msgMustExec(t, s, "send", limitSend("over"))
}

func TestMeshTotalMessageCapacity(t *testing.T) {
	s := limitsStore(t)
	for i := range 10 {
		msgMustExec(t, s, "register", map[string]any{"node_id": fmt.Sprintf("r%d", i), "incarnation": "i"})
	}
	seedLimitMessages(t, s, 9999, true)
	msgMustExec(t, s, "send", limitSend("last"))
	if err := msgExecErr(t, s, "send", limitSend("over")); err == nil || !strings.Contains(err.Error(), "max 10000 total") {
		t.Fatalf("expected total cap: %v", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 10000 {
		t.Fatalf("total=%d err=%v", count, err)
	}
	// ACK frees inbox capacity but not stored-row capacity (prune does that).
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "message_id": "last"})
	if err := msgExecErr(t, s, "send", limitSend("over")); err == nil || !strings.Contains(err.Error(), "max 10000 total") {
		t.Fatalf("ACK unexpectedly freed stored-row capacity: %v", err)
	}
}

func TestMeshSnapshotClaimListBound(t *testing.T) {
	s := limitsStore(t)
	// Insert in reverse order to prove the bounded result also has stable order.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 1000; i >= 0; i-- {
		if _, err := tx.Exec(`INSERT INTO claims(task_id,node_id,incarnation,resource,paths,context_revision,expires_at,state)
			VALUES(?, 's', 'i', 'repo', '["file"]', 0, 1, 'released')`, fmt.Sprintf("task-%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var snap msgSnapResp
	if err := json.Unmarshal(msgMustExec(t, s, "snapshot", map[string]any{}), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Claims) != 1000 {
		t.Fatalf("snapshot claims=%d want 1000", len(snap.Claims))
	}
	for i, claim := range snap.Claims {
		if want := fmt.Sprintf("task-%04d", i); claim.TaskID != want {
			t.Fatalf("claim[%d]=%s want %s", i, claim.TaskID, want)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM claims").Scan(&count); err != nil || count != 1001 {
		t.Fatalf("snapshot deleted overflow: count=%d err=%v", count, err)
	}
}

func TestMeshPrivateDirectoryAndDatabaseModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode assertions are not applicable to Windows ACLs")
	}
	dir := filepath.Join(t.TempDir(), "new-mesh")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "ledger.db"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode=%04o want %04o", filepath.Base(path), got, want)
		}
	}
}
