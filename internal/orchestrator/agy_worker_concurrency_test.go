package orchestrator

// Portable concurrency tests for #986 batch-1 orchestrator slice #2 (A-3):
// concurrent agyHandle.Wait callers must share a single cmd.Wait goroutine
// and return the same (receipt, err) pair. POSIX process-group fixtures
// live in agy_worker_procgroup_test.go.

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// TestAgyHandleConcurrentWaitSameResult: concurrent Wait calls must share a
// single cmd.Wait goroutine and return the same (receipt, err) pair. Run
// under -race: duplicate cmd.Wait callers race on exec.Cmd internals and on
// the shared stdout buffer. The phase is bounded so a pre-fix deadlock fails
// the test instead of wedging the whole suite.
func TestAgyHandleConcurrentWaitSameResult(t *testing.T) {
	requireCommand(t, "sh")
	cmd := exec.Command("sh", "-c", "sleep 0.2; exit 0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	h := &agyHandle{
		cmd:       cmd,
		stdout:    &stdout,
		stderr:    &stderr,
		startedAt: fixedClock(),
		clock:     fixedClock,
	}

	const waiters = 4
	type waitResult struct {
		receipt Receipt
		err     error
	}
	results := make([]waitResult, waiters)
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := h.Wait(context.Background())
			results[i] = waitResult{receipt: r, err: err}
		}(i)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent Wait calls did not return within 30s: concurrent cmd.Wait deadlock (A-3)")
	}

	for i := 1; i < waiters; i++ {
		if (results[i].err == nil) != (results[0].err == nil) {
			t.Errorf("Wait waiter %d err = %v, waiter 0 err = %v (divergent results across concurrent Waits)", i, results[i].err, results[0].err)
		}
		if results[i].receipt.OK != results[0].receipt.OK {
			t.Errorf("Wait waiter %d receipt.OK = %v, waiter 0 receipt.OK = %v (divergent receipts)", i, results[i].receipt.OK, results[0].receipt.OK)
		}
	}
	if results[0].err != nil || !results[0].receipt.OK {
		t.Errorf("Wait err = %v, receipt.OK = %v; want nil/true", results[0].err, results[0].receipt.OK)
	}
}
