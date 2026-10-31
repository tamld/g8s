package reach

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type stubBackend struct {
	name string
	fn   func(context.Context, Request) (Response, error)
}

func (s stubBackend) Name() string { return s.name }

func (s stubBackend) Fetch(ctx context.Context, req Request) (Response, error) {
	return s.fn(ctx, req)
}

func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err    error
		status int
		want   string
	}{
		{nil, 200, "ok"},
		{nil, 404, "terminal"},
		{nil, 429, "fallback"},
		{nil, 503, "fallback"},
		{&StatusError{Status: 500}, 0, "fallback"},
		{fmt.Errorf("invalid URL: bad"), 0, "terminal"},
		{context.DeadlineExceeded, 0, "fallback"},
	}
	for _, tc := range tests {
		if got := Classify(tc.err, tc.status); got != tc.want {
			t.Errorf("Classify(%v, %d) = %q, want %q", tc.err, tc.status, got, tc.want)
		}
	}
}

func TestChainFallbackOnTransient(t *testing.T) {
	t.Parallel()
	calls := 0
	backends := []Backend{
		stubBackend{name: "first", fn: func(context.Context, Request) (Response, error) {
			calls++
			return Response{}, &StatusError{Status: 503, Msg: "upstream"}
		}},
		stubBackend{name: "second", fn: func(context.Context, Request) (Response, error) {
			calls++
			return Response{Body: "ok", Status: 200, Provenance: "second"}, nil
		}},
	}
	resp, attempts, err := Chain{}.Run(context.Background(), "web", "read", Request{URL: "https://example.com"}, backends)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Body != "ok" || len(attempts) != 2 || attempts[0].Class != "fallback" || attempts[1].Class != "ok" {
		t.Fatalf("unexpected outcome resp=%+v attempts=%+v", resp, attempts)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestChainStopsOnTerminal(t *testing.T) {
	t.Parallel()
	secondCalled := false
	backends := []Backend{
		stubBackend{name: "first", fn: func(context.Context, Request) (Response, error) {
			return Response{}, &StatusError{Status: 404, Msg: "missing"}
		}},
		stubBackend{name: "second", fn: func(context.Context, Request) (Response, error) {
			secondCalled = true
			return Response{Body: "nope", Status: 200, Provenance: "second"}, nil
		}},
	}
	_, attempts, err := Chain{}.Run(context.Background(), "web", "read", Request{URL: "https://example.com/x"}, backends)
	if err == nil {
		t.Fatal("expected error")
	}
	if secondCalled {
		t.Fatal("second backend should not run after terminal failure")
	}
	if len(attempts) != 1 || attempts[0].Class != "terminal" {
		t.Fatalf("attempts = %+v", attempts)
	}
}

func TestChainAllBackendsFailAggregates(t *testing.T) {
	t.Parallel()
	backends := []Backend{
		stubBackend{name: "a", fn: func(context.Context, Request) (Response, error) {
			return Response{}, errors.New("timeout")
		}},
		stubBackend{name: "b", fn: func(context.Context, Request) (Response, error) {
			return Response{}, &StatusError{Status: 502}
		}},
	}
	_, attempts, err := Chain{}.Run(context.Background(), "web", "read", Request{URL: "https://example.com"}, backends)
	if err == nil || !strings.Contains(err.Error(), "all backends failed") {
		t.Fatalf("err = %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v", attempts)
	}
}

func TestChainNilOrdererKeepsDeclaredOrder(t *testing.T) {
	t.Parallel()
	order := []string{}
	backends := []Backend{
		stubBackend{name: "alpha", fn: func(context.Context, Request) (Response, error) {
			order = append(order, "alpha")
			return Response{}, &StatusError{Status: 503}
		}},
		stubBackend{name: "beta", fn: func(context.Context, Request) (Response, error) {
			order = append(order, "beta")
			return Response{Body: "done", Status: 200, Provenance: "beta"}, nil
		}},
	}
	_, _, err := Chain{}.Run(context.Background(), "web", "read", Request{URL: "https://example.com"}, backends)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(order) != 2 || order[0] != "alpha" || order[1] != "beta" {
		t.Fatalf("order = %v", order)
	}
}

type reorderOrderer struct{}

func (reorderOrderer) Order(_, _ string, declared []string) []string {
	if len(declared) < 2 {
		return declared
	}
	return []string{declared[1], declared[0]}
}

func TestChainOrdererReorders(t *testing.T) {
	t.Parallel()
	order := []string{}
	backends := []Backend{
		stubBackend{name: "alpha", fn: func(context.Context, Request) (Response, error) {
			order = append(order, "alpha")
			return Response{Body: "a", Status: 200, Provenance: "alpha"}, nil
		}},
		stubBackend{name: "beta", fn: func(context.Context, Request) (Response, error) {
			order = append(order, "beta")
			return Response{Body: "b", Status: 200, Provenance: "beta"}, nil
		}},
	}
	_, _, err := Chain{Orderer: reorderOrderer{}}.Run(context.Background(), "web", "read", Request{URL: "https://example.com"}, backends)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(order) != 1 || order[0] != "beta" {
		t.Fatalf("expected beta first, order=%v", order)
	}
}

type recordSpy struct {
	records []string
}

func (r *recordSpy) Record(channel, action, backend string, rank int, code, fingerprint string) {
	r.records = append(r.records, fmt.Sprintf("%s/%s:%s@%d=%s:%s", channel, action, backend, rank, code, fingerprint))
}

func TestChainRecorderPerAttempt(t *testing.T) {
	t.Parallel()
	spy := &recordSpy{}
	backends := []Backend{
		stubBackend{name: "a", fn: func(context.Context, Request) (Response, error) {
			return Response{}, &StatusError{Status: 500}
		}},
		stubBackend{name: "b", fn: func(context.Context, Request) (Response, error) {
			return Response{Body: "x", Status: 200, Provenance: "b"}, nil
		}},
	}
	_, _, err := Chain{Recorder: spy}.Run(context.Background(), "web", "read", Request{URL: "https://example.com"}, backends)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(spy.records) != 2 {
		t.Fatalf("records = %v", spy.records)
	}
	if !strings.Contains(spy.records[0], "=fallback:") || !strings.Contains(spy.records[1], "=ok:") {
		t.Fatalf("records = %v", spy.records)
	}
}
