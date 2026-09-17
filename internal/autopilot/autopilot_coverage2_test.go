package autopilot

import (
	"context"
	"testing"
)

func TestSchedulerStartCoverageCronErr(t *testing.T) {
	config := DefaultConfig()
	config.Cron = "invalid cron"
	_, err := NewScheduler(config, func(ctx context.Context, item *WorkItem) error { return nil })
	if err == nil {
		t.Fatalf("expected err")
	}
}
