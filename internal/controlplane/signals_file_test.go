package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// taskSignalLine represents one line in <db-dir>/signals/tasks.jsonl.
type taskSignalLine struct {
	TS     string `json:"ts"`
	TaskID string `json:"task_id"`
	From   string `json:"from"`
	To     string `json:"to"`
}

func readSignalLines(t *testing.T, signalPath string) []taskSignalLine {
	t.Helper()
	f, err := os.Open(signalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("open signals file %s: %v", signalPath, err)
	}
	defer f.Close()

	var lines []taskSignalLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "" {
			continue
		}
		var line taskSignalLine
		if err := json.Unmarshal([]byte(text), &line); err != nil {
			t.Fatalf("corrupted signal json line %q: %v", text, err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan signals file: %v", err)
	}
	return lines
}

func submitFixture(t *testing.T, s *Store, key string, maxAttempts int) *Task {
	t.Helper()
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	req := SubmitTaskRequest{
		IdempotencyKey: key,
		Model:          "test-model",
		AddDirs:        []string{t.TempDir()},
		MaxAttempts:    maxAttempts,
		Payload:        json.RawMessage(`{"prompt":"test task prompt"}`),
	}
	task, err := s.SubmitTask(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitTask %s: %v", key, err)
	}
	return task
}

func claimAndStartFixture(t *testing.T, s *Store, workerID string, leaseDuration int) (*Task, string) {
	t.Helper()
	claimed, err := s.ClaimTask(context.Background(), workerID, leaseDuration)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	token := deref(claimed.LeaseToken)
	if !s.StartTask(claimed.TaskID, workerID, token) {
		t.Fatalf("StartTask failed")
	}
	return claimed, token
}

func TestSignalsFile_TerminalTransition(t *testing.T) {
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	// 1. Submit task -> QUEUED (non-terminal, no signal)
	task := submitFixture(t, s, "test-term-1", 3)
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signal lines after SubmitTask, got %d", len(lines))
	}

	// 2. Claim and Start task -> LEASED -> RUNNING (non-terminal, no signal)
	claimed, token := claimAndStartFixture(t, s, "worker-1", 60)
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signal lines after Claim/StartTask, got %d", len(lines))
	}

	// 3. FinishAttempt -> WORKER_COMPLETED (terminal transition)
	_, err := s.FinishAttempt(claimed.TaskID, "worker-1", token, FinishAttemptParams{
		Result:  json.RawMessage(`{"ok":true}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	// Signal file created + exactly one valid JSON line
	lines := readSignalLines(t, signalsPath)
	if len(lines) != 1 {
		t.Fatalf("expected 1 signal line after FinishAttempt, got %d", len(lines))
	}
	line := lines[0]
	if line.TaskID != task.TaskID {
		t.Errorf("signal task_id = %q, want %q", line.TaskID, task.TaskID)
	}
	if line.From != StateRunning {
		t.Errorf("signal from = %q, want %q", line.From, StateRunning)
	}
	if line.To != StateWorkerCompleted {
		t.Errorf("signal to = %q, want %q", line.To, StateWorkerCompleted)
	}
	parsedTS, err := time.Parse(time.RFC3339, line.TS)
	if err != nil {
		t.Fatalf("parse signal ts %q: %v", line.TS, err)
	}
	if parsedTS.Unix() != clock.Now().Unix() {
		t.Errorf("signal ts = %v, want %v", parsedTS, clock.Now())
	}
}

func TestSignalsFile_NonTerminalTransition(t *testing.T) {
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	// Submit (None -> QUEUED)
	task := submitFixture(t, s, "test-nonterm-1", 3)

	// Claim and Start (QUEUED -> LEASED -> RUNNING)
	claimed, token := claimAndStartFixture(t, s, "worker-1", 60)

	// Pause with BLOCKED (RUNNING -> BLOCKED)
	var err error
	_, err = s.PauseTask(claimed.TaskID, "worker-1", token, StateBlocked, json.RawMessage(`{}`), "blocked by dep")
	if err != nil {
		t.Fatalf("PauseTask BLOCKED: %v", err)
	}

	// Resume (BLOCKED -> QUEUED)
	_, err = s.ResumeTask(context.Background(), task.TaskID, nil, "unblocked")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}

	// Assert NO signals file was created for non-terminal transitions
	if lines := readSignalLines(t, signalsPath); len(lines) != 0 {
		t.Fatalf("expected 0 signal lines for non-terminal transitions, got %d", len(lines))
	}
}

func TestSignalsFile_AllFiveStates(t *testing.T) {
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	// 1. WORKER_COMPLETED via FinishAttempt
	t1 := submitFixture(t, s, "test-five-1", 3)
	c1, tok1 := claimAndStartFixture(t, s, "w-1", 60)
	_, err := s.FinishAttempt(c1.TaskID, "w-1", tok1, FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"ok"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	// 2. CANCELLED via CancelTask
	t2 := submitFixture(t, s, "test-five-2", 3)
	if err := s.CancelTask(context.Background(), t2.TaskID, "user cancellation"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	// 3. NEEDS_INFO via PauseTask
	_ = submitFixture(t, s, "test-five-3", 3)
	c3, tok3 := claimAndStartFixture(t, s, "w-1", 60)
	_, err = s.PauseTask(c3.TaskID, "w-1", tok3, StateNeedsInfo, json.RawMessage(`{}`), "operator input needed")
	if err != nil {
		t.Fatalf("PauseTask NEEDS_INFO: %v", err)
	}

	// 4. FAILED via ReconcileExpired (retry budget exhausted)
	t4 := submitFixture(t, s, "test-five-4", 1)
	_, _ = s.ClaimTask(context.Background(), "w-1", 60)
	// Expire the lease
	clock.Advance(100 * time.Second)
	reconciled, err := s.ReconcileExpired(context.Background())
	if err != nil {
		t.Fatalf("ReconcileExpired: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("expected 1 reconciled task, got %d", reconciled)
	}

	// 5. SUCCEEDED via transition / appendTaskSignal
	t5 := submitFixture(t, s, "test-five-5", 3)
	s.appendTaskSignal(t5.TaskID, StateRunning, StateSucceeded, clock.Now())

	// Verify all 5 lines in order
	lines := readSignalLines(t, signalsPath)
	if len(lines) != 5 {
		t.Fatalf("expected 5 signal lines, got %d", len(lines))
	}

	expected := []struct {
		taskID string
		to     string
	}{
		{t1.TaskID, StateWorkerCompleted},
		{t2.TaskID, StateCancelled},
		{c3.TaskID, StateNeedsInfo},
		{t4.TaskID, StateFailed},
		{t5.TaskID, StateSucceeded},
	}

	for i, exp := range expected {
		if lines[i].TaskID != exp.taskID {
			t.Errorf("line %d: task_id = %q, want %q", i, lines[i].TaskID, exp.taskID)
		}
		if lines[i].To != exp.to {
			t.Errorf("line %d: to = %q, want %q", i, lines[i].To, exp.to)
		}
		if _, err := time.Parse(time.RFC3339, lines[i].TS); err != nil {
			t.Errorf("line %d: invalid ts %q: %v", i, lines[i].TS, err)
		}
	}
}

func TestSignalsFile_CloseAndReopen(t *testing.T) {
	clock := newFakeClock()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	signalsPath := filepath.Join(tempDir, "signals", "tasks.jsonl")

	// Store instance 1: submit and finish
	s1, err := NewControlPlane(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("NewControlPlane 1: %v", err)
	}
	t1 := submitFixture(t, s1, "test-reopen-1", 3)
	c1, tok1 := claimAndStartFixture(t, s1, "w-1", 60)
	_, err = s1.FinishAttempt(c1.TaskID, "w-1", tok1, FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"ok"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt 1: %v", err)
	}

	if err := s1.Close(); err != nil {
		t.Fatalf("s1.Close: %v", err)
	}

	if lines := readSignalLines(t, signalsPath); len(lines) != 1 {
		t.Fatalf("expected 1 signal line before reopen, got %d", len(lines))
	}

	// Store instance 2: reopen same db, submit and cancel
	s2, err := NewControlPlane(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("NewControlPlane 2: %v", err)
	}
	defer s2.Close()

	t2 := submitFixture(t, s2, "test-reopen-2", 3)
	if err := s2.CancelTask(context.Background(), t2.TaskID, "stop"); err != nil {
		t.Fatalf("CancelTask 2: %v", err)
	}

	lines := readSignalLines(t, signalsPath)
	if len(lines) != 2 {
		t.Fatalf("expected 2 signal lines after reopen, got %d", len(lines))
	}
	if lines[0].TaskID != t1.TaskID || lines[0].To != StateWorkerCompleted {
		t.Errorf("line 0 mismatch: %+v", lines[0])
	}
	if lines[1].TaskID != t2.TaskID || lines[1].To != StateCancelled {
		t.Errorf("line 1 mismatch: %+v", lines[1])
	}
}

func TestSignalsFile_CrashWindowHonesty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip chmod dir permission test on windows")
	}

	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsDir := filepath.Join(filepath.Dir(dbPath), "signals")

	// Submit and claim/start task
	task := submitFixture(t, s, "test-crash-1", 3)
	claimed, token := claimAndStartFixture(t, s, "w-1", 60)

	// Make signals dir unwritable before the terminal transition
	if err := os.MkdirAll(signalsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll signalsDir: %v", err)
	}
	if err := os.Chmod(signalsDir, 0o500); err != nil {
		t.Fatalf("Chmod signalsDir 0500: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(signalsDir, 0o755) })

	// Terminal transition: FinishAttempt
	// The DB transaction MUST commit even if appending the signal fails.
	finished, err := s.FinishAttempt(claimed.TaskID, "w-1", token, FinishAttemptParams{
		Result:  json.RawMessage(`{"status":"done"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt must not fail when signal write fails: %v", err)
	}
	if finished.State != StateWorkerCompleted {
		t.Errorf("finished state = %q, want %q", finished.State, StateWorkerCompleted)
	}

	// Verify directly from DB that the transition committed
	dbTask, err := s.GetTask(context.Background(), task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if dbTask.State != StateWorkerCompleted {
		t.Fatalf("db task state = %q, want %q", dbTask.State, StateWorkerCompleted)
	}
	if dbTask.CompletedAt == nil {
		t.Fatalf("expected completed_at to be set on committed task")
	}
}

func TestSignalsFile_ConcurrentAppends(t *testing.T) {
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	signalsPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		taskID := fmt.Sprintf("task-conc-%d", i)
		go func(id string) {
			defer wg.Done()
			s.appendTaskSignal(id, StateRunning, StateWorkerCompleted, clock.Now())
		}(taskID)
	}
	wg.Wait()

	lines := readSignalLines(t, signalsPath)
	if len(lines) != n {
		t.Fatalf("expected %d signal lines, got %d", n, len(lines))
	}
	seen := make(map[string]bool)
	for _, l := range lines {
		if l.To != StateWorkerCompleted {
			t.Errorf("expected to=%s, got %s", StateWorkerCompleted, l.To)
		}
		seen[l.TaskID] = true
	}
	if len(seen) != n {
		t.Errorf("expected %d unique tasks, got %d", n, len(seen))
	}
}
