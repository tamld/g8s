package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func TestUnbufferedPipeStreamerLineProcessing(t *testing.T) {
	var mu sync.Mutex
	var received []StreamEvent

	cb := func(event StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, event)
	}

	streamer := NewUnbufferedPipeStreamer("task-123", 1, "stdout", cb)
	var backingBuffer bytes.Buffer
	writer := streamer.PipeTee(&backingBuffer)

	// Write multi-line text chunk
	lines := "line 1: initializing\nline 2: scanning files\nline 3: done\n"
	n, err := writer.Write([]byte(lines))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(lines) {
		t.Fatalf("wrote %d bytes, want %d", n, len(lines))
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Backing buffer must contain the exact raw stream
	if backingBuffer.String() != lines {
		t.Fatalf("backing buffer = %q, want %q", backingBuffer.String(), lines)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 3 {
		t.Fatalf("received %d events, want 3", len(received))
	}
	if received[0].Line != "line 1: initializing" || received[0].Stream != "stdout" {
		t.Errorf("received[0] = %+v", received[0])
	}
	if received[1].Line != "line 2: scanning files" || received[1].Stream != "stdout" {
		t.Errorf("received[1] = %+v", received[1])
	}
	if received[2].Line != "line 3: done" || received[2].Stream != "stdout" {
		t.Errorf("received[2] = %+v", received[2])
	}
}

func TestSupervisorWithStreamCallbackIntegration(t *testing.T) {
	env := newWorkerEnv(t, nil)

	var mu sync.Mutex
	var streamEvents []StreamEvent

	cb := func(event StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		streamEvents = append(streamEvents, event)
	}

	sup := NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithStreamCallback(cb),
	)

	ctx := context.Background()
	task := submitTask(t, env, "test-stream-idem", 1, map[string]any{"prompt": "test live stream"})

	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		go func() {
			time.Sleep(5 * time.Millisecond)
			if opts.Stdout != nil {
				fmt.Fprintln(opts.Stdout, "step 1: started worker stream")
				fmt.Fprintln(opts.Stdout, "step 2: processing payload")
			}
			if opts.Stderr != nil {
				fmt.Fprintln(opts.Stderr, "warn: non-fatal diagnostic warning")
			}
			_ = os.WriteFile(opts.ResultPath, []byte(`{"ok":true,"status":"succeeded"}`), 0600)
			close(child.done)
		}()
		return child
	}

	resTask, err := sup.RunOnce(ctx, RunOptions{WorkerID: "worker-stream-1", LeaseSeconds: 30})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if resTask.TaskID != task.TaskID {
		t.Fatalf("resTask.TaskID = %s, want %s", resTask.TaskID, task.TaskID)
	}
	if resTask.State != controlplane.StateSucceeded {
		t.Fatalf("task.State = %s, want SUCCEEDED", resTask.State)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(streamEvents) < 3 {
		t.Fatalf("expected at least 3 stream events, got %d: %+v", len(streamEvents), streamEvents)
	}

	var hasStdout, hasStderr bool
	for _, ev := range streamEvents {
		if ev.Stream == "stdout" && strings.Contains(ev.Line, "step 1") {
			hasStdout = true
		}
		if ev.Stream == "stderr" && strings.Contains(ev.Line, "warn:") {
			hasStderr = true
		}
	}
	if !hasStdout || !hasStderr {
		t.Errorf("missing stdout or stderr events in %+v", streamEvents)
	}
}
