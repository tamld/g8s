package worker

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUnbufferedPipeStreamer(t *testing.T) {
	r, w := io.Pipe()

	var captured []string
	var mu sync.Mutex

	streamer := NewUnbufferedPipeStreamer(r, nil, func(line string) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, line)
	})

	// Send lines with small delays
	go func() {
		_, _ = w.Write([]byte("line 1\n"))
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("line 2\nline 3\n"))
		time.Sleep(10 * time.Millisecond)
		_ = w.Close()
	}()

	select {
	case <-streamer.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("streamer timed out")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(captured) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(captured), captured)
	}
	if captured[0] != "line 1" || captured[1] != "line 2" || captured[2] != "line 3" {
		t.Fatalf("unexpected line contents: %v", captured)
	}
}

func TestUnbufferedPipeStreamerPassthrough(t *testing.T) {
	input := "hello world\nsecond line\n"
	inReader := strings.NewReader(input)
	var outBuf bytes.Buffer

	streamer := NewUnbufferedPipeStreamer(inReader, &outBuf, nil)
	<-streamer.Done()

	if outBuf.String() != input {
		t.Fatalf("expected passthrough output %q, got %q", input, outBuf.String())
	}
}
