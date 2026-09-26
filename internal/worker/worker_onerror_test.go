package worker

// #394 PR-A2: RunOnce errors must surface to the caller through
// LoopOptions.OnError — the concurrent drain parks on errors (claims may be
// transiently empty for the same reasons), but the supervisor-level caller
// needs to distinguish "queue drained" from "control-plane failure" so the
// CLI can exit non-zero instead of reporting a successful drain.

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestRunLoopConcurrentReportsErrorsViaOnError(t *testing.T) {
	env := newWorkerEnv(t, nil)
	submitTask(t, env, "onerr-1", 1, nil)
	submitTask(t, env, "onerr-2", 1, nil)
	// Close the control plane so the next claim/finish path fails hard.
	if err := env.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	var mu sync.Mutex
	errs := []error{}
	code := env.sup.RunLoop(context.Background(), LoopOptions{
		WorkerID:     "w-onerr",
		LeaseSeconds: 60,
		Concurrency:  2,
		OnError: func(err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		},
	})
	if len(errs) == 0 {
		t.Fatal("RunLoop swallowed RunOnce errors: OnError never invoked")
	}
	found := false
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected at least one non-cancel error, got %v", errs)
	}
	if code != 0 {
		t.Fatalf("drain exit code contract unchanged (cmd decides non-zero via OnError), got %d", code)
	}
}

func TestRunLoopSerialReportsErrorsViaOnError(t *testing.T) {
	env := newWorkerEnv(t, nil)
	if err := env.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	var got error
	code := env.sup.RunLoop(context.Background(), LoopOptions{
		WorkerID:     "w-onerr-serial",
		LeaseSeconds: 60,
		Concurrency:  1,
		OnError: func(err error) {
			if got == nil {
				got = err
			}
		},
	})
	if got == nil {
		t.Fatal("serial RunLoop swallowed the RunOnce error: OnError never invoked")
	}
	if code != 0 {
		t.Fatalf("serial drain exit code contract unchanged, got %d", code)
	}
}
