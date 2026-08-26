package worker

import (
	"bufio"
	"io"
	"sync"
)

// LineCallback is called whenever a full line is captured from stdout/stderr.
type LineCallback func(line string)

// UnbufferedPipeStreamer reads bytes from an io.Reader in real-time, splits lines
// immediately, and broadcasts them to listeners without waiting for process exit.
type UnbufferedPipeStreamer struct {
	reader   io.Reader
	writer   io.Writer
	callback LineCallback
	done     chan struct{}
	mu       sync.Mutex
}

// NewUnbufferedPipeStreamer creates a real-time line streamer.
func NewUnbufferedPipeStreamer(r io.Reader, w io.Writer, cb LineCallback) *UnbufferedPipeStreamer {
	s := &UnbufferedPipeStreamer{
		reader:   r,
		writer:   w,
		callback: cb,
		done:     make(chan struct{}),
	}
	go s.pump()
	return s
}

func (s *UnbufferedPipeStreamer) pump() {
	defer close(s.done)
	scanner := bufio.NewScanner(s.reader)
	// Allow large lines up to 1MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		s.mu.Lock()
		if s.writer != nil {
			_, _ = s.writer.Write([]byte(line + "\n"))
		}
		if s.callback != nil {
			s.callback(line)
		}
		s.mu.Unlock()
	}
}

// Done returns a channel that signals when the reader is exhausted.
func (s *UnbufferedPipeStreamer) Done() <-chan struct{} {
	return s.done
}
