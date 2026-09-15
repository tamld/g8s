// Package autopilot implements the cron-based supervisor trigger that scans
// GitHub issues, failing CI runs, static analysis findings, and stale
// @agy-fix-me TODOs. It uses a priority queue weighted by severity,
// confidence, and cost-inverse.
package autopilot

import (
	"container/heap"
	"sort"
	"sync"
	"time"
)

// WorkItem represents a unit of work discovered by the autopilot scanner.
type WorkItem struct {
	ID          string     `json:"id"`
	Source      SourceType `json:"source"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Labels      []string   `json:"labels"`
	URL         string     `json:"url,omitempty"`
	FilePath    string     `json:"file_path,omitempty"`
	LineNumber  int        `json:"line_number,omitempty"`

	// Scoring factors
	Severity   float64 `json:"severity"`   // 0-1: how critical is this
	Confidence float64 `json:"confidence"` // 0-1: how confident we are this needs fixing
	Cost       float64 `json:"cost"`       // estimated effort (1 = low, higher = more effort)

	// Computed score (higher = more important)
	Score float64 `json:"score"`

	// Metadata
	DiscoveredAt time.Time         `json:"discovered_at"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// SourceType identifies where the work item came from.
type SourceType string

const (
	SourceGitHubIssue    SourceType = "github_issue"
	SourceFailingCI      SourceType = "failing_ci"
	SourceStaticAnalysis SourceType = "static_analysis"
	SourceStaleTodo      SourceType = "stale_todo"
)

// priorityQueue is the internal heap implementation.
// Uses min-heap with inverted scores for max-heap behavior.
type priorityQueue []*WorkItem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// For max-heap behavior with container/heap (which is a min-heap):
	// Return true if i has HIGHER score than j, so i bubbles to top (index 0).
	// Then Pop() returns the highest score first.
	return pq[i].Score > pq[j].Score
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *priorityQueue) Push(x any) {
	if item, ok := x.(*WorkItem); ok {
		*pq = append(*pq, item)
	}
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	if n == 0 {
		return nil
	}
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

// PriorityQueue is a thread-safe priority queue using container/heap.
// It embeds the internal priorityQueue to implement heap.Interface.
type PriorityQueue struct {
	mu    sync.RWMutex
	items priorityQueue
	index map[string]int // item ID -> index in heap
}

// NewPriorityQueue creates a new empty priority queue.
func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		items: make(priorityQueue, 0),
		index: make(map[string]int),
	}
}

// Len returns the number of items in the queue (implements heap.Interface).
func (pq *PriorityQueue) Len() int {
	return len(pq.items)
}

// Less implements heap.Interface for the internal heap.
func (pq *PriorityQueue) Less(i, j int) bool {
	return pq.items.Less(i, j)
}

// Swap implements heap.Interface for the internal heap.
func (pq *PriorityQueue) Swap(i, j int) {
	pq.items.Swap(i, j)
	pq.index[pq.items[i].ID] = i
	pq.index[pq.items[j].ID] = j
}

// Push implements heap.Interface for the internal heap.
func (pq *PriorityQueue) Push(x any) {
	if item, ok := x.(*WorkItem); ok {
		pq.index[item.ID] = len(pq.items)
		pq.items.Push(item)
	}
}

// Pop implements heap.Interface for the internal heap.
func (pq *PriorityQueue) Pop() any {
	if len(pq.items) == 0 {
		return nil
	}
	popped := pq.items.Pop()
	if item, ok := popped.(*WorkItem); ok {
		delete(pq.index, item.ID)
		return item
	}
	return nil
}

// Enqueue adds an item to the queue (thread-safe).
func (pq *PriorityQueue) Enqueue(item *WorkItem) {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	heap.Push(pq, item)
}

// Dequeue removes and returns the highest-priority item (thread-safe).
func (pq *PriorityQueue) Dequeue() *WorkItem {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if len(pq.items) == 0 {
		return nil
	}
	if item, ok := heap.Pop(pq).(*WorkItem); ok {
		return item
	}
	return nil
}

// Peek returns the highest-priority item without removing it (thread-safe).
func (pq *PriorityQueue) Peek() *WorkItem {
	pq.mu.RLock()
	defer pq.mu.RUnlock()

	if len(pq.items) == 0 {
		return nil
	}
	return pq.items[0]
}

// Get returns an item by ID (thread-safe).
func (pq *PriorityQueue) Get(id string) *WorkItem {
	pq.mu.RLock()
	defer pq.mu.RUnlock()

	if idx, ok := pq.index[id]; ok {
		return pq.items[idx]
	}
	return nil
}

// Remove removes an item by ID (thread-safe).
func (pq *PriorityQueue) Remove(id string) bool {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	idx, ok := pq.index[id]
	if !ok {
		return false
	}

	heap.Remove(pq, idx)
	delete(pq.index, id)
	return true
}

// Clear removes all items (thread-safe).
func (pq *PriorityQueue) Clear() {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	pq.items = make(priorityQueue, 0)
	pq.index = make(map[string]int)
}

// Items returns a copy of all items sorted by score (highest first).
func (pq *PriorityQueue) Items() []*WorkItem {
	pq.mu.RLock()
	defer pq.mu.RUnlock()

	result := make([]*WorkItem, len(pq.items))
	copy(result, pq.items)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result
}

// CalculateScore computes the priority score for a work item.
// score = severity * severity_weight + confidence * confidence_weight + (1/cost) * cost_inverse_weight
func CalculateScore(item *WorkItem, weights PriorityWeights) float64 {
	// Normalize cost to 0-1 range (1 = low cost = high priority)
	// Using inverse: cost 1 -> 1.0, cost 10 -> 0.1
	costInverse := 1.0 / (1.0 + item.Cost)

	score := item.Severity*weights.Severity +
		item.Confidence*weights.Confidence +
		costInverse*weights.CostInverse

	return score
}

// UpdateScore recalculates and updates the score for an item, then re-heapifies.
func (pq *PriorityQueue) UpdateScore(id string, severity, confidence, cost float64, weights PriorityWeights) bool {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	idx, ok := pq.index[id]
	if !ok {
		return false
	}

	item := pq.items[idx]
	item.Severity = severity
	item.Confidence = confidence
	item.Cost = cost
	item.Score = CalculateScore(item, weights)

	// Re-heapify: bubble up or down
	heap.Fix(pq, idx)
	return true
}

// TopN returns the top N items by score (thread-safe).
func (pq *PriorityQueue) TopN(n int) []*WorkItem {
	pq.mu.RLock()
	defer pq.mu.RUnlock()

	if n <= 0 || len(pq.items) == 0 {
		return nil
	}
	if n > len(pq.items) {
		n = len(pq.items)
	}

	// Copy and sort
	result := make([]*WorkItem, len(pq.items))
	copy(result, pq.items)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})

	return result[:n]
}
