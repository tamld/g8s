package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func submitAffinityTask(t *testing.T, env *workerEnv, key, provider, model string, priority int) *controlplane.Task {
	t.Helper()
	payloadMap := map[string]any{
		"prompt": fmt.Sprintf("task %s", key),
	}
	if provider != "" {
		payloadMap["provider"] = provider
	}
	if model != "" {
		payloadMap["model"] = model
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	task, err := env.store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: key,
		Model:          model,
		Provider:       provider,
		AddDirs:        []string{"/tmp/test"},
		Priority:       priority,
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("submit affinity task %s: %v", key, err)
	}
	return task
}

func TestWorkerClaimAffinity_RunOnce_FilterPermutations(t *testing.T) {
	env := newWorkerEnv(t, nil, WithPollInterval(5*time.Millisecond))
	ctx := context.Background()

	// Enqueue 4 tasks across 2 providers and 2 models
	t1 := submitAffinityTask(t, env, "t-agy-flash", "agy", "gemini-3.8-flash-high", 10)
	t2 := submitAffinityTask(t, env, "t-agy-sonnet", "agy", "claude-3-7-sonnet", 20)
	t3 := submitAffinityTask(t, env, "t-codex-flash", "codex", "gemini-3.8-flash-high", 30)
	t4 := submitAffinityTask(t, env, "t-codex-sonnet", "codex", "claude-3-7-sonnet", 40)

	// 1. Both set: Provider="agy", Model="gemini-3.8-flash-high" MUST claim t1
	claimed1, err := env.sup.RunOnce(ctx, RunOptions{
		WorkerID:     "w-both",
		LeaseSeconds: 60,
		Provider:     "agy",
		Model:        "gemini-3.8-flash-high",
	})
	if err != nil {
		t.Fatalf("RunOnce both set: %v", err)
	}
	if claimed1 == nil || claimed1.TaskID != t1.TaskID {
		t.Fatalf("expected claimed t1 (%s), got %+v", t1.TaskID, claimed1)
	}

	// 2. Model only: Model="claude-3-7-sonnet" -> should claim t4 (priority 40 > t2 priority 20)
	claimed2, err := env.sup.RunOnce(ctx, RunOptions{
		WorkerID:     "w-model",
		LeaseSeconds: 60,
		Model:        "claude-3-7-sonnet",
	})
	if err != nil {
		t.Fatalf("RunOnce model only: %v", err)
	}
	if claimed2 == nil || claimed2.TaskID != t4.TaskID {
		t.Fatalf("expected claimed t4 (%s), got %+v", t4.TaskID, claimed2)
	}

	// 3. Provider only: Provider="codex" -> should claim t3 (remaining codex task)
	claimed3, err := env.sup.RunOnce(ctx, RunOptions{
		WorkerID:     "w-prov",
		LeaseSeconds: 60,
		Provider:     "codex",
	})
	if err != nil {
		t.Fatalf("RunOnce provider only: %v", err)
	}
	if claimed3 == nil || claimed3.TaskID != t3.TaskID {
		t.Fatalf("expected claimed t3 (%s), got %+v", t3.TaskID, claimed3)
	}

	// 4. Empty filter: should claim remaining t2
	claimed4, err := env.sup.RunOnce(ctx, RunOptions{
		WorkerID:     "w-empty",
		LeaseSeconds: 60,
	})
	if err != nil {
		t.Fatalf("RunOnce empty: %v", err)
	}
	if claimed4 == nil || claimed4.TaskID != t2.TaskID {
		t.Fatalf("expected claimed t2 (%s), got %+v", t2.TaskID, claimed4)
	}
}

func TestWorkerClaimAffinity_MismatchRemainsQueuedUntouched(t *testing.T) {
	env := newWorkerEnv(t, nil, WithPollInterval(5*time.Millisecond))
	ctx := context.Background()

	task := submitAffinityTask(t, env, "t-specific", "codex", "claude-3-7-sonnet", 50)

	// Worker runs with mismatched model
	claimed, err := env.sup.RunOnce(ctx, RunOptions{
		WorkerID:     "w-mismatch",
		LeaseSeconds: 60,
		Provider:     "codex",
		Model:        "gemini-3.8-flash-high", // mismatch
	})
	if err != nil {
		t.Fatalf("RunOnce mismatch: %v", err)
	}
	if claimed != nil {
		t.Fatalf("expected nil for mismatched claim, got %+v", claimed)
	}

	// Assert fake runner did not spawn any child
	env.runner.mu.Lock()
	spawnCount := len(env.runner.spawned)
	env.runner.mu.Unlock()
	if spawnCount != 0 {
		t.Fatalf("expected 0 spawned processes, got %d", spawnCount)
	}

	// Assert the task remains QUEUED with attempts=0 and no lease
	fresh, err := env.store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if fresh.State != controlplane.StateQueued {
		t.Fatalf("task state = %s, want QUEUED", fresh.State)
	}
	if fresh.Attempts != 0 {
		t.Fatalf("task attempts = %d, want 0", fresh.Attempts)
	}
	if fresh.LeaseOwner != nil {
		t.Fatalf("task lease_owner = %v, want nil", *fresh.LeaseOwner)
	}
	if fresh.LeaseToken != nil {
		t.Fatalf("task lease_token = %v, want nil", *fresh.LeaseToken)
	}
}

func TestWorkerClaimAffinity_SerialDrain(t *testing.T) {
	env := newWorkerEnv(t, nil, WithPollInterval(5*time.Millisecond))
	ctx := context.Background()

	// 3 matching tasks and 2 non-matching tasks
	var matchIDs []string
	for i := 0; i < 3; i++ {
		tMatch := submitAffinityTask(t, env, fmt.Sprintf("serial-match-%d", i), "agy", "flash-model", i)
		matchIDs = append(matchIDs, tMatch.TaskID)
	}
	var mismatchIDs []string
	for i := 0; i < 2; i++ {
		tMismatch := submitAffinityTask(t, env, fmt.Sprintf("serial-mismatch-%d", i), "agy", "other-model", 20+i)
		mismatchIDs = append(mismatchIDs, tMismatch.TaskID)
	}

	var claimedInLoop []string
	var mu sync.Mutex
	code := env.sup.RunLoop(ctx, LoopOptions{
		WorkerID:     "w-serial",
		LeaseSeconds: 60,
		Provider:     "agy",
		Model:        "flash-model",
		OnTask: func(t *controlplane.Task) {
			mu.Lock()
			claimedInLoop = append(claimedInLoop, t.TaskID)
			mu.Unlock()
		},
	})
	if code != 0 {
		t.Fatalf("expected serial drain exit 0, got %d", code)
	}

	if len(claimedInLoop) != 3 {
		t.Fatalf("expected 3 tasks claimed in serial loop, got %d", len(claimedInLoop))
	}
	sort.Strings(matchIDs)
	sort.Strings(claimedInLoop)
	if !reflect.DeepEqual(matchIDs, claimedInLoop) {
		t.Fatalf("claimed tasks %v != expected %v", claimedInLoop, matchIDs)
	}

	// Verify mismatch tasks remain in queue untouched
	for _, id := range mismatchIDs {
		tState, err := env.store.GetTask(ctx, id)
		if err != nil {
			t.Fatalf("get task %s: %v", id, err)
		}
		if tState.State != controlplane.StateQueued || tState.Attempts != 0 || tState.LeaseOwner != nil {
			t.Fatalf("mismatch task %s mutated: state=%s, attempts=%d, leaseOwner=%v", id, tState.State, tState.Attempts, tState.LeaseOwner)
		}
	}
}

func TestWorkerClaimAffinity_ConcurrentDrain(t *testing.T) {
	env := newWorkerEnv(t, nil, WithPollInterval(5*time.Millisecond))
	ctx := context.Background()

	// 6 matching tasks and 4 non-matching tasks
	var matchIDs []string
	for i := 0; i < 6; i++ {
		tMatch := submitAffinityTask(t, env, fmt.Sprintf("conc-match-%d", i), "codex", "model-x", i)
		matchIDs = append(matchIDs, tMatch.TaskID)
	}
	var mismatchIDs []string
	for i := 0; i < 4; i++ {
		tMismatch := submitAffinityTask(t, env, fmt.Sprintf("conc-mismatch-%d", i), "codex", "model-y", 50+i)
		mismatchIDs = append(mismatchIDs, tMismatch.TaskID)
	}

	var claimedInLoop []string
	var mu sync.Mutex
	code := env.sup.RunLoop(ctx, LoopOptions{
		WorkerID:     "w-conc",
		LeaseSeconds: 60,
		Provider:     "codex",
		Model:        "model-x",
		Concurrency:  3,
		OnTask: func(t *controlplane.Task) {
			mu.Lock()
			claimedInLoop = append(claimedInLoop, t.TaskID)
			mu.Unlock()
		},
	})
	if code != 0 {
		t.Fatalf("expected concurrent drain exit 0, got %d", code)
	}

	if len(claimedInLoop) != 6 {
		t.Fatalf("expected 6 tasks claimed in concurrent loop, got %d", len(claimedInLoop))
	}
	sort.Strings(matchIDs)
	sort.Strings(claimedInLoop)
	if !reflect.DeepEqual(matchIDs, claimedInLoop) {
		t.Fatalf("claimed tasks %v != expected %v", claimedInLoop, matchIDs)
	}

	// Verify mismatch tasks remain in queue untouched
	for _, id := range mismatchIDs {
		tState, err := env.store.GetTask(ctx, id)
		if err != nil {
			t.Fatalf("get task %s: %v", id, err)
		}
		if tState.State != controlplane.StateQueued || tState.Attempts != 0 || tState.LeaseOwner != nil {
			t.Fatalf("mismatch task %s mutated: state=%s, attempts=%d, leaseOwner=%v", id, tState.State, tState.Attempts, tState.LeaseOwner)
		}
	}
}

func TestWorkerClaimAffinity_SerialConcurrentEquivalence(t *testing.T) {
	// For each filter permutation, run serial and concurrent drain on identical queues
	permutations := []struct {
		name     string
		provider string
		model    string
	}{
		{name: "empty-filter", provider: "", model: ""},
		{name: "provider-only", provider: "p1", model: ""},
		{name: "model-only", provider: "", model: "m1"},
		{name: "both-set", provider: "p1", model: "m1"},
	}

	for _, tc := range permutations {
		t.Run(tc.name, func(t *testing.T) {
			populateQueue := func(env *workerEnv) {
				submitAffinityTask(t, env, "k-p1-m1", "p1", "m1", 10)
				submitAffinityTask(t, env, "k-p1-m2", "p1", "m2", 20)
				submitAffinityTask(t, env, "k-p2-m1", "p2", "m1", 30)
				submitAffinityTask(t, env, "k-p2-m2", "p2", "m2", 40)
			}

			// Run serial drain
			envSerial := newWorkerEnv(t, nil, WithPollInterval(5*time.Millisecond))
			populateQueue(envSerial)
			var serialClaimed []string
			var serialMu sync.Mutex
			codeSerial := envSerial.sup.RunLoop(context.Background(), LoopOptions{
				WorkerID:     "w-serial",
				LeaseSeconds: 60,
				Provider:     tc.provider,
				Model:        tc.model,
				Concurrency:  1,
				OnTask: func(t *controlplane.Task) {
					serialMu.Lock()
					serialClaimed = append(serialClaimed, t.IdempotencyKey)
					serialMu.Unlock()
				},
			})
			if codeSerial != 0 {
				t.Fatalf("serial drain exit = %d", codeSerial)
			}

			// Run concurrent drain
			envConcurrent := newWorkerEnv(t, nil, WithPollInterval(5*time.Millisecond))
			populateQueue(envConcurrent)
			var concurrentClaimed []string
			var concurrentMu sync.Mutex
			codeConcurrent := envConcurrent.sup.RunLoop(context.Background(), LoopOptions{
				WorkerID:     "w-conc",
				LeaseSeconds: 60,
				Provider:     tc.provider,
				Model:        tc.model,
				Concurrency:  3,
				OnTask: func(t *controlplane.Task) {
					concurrentMu.Lock()
					concurrentClaimed = append(concurrentClaimed, t.IdempotencyKey)
					concurrentMu.Unlock()
				},
			})
			if codeConcurrent != 0 {
				t.Fatalf("concurrent drain exit = %d", codeConcurrent)
			}

			sort.Strings(serialClaimed)
			sort.Strings(concurrentClaimed)
			if !reflect.DeepEqual(serialClaimed, concurrentClaimed) {
				t.Fatalf("serial claimed %v != concurrent claimed %v for filter %s", serialClaimed, concurrentClaimed, tc.name)
			}
		})
	}
}
