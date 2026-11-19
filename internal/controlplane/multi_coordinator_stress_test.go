package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMultiCoordinatorSQLiteWALStressAndFencing(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "multi-coord.sqlite3")
	baseClock := newFakeClock()

	// 3 separate coordinator *Store pools sharing the same SQLite WAL DB with +-2s clock skew.
	skewedClock := func(delta time.Duration) func() time.Time {
		return func() time.Time {
			return baseClock.Now().Add(delta)
		}
	}
	coordA, err := NewControlPlane(dbPath, skewedClock(0))
	if err != nil {
		t.Fatalf("NewControlPlane coordA: %v", err)
	}
	defer func() { _ = coordA.Close() }()

	coordB, err := NewControlPlane(dbPath, skewedClock(2*time.Second))
	if err != nil {
		t.Fatalf("NewControlPlane coordB: %v", err)
	}
	defer func() { _ = coordB.Close() }()

	coordC, err := NewControlPlane(dbPath, skewedClock(-2*time.Second))
	if err != nil {
		t.Fatalf("NewControlPlane coordC: %v", err)
	}
	defer func() { _ = coordC.Close() }()

	coords := []*Store{coordA, coordB, coordC}
	coordNames := []string{"coord-a", "coord-b", "coord-c"}

	const totalTasks = 24
	const staleCount = 6

	// 1. Concurrent task submission + duplicate idempotency retries across 3 coordinators.
	taskIDs := make([]string, totalTasks)
	var submitErrs atomic.Int32
	var submitWG sync.WaitGroup
	for i := 0; i < totalTasks; i++ {
		i := i
		submitWG.Add(1)
		go func() {
			defer submitWG.Done()
			store := coords[i%len(coords)]
			key := fmt.Sprintf("stress-task-%02d", i)
			req := SubmitTaskRequest{
				IdempotencyKey: key,
				MaxAttempts:    3,
				Model:          "test-model",
				AddDirs:        []string{testScopeDir},
				Payload:        json.RawMessage(fmt.Sprintf(`{"prompt":"stress-%d","role":"collector"}`, i)),
			}
			task, sErr := store.SubmitTask(ctx, req)
			if sErr != nil || task == nil {
				submitErrs.Add(1)
				return
			}
			taskIDs[i] = task.TaskID

			// Duplicate idempotent retry on a different skewed coordinator must return the exact same TaskID.
			other := coords[(i+1)%len(coords)]
			dup, dErr := other.SubmitTask(ctx, req)
			if dErr != nil || dup == nil || dup.TaskID != task.TaskID {
				submitErrs.Add(1)
			}
		}()
	}
	submitWG.Wait()
	if submitErrs.Load() != 0 {
		t.Fatalf("concurrent submit/idempotency had %d errors", submitErrs.Load())
	}

	// 2. Inject 6 stalling leased tasks with short 5s TTL, then advance clock past +-2s skew.
	type staleClaim struct {
		taskID     string
		workerID   string
		leaseToken string
	}
	var staleClaims []staleClaim
	for i := 0; i < staleCount; i++ {
		workerID := fmt.Sprintf("stalled-worker-%d", i)
		cStore := coords[i%len(coords)]
		claimed, cErr := cStore.ClaimTask(ctx, workerID, 5)
		if cErr != nil || claimed == nil || claimed.LeaseToken == nil {
			t.Fatalf("stale ClaimTask %d: task=%v err=%v", i, claimed, cErr)
		}
		if !cStore.StartTask(claimed.TaskID, workerID, *claimed.LeaseToken) {
			t.Fatalf("StartTask failed for %s", claimed.TaskID)
		}
		staleClaims = append(staleClaims, staleClaim{
			taskID:     claimed.TaskID,
			workerID:   workerID,
			leaseToken: *claimed.LeaseToken,
		})
	}

	// Advance base clock by 10s so all 3 skewed coordinators (+0s/+2s/-2s) see lease expiry (>5s).
	baseClock.Advance(10 * time.Second)

	// 3. Race 3 coordinators calling ReconcileExpired + 16 healthy workers + 6 stale workers finishing late.
	var staleWG sync.WaitGroup
	var staleAccepted atomic.Int32
	for _, sc := range staleClaims {
		sc := sc
		staleWG.Add(1)
		go func() {
			defer staleWG.Done()
			// Wait a moment so reclaim or healthy workers overlap with the stale FinishAttempt.
			time.Sleep(15 * time.Millisecond)
			if _, fErr := coordC.FinishAttempt(sc.taskID, sc.workerID, sc.leaseToken, FinishAttemptParams{
				Result:  json.RawMessage(`{"ok":true,"stale":true}`),
				Success: true,
			}); fErr == nil {
				staleAccepted.Add(1)
			}
		}()
	}

	var reconcileWG sync.WaitGroup
	for idx, c := range coords {
		c := c
		idx := idx
		reconcileWG.Add(1)
		go func() {
			defer reconcileWG.Done()
			if _, rErr := c.ReconcileExpired(ctx); rErr != nil {
				t.Errorf("coord %d ReconcileExpired: %v", idx, rErr)
			}
		}()
	}

	const healthyWorkers = 16
	var completedCount atomic.Int32
	var workerErrs atomic.Int32
	var workerWG sync.WaitGroup
	for w := 0; w < healthyWorkers; w++ {
		w := w
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			cStore := coords[w%len(coords)]
			workerID := fmt.Sprintf("healthy-worker-%02d", w)
			for i := 0; i < 64; i++ {
				claimed, cErr := cStore.ClaimTask(ctx, workerID, 30)
				if cErr != nil {
					workerErrs.Add(1)
					return
				}
				if claimed == nil || claimed.LeaseToken == nil {
					if int(completedCount.Load()) >= totalTasks {
						return
					}
					time.Sleep(5 * time.Millisecond)
					continue
				}
				if !cStore.StartTask(claimed.TaskID, workerID, *claimed.LeaseToken) {
					continue
				}
				resJSON := json.RawMessage(fmt.Sprintf(`{"ok":true,"worker":%q}`, workerID))
				if _, fErr := cStore.FinishAttempt(claimed.TaskID, workerID, *claimed.LeaseToken, FinishAttemptParams{
					Result:  resJSON,
					Success: true,
				}); fErr != nil {
					workerErrs.Add(1)
					return
				}
				completedCount.Add(1)
			}
		}()
	}

	reconcileWG.Wait()
	staleWG.Wait()
	workerWG.Wait()

	if staleAccepted.Load() != 0 {
		t.Fatalf("stale worker FinishAttempt succeeded %d times despite lease expiry/reclaim", staleAccepted.Load())
	}
	if workerErrs.Load() != 0 {
		t.Fatalf("healthy workers encountered %d errors under contention", workerErrs.Load())
	}
	if int(completedCount.Load()) != totalTasks {
		t.Fatalf("completedCount = %d, want %d (lost or duplicated task)", completedCount.Load(), totalTasks)
	}

	// 4. Race all 3 coordinators on AcceptResult AND ClaimDelivery for every task:
	//    exactly-one coordinator must win AcceptResult, and exactly-one must win ClaimDelivery.
	taskAcceptWinners := make([]atomic.Int32, totalTasks)
	var acceptGate sync.WaitGroup
	startAccept := make(chan struct{})
	for idx, tid := range taskIDs {
		idx, tid := idx, tid
		for ci, c := range coords {
			c := c
			name := coordNames[ci]
			acceptGate.Add(1)
			go func() {
				defer acceptGate.Done()
				<-startAccept
				if err := c.AcceptResult(ctx, tid, name); err == nil {
					taskAcceptWinners[idx].Add(1)
				}
			}()
		}
	}
	close(startAccept)
	acceptGate.Wait()
	for idx, tid := range taskIDs {
		if got := taskAcceptWinners[idx].Load(); got != 1 {
			t.Fatalf("task %s AcceptResult winners = %d, want exactly 1", tid, got)
		}
	}

	taskDeliverWinners := make([]atomic.Int32, totalTasks)
	taskDeliverDups := make([]atomic.Int32, totalTasks)
	var deliverGate sync.WaitGroup
	startDeliver := make(chan struct{})
	for idx, tid := range taskIDs {
		idx, tid := idx, tid
		for ci, c := range coords {
			c := c
			name := coordNames[ci]
			deliverGate.Add(1)
			go func() {
				defer deliverGate.Done()
				<-startDeliver
				err := c.ClaimDelivery(ctx, tid, name, "deadbeef")
				if err == nil {
					taskDeliverWinners[idx].Add(1)
				} else if errors.Is(err, ErrTaskAlreadyDelivered) {
					taskDeliverDups[idx].Add(1)
				} else {
					t.Errorf("unexpected ClaimDelivery error for %s (%s): %v", tid, name, err)
				}
			}()
		}
	}
	close(startDeliver)
	deliverGate.Wait()
	for idx, tid := range taskIDs {
		w := taskDeliverWinners[idx].Load()
		d := taskDeliverDups[idx].Load()
		if w != 1 || d != int32(len(coords)-1) {
			t.Fatalf("task %s ClaimDelivery winners=%d dupErrors=%d, want 1 and %d", tid, w, d, len(coords)-1)
		}
	}

	// 5. Final invariant check: all 24 tasks are SUPERVISOR_ACCEPTED and 24 rows exist in task_deliveries.
	for _, tid := range taskIDs {
		tk, gErr := coordA.GetTask(ctx, tid)
		if gErr != nil || tk == nil || tk.State != StateSupervisorAccepted {
			t.Fatalf("final task %s = (%+v, %v), want state %s", tid, tk, gErr, StateSupervisorAccepted)
		}
	}
	var deliveryRows int
	if err := coordB.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_deliveries`).Scan(&deliveryRows); err != nil {
		t.Fatalf("count task_deliveries: %v", err)
	}
	if deliveryRows != totalTasks {
		t.Fatalf("task_deliveries row count = %d, want %d", deliveryRows, totalTasks)
	}
}
