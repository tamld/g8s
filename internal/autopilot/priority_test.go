// Package autopilot implements the cron-based supervisor trigger that scans
// GitHub issues, failing CI runs, static analysis findings, and stale
// @agy-fix-me TODOs. It uses a priority queue weighted by severity,
// confidence, and cost-inverse.
package autopilot

import (
	"container/heap"
	"testing"
	"time"
)

func TestPriorityQueue_Basic(t *testing.T) {
	pq := NewPriorityQueue()

	item1 := &WorkItem{
		ID:           "item1",
		Score:        0.5,
		DiscoveredAt: time.Now(),
	}
	item2 := &WorkItem{
		ID:           "item2",
		Score:        0.8,
		DiscoveredAt: time.Now(),
	}
	item3 := &WorkItem{
		ID:           "item3",
		Score:        0.3,
		DiscoveredAt: time.Now(),
	}

	heap.Push(pq, item1)
	heap.Push(pq, item2)
	heap.Push(pq, item3)

	if pq.Len() != 3 {
		t.Errorf("expected 3 items, got %d", pq.Len())
	}

	// Pop should return highest score first
	top, ok := heap.Pop(pq).(*WorkItem)
	if !ok || top.ID != "item2" || top.Score != 0.8 {
		t.Fatalf("expected item2 with score 0.8, got %v", top)
	}

	top, ok = heap.Pop(pq).(*WorkItem)
	if !ok || top.ID != "item1" || top.Score != 0.5 {
		t.Fatalf("expected item1 with score 0.5, got %v", top)
	}

	top, ok = heap.Pop(pq).(*WorkItem)
	if !ok || top.ID != "item3" || top.Score != 0.3 {
		t.Fatalf("expected item3 with score 0.3, got %v", top)
	}

	if pq.Len() != 0 {
		t.Errorf("expected 0 items after popping all, got %d", pq.Len())
	}
}

func TestPriorityQueue_Peek(t *testing.T) {
	pq := NewPriorityQueue()

	if pq.Peek() != nil {
		t.Error("expected nil from empty queue")
	}

	item := &WorkItem{ID: "test", Score: 0.7, DiscoveredAt: time.Now()}
	heap.Push(pq, item)

	peeked := pq.Peek()
	if peeked == nil || peeked.ID != "test" {
		t.Error("peek failed")
	}

	if pq.Len() != 1 {
		t.Error("peek should not remove item")
	}
}

func TestPriorityQueue_GetAndRemove(t *testing.T) {
	pq := NewPriorityQueue()

	item1 := &WorkItem{ID: "item1", Score: 0.5, DiscoveredAt: time.Now()}
	item2 := &WorkItem{ID: "item2", Score: 0.8, DiscoveredAt: time.Now()}

	heap.Push(pq, item1)
	heap.Push(pq, item2)

	// Get existing
	got := pq.Get("item1")
	if got == nil || got.ID != "item1" {
		t.Error("Get failed for existing item")
	}

	// Get non-existing
	got = pq.Get("nonexistent")
	if got != nil {
		t.Error("Get should return nil for non-existing item")
	}

	// Remove existing
	ok := pq.Remove("item1")
	if !ok {
		t.Error("Remove should return true for existing item")
	}
	if pq.Len() != 1 {
		t.Errorf("expected 1 item after remove, got %d", pq.Len())
	}
	if pq.Get("item1") != nil {
		t.Error("removed item should not be retrievable")
	}

	// Remove non-existing
	ok = pq.Remove("nonexistent")
	if ok {
		t.Error("Remove should return false for non-existing item")
	}
}

func TestPriorityQueue_Items(t *testing.T) {
	pq := NewPriorityQueue()

	items := []*WorkItem{
		{ID: "a", Score: 0.1, DiscoveredAt: time.Now()},
		{ID: "b", Score: 0.9, DiscoveredAt: time.Now()},
		{ID: "c", Score: 0.5, DiscoveredAt: time.Now()},
	}

	for _, item := range items {
		heap.Push(pq, item)
	}

	result := pq.Items()
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d", len(result))
	}

	// Should be sorted by score descending
	if result[0].ID != "b" || result[1].ID != "c" || result[2].ID != "a" {
		t.Errorf("items not sorted correctly: %v", result)
	}
}

func TestPriorityQueue_TopN(t *testing.T) {
	pq := NewPriorityQueue()

	for i := 0; i < 10; i++ {
		heap.Push(pq, &WorkItem{
			ID:           string(rune('a' + i)),
			Score:        float64(i) / 10.0,
			DiscoveredAt: time.Now(),
		})
	}

	top3 := pq.TopN(3)
	if len(top3) != 3 {
		t.Errorf("expected 3 items, got %d", len(top3))
	}
	if top3[0].Score != 0.9 || top3[1].Score != 0.8 || top3[2].Score != 0.7 {
		t.Errorf("top3 scores wrong: %v", top3)
	}

	// More than available
	top15 := pq.TopN(15)
	if len(top15) != 10 {
		t.Errorf("expected 10 items, got %d", len(top15))
	}

	// Zero or negative
	if pq.TopN(0) != nil {
		t.Error("TopN(0) should return nil")
	}
	if pq.TopN(-1) != nil {
		t.Error("TopN(-1) should return nil")
	}
}

func TestCalculateScore(t *testing.T) {
	weights := PriorityWeights{
		Severity:    1.0,
		Confidence:  1.0,
		CostInverse: 0.5,
	}

	item := &WorkItem{
		Severity:   0.8,
		Confidence: 0.9,
		Cost:       2.0,
	}

	score := CalculateScore(item, weights)
	// costInverse = 1/(1+2) = 0.333
	// score = 0.8*1.0 + 0.9*1.0 + 0.333*0.5 = 0.8 + 0.9 + 0.167 = 1.867
	expected := 0.8*1.0 + 0.9*1.0 + (1.0/(1.0+2.0))*0.5
	if abs(score-expected) > 0.001 {
		t.Errorf("score = %.3f, expected %.3f", score, expected)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Cron != "0 * * * *" {
		t.Errorf("default cron = %q, expected %q", cfg.Cron, "0 * * * *")
	}
	if cfg.MaxItemsPerTick != 50 {
		t.Errorf("default max items = %d, expected 50", cfg.MaxItemsPerTick)
	}
	if cfg.MinScoreThreshold != 0.1 {
		t.Errorf("default min score = %f, expected 0.1", cfg.MinScoreThreshold)
	}
	if cfg.LookbackWindow != 24*time.Hour {
		t.Errorf("default lookback = %v, expected 24h", cfg.LookbackWindow)
	}
	if cfg.CodebasePath != "." {
		t.Errorf("default codebase path = %q, expected .", cfg.CodebasePath)
	}

	weights := cfg.PriorityWeights
	if weights.Severity != 1.0 || weights.Confidence != 1.0 || weights.CostInverse != 0.5 {
		t.Errorf("default weights = %+v", weights)
	}

	sources := cfg.ScanSources
	if !sources.GitHubIssues || !sources.FailingCI || !sources.StaticAnalysis || !sources.StaleTodos {
		t.Errorf("default sources = %+v", sources)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
