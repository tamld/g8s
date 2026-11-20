package worker

import (
	"bufio"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/dispatch"
)

// maxStreamLineBytes bounds a single streamed line to the per-stream capture budget
// (dispatch.MaxCaptureBytes) plus one byte of delimiter room (#981): a line larger than
// the budget trips the scanner into a typed bounded failure instead of an unbounded
// buffer, matching how captured output itself is bounded.
const maxStreamLineBytes = dispatch.MaxCaptureBytes + 1

// StreamEvent carries one streamed line emitted by an active attempt.
type StreamEvent struct {
	TaskID    string    `json:"task_id"`
	Attempt   int       `json:"attempt"`
	Stream    string    `json:"stream"` // "stdout" or "stderr"
	Line      string    `json:"line"`
	Timestamp time.Time `json:"timestamp"`
}

// StreamCallback is invoked concurrently whenever a child emits a line.
type StreamCallback func(event StreamEvent)

// UnbufferedPipeStreamer reads lines from an io.Reader without libc 4KB buffer delays
// and forwards them to the provided StreamCallback.
type UnbufferedPipeStreamer struct {
	taskID   string
	attempt  int
	stream   string
	callback StreamCallback
	clock    func() time.Time
	wg       sync.WaitGroup
}

// NewUnbufferedPipeStreamer creates a streamer for a task attempt.
func NewUnbufferedPipeStreamer(taskID string, attempt int, stream string, cb StreamCallback) *UnbufferedPipeStreamer {
	return &UnbufferedPipeStreamer{
		taskID:   taskID,
		attempt:  attempt,
		stream:   stream,
		callback: cb,
		clock:    time.Now,
	}
}

// PipeTee returns an io.Writer that writes to dst and simultaneously streams lines to the callback.
func (u *UnbufferedPipeStreamer) PipeTee(dst io.Writer) io.WriteCloser {
	if u.callback == nil {
		if wc, ok := dst.(io.WriteCloser); ok {
			return wc
		}
		return &nopWriteCloser{dst}
	}

	pr, pw := io.Pipe()
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		scanner := bufio.NewScanner(pr)
		// #981: size the scanner to the capture budget so long single lines (e.g. a
		// large agy JSONL result event) are streamed whole instead of silently
		// tripping the legacy 64KB bufio default.
		scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLineBytes)
		for scanner.Scan() {
			u.callback(StreamEvent{
				TaskID:    u.taskID,
				Attempt:   u.attempt,
				Stream:    u.stream,
				Line:      scanner.Text(),
				Timestamp: u.clock(),
			})
		}
		if err := scanner.Err(); err != nil {
			// #981: the reader side abandoned the pipe mid-stream. Closing the read
			// end with the cause unblocks any pending (or future) pipe write with that
			// error — the tee writer fails fast instead of deadlocking the child on a
			// full OS pipe and truncating evidence. pw.CloseWithError would wake
			// writers with only an opaque io.ErrClosedPipe.
			_ = pr.CloseWithError(fmt.Errorf("stream capture scanner aborted (%s): %w", u.stream, err))
		}
	}()

	return &teeWriteCloser{
		writer:   io.MultiWriter(dst, pw),
		pipeW:    pw,
		streamer: u,
	}
}

// Wait waits for background scanner to finish consuming remaining buffered lines.
func (u *UnbufferedPipeStreamer) Wait() {
	u.wg.Wait()
}

type nopWriteCloser struct {
	io.Writer
}

func (n *nopWriteCloser) Close() error {
	return nil
}

type teeWriteCloser struct {
	writer   io.Writer
	pipeW    *io.PipeWriter
	streamer *UnbufferedPipeStreamer
}

func (t *teeWriteCloser) Write(p []byte) (n int, err error) {
	return t.writer.Write(p)
}

func (t *teeWriteCloser) Close() error {
	err := t.pipeW.Close()
	t.streamer.Wait()
	return err
}
