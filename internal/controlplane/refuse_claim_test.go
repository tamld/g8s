package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestRefuseClaimReleasesLeaseWithoutBurningAttempt (#987): a refused claim
// returns the task to QUEUED with the attempt budget restored and re-claim
// visibility deferred via request_json $.not_before.
func TestRefuseClaimReleasesLeaseWithoutBurningAttempt(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	task := submitRefusalFixture(t, store, "refuse-release", 1, map[string]any{
		"prompt":     "inventory the module",
		"provider":   "cursor",
		"permission": "read_only",
	})

	claimed, err := store.ClaimTask(ctx, "w-cwd-slash", 60)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask: err=%v claimed=%v", err, claimed)
	}
	if claimed.TaskID != task.TaskID {
		t.Fatalf("claimed %s, want %s", claimed.TaskID, task.TaskID)
	}
	// Claim bumps the attempt budget.
	mid, _ := store.GetTask(ctx, task.TaskID)
	if mid.Attempts != 1 {
		t.Fatalf("claim must bump attempts to 1, got %d", mid.Attempts)
	}

	before := float64(time.Now().UnixNano()) / 1e9
	if err := store.RefuseClaim(ctx, task.TaskID, "w-cwd-slash", *claimed.LeaseToken, "E_WORKER_CWD_NOT_GIT_REPO", "worker cwd / is not a git repository", 3600); err != nil {
		t.Fatalf("RefuseClaim: %v", err)
	}

	got, err := store.GetTask(ctx, task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != StateQueued {
		t.Fatalf("refused task must be QUEUED, got %q", got.State)
	}
	if got.LeaseToken != nil {
		t.Fatalf("lease_token must be cleared, got %v", *got.LeaseToken)
	}
	if got.LeaseOwner != nil {
		t.Fatalf("lease_owner must be cleared, got %v", *got.LeaseOwner)
	}
	if got.LeaseExpiresAt != nil {
		t.Fatalf("lease expiry must be cleared, got %v", *got.LeaseExpiresAt)
	}
	if got.Attempts != 0 {
		t.Fatalf("refusal must restore the attempt budget, got attempts=%d", got.Attempts)
	}
	if got.LastError == nil || *got.LastError != "worker cwd / is not a git repository" {
		t.Fatalf("last_error must carry the refusal reason, got %v", got.LastError)
	}

	var req map[string]any
	if err := json.Unmarshal(got.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	nb, ok := req["not_before"].(float64)
	if !ok {
		t.Fatalf("expected numeric not_before after refusal, got %v", req["not_before"])
	}
	if nb < before+3600-1 || nb > before+3600+60 {
		t.Fatalf("not_before %v must defer ~3600s from %v", nb, before)
	}
	if req["prompt"] != "inventory the module" {
		t.Fatalf("json_set must preserve sibling request fields, got %v", req["prompt"])
	}

	// Deferred: an immediate claim must not return the task.
	if again, err := store.ClaimTask(ctx, "w-poller", 60); err != nil || again != nil {
		t.Fatalf("deferred task must not be claimable yet, err=%v task=%v", err, again)
	}

	// The refusal is auditable.
	events, err := store.Events(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType != "task_claim_refused" {
			continue
		}
		found = true
		var details map[string]any
		if err := json.Unmarshal(e.Details, &details); err != nil {
			t.Fatalf("decode refusal event: %v", err)
		}
		if details["code"] != "E_WORKER_CWD_NOT_GIT_REPO" || details["defer_seconds"] != float64(3600) {
			t.Fatalf("refusal event must carry typed code and defer, got %v", details)
		}
		if e.Actor != "w-cwd-slash" {
			t.Fatalf("refusal event actor must be the refusing worker, got %q", e.Actor)
		}
	}
	if !found {
		t.Fatal("expected task_claim_refused event")
	}
}

// TestRefuseClaimWithoutDeferralIsImmediatelyClaimable: deferSeconds=0 sets
// no future not_before, so the refused task is claimable right away.
func TestRefuseClaimWithoutDeferralIsImmediatelyClaimable(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	submitRefusalFixture(t, store, "refuse-nodefer", 1, map[string]any{"prompt": "p"})
	claimed, err := store.ClaimTask(ctx, "w-1", 60)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask: err=%v claimed=%v", err, claimed)
	}
	if err := store.RefuseClaim(ctx, claimed.TaskID, "w-1", *claimed.LeaseToken, "E_WORKER_CWD_NOT_GIT_REPO", "no git", 0); err != nil {
		t.Fatalf("RefuseClaim: %v", err)
	}
	reclaimed, err := store.ClaimTask(ctx, "w-capable", 60)
	if err != nil || reclaimed == nil {
		t.Fatalf("refused task must be immediately claimable with defer=0, err=%v task=%v", err, reclaimed)
	}
	if reclaimed.TaskID != claimed.TaskID {
		t.Fatalf("re-claimed %s, want %s", reclaimed.TaskID, claimed.TaskID)
	}
	if reclaimed.Attempts != 1 {
		t.Fatalf("re-claim must burn exactly one attempt, got %d", reclaimed.Attempts)
	}
}

// TestRefuseClaimCASMismatch: a stale lease token must not release someone
// else's claim — the store surfaces ErrClaimNotHeld and the task stays leased.
func TestRefuseClaimCASMismatch(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	submitRefusalFixture(t, store, "refuse-cas", 1, map[string]any{"prompt": "p"})
	claimed, err := store.ClaimTask(ctx, "w-owner", 60)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask: err=%v claimed=%v", err, claimed)
	}
	err = store.RefuseClaim(ctx, claimed.TaskID, "w-stranger", "stale-token", "E_WORKER_CWD_NOT_GIT_REPO", "no git", 10)
	if !errors.Is(err, ErrClaimNotHeld) {
		t.Fatalf("expected ErrClaimNotHeld, got %v", err)
	}
	got, _ := store.GetTask(ctx, claimed.TaskID)
	if got.State != StateLeased {
		t.Fatalf("foreign refusal must not mutate the lease, got state %q", got.State)
	}
	if got.Attempts != 1 {
		t.Fatalf("foreign refusal must not touch attempts, got %d", got.Attempts)
	}
}

// submitRefusalFixture persists one task with the given payload for the
// refusal tests, mirroring the CLI submit payload shape.
func submitRefusalFixture(t *testing.T, store *Store, idem string, maxAttempts int, payload map[string]any) *Task {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task, err := store.SubmitTask(context.Background(), SubmitTaskRequest{
		IdempotencyKey: idem,
		Payload:        raw,
		Model:          "gemini-3.8-flash-high",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "30s",
		AddDirs:        []string{t.TempDir()},
		MaxAttempts:    maxAttempts,
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	return task
}
