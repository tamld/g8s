package worker

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/dispatch"
)

// TestPipeTeeLongLineNoDeadlock is the #981 red-test: a single stdout line larger
// than the legacy 64KB bufio.Scanner default (e.g. an oversized agy JSONL result
// event) must be captured in full — PipeTee sizes its scanner buffer to the capture
// budget (dispatch.MaxCaptureBytes) — and Write/Close must complete without the
// capture pipe stalling forever.
func TestPipeTeeLongLineNoDeadlock(t *testing.T) {
	line := strings.Repeat("x", 128*1024) // 128KB single line: > legacy 64KB scanner max, < 2MiB capture budget
	payload := line + "\n"

	var mu sync.Mutex
	var got []string
	cb := func(ev StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev.Line)
	}

	streamer := NewUnbufferedPipeStreamer("task-981-longline", 1, "stdout", cb)
	var backing bytes.Buffer
	wc := streamer.PipeTee(&backing)

	done := make(chan error, 1)
	go func() {
		_, err := wc.Write([]byte(payload))
		if err == nil {
			err = wc.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Write/Close failed: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("deadlock: PipeTee Write/Close did not complete within 8s — capture pipe stalled on >64KB single line (#981)")
	}

	streamer.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("received %d stream events, want 1 (the full long line)", len(got))
	}
	if got[0] != line {
		t.Fatalf("captured line length = %d, want %d — long line must be captured intact", len(got[0]), len(line))
	}
	if backing.String() != payload {
		t.Fatalf("backing buffer diverged from raw stream: len=%d, want %d", backing.Len(), len(payload))
	}
}

// TestPipeTeeOverBudgetLineFailsFast is the #981 companion red-test: a single line
// beyond the capture budget must make the tee writer fail fast with a typed bounded
// error (wrapping bufio.ErrTooLong) instead of blocking forever on a stalled pipe.
func TestPipeTeeOverBudgetLineFailsFast(t *testing.T) {
	overBudget := strings.Repeat("y", dispatch.MaxCaptureBytes+64*1024) // single line > 2MiB budget

	var mu sync.Mutex
	cb := func(ev StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
	}

	streamer := NewUnbufferedPipeStreamer("task-981-overbudget", 1, "stdout", cb)
	var backing bytes.Buffer
	wc := streamer.PipeTee(&backing)

	done := make(chan error, 1)
	go func() {
		_, err := wc.Write([]byte(overBudget + "\n"))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Write returned nil error for over-budget single line, want typed bounded error (bufio.ErrTooLong)")
		}
		if !errors.Is(err, bufio.ErrTooLong) {
			t.Fatalf("error = %v, want error wrapping bufio.ErrTooLong", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("deadlock: PipeTee Write did not fail fast on over-budget line — pipe writer blocked forever (#981)")
	}

	closed := make(chan error, 1)
	go func() {
		closed <- wc.Close()
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close after bounded failure: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("deadlock: PipeTee Close did not complete after bounded failure")
	}
}
