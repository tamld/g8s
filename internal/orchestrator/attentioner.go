package orchestrator

import (
	"context"
	"strings"

	"github.com/tamld/g8s/internal/heartbeat"
)

// AttentionReflectionPrefix is the prompt preamble injected before worker execution.
const AttentionReflectionPrefix = `Before you start, take 30 seconds to answer:
1. What 2-3 things could you get wrong?
2. Which test would you write FIRST to prove the design is safe?
3. What contract from your brief would you violate by accident?

Then start.

`

// AttentionSelfReviewPrompt is the non-blocking reflection prompt fired after task success.
const AttentionSelfReviewPrompt = `Your task succeeded. Before next attempt, write a 1-line
answer: "The test that would have FAILED on the code I just wrote is
________." Then move on.`

// AttentionerHook implements HookMount to redistribute worker compute to risk analysis
// and failure mode proofing before implementation, and self-review upon success per DEBT-47.
type AttentionerHook struct {
	Store    *heartbeat.Store
	Recorder func(sessionID string, evt heartbeat.Event) error
}

// NewAttentionerHook constructs an AttentionerHook using the default heartbeat store.
func NewAttentionerHook() *AttentionerHook {
	return &AttentionerHook{}
}

// NewAttentionerHookWithStore constructs an AttentionerHook targeting a custom heartbeat store.
func NewAttentionerHookWithStore(store *heartbeat.Store) *AttentionerHook {
	return &AttentionerHook{Store: store}
}

// NewAttentionerHookWithRecorder constructs an AttentionerHook with an explicit recorder callback.
func NewAttentionerHookWithRecorder(recorder func(sessionID string, evt heartbeat.Event) error) *AttentionerHook {
	return &AttentionerHook{Recorder: recorder}
}

// Name returns the hook identifier.
func (h *AttentionerHook) Name() string { return "attentioner" }

// PreSpawn injects a reflection prompt at the START of the worker
// prompt so the implementer allocates compute to risk analysis
// before any code is written.
func (h *AttentionerHook) PreSpawn(_ context.Context, task TaskSpec) (TaskSpec, error) {
	if !strings.HasPrefix(task.Prompt, "Before you start, take 30 seconds to answer:") {
		task.Prompt = AttentionReflectionPrefix + task.Prompt
	}
	task.Task.Prompt = task.Prompt
	if task.SessionID == "" {
		task.SessionID = task.TaskID
	}
	return task, nil
}

// PostWait fires a non-blocking self-review prompt after the worker
// reports success. The worker must acknowledge the review before
// the next attempt.
func (h *AttentionerHook) PostWait(_ context.Context, task TaskSpec, receipt Receipt) error {
	if !receipt.OK {
		return nil
	}
	sessionID := task.SessionID
	if sessionID == "" {
		sessionID = task.TaskID
	}
	if sessionID == "" {
		return nil
	}
	go func() {
		evt := heartbeat.Event{
			Kind:   "self_review_required",
			Prompt: AttentionSelfReviewPrompt,
		}
		if h.Recorder != nil {
			_ = h.Recorder(sessionID, evt)
		} else if h.Store != nil {
			_, _ = h.Store.RecordEvent(sessionID, evt)
		} else {
			_, _ = heartbeat.Record(sessionID, evt)
		}
	}()
	return nil
}
