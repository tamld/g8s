package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// TransportKind distinguishes process-oriented CLI backends from request-oriented HTTP API backends (#940).
type TransportKind string

const (
	// TransportKindProcess identifies subprocess-backed CLI providers with an OS PID.
	TransportKindProcess TransportKind = "process"
	// TransportKindRequest identifies HTTP/API request providers without an OS process PID.
	TransportKindRequest TransportKind = "request"
)

// NoPID is returned by Handle.PID() adapters wrapping RequestHandle (never 0).
const NoPID = -1

// ProcessHandle represents a live OS subprocess execution handle.
type ProcessHandle interface {
	Handle
}

// ProcessTransport is implemented by subprocess-backed CLI worker adapters.
type ProcessTransport interface {
	Provider
	TransportKind() TransportKind
	SpawnProcess(ctx context.Context, spec Spec) (ProcessHandle, error)
}

// RequestHandle represents an asynchronous HTTP/API request execution without an OS PID.
type RequestHandle interface {
	RequestID() string
	TransportKind() TransportKind
	Wait(ctx context.Context) (Receipt, error)
	Cancel(ctx context.Context) error
	StdoutStream() io.ReadCloser
}

// RequestTransport is implemented by HTTP/API LLM gateway adapters.
type RequestTransport interface {
	Provider
	TransportKind() TransportKind
	StartRequest(ctx context.Context, spec Spec) (RequestHandle, error)
}

var globalRequestSeq atomic.Uint64

func nextRequestID(providerName string) string {
	seq := globalRequestSeq.Add(1)
	if providerName == "" {
		providerName = "http"
	}
	return fmt.Sprintf("req-%s-%d", providerName, seq)
}

// liveStreamBuf supports concurrent append + broadcast without producer backpressure blocking.
type liveStreamBuf struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   []byte
	closed bool
}

func newLiveStreamBuf() *liveStreamBuf {
	b := &liveStreamBuf{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *liveStreamBuf) WriteChunk(p []byte) {
	if len(p) == 0 {
		return
	}
	b.mu.Lock()
	b.data = append(b.data, p...)
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *liveStreamBuf) WriteString(s string) {
	if s == "" {
		return
	}
	b.WriteChunk([]byte(s))
}

func (b *liveStreamBuf) Close() {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *liveStreamBuf) NewReader() io.ReadCloser {
	return &liveStreamReader{b: b}
}

type liveStreamReader struct {
	b      *liveStreamBuf
	offset int
	closed atomic.Bool
}

func (r *liveStreamReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.closed.Load() {
		return 0, io.EOF
	}
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	for r.offset >= len(r.b.data) && !r.b.closed && !r.closed.Load() {
		r.b.cond.Wait()
	}
	if r.closed.Load() {
		return 0, io.EOF
	}
	if r.offset < len(r.b.data) {
		n := copy(p, r.b.data[r.offset:])
		r.offset += n
		return n, nil
	}
	return 0, io.EOF
}

func (r *liveStreamReader) Close() error {
	r.closed.Store(true)
	r.b.mu.Lock()
	r.b.cond.Broadcast()
	r.b.mu.Unlock()
	return nil
}

// asyncRequestHandle implements RequestHandle for in-flight HTTP requests.
type asyncRequestHandle struct {
	reqID     string
	provider  string
	start     time.Time
	cancel    context.CancelFunc
	cancelled atomic.Bool
	streamBuf *liveStreamBuf
	done      chan struct{}
	receipt   Receipt
	err       error
}

func newAsyncRequestHandle(providerName string, cancel context.CancelFunc) *asyncRequestHandle {
	return &asyncRequestHandle{
		reqID:     nextRequestID(providerName),
		provider:  providerName,
		start:     time.Now(),
		cancel:    cancel,
		streamBuf: newLiveStreamBuf(),
		done:      make(chan struct{}),
	}
}

func (h *asyncRequestHandle) complete(rc Receipt, err error) {
	if h.cancel != nil {
		h.cancel()
	}
	h.streamBuf.Close()
	rc.Provider = h.provider
	rc.TransportKind = TransportKindRequest
	rc.RequestID = h.reqID
	if rc.DurationMs <= 0 {
		rc.DurationMs = time.Since(h.start).Milliseconds()
	}
	if h.cancelled.Load() && rc.Status != "COMPLETED" {
		rc.Status = "CANCELLED"
		rc.ExitCode = -1
		err = context.Canceled
	} else if rc.Status != "COMPLETED" && err == nil {
		if rc.Stderr != "" {
			err = fmt.Errorf("%s", rc.Stderr)
		} else {
			err = fmt.Errorf("%s failed", h.provider)
		}
	}
	h.receipt = rc
	h.err = err
	close(h.done)
}

func (h *asyncRequestHandle) RequestID() string { return h.reqID }

func (h *asyncRequestHandle) TransportKind() TransportKind { return TransportKindRequest }

func (h *asyncRequestHandle) Wait(ctx context.Context) (Receipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-h.done:
	case <-ctx.Done():
		select {
		case <-h.done:
		default:
			if h.cancel != nil {
				h.cancel()
			}
			<-h.done
			rc := h.receipt
			if h.cancelled.Load() || errors.Is(ctx.Err(), context.Canceled) {
				rc.Status = "CANCELLED"
			} else {
				rc.Status = "TIMEOUT"
			}
			rc.ExitCode = -1
			return rc, ctx.Err()
		}
	}
	if h.cancelled.Load() && h.receipt.Status != "COMPLETED" {
		rc := h.receipt
		rc.Status = "CANCELLED"
		rc.ExitCode = -1
		return rc, context.Canceled
	}
	return h.receipt, h.err
}

func (h *asyncRequestHandle) Cancel(_ context.Context) error {
	h.cancelled.Store(true)
	if h.cancel != nil {
		h.cancel()
	}
	return nil
}

func (h *asyncRequestHandle) StdoutStream() io.ReadCloser {
	if h.streamBuf == nil {
		return io.NopCloser(bytes.NewReader(nil))
	}
	return h.streamBuf.NewReader()
}

// requestHandleAdapter bridges RequestHandle onto legacy Handle callers without faking PID 0.
type requestHandleAdapter struct {
	rh RequestHandle
}

func newRequestHandleAdapter(rh RequestHandle) Handle {
	return &requestHandleAdapter{rh: rh}
}

func (a *requestHandleAdapter) PID() int { return NoPID }

func (a *requestHandleAdapter) Wait(ctx context.Context) (Receipt, error) {
	return a.rh.Wait(ctx)
}

func (a *requestHandleAdapter) Cancel(ctx context.Context) error {
	return a.rh.Cancel(ctx)
}

func (a *requestHandleAdapter) StdoutStream() io.ReadCloser {
	return a.rh.StdoutStream()
}

// UnwrapRequestHandle returns the underlying typed RequestHandle.
func (a *requestHandleAdapter) UnwrapRequestHandle() RequestHandle { return a.rh }
