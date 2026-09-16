package autopilot

import (
	"context"
	"testing"
    "log"
)

func TestPriorityQueueCoverage(t *testing.T) {
	pq := NewPriorityQueue()

	// Enqueue an item
	item := &WorkItem{
		ID: "item1",
		Severity: 1.0,
		Confidence: 1.0,
		Cost: 1.0,
	}
	pq.Enqueue(item)

	if pq.Len() != 1 {
		t.Errorf("Expected len 1, got %d", pq.Len())
	}

	pq.UpdateScore("item1", 2.0, 1.0, 1.0, DefaultPriorityWeights())

	popped := pq.Dequeue()
	if popped == nil || popped.ID != "item1" {
		t.Errorf("Expected popped item1, got %v", popped)
	}

	pq.Enqueue(item)
	pq.Clear()
	if pq.Len() != 0 {
		t.Errorf("Expected len 0 after clear, got %d", pq.Len())
	}
}

func TestSchedulerStartCoverage(t *testing.T) {
	config := DefaultConfig()
    config.Cron = "0 0 * * * *"
	sched, err := NewScheduler(config, func(ctx context.Context, item *WorkItem) error { return nil })
    if err != nil {
        t.Fatalf("unexpected err: %v", err)
    }

	sched.SetLogger(log.Default())
	_ = sched.GetQueue()
	_ = sched.GetConfig()
    _ = sched.IsRunning()
	_ = sched.Start()
}
