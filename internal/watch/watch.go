// Package watch implements the blocking watch primitive (#371): poll a
// condition until it reaches a terminal state, then emit a verdict. Run
// `g8s watch` as a background process — the process-exit notification is the
// push channel that wakes the supervisor without any sleep-polling.
package watch

import (
	"context"
	"fmt"
	"time"
)

// VerdictState classifies the terminal outcome of a watch.
type VerdictState string

const (
	// StatePassed means every observed item is in the desired terminal state.
	StatePassed VerdictState = "passed"
	// StateFailed means a watched item reached a negative terminal state.
	StateFailed VerdictState = "failed"
	// StateTimeout means the deadline elapsed without a terminal outcome.
	StateTimeout VerdictState = "timeout"
)

// CheckResult is one observation of the watched target.
type CheckResult struct {
	// Done reports whether the target reached a terminal state.
	Done bool
	// Passed is the desired terminal outcome (Done must be true).
	Passed bool
	// Failed lists items observed in a negative terminal state.
	Failed []string
	// Detail carries human-readable context for the verdict.
	Detail string
}

// Checker performs one observation of the watched target.
type Checker func(ctx context.Context) (CheckResult, error)

// Sleeper pauses between polls; abstracted so tests run without real delays.
type Sleeper func(ctx context.Context, d time.Duration) error

// Watcher polls a Checker until terminal or deadline.
type Watcher struct {
	Checker  Checker
	Interval time.Duration
	Timeout  time.Duration
	Clock    func() time.Time
	Sleeper  Sleeper
}

// Verdict is the terminal outcome of a watch run.
type Verdict struct {
	State   VerdictState
	Failed  []string
	Detail  string
	Elapsed time.Duration
	Polls   int
}

// ErrWatchChecker describes a checker failure that is retried until the
// deadline (transient infrastructure errors, e.g. network flaps).
type ErrWatchChecker struct{ Err error }

func (e *ErrWatchChecker) Error() string {
	return fmt.Sprintf("watch checker error: %v", e.Err)
}

// Run polls until the checker reports terminal or the deadline elapses.
func (w Watcher) Run(ctx context.Context) (Verdict, error) {
	clock := w.Clock
	if clock == nil {
		clock = time.Now
	}
	sleep := w.Sleeper
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	interval := w.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}

	start := clock()
	deadline := start.Add(timeout)
	var lastErr error
	polls := 0
	for {
		if clock().After(deadline) {
			return Verdict{State: StateTimeout, Detail: lastDetail(lastErr), Elapsed: clock().Sub(start), Polls: polls}, nil
		}
		polls++
		res, err := w.Checker(ctx)
		if err != nil {
			lastErr = err
		} else if res.Done {
			state := StatePassed
			if !res.Passed {
				state = StateFailed
			}
			return Verdict{State: state, Failed: res.Failed, Detail: res.Detail, Elapsed: clock().Sub(start), Polls: polls}, nil
		}
		if werr := sleep(ctx, interval); werr != nil {
			return Verdict{State: StateTimeout, Detail: lastDetail(lastErr), Elapsed: clock().Sub(start), Polls: polls}, nil
		}
	}
}

func lastDetail(lastErr error) string {
	if lastErr != nil {
		return (&ErrWatchChecker{Err: lastErr}).Error()
	}
	return "deadline exceeded"
}
