package worker

import (
	"bufio"
	"io"
	"sync"
	"time"
)

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
		for scanner.Scan() {
			u.callback(StreamEvent{
				TaskID:    u.taskID,
				Attempt:   u.attempt,
				Stream:    u.stream,
				Line:      scanner.Text(),
				Timestamp: u.clock(),
			})
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
