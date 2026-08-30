package hooks

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/heartbeat"
)

func TestPreSpawnInjectsReflectionPrompt(t *testing.T) {
	hook := NewAttentionerHook()
	ctx := context.Background()

	originalPrompt := "Refactor auth middleware to use pure-Go context tokens"
	task := TaskSpec{
		TaskID: "task-001",
		Prompt: originalPrompt,
	}

	res, err := hook.PreSpawn(ctx, task)
	if err != nil {
		t.Fatalf("PreSpawn failed: %v", err)
	}

	// 1. Verify 3 questions are prepended
	expectedQuestions := []string{
		"1. What 2-3 things could you get wrong?",
		"2. Which test would you write FIRST to prove the design is safe?",
		"3. What contract from your brief would you violate by accident?",
	}

	for _, q := range expectedQuestions {
		if !strings.Contains(res.Prompt, q) {
			t.Errorf("expected prompt to contain question %q, got:\n%s", q, res.Prompt)
		}
	}

	// 2. Verify original prompt is appended at the end
	if !strings.HasSuffix(res.Prompt, originalPrompt) {
		t.Errorf("expected prompt to end with original prompt %q, got:\n%s", originalPrompt, res.Prompt)
	}

	// 3. Verify task.Task.Prompt is also updated
	if res.Task.Prompt != res.Prompt {
		t.Errorf("expected task.Task.Prompt to match task.Prompt, got %q vs %q", res.Task.Prompt, res.Prompt)
	}

	// 4. Verify idempotent behavior (not double-prepending)
	res2, err := hook.PreSpawn(ctx, res)
	if err != nil {
		t.Fatalf("second PreSpawn failed: %v", err)
	}
	if res2.Prompt != res.Prompt {
		t.Errorf("PreSpawn should be idempotent when already prepended, got:\n%s", res2.Prompt)
	}
}

func TestPostWaitOnlyFiresOnSuccess(t *testing.T) {
	var mu sync.Mutex
	recordedEvents := make([]heartbeat.Event, 0)
	recordedSessions := make([]string, 0)

	recorder := func(sessionID string, evt heartbeat.Event) error {
		mu.Lock()
		defer mu.Unlock()
		recordedSessions = append(recordedSessions, sessionID)
		recordedEvents = append(recordedEvents, evt)
		return nil
	}

	hook := NewAttentionerHookWithRecorder(recorder)
	ctx := context.Background()

	task := TaskSpec{
		TaskID:    "task-failure",
		SessionID: "sess-failure",
	}

	// Case 1: Failure receipt (OK=false) -> must NOT record any event
	failReceipt := Receipt{
		OK:         false,
		ReturnCode: 1,
	}

	err := hook.PostWait(ctx, task, failReceipt)
	if err != nil {
		t.Fatalf("PostWait returned error on failure receipt: %v", err)
	}

	// Allow brief window for any errant goroutine
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(recordedEvents) != 0 {
		t.Fatalf("expected 0 events recorded on failure receipt, got %d", len(recordedEvents))
	}
	mu.Unlock()

	// Case 2: Success receipt (OK=true) -> MUST record self-review event
	successTask := TaskSpec{
		TaskID:    "task-success",
		SessionID: "sess-success",
	}
	successReceipt := Receipt{
		OK:         true,
		ReturnCode: 0,
	}

	err = hook.PostWait(ctx, successTask, successReceipt)
	if err != nil {
		t.Fatalf("PostWait returned error on success receipt: %v", err)
	}

	// Wait for background goroutine to execute recorder
	var eventsCount int
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		eventsCount = len(recordedEvents)
		mu.Unlock()
		if eventsCount > 0 {
			break
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(recordedEvents) != 1 {
		t.Fatalf("expected 1 event recorded on success receipt, got %d", len(recordedEvents))
	}
	if recordedSessions[0] != "sess-success" {
		t.Errorf("expected recorded session sess-success, got %s", recordedSessions[0])
	}
	if recordedEvents[0].Kind != "self_review_required" {
		t.Errorf("expected event kind self_review_required, got %s", recordedEvents[0].Kind)
	}
	if !strings.Contains(recordedEvents[0].Prompt, "The test that would have FAILED") {
		t.Errorf("expected event prompt to contain self-review instruction, got %s", recordedEvents[0].Prompt)
	}
}

func TestPostWaitNonBlocking(t *testing.T) {
	blockCh := make(chan struct{})
	startedCh := make(chan struct{})

	slowRecorder := func(sessionID string, evt heartbeat.Event) error {
		close(startedCh)
		<-blockCh // block until test unblocks
		return nil
	}

	hook := NewAttentionerHookWithRecorder(slowRecorder)
	ctx := context.Background()

	task := TaskSpec{
		TaskID:    "task-slow",
		SessionID: "sess-slow",
	}
	receipt := Receipt{
		OK: true,
	}

	start := time.Now()
	err := hook.PostWait(ctx, task, receipt)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("PostWait returned unexpected error: %v", err)
	}

	// PostWait must return immediately (< 50ms) even though recorder blocks
	if elapsed > 50*time.Millisecond {
		t.Errorf("PostWait blocked for %v, expected non-blocking immediate return", elapsed)
	}

	// Wait for goroutine to have started
	select {
	case <-startedCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("background goroutine did not start")
	}

	// Unblock background goroutine
	close(blockCh)
}

func TestAttentionerHookName(t *testing.T) {
	hook := NewAttentionerHook()
	if hook.Name() != "attentioner" {
		t.Errorf("expected hook name attentioner, got %s", hook.Name())
	}
}
