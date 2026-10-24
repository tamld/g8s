package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestClaimAffinityFiltering(t *testing.T) {
	ctx := context.Background()

	t.Run("filter combinations: empty, provider-only, model-only, both AND", func(t *testing.T) {
		store, _ := newTestStore(t)

		// 4 tasks: (p1, m1), (p1, m2), (p2, m1), (p2, m2)
		t_p1_m1, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-p1-m1",
			Model:          "m1",
			Provider:       "p1",
			AddDirs:        []string{testScopeDir},
			Priority:       10,
			Payload:        json.RawMessage(`{"prompt":"p1 m1","provider":"p1","model":"m1"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		t_p1_m2, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-p1-m2",
			Model:          "m2",
			Provider:       "p1",
			AddDirs:        []string{testScopeDir},
			Priority:       20,
			Payload:        json.RawMessage(`{"prompt":"p1 m2","provider":"p1","model":"m2"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		t_p2_m1, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-p2-m1",
			Model:          "m1",
			Provider:       "p2",
			AddDirs:        []string{testScopeDir},
			Priority:       30,
			Payload:        json.RawMessage(`{"prompt":"p2 m1","provider":"p2","model":"m1"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		t_p2_m2, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-p2-m2",
			Model:          "m2",
			Provider:       "p2",
			AddDirs:        []string{testScopeDir},
			Priority:       40,
			Payload:        json.RawMessage(`{"prompt":"p2 m2","provider":"p2","model":"m2"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		// 1. Both set: Claim with Provider="p1", Model="m1" -> MUST match only t_p1_m1 (even though priority 10 is lowest)
		claimed, err := store.ClaimTaskFiltered(ctx, "w1", 60, ClaimFilter{Provider: "p1", Model: "m1"})
		if err != nil {
			t.Fatalf("ClaimTaskFiltered: %v", err)
		}
		if claimed == nil || claimed.TaskID != t_p1_m1.TaskID {
			t.Fatalf("expected to claim t_p1_m1, got %+v", claimed)
		}

		// 2. Model only: Claim with Model="m1" -> should claim remaining m1 task (t_p2_m1, priority 30)
		claimedM1, err := store.ClaimTaskFiltered(ctx, "w2", 60, ClaimFilter{Model: "m1"})
		if err != nil {
			t.Fatalf("ClaimTaskFiltered model only: %v", err)
		}
		if claimedM1 == nil || claimedM1.TaskID != t_p2_m1.TaskID {
			t.Fatalf("expected to claim t_p2_m1, got %+v", claimedM1)
		}

		// 3. Provider only: Claim with Provider="p1" -> should claim remaining p1 task (t_p1_m2, priority 20)
		claimedP1, err := store.ClaimTaskFiltered(ctx, "w3", 60, ClaimFilter{Provider: "p1"})
		if err != nil {
			t.Fatalf("ClaimTaskFiltered provider only: %v", err)
		}
		if claimedP1 == nil || claimedP1.TaskID != t_p1_m2.TaskID {
			t.Fatalf("expected to claim t_p1_m2, got %+v", claimedP1)
		}

		// 4. Empty filter: Claim with ClaimFilter{} -> claims highest priority remaining task (t_p2_m2, priority 40)
		claimedEmpty, err := store.ClaimTaskFiltered(ctx, "w4", 60, ClaimFilter{})
		if err != nil {
			t.Fatalf("ClaimTaskFiltered empty: %v", err)
		}
		if claimedEmpty == nil || claimedEmpty.TaskID != t_p2_m2.TaskID {
			t.Fatalf("expected to claim t_p2_m2, got %+v", claimedEmpty)
		}
	})

	t.Run("mismatching and absent model remain QUEUED with attempts and leases untouched", func(t *testing.T) {
		store, dbPath := newTestStore(t)

		// Submit a task with model="m-known"
		taskKnown, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-known",
			Model:          "m-known",
			AddDirs:        []string{testScopeDir},
			Priority:       10,
			Payload:        json.RawMessage(`{"prompt":"known model task","model":"m-known"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		// Submit a task with absent model in payload
		taskAbsent, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-absent",
			Model:          "placeholder-will-not-be-in-payload",
			AddDirs:        []string{testScopeDir},
			Priority:       20,
			Payload:        json.RawMessage(`{"prompt":"absent model task"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		// Claim with model="nonexistent-model"
		claimed, err := store.ClaimTaskFiltered(ctx, "w-test", 60, ClaimFilter{Model: "nonexistent-model"})
		if err != nil {
			t.Fatalf("ClaimTaskFiltered mismatching model: %v", err)
		}
		if claimed != nil {
			t.Fatalf("expected nil for nonexistent model claim, got %s", claimed.TaskID)
		}

		// Verify raw SQLite state: both tasks MUST remain QUEUED, attempts=0, lease fields NULL
		db := openRawDB(t, dbPath)
		for _, taskID := range []string{taskKnown.TaskID, taskAbsent.TaskID} {
			var state string
			var attempts int
			var leaseOwner, leaseToken sql.NullString
			var leaseExpiresAt sql.NullFloat64
			err := db.QueryRowContext(ctx,
				"SELECT state, attempts, lease_owner, lease_token, lease_expires_at FROM tasks WHERE task_id = ?",
				taskID).Scan(&state, &attempts, &leaseOwner, &leaseToken, &leaseExpiresAt)
			if err != nil {
				t.Fatalf("query task %s: %v", taskID, err)
			}
			if state != "QUEUED" {
				t.Errorf("task %s state = %s, want QUEUED", taskID, state)
			}
			if attempts != 0 {
				t.Errorf("task %s attempts = %d, want 0", taskID, attempts)
			}
			if leaseOwner.Valid {
				t.Errorf("task %s lease_owner = %v, want NULL", taskID, leaseOwner.String)
			}
			if leaseToken.Valid {
				t.Errorf("task %s lease_token = %v, want NULL", taskID, leaseToken.String)
			}
			if leaseExpiresAt.Valid {
				t.Errorf("task %s lease_expires_at = %v, want NULL", taskID, leaseExpiresAt.Float64)
			}
		}

		// Claim with Model="m-known": taskAbsent MUST NOT be matched even though it has higher priority (20 > 10)
		claimedKnown, err := store.ClaimTaskFiltered(ctx, "w-test", 60, ClaimFilter{Model: "m-known"})
		if err != nil {
			t.Fatalf("ClaimTaskFiltered m-known: %v", err)
		}
		if claimedKnown == nil || claimedKnown.TaskID != taskKnown.TaskID {
			t.Fatalf("expected to claim taskKnown (%s), got %+v", taskKnown.TaskID, claimedKnown)
		}

		// taskAbsent remains untouched in QUEUED
		var absentState string
		var absentAttempts int
		err = db.QueryRowContext(ctx, "SELECT state, attempts FROM tasks WHERE task_id = ?", taskAbsent.TaskID).Scan(&absentState, &absentAttempts)
		if err != nil {
			t.Fatalf("query absent task: %v", err)
		}
		if absentState != "QUEUED" || absentAttempts != 0 {
			t.Fatalf("taskAbsent state=%s attempts=%d, want QUEUED / 0", absentState, absentAttempts)
		}
	})

	t.Run("legacy wrappers ClaimTask and ClaimTaskProvider delegates correctly", func(t *testing.T) {
		store, _ := newTestStore(t)

		tLegacy, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-leg",
			Model:          "m-legacy",
			AddDirs:        []string{testScopeDir},
			Priority:       10,
			Payload:        json.RawMessage(`{"prompt":"legacy","provider":"prov-leg","model":"m-legacy"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		// ClaimTaskProvider with matching provider
		claimedProv, err := store.ClaimTaskProvider(ctx, "w-prov", 60, "prov-leg")
		if err != nil {
			t.Fatalf("ClaimTaskProvider: %v", err)
		}
		if claimedProv == nil || claimedProv.TaskID != tLegacy.TaskID {
			t.Fatalf("expected claimedProv=%s, got %+v", tLegacy.TaskID, claimedProv)
		}

		// Resubmit another and claim via ClaimTask
		tUnrestricted, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-unres",
			Model:          "m-unres",
			AddDirs:        []string{testScopeDir},
			Priority:       10,
			Payload:        json.RawMessage(`{"prompt":"unrestricted"}`),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}

		claimedUnres, err := store.ClaimTask(ctx, "w-unres", 60)
		if err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		if claimedUnres == nil || claimedUnres.TaskID != tUnrestricted.TaskID {
			t.Fatalf("expected claimedUnres=%s, got %+v", tUnrestricted.TaskID, claimedUnres)
		}
	})
}

func TestClaimAffinityConcurrency(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	// Submit 10 matching tasks and 10 non-matching tasks
	numMatching := 10
	numMismatched := 10

	for i := 0; i < numMatching; i++ {
		_, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: fmt.Sprintf("k-match-%d", i),
			Model:          "target-model",
			Provider:       "target-provider",
			AddDirs:        []string{testScopeDir},
			Priority:       i,
			Payload:        json.RawMessage(`{"prompt":"matching task","provider":"target-provider","model":"target-model"}`),
		})
		if err != nil {
			t.Fatalf("submit match %d: %v", i, err)
		}
	}

	for i := 0; i < numMismatched; i++ {
		_, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: fmt.Sprintf("k-mismatch-%d", i),
			Model:          "other-model",
			Provider:       "target-provider",
			AddDirs:        []string{testScopeDir},
			Priority:       50 + i, // higher priority!
			Payload:        json.RawMessage(`{"prompt":"mismatched task","provider":"target-provider","model":"other-model"}`),
		})
		if err != nil {
			t.Fatalf("submit mismatch %d: %v", i, err)
		}
	}

	// 20 concurrent workers attempting to claim matching tasks
	numWorkers := 20
	var wg sync.WaitGroup
	var claimMu sync.Mutex
	claimedIDs := make(map[string]int)

	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		workerID := fmt.Sprintf("worker-%d", i)
		go func() {
			defer wg.Done()
			task, err := store.ClaimTaskFiltered(ctx, workerID, 60, ClaimFilter{
				Provider: "target-provider",
				Model:    "target-model",
			})
			if err != nil {
				t.Errorf("worker claim error: %v", err)
				return
			}
			if task != nil {
				claimMu.Lock()
				claimedIDs[task.TaskID]++
				claimMu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Exactly numMatching tasks should be claimed
	if len(claimedIDs) != numMatching {
		t.Fatalf("claimed %d distinct tasks, want %d", len(claimedIDs), numMatching)
	}

	// Single-winning claim: each task claimed exactly once
	for taskID, count := range claimedIDs {
		if count != 1 {
			t.Errorf("task %s was claimed %d times (expected exactly 1 winning claim)", taskID, count)
		}
	}
}
