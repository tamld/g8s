package watch

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClock advances on every read so deadlines elapse without sleeping.
type fakeClock struct{ ticks int }

func (f *fakeClock) Now() time.Time {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	f.ticks++
	return base.Add(time.Duration(f.ticks) * time.Minute)
}

func countingSleeper(calls *int) Sleeper {
	return func(ctx context.Context, d time.Duration) error {
		*calls++
		return nil
	}
}

// Red→Green (#371): an unsatisfiable checker must produce a TIMEOUT verdict
// when the injected clock runs past the deadline — no real sleeping.
func TestWatchTimeout(t *testing.T) {
	clock := &fakeClock{}
	calls := 0
	polls := 0
	w := Watcher{
		Checker: func(ctx context.Context) (CheckResult, error) {
			polls++
			return CheckResult{Done: false}, nil
		},
		Interval: time.Minute,
		Timeout:  3 * time.Minute,
		Clock:    clock.Now,
		Sleeper:  countingSleeper(&calls),
	}
	v, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.State != StateTimeout {
		t.Errorf("expected timeout, got %s", v.State)
	}
	if polls != 3 {
		t.Errorf("expected 3 polls before deadline, got %d", polls)
	}
	if calls != 3 {
		t.Errorf("expected 3 sleeps, got %d", calls)
	}
}

// A checker that satisfies on the second poll yields PASSED with the poll
// count recorded.
func TestWatchPassedOnSecondPoll(t *testing.T) {
	clock := &fakeClock{}
	n := 0
	calls := 0
	w := Watcher{
		Checker: func(ctx context.Context) (CheckResult, error) {
			n++
			if n >= 2 {
				return CheckResult{Done: true, Passed: true, Detail: "all checks success"}, nil
			}
			return CheckResult{Done: false}, nil
		},
		Interval: time.Minute,
		Timeout:  10 * time.Minute,
		Clock:    clock.Now,
		Sleeper:  countingSleeper(&calls),
	}
	v, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.State != StatePassed {
		t.Errorf("expected passed, got %s", v.State)
	}
	if v.Polls != 2 {
		t.Errorf("expected 2 polls, got %d", v.Polls)
	}
}

// A negative terminal observation yields FAILED with the failing items.
func TestWatchFailedTerminal(t *testing.T) {
	clock := &fakeClock{}
	w := Watcher{
		Checker: func(ctx context.Context) (CheckResult, error) {
			return CheckResult{Done: true, Passed: false, Failed: []string{"Test windows-latest"}}, nil
		},
		Interval: time.Minute,
		Timeout:  10 * time.Minute,
		Clock:    clock.Now,
		Sleeper:  countingSleeper(new(int)),
	}
	v, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.State != StateFailed {
		t.Errorf("expected failed, got %s", v.State)
	}
	if len(v.Failed) != 1 || v.Failed[0] != "Test windows-latest" {
		t.Errorf("expected failed item recorded, got %v", v.Failed)
	}
}

// Checker errors are retried until the deadline, then surface in the detail.
func TestWatchCheckerErrorsRetried(t *testing.T) {
	clock := &fakeClock{}
	boom := errors.New("network flap")
	w := Watcher{
		Checker: func(ctx context.Context) (CheckResult, error) {
			return CheckResult{}, boom
		},
		Interval: time.Minute,
		Timeout:  2 * time.Minute,
		Clock:    clock.Now,
		Sleeper:  countingSleeper(new(int)),
	}
	v, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.State != StateTimeout {
		t.Errorf("expected timeout after retries, got %s", v.State)
	}
	if v.Detail == "" {
		t.Errorf("expected checker error preserved in detail")
	}
}

// Context cancellation through the real sleeper stops the watch.
func TestWatchContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := Watcher{
		Checker: func(ctx context.Context) (CheckResult, error) {
			return CheckResult{Done: false}, nil
		},
		Interval: time.Minute,
		Timeout:  time.Hour,
		Sleeper: func(ctx context.Context, d time.Duration) error {
			return ctx.Err()
		},
	}
	v, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.State != StateTimeout {
		t.Errorf("expected timeout on cancelled context, got %s", v.State)
	}
}
