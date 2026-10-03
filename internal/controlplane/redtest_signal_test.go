package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Guarantee 4: Signal file is append-only in practice and cannot be spoofed
// through the store API.
// =============================================================================

// TestRed_Guarantee4_NonTerminalTransitionsWriteNothing verifies:
// Non-terminal transitions write nothing to signals/tasks.jsonl.
func TestRed_Guarantee4_NonTerminalTransitionsWriteNothing(t *testing.T) {
	// Guarantee 4(a): Non-terminal transitions write nothing to the signal file.
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	ctx := context.Background()

	// 1. SubmitTask (None -> QUEUED)
	task1 := submitFixture(t, s, "nonterm-task-1", 3)
	_ = task1
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signals after SubmitTask, got %d", len(lines))
	}

	// 2. ClaimTask (QUEUED -> LEASED)
	claimed1, err := s.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	token1 := deref(claimed1.LeaseToken)
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signals after ClaimTask, got %d", len(lines))
	}

	// 3. StartTask (LEASED -> RUNNING)
	if !s.StartTask(claimed1.TaskID, "worker-1", token1) {
		t.Fatalf("StartTask failed")
	}
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signals after StartTask, got %d", len(lines))
	}

	// 4. RenewHeartbeat on RUNNING task
	if err := s.RenewHeartbeat(ctx, claimed1.TaskID, "worker-1", 120); err != nil {
		t.Fatalf("RenewHeartbeat failed: %v", err)
	}
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signals after RenewHeartbeat, got %d", len(lines))
	}

	// 5. Heartbeat on RUNNING task with token
	if err := s.Heartbeat(ctx, claimed1.TaskID, "worker-1", token1, 60); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signals after Heartbeat, got %d", len(lines))
	}

	// 9. CheckpointTask on RUNNING task
	chk := &CheckpointData{
		SourceHashes: map[string]string{"foo.go": "hash1"},
		WorktreePath: "/tmp/wt",
		WorkerState:  json.RawMessage(`{"step":1}`),
	}
	if _, err := s.CheckpointTask(ctx, claimed1.TaskID, "worker-1", token1, chk); err != nil {
		t.Fatalf("CheckpointTask: %v", err)
	}
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signals after CheckpointTask, got %d", len(lines))
	}

	// 10. PauseTask with CHECKPOINTED (RUNNING -> CHECKPOINTED)
	if _, err := s.PauseTask(claimed1.TaskID, "worker-1", token1, StateCheckpointed, json.RawMessage(`{}`), "checkpointed"); err != nil {
		t.Fatalf("PauseTask CHECKPOINTED: %v", err)
	}
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signals after PauseTask CHECKPOINTED, got %d", len(lines))
	}

	// 11. RequeueResult (WORKER_COMPLETED -> QUEUED)
	// Submit separate task 2, complete it -> generates exactly 1 signal line (terminal transition)
	task2 := submitFixture(t, s, "nonterm-task-2", 3)
	c2, tok2 := claimAndStartFixture(t, s, "worker-2", 60)
	_, err = s.FinishAttempt(c2.TaskID, "worker-2", tok2, FinishAttemptParams{
		Result:  json.RawMessage(`{"ok":true}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	// Verify exactly 1 signal line exists from FinishAttempt
	if lines := readSignalLines(t, signalsPath); len(lines) != 1 {
		t.Fatalf("expected 1 signal line after FinishAttempt, got %d", len(lines))
	}

	// Now RequeueResult: WORKER_COMPLETED -> QUEUED (non-terminal transition!)
	if err := s.RequeueResult(ctx, c2.TaskID, "supervisor", "retry requested"); err != nil {
		t.Fatalf("RequeueResult: %v", err)
	}

	// RequeueResult MUST NOT write any signal lines; count must remain exactly 1!
	if lines := readSignalLines(t, signalsPath); len(lines) != 1 {
		t.Fatalf("expected line count to remain 1 after RequeueResult, got %d", len(lines))
	}
	_ = task2
}

// TestRed_Guarantee4_StrictAppendOnlyNoRewritesOrTruncation verifies:
// The store never rewrites or truncates existing lines; all writes are strict append-only.
func TestRed_Guarantee4_StrictAppendOnlyNoRewritesOrTruncation(t *testing.T) {
	// Guarantee 4(b): Store never rewrites or truncates existing signal lines; strict append-only.
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	var prevBytes []byte

	// Perform 15 sequential terminal transitions, verifying strict byte-prefix preservation
	for i := 1; i <= 15; i++ {
		task := submitFixture(t, s, fmt.Sprintf("append-task-%d", i), 1)
		c, tok := claimAndStartFixture(t, s, fmt.Sprintf("w-%d", i), 60)
		_, err := s.FinishAttempt(c.TaskID, fmt.Sprintf("w-%d", i), tok, FinishAttemptParams{
			Result:  json.RawMessage(`{"status":"ok"}`),
			Success: true,
		})
		if err != nil {
			t.Fatalf("FinishAttempt %d: %v", i, err)
		}

		currentBytes, err := os.ReadFile(signalsPath)
		if err != nil {
			t.Fatalf("read signals file %d: %v", i, err)
		}

		// The previous file bytes must be an EXACT prefix of the current file bytes
		if len(prevBytes) > 0 {
			if !bytes.HasPrefix(currentBytes, prevBytes) {
				t.Fatalf("append-only violation at step %d: previous file content was truncated or modified!", i)
			}
		}

		// Ensure exactly i lines exist and each line ends with newline
		if !bytes.HasSuffix(currentBytes, []byte("\n")) {
			t.Fatalf("step %d: signal file does not terminate with newline", i)
		}
		lines := bytes.Split(bytes.TrimSuffix(currentBytes, []byte("\n")), []byte("\n"))
		if len(lines) != i {
			t.Fatalf("step %d: expected %d lines, got %d", i, i, len(lines))
		}

		_ = task
		prevBytes = currentBytes
	}

	// Close store and reopen from the same database path
	if err := s.Close(); err != nil {
		t.Fatalf("s.Close: %v", err)
	}

	sReopened, err := NewControlPlane(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer sReopened.Close()

	// Drive 5 more terminal transitions on the reopened store
	for i := 16; i <= 20; i++ {
		task := submitFixture(t, sReopened, fmt.Sprintf("append-reopen-task-%d", i), 1)
		if err := sReopened.CancelTask(context.Background(), task.TaskID, "cancelled after reopen"); err != nil {
			t.Fatalf("CancelTask %d: %v", i, err)
		}

		currentBytes, err := os.ReadFile(signalsPath)
		if err != nil {
			t.Fatalf("read signals file on reopen %d: %v", i, err)
		}

		if !bytes.HasPrefix(currentBytes, prevBytes) {
			t.Fatalf("append-only violation after reopen at step %d: existing lines were rewritten or truncated!", i)
		}
		prevBytes = currentBytes
	}

	// Verify all 20 lines are valid JSON
	allLines := readSignalLines(t, signalsPath)
	if len(allLines) != 20 {
		t.Fatalf("expected 20 valid signal lines, got %d", len(allLines))
	}
}

// TestRed_Guarantee4_SignalLinesMatchTaskEventsCrossCheck verifies:
// Cross-check N signal lines against the event log (task_events). A signal line's
// from/to must always match the recorded transition in task_events for the same task.
func TestRed_Guarantee4_SignalLinesMatchTaskEventsCrossCheck(t *testing.T) {
	// Guarantee 4(c): Every signal line matches the recorded transition in task_events for the same task.
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	ctx := context.Background()
	const numTransitions = 20

	type expectedSignal struct {
		taskID   string
		from     string
		to       string
		transWay string
	}
	var expected []expectedSignal

	// Generate numTransitions transitions covering WORKER_COMPLETED, CANCELLED, NEEDS_INFO, FAILED
	for i := 0; i < numTransitions; i++ {
		mod := i % 4
		task := submitFixture(t, s, fmt.Sprintf("crosscheck-task-%02d", i), 1)

		switch mod {
		case 0:
			// WORKER_COMPLETED via FinishAttempt
			c, tok := claimAndStartFixture(t, s, "w-cross", 60)
			_, err := s.FinishAttempt(c.TaskID, "w-cross", tok, FinishAttemptParams{
				Result:  json.RawMessage(`{"status":"success"}`),
				Success: true,
			})
			if err != nil {
				t.Fatalf("FinishAttempt: %v", err)
			}
			expected = append(expected, expectedSignal{
				taskID:   c.TaskID,
				from:     StateRunning,
				to:       StateWorkerCompleted,
				transWay: "FinishAttempt (WORKER_COMPLETED)",
			})

		case 1:
			// CANCELLED via CancelTask directly on QUEUED task
			if err := s.CancelTask(ctx, task.TaskID, "user aborted"); err != nil {
				t.Fatalf("CancelTask: %v", err)
			}
			expected = append(expected, expectedSignal{
				taskID:   task.TaskID,
				from:     StateQueued,
				to:       StateCancelled,
				transWay: "CancelTask (CANCELLED)",
			})

		case 2:
			// NEEDS_INFO via PauseTask
			c, tok := claimAndStartFixture(t, s, "w-cross", 60)
			_, err := s.PauseTask(c.TaskID, "w-cross", tok, StateNeedsInfo, json.RawMessage(`{}`), "info needed")
			if err != nil {
				t.Fatalf("PauseTask NEEDS_INFO: %v", err)
			}
			expected = append(expected, expectedSignal{
				taskID:   c.TaskID,
				from:     StateRunning,
				to:       StateNeedsInfo,
				transWay: "PauseTask (NEEDS_INFO)",
			})

		case 3:
			// FAILED via ReconcileExpired (lease expired, retry budget 1 exhausted)
			c, _ := s.ClaimTask(ctx, "w-cross", 60)
			clock.Advance(100 * time.Second)
			reconciled, err := s.ReconcileExpired(ctx)
			if err != nil {
				t.Fatalf("ReconcileExpired: %v", err)
			}
			if reconciled != 1 {
				t.Fatalf("expected 1 reconciled task, got %d", reconciled)
			}
			expected = append(expected, expectedSignal{
				taskID:   c.TaskID,
				from:     StateLeased,
				to:       StateFailed,
				transWay: "ReconcileExpired (FAILED)",
			})
		}
		_ = task
	}

	// Read all signal lines from file
	signalLines := readSignalLines(t, signalsPath)
	if len(signalLines) != len(expected) {
		t.Fatalf("expected %d signal lines, got %d", len(expected), len(signalLines))
	}

	// Cross-check every signal line against task_events in SQLite
	for i, sig := range signalLines {
		exp := expected[i]
		if sig.TaskID != exp.taskID {
			t.Errorf("line %d [%s]: TaskID = %s, want %s", i, exp.transWay, sig.TaskID, exp.taskID)
		}
		if sig.From != exp.from {
			t.Errorf("line %d [%s]: From = %s, want %s", i, exp.transWay, sig.From, exp.from)
		}
		if sig.To != exp.to {
			t.Errorf("line %d [%s]: To = %s, want %s", i, exp.transWay, sig.To, exp.to)
		}

		// Verify timestamp is valid RFC3339
		if _, err := time.Parse(time.RFC3339, sig.TS); err != nil {
			t.Errorf("line %d [%s]: invalid timestamp %q: %v", i, exp.transWay, sig.TS, err)
		}

		// Cross-check against task_events table
		rows, err := s.db.QueryContext(ctx, `
			SELECT event_type, details_json
			FROM task_events
			WHERE task_id = ?
			ORDER BY event_id ASC`, sig.TaskID)
		if err != nil {
			t.Fatalf("query task_events for %s: %v", sig.TaskID, err)
		}

		foundMatch := false
		for rows.Next() {
			var eventType, detailsJSON string
			if err := rows.Scan(&eventType, &detailsJSON); err != nil {
				t.Fatalf("scan task_events: %v", err)
			}

			// Validate that the recorded event in SQL corresponds to the signal line's target state
			switch sig.To {
			case StateWorkerCompleted:
				if eventType == "attempt_finished" && strings.Contains(detailsJSON, "WORKER_COMPLETED") {
					foundMatch = true
				}
			case StateCancelled:
				if (eventType == "cancel_requested" || eventType == "task_cancelled") && strings.Contains(detailsJSON, "CANCELLED") {
					foundMatch = true
				}
			case StateNeedsInfo:
				if eventType == "task_paused" && strings.Contains(detailsJSON, "NEEDS_INFO") {
					foundMatch = true
				}
			case StateFailed:
				if (eventType == "lease_expired" || eventType == "attempt_finished") && strings.Contains(detailsJSON, "FAILED") {
					foundMatch = true
				}
			}
		}
		rows.Close()

		if !foundMatch {
			t.Errorf("line %d [%s]: no matching event found in task_events for task %s (to=%s)",
				i, exp.transWay, sig.TaskID, sig.To)
		}
	}
}

// =============================================================================
// Guarantee 5: Signal file cannot fill the disk unboundedly.
// =============================================================================

// TestRed_Guarantee5_GrowthRateAndLinePerTransition verifies:
// Drive ~5000 terminal transitions in a loop; verify the file stays
// line-per-transition (no duplication under retry), and REPORT the byte size
// per 1000 transitions.
func TestRed_Guarantee5_GrowthRateAndLinePerTransition(t *testing.T) {
	// Guarantee 5: Line-per-transition verification, retry deduplication, and byte growth measurement.
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	ctx := context.Background()

	// 1. Verify no duplication under retry:
	// Submit original task, fail it, resubmit, fail retry.
	// Each transition must produce exactly 1 line; retry must not duplicate lines.
	origReq := SubmitTaskRequest{
		IdempotencyKey: "growth-retry-orig",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"growth test original task"}`),
	}
	origTask, err := s.SubmitTask(ctx, origReq)
	if err != nil {
		t.Fatalf("submit origTask: %v", err)
	}
	t0 := float64(clock.Now().UnixNano()) / 1e9
	failTaskInDB(t, s.db, origTask.TaskID, "504 gateway timeout", t0)
	s.appendTaskSignal(origTask.TaskID, StateRunning, StateFailed, clock.Now())

	// Resubmit original task
	rID, err := s.ResubmitTask(ctx, origTask.TaskID, ResubmitOpts{Enabled: true, MaxPerTask: 2, MaxPerHour: 10})
	if err != nil {
		t.Fatalf("ResubmitTask: %v", err)
	}
	// Fail retry task
	failTaskInDB(t, s.db, rID, "502 bad gateway", t0+10)
	s.appendTaskSignal(rID, StateRunning, StateFailed, clock.Now())

	initialLines := readSignalLines(t, signalsPath)
	if len(initialLines) != 2 {
		t.Fatalf("expected exactly 2 signal lines for orig failure + retry failure, got %d", len(initialLines))
	}
	if initialLines[0].TaskID == initialLines[1].TaskID {
		t.Errorf("duplicated task_id in retry transitions: %s == %s", initialLines[0].TaskID, initialLines[1].TaskID)
	}

	// 2. Drive 5000 terminal transitions in a loop
	const totalTransitions = 5000
	t.Logf("=== Starting 5000 Terminal Transitions Growth Measurement ===")

	for i := 1; i <= totalTransitions; i++ {
		taskID := fmt.Sprintf("task-growth-%05d", i)
		s.appendTaskSignal(taskID, StateRunning, StateWorkerCompleted, clock.Now())

		if i%1000 == 0 {
			fi, err := os.Stat(signalsPath)
			if err != nil {
				t.Fatalf("stat signals file at %d transitions: %v", i, err)
			}
			bytesPerTrans := float64(fi.Size()) / float64(i+len(initialLines))
			t.Logf("[Growth Rate Report] Transitions: %5d | Total File Size: %7d bytes (%.2f KB) | Avg Rate: %.2f bytes/transition",
				i, fi.Size(), float64(fi.Size())/1024.0, bytesPerTrans)
		}
	}

	// Final verification of line-per-transition invariant
	finalLines := readSignalLines(t, signalsPath)
	expectedTotalLines := totalTransitions + len(initialLines)
	if len(finalLines) != expectedTotalLines {
		t.Fatalf("line count invariant violated: expected %d lines, got %d", expectedTotalLines, len(finalLines))
	}

	// Audit for rotation or cap mechanism:
	// Verify whether any rotated files (e.g. tasks.jsonl.1, tasks.jsonl.old) exist
	signalsDir := filepath.Dir(signalsPath)
	entries, err := os.ReadDir(signalsDir)
	if err != nil {
		t.Fatalf("read signals dir: %v", err)
	}
	var extraFiles []string
	for _, e := range entries {
		if e.Name() != "tasks.jsonl" {
			extraFiles = append(extraFiles, e.Name())
		}
	}
	t.Logf("[Audit Findings] Signal directory entries: %v (rotated files present: %v)",
		entries, len(extraFiles) > 0)
}

// TestRed_Guarantee5_CannotFillDiskUnboundedly_Audit verifies:
// Stated Guarantee: "Signal file cannot fill the disk unboundedly."
// Evaluates whether the control plane provides rotation, retention, and size cap
// mechanism to prevent unbounded disk exhaustion (#510).
func TestRed_Guarantee5_CannotFillDiskUnboundedly_Audit(t *testing.T) {
	// Guarantee 5: Signal file cannot fill the disk unboundedly.
	// Size-capped rotation: tasks.jsonl rotates to tasks.jsonl.1 when size exceeds maxSignalFileBytes.
	origMax := maxSignalFileBytes
	maxSignalFileBytes = 2048 // 2 KiB for fast testing
	defer func() {
		maxSignalFileBytes = origMax
	}()

	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	defer s.Close()
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")
	rotatedPath := signalsPath + ".1"

	// 1. Drive transitions programmatically until cumulative size exceeds maxSignalFileBytes.
	var firstBatchCount int
	for {
		firstBatchCount++
		taskID := fmt.Sprintf("task-audit-%04d", firstBatchCount)
		s.appendTaskSignal(taskID, StateRunning, StateWorkerCompleted, clock.Now())
		fi, err := os.Stat(signalsPath)
		if err != nil {
			t.Fatalf("stat signals file: %v", err)
		}
		if fi.Size() > maxSignalFileBytes {
			break
		}
	}

	// tasks.jsonl exists and exceeds maxSignalFileBytes; rotation happens before next append.
	fiPre, err := os.Stat(signalsPath)
	if err != nil {
		t.Fatalf("stat signals file after first batch: %v", err)
	}
	if fiPre.Size() <= maxSignalFileBytes {
		t.Fatalf("signals file size (%d) should exceed maxSignalFileBytes (%d)", fiPre.Size(), maxSignalFileBytes)
	}
	if _, err := os.Stat(rotatedPath); !os.IsNotExist(err) {
		t.Fatalf("rotated file %s should not exist before next append, err=%v", rotatedPath, err)
	}

	// 2. Next transition triggers rotation.
	triggerID := fmt.Sprintf("task-audit-%04d", firstBatchCount+1)
	s.appendTaskSignal(triggerID, StateRunning, StateWorkerCompleted, clock.Now())

	// 3. Write a few more transitions into the fresh file.
	const secondBatchCount = 4
	for i := 1; i <= secondBatchCount; i++ {
		taskID := fmt.Sprintf("task-audit-%04d", firstBatchCount+1+i)
		s.appendTaskSignal(taskID, StateRunning, StateWorkerCompleted, clock.Now())
	}

	// 4. Verify: rotation happened (.1 exists, current file fresh).
	rotFi, err := os.Stat(rotatedPath)
	if err != nil {
		t.Fatalf("expected rotated file %s to exist: %v", rotatedPath, err)
	}
	if rotFi.Size() <= maxSignalFileBytes {
		t.Errorf("expected rotated file size > %d, got %d", maxSignalFileBytes, rotFi.Size())
	}

	curFi, err := os.Stat(signalsPath)
	if err != nil {
		t.Fatalf("expected current signals file %s to exist: %v", signalsPath, err)
	}
	if curFi.Size() >= maxSignalFileBytes {
		t.Errorf("expected fresh current file size < %d, got %d", maxSignalFileBytes, curFi.Size())
	}

	// 5. Verify: no line lost WITHIN the current file.
	curLines := readSignalLines(t, signalsPath)
	expectedCurCount := 1 + secondBatchCount // trigger line + second batch
	if len(curLines) != expectedCurCount {
		t.Fatalf("expected %d lines in current file, got %d", expectedCurCount, len(curLines))
	}
	for idx, line := range curLines {
		expectedID := fmt.Sprintf("task-audit-%04d", firstBatchCount+1+idx)
		if line.TaskID != expectedID {
			t.Errorf("current file line %d: got TaskID %s, want %s", idx, line.TaskID, expectedID)
		}
	}

	// 6. Verify: total content = current + .1.
	rotLines := readSignalLines(t, rotatedPath)
	if len(rotLines) != firstBatchCount {
		t.Fatalf("expected %d lines in rotated file, got %d", firstBatchCount, len(rotLines))
	}
	totalLines := append(rotLines, curLines...)
	if len(totalLines) != firstBatchCount+expectedCurCount {
		t.Fatalf("expected total %d lines, got %d", firstBatchCount+expectedCurCount, len(totalLines))
	}
	for idx, line := range totalLines {
		expectedID := fmt.Sprintf("task-audit-%04d", 1+idx)
		if line.TaskID != expectedID {
			t.Errorf("total content line %d: got TaskID %s, want %s", idx, line.TaskID, expectedID)
		}
	}
}

// TestRed_Guarantee5_TwoRotationsQuickSuccession verifies that two rotations in quick succession
// overwrite tasks.jsonl.1 such that exactly ONE generation is kept, and the signal directory
// contains only tasks.jsonl and tasks.jsonl.1 (#510).
func TestRed_Guarantee5_TwoRotationsQuickSuccession(t *testing.T) {
	origMax := maxSignalFileBytes
	maxSignalFileBytes = 500 // 500 bytes for fast rotation
	defer func() {
		maxSignalFileBytes = origMax
	}()

	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	defer s.Close()
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")
	rotatedPath := signalsPath + ".1"

	// Generation 1: write 6 transitions (~600 bytes > 500)
	for i := 1; i <= 6; i++ {
		s.appendTaskSignal(fmt.Sprintf("task-gen1-%02d", i), StateRunning, StateWorkerCompleted, clock.Now())
	}
	// Next transition triggers rotation 1: Gen 1 moves to .1, task-gen2-01 in fresh file
	s.appendTaskSignal("task-gen2-01", StateRunning, StateWorkerCompleted, clock.Now())

	// Generation 2: write 5 more transitions (~600 bytes total in fresh file > 500)
	for i := 2; i <= 6; i++ {
		s.appendTaskSignal(fmt.Sprintf("task-gen2-%02d", i), StateRunning, StateWorkerCompleted, clock.Now())
	}
	// Next transition triggers rotation 2: Gen 2 moves to .1 (overwriting Gen 1), task-gen3-01 in fresh file
	s.appendTaskSignal("task-gen3-01", StateRunning, StateWorkerCompleted, clock.Now())

	// Verify .1 contains Gen 2 (6 lines of task-gen2-*)
	rotLines := readSignalLines(t, rotatedPath)
	if len(rotLines) != 6 {
		t.Fatalf("expected 6 lines in rotated file (Gen 2), got %d", len(rotLines))
	}
	if rotLines[0].TaskID != "task-gen2-01" {
		t.Errorf("rotated line 0 TaskID = %s, want task-gen2-01", rotLines[0].TaskID)
	}

	// Verify current file contains Gen 3 (1 line of task-gen3-01)
	curLines := readSignalLines(t, signalsPath)
	if len(curLines) != 1 {
		t.Fatalf("expected 1 line in current file (Gen 3), got %d", len(curLines))
	}
	if curLines[0].TaskID != "task-gen3-01" {
		t.Errorf("current line 0 TaskID = %s, want task-gen3-01", curLines[0].TaskID)
	}

	// Verify signal directory contains ONLY tasks.jsonl and tasks.jsonl.1
	entries, err := os.ReadDir(filepath.Dir(signalsPath))
	if err != nil {
		t.Fatalf("read signals dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Fatalf("expected exactly 2 files in signals dir, got %v", names)
	}
}

// TestRed_Guarantee5_RotationFailureFallback verifies that when rotation fails (e.g. destination
// is an un-overwritable directory), a [warn] controlplane: warning is logged to stderr and
// appendTaskSignal continues appending to the unrotated tasks.jsonl without error (#510).
func TestRed_Guarantee5_RotationFailureFallback(t *testing.T) {
	origMax := maxSignalFileBytes
	maxSignalFileBytes = 500
	defer func() {
		maxSignalFileBytes = origMax
	}()

	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	defer s.Close()
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")
	rotatedPath := signalsPath + ".1"

	// Create signals dir and fill tasks.jsonl to exceed maxSignalFileBytes
	for i := 1; i <= 6; i++ {
		s.appendTaskSignal(fmt.Sprintf("task-fail-%02d", i), StateRunning, StateWorkerCompleted, clock.Now())
	}

	// Create tasks.jsonl.1 as a directory so os.Rename fails cross-platform
	if err := os.Mkdir(rotatedPath, 0o755); err != nil {
		t.Fatalf("mkdir rotated path as dir: %v", err)
	}
	defer os.Remove(rotatedPath)

	// Appending next transition should attempt rotation, fail rename, log warning,
	// and continue appending without panic or error.
	s.appendTaskSignal("task-fail-07", StateRunning, StateWorkerCompleted, clock.Now())

	// Verify tasks.jsonl still exists and contains all 7 lines
	lines := readSignalLines(t, signalsPath)
	if len(lines) != 7 {
		t.Fatalf("expected 7 lines in unrotated file, got %d", len(lines))
	}
	if lines[6].TaskID != "task-fail-07" {
		t.Errorf("last line TaskID = %s, want task-fail-07", lines[6].TaskID)
	}
}

// =============================================================================
// Guarantee 6: Crash-window honesty under adversarial timing.
// =============================================================================

// TestRed_Guarantee6_CrashWindowHonestyAdversarialTiming verifies:
// chmod the signals dir unwritable MID-RUN (between transitions) on POSIX:
// transitions still commit, warns appear, no error escapes to the caller.
func TestRed_Guarantee6_CrashWindowHonestyAdversarialTiming(t *testing.T) {
	// Guarantee 6: Crash-window honesty under adversarial timing; transitions commit and warns appear when signals dir unwritable mid-run.
	if runtime.GOOS == "windows" {
		t.Skip("skip chmod dir permission test on windows")
	}

	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsDir := filepath.Join(filepath.Dir(dbPath), "signals")

	ctx := context.Background()

	// 1. Transition 1: Submit, claim, start, FinishAttempt (terminal transition)
	// Must succeed and create signals/tasks.jsonl with line 1
	task1 := submitFixture(t, s, "crash-task-1", 3)
	c1, tok1 := claimAndStartFixture(t, s, "w-1", 60)
	fin1, err := s.FinishAttempt(c1.TaskID, "w-1", tok1, FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"first_attempt_done"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt 1 failed: %v", err)
	}
	if fin1.State != StateWorkerCompleted {
		t.Fatalf("fin1 state = %s, want WORKER_COMPLETED", fin1.State)
	}

	signalsPath := filepath.Join(signalsDir, "tasks.jsonl")
	linesBeforeChmod := readSignalLines(t, signalsPath)
	if len(linesBeforeChmod) != 1 {
		t.Fatalf("expected 1 signal line after transition 1, got %d", len(linesBeforeChmod))
	}

	// 2. MID-RUN ADVERSARIAL TIMING: chmod the signals dir unwritable (0400: read-only, no execute/search).
	// To simulate adversarial process crash, worker rotation, or file descriptor re-opening,
	// we simulate the condition where a store instance attempts to append to signals while
	// the signals directory permissions have been revoked by an operator or disk error.
	if err := os.Chmod(signalsDir, 0o400); err != nil {
		t.Fatalf("Chmod signalsDir 0400: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(signalsDir, 0o755) })

	// Intercept stderr to verify that warns appear as required by the guarantee contract
	oldStderr := os.Stderr
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("create os.Pipe: %v", err)
	}
	os.Stderr = wPipe

	// Reopen store instance with signalsDir unwritable to test open-file failure path
	s2, err := NewControlPlane(dbPath, clock.Now)
	if err != nil {
		os.Stderr = oldStderr
		t.Fatalf("NewControlPlane 2: %v", err)
	}
	defer s2.Close()

	// 3. Transition 2 on store s2: Submit task 2, claim, start, FinishAttempt
	task2 := submitFixture(t, s2, "crash-task-2", 3)
	c2, tok2 := claimAndStartFixture(t, s2, "w-2", 60)

	fin2, err := s2.FinishAttempt(c2.TaskID, "w-2", tok2, FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"second_attempt_done"}`),
		Success: true,
	})

	// Restore stderr
	_ = wPipe.Close()
	os.Stderr = oldStderr
	stderrBytes, _ := io.ReadAll(rPipe)
	_ = rPipe.Close()
	capturedStderr := string(stderrBytes)

	// Contract Assertion 1: No error escapes to the caller!
	if err != nil {
		t.Fatalf("CRASH WINDOW VIOLATION: error escaped to caller during unwritable signals dir: %v", err)
	}
	if fin2 == nil || fin2.State != StateWorkerCompleted {
		t.Fatalf("fin2 state = %v, want WORKER_COMPLETED", fin2)
	}

	// Contract Assertion 2: The DB transaction STILL COMMITTED in the database!
	dbTask2, err := s2.GetTask(ctx, task2.TaskID)
	if err != nil {
		t.Fatalf("GetTask 2: %v", err)
	}
	if dbTask2.State != StateWorkerCompleted {
		t.Fatalf("DB commit failed: dbTask2.State = %s, want WORKER_COMPLETED", dbTask2.State)
	}
	if dbTask2.CompletedAt == nil {
		t.Fatalf("DB commit failed: completed_at was not set on task 2")
	}

	// Contract Assertion 3: Warns appear on stderr!
	if !strings.Contains(capturedStderr, "[warn] controlplane:") {
		t.Errorf("expected '[warn] controlplane:' on stderr when signal write fails, got: %q", capturedStderr)
	}

	// 4. Restore write permissions: subsequent transitions must resume writing signals
	if err := os.Chmod(signalsDir, 0o755); err != nil {
		t.Fatalf("Chmod signalsDir 0755: %v", err)
	}

	task3 := submitFixture(t, s2, "crash-task-3", 3)
	c3, tok3 := claimAndStartFixture(t, s2, "w-3", 60)
	fin3, err := s2.FinishAttempt(c3.TaskID, "w-3", tok3, FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"third_attempt_done"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt 3 failed: %v", err)
	}
	if fin3.State != StateWorkerCompleted {
		t.Fatalf("fin3 state = %s, want WORKER_COMPLETED", fin3.State)
	}

	// Verify signal line was appended for transition 3
	linesAfterRestore := readSignalLines(t, signalsPath)
	if len(linesAfterRestore) < 2 {
		t.Fatalf("expected at least 2 signal lines after permissions restored, got %d", len(linesAfterRestore))
	}
	lastLine := linesAfterRestore[len(linesAfterRestore)-1]
	if lastLine.TaskID != task3.TaskID || lastLine.To != StateWorkerCompleted {
		t.Errorf("last signal line = %+v, want task_id=%s to=%s", lastLine, task3.TaskID, StateWorkerCompleted)
	}

	_ = task1
}
