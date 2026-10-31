package reach

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"strings"
)

// Orderer reorders declared backends from health evidence (S2).
type Orderer interface {
	Order(channel, action string, declared []string) []string
}

// Recorder receives one call per backend attempt (S2 health ledger).
type Recorder interface {
	Record(channel, action, backend string, rank int, code, fingerprint string)
}

// Chain dispatches through ordered backends with optional health hooks.
type Chain struct {
	Orderer  Orderer
	Recorder Recorder
}

// Attempt records one backend try in the fallback chain.
type Attempt struct {
	Rank    int    `json:"rank"`
	Backend string `json:"backend"`
	Class   string `json:"class"`
}

// Execute resolves backends from the registry and runs the chain.
func (c Chain) Execute(ctx context.Context, channel Channel, action Action, req Request) (Response, []Attempt, error) {
	backends, err := Lookup(channel, action)
	if err != nil {
		return Response{}, nil, err
	}
	return c.Run(ctx, string(channel), string(action), req, backends)
}

// Run executes backends in declared order (after optional Orderer reorder).
func (c Chain) Run(ctx context.Context, channel, action string, req Request, backends []Backend) (Response, []Attempt, error) {
	if len(backends) == 0 {
		return Response{}, nil, fmt.Errorf("reach: no backends configured for %s/%s", channel, action)
	}

	names := make([]string, len(backends))
	byName := make(map[string]Backend, len(backends))
	for i, b := range backends {
		names[i] = b.Name()
		byName[b.Name()] = b
	}
	order := names
	if c.Orderer != nil {
		order = c.Orderer.Order(channel, action, names)
	}

	var attempts []Attempt
	var fallbacks []string

	for rank, name := range order {
		b := byName[name]
		if b == nil {
			continue
		}
		resp, err := b.Fetch(ctx, req)
		class := Classify(err, resp.Status)
		fp := fingerprint(err)
		if c.Recorder != nil {
			c.Recorder.Record(channel, action, name, rank, class, fp)
		}
		attempts = append(attempts, Attempt{Rank: rank, Backend: name, Class: class})

		if class == "ok" {
			return resp, attempts, nil
		}
		if class == "terminal" {
			if err != nil {
				return Response{}, attempts, err
			}
			return Response{}, attempts, fmt.Errorf("reach: terminal failure on backend %q", name)
		}
		fallbacks = append(fallbacks, fmt.Sprintf("%s=%s", name, class))
	}

	return Response{}, attempts, fmt.Errorf("reach: all backends failed (%s)", strings.Join(fallbacks, ", "))
}

// Classify maps errors and HTTP status into ok, fallback, or terminal.
func Classify(err error, status int) string {
	if err == nil && status >= 200 && status < 300 {
		return "ok"
	}
	if st, ok := StatusFromError(err); ok {
		status = st
	}
	if status == 429 || status >= 500 {
		return "fallback"
	}
	if status >= 400 && status < 500 {
		return "terminal"
	}
	if err == nil {
		return "terminal"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "fallback"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "fallback"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "fallback"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "invalid") || strings.Contains(msg, "not found in path") {
		return "terminal"
	}
	return "fallback"
}

func fingerprint(err error) string {
	if err == nil {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(err.Error()))
	return fmt.Sprintf("%08x", h.Sum32())
}
