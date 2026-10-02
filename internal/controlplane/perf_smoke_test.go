package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type opLatencyStats struct {
	p50   time.Duration
	p95   time.Duration
	min   time.Duration
	max   time.Duration
	count int
}

func calcLatencyStats(samples []time.Duration) opLatencyStats {
	if len(samples) == 0 {
		return opLatencyStats{}
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	p50Idx := int(float64(len(sorted)-1) * 0.50)
	p95Idx := int(float64(len(sorted)-1) * 0.95)

	return opLatencyStats{
		p50:   sorted[p50Idx],
		p95:   sorted[p95Idx],
		min:   sorted[0],
		max:   sorted[len(sorted)-1],
		count: len(sorted),
	}
}

func durToMS(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// TestPerfSmokeQueueOps is a smoke benchmark for queue operations
// (submit, claim, heartbeat, finish) under concurrent workers.
// It verifies baseline responsiveness and fails on pathological lock contention.
func TestPerfSmokeQueueOps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping queue perf smoke benchmark in short mode")
	}

	dbPath := filepath.Join(t.TempDir(), "perf-smoke.sqlite3")
	store, err := NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()

	const numTasks = 200
	const numWorkers = 10
	ctx := context.Background()

	// 1. Pre-seed queue of ~200 tasks while measuring per-op submit latency
	submitLatencies := make([]time.Duration, numTasks)
	for i := 0; i < numTasks; i++ {
		req := SubmitTaskRequest{
			IdempotencyKey: fmt.Sprintf("perf-smoke-task-%04d", i),
			Payload:        []byte(fmt.Sprintf(`{"task_index":%d,"prompt":"perf smoke op","role":"collector"}`, i)),
			MaxAttempts:    3,
			Model:          "smoke-model",
			AddDirs:        []string{"/tmp/g8s-cp-test-scope"},
		}
		start := time.Now()
		task, err := store.SubmitTask(ctx, req)
		submitLatencies[i] = time.Since(start)
		if err != nil {
			t.Fatalf("SubmitTask [%d]: %v", i, err)
		}
		if task == nil {
			t.Fatalf("SubmitTask [%d] returned nil task", i)
		}
	}

	// 2. Concurrently execute claim -> heartbeat -> finish across N=10 goroutine workers
	type workerMetrics struct {
		claims     []time.Duration
		heartbeats []time.Duration
		finishes   []time.Duration
	}

	var (
		wg            sync.WaitGroup
		finishedCount atomic.Int64
	)
	workerResults := make([]workerMetrics, numWorkers)

	wg.Add(numWorkers)
	for w := 0; w < numWorkers; w++ {
		workerIdx := w
		workerID := fmt.Sprintf("worker-%02d", workerIdx)
		go func() {
			defer wg.Done()
			var m workerMetrics
			for finishedCount.Load() < int64(numTasks) {
				claimStart := time.Now()
				task, err := store.ClaimTask(ctx, workerID, 60)
				claimDur := time.Since(claimStart)

				if err != nil {
					time.Sleep(1 * time.Millisecond)
					continue
				}
				if task == nil {
					if finishedCount.Load() >= int64(numTasks) {
						break
					}
					time.Sleep(1 * time.Millisecond)
					continue
				}

				m.claims = append(m.claims, claimDur)

				token := ""
				if task.LeaseToken != nil {
					token = *task.LeaseToken
				}

				if !store.StartTask(task.TaskID, workerID, token) {
					continue
				}

				hbStart := time.Now()
				hbErr := store.Heartbeat(ctx, task.TaskID, workerID, token, 60)
				hbDur := time.Since(hbStart)
				if hbErr == nil {
					m.heartbeats = append(m.heartbeats, hbDur)
				}

				finStart := time.Now()
				_, finErr := store.FinishAttempt(task.TaskID, workerID, token, FinishAttemptParams{
					Result:  json.RawMessage(`{"status":"success"}`),
					Success: true,
				})
				finDur := time.Since(finStart)
				if finErr != nil {
					continue
				}

				m.finishes = append(m.finishes, finDur)
				finishedCount.Add(1)
			}
			workerResults[workerIdx] = m
		}()
	}

	wg.Wait()

	if finished := finishedCount.Load(); finished != int64(numTasks) {
		t.Fatalf("expected %d tasks finished, got %d", numTasks, finished)
	}

	var claimLatencies []time.Duration
	var heartbeatLatencies []time.Duration
	var finishLatencies []time.Duration
	for _, res := range workerResults {
		claimLatencies = append(claimLatencies, res.claims...)
		heartbeatLatencies = append(heartbeatLatencies, res.heartbeats...)
		finishLatencies = append(finishLatencies, res.finishes...)
	}

	if len(submitLatencies) < numTasks {
		t.Fatalf("expected >= %d submit samples, got %d", numTasks, len(submitLatencies))
	}
	if len(claimLatencies) < numTasks {
		t.Fatalf("expected >= %d claim samples, got %d", numTasks, len(claimLatencies))
	}
	if len(finishLatencies) < numTasks {
		t.Fatalf("expected >= %d finish samples, got %d", numTasks, len(finishLatencies))
	}

	submitStats := calcLatencyStats(submitLatencies)
	claimStats := calcLatencyStats(claimLatencies)
	heartbeatStats := calcLatencyStats(heartbeatLatencies)
	finishStats := calcLatencyStats(finishLatencies)

	// Machine-readable output lines for docs capture
	perfSubmit := fmt.Sprintf("PERF submit=p50=%.2fms p95=%.2fms", durToMS(submitStats.p50), durToMS(submitStats.p95))
	perfClaim := fmt.Sprintf("PERF claim=p50=%.2fms p95=%.2fms", durToMS(claimStats.p50), durToMS(claimStats.p95))
	perfHeartbeat := fmt.Sprintf("PERF heartbeat=p50=%.2fms p95=%.2fms", durToMS(heartbeatStats.p50), durToMS(heartbeatStats.p95))
	perfFinish := fmt.Sprintf("PERF finish=p50=%.2fms p95=%.2fms", durToMS(finishStats.p50), durToMS(finishStats.p95))
	perfCombined := fmt.Sprintf("PERF submit=p50=%.2fms p95=%.2fms claim=p50=%.2fms p95=%.2fms finish=p50=%.2fms p95=%.2fms",
		durToMS(submitStats.p50), durToMS(submitStats.p95),
		durToMS(claimStats.p50), durToMS(claimStats.p95),
		durToMS(finishStats.p50), durToMS(finishStats.p95),
	)

	fmt.Fprintln(os.Stdout, perfCombined)
	fmt.Fprintln(os.Stdout, perfSubmit)
	fmt.Fprintln(os.Stdout, perfClaim)
	fmt.Fprintln(os.Stdout, perfHeartbeat)
	fmt.Fprintln(os.Stdout, perfFinish)

	t.Log(perfCombined)
	t.Log(perfSubmit)
	t.Log(perfClaim)
	t.Log(perfHeartbeat)
	t.Log(perfFinish)

	// Pathology assertion: fail only on lock catastrophe (p95 > 10s)
	const maxPathology = 10 * time.Second
	if submitStats.p95 > maxPathology {
		t.Fatalf("pathological lock contention on submit: p95=%v > %v", submitStats.p95, maxPathology)
	}
	if claimStats.p95 > maxPathology {
		t.Fatalf("pathological lock contention on claim: p95=%v > %v", claimStats.p95, maxPathology)
	}
	if finishStats.p95 > maxPathology {
		t.Fatalf("pathological lock contention on finish: p95=%v > %v", finishStats.p95, maxPathology)
	}
	if heartbeatStats.p95 > maxPathology {
		t.Fatalf("pathological lock contention on heartbeat: p95=%v > %v", heartbeatStats.p95, maxPathology)
	}
}
