// Package hooks provides lifecycle hook implementations for g8s orchestrator workers.
package hooks

import (
	"github.com/tamld/g8s/internal/heartbeat"
	"github.com/tamld/g8s/internal/orchestrator"
)

// TaskSpec aliases orchestrator.TaskSpec for convenience.
type TaskSpec = orchestrator.TaskSpec

// Receipt aliases orchestrator.Receipt for convenience.
type Receipt = orchestrator.Receipt

// AttentionerHook aliases orchestrator.AttentionerHook.
type AttentionerHook = orchestrator.AttentionerHook

// NewAttentionerHook constructs an AttentionerHook using the default heartbeat store.
func NewAttentionerHook() *AttentionerHook {
	return orchestrator.NewAttentionerHook()
}

// NewAttentionerHookWithStore constructs an AttentionerHook targeting a custom heartbeat store.
func NewAttentionerHookWithStore(store *heartbeat.Store) *AttentionerHook {
	return orchestrator.NewAttentionerHookWithStore(store)
}

// NewAttentionerHookWithRecorder constructs an AttentionerHook with an explicit recorder callback.
func NewAttentionerHookWithRecorder(recorder func(sessionID string, evt heartbeat.Event) error) *AttentionerHook {
	return orchestrator.NewAttentionerHookWithRecorder(recorder)
}
