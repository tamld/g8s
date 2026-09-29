package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestClaimTaskProviderFiltering(t *testing.T) {
	ctx := context.Background()

	t.Run("verify request_json path for provider", func(t *testing.T) {
		store, path := newTestStore(t)
		payload := json.RawMessage(`{"prompt":"test","provider":"codex","model":"gemini-3.8-flash-high"}`)
		task, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "k-path-check",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        payload,
		})
		if err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}

		db := openRawDB(t, path)
		var extracted sql.NullString
		err = db.QueryRowContext(ctx, "SELECT json_extract(request_json, '$.provider') FROM tasks WHERE task_id = ?", task.TaskID).Scan(&extracted)
		if err != nil {
			t.Fatalf("QueryRow json_extract: %v", err)
		}
		if !extracted.Valid || extracted.String != "codex" {
			t.Fatalf("json_extract(request_json, '$.provider') = %v (valid=%v), want 'codex'", extracted.String, extracted.Valid)
		}
	})

	t.Run("filter by provider claim affinity", func(t *testing.T) {
		store, _ := newTestStore(t)

		// Submit two tasks: one with provider="codex", one without provider
		taskCodex, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "task-codex",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Priority:       10,
			Payload:        json.RawMessage(`{"prompt":"codex work","provider":"codex","model":"gemini-3.8-flash-high"}`),
		})
		if err != nil {
			t.Fatalf("submit codex task: %v", err)
		}

		taskLegacy, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "task-legacy",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Priority:       20, // higher priority!
			Payload:        json.RawMessage(`{"prompt":"legacy work","model":"gemini-3.8-flash-high"}`),
		})
		if err != nil {
			t.Fatalf("submit legacy task: %v", err)
		}

		// ClaimTaskProvider(..., "codex") returns only the codex task, even though taskLegacy has higher priority
		claimed, err := store.ClaimTaskProvider(ctx, "worker-codex", 60, "codex")
		if err != nil {
			t.Fatalf("ClaimTaskProvider: %v", err)
		}
		if claimed == nil {
			t.Fatalf("expected to claim taskCodex, got nil")
		}
		if claimed.TaskID != taskCodex.TaskID {
			t.Fatalf("ClaimTaskProvider claimed %s, want %s (codex)", claimed.TaskID, taskCodex.TaskID)
		}

		// Another codex claim attempt returns no task (queue has no more codex tasks)
		claimed2, err := store.ClaimTaskProvider(ctx, "worker-codex", 60, "codex")
		if err != nil {
			t.Fatalf("ClaimTaskProvider second call: %v", err)
		}
		if claimed2 != nil {
			t.Fatalf("expected nil for second codex claim, got %s", claimed2.TaskID)
		}

		// Unfiltered ClaimTask claims taskLegacy
		claimedLegacy, err := store.ClaimTask(ctx, "worker-legacy", 60)
		if err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		if claimedLegacy == nil {
			t.Fatalf("expected to claim taskLegacy, got nil")
		}
		if claimedLegacy.TaskID != taskLegacy.TaskID {
			t.Fatalf("ClaimTask claimed %s, want %s", claimedLegacy.TaskID, taskLegacy.TaskID)
		}
	})

	t.Run("unfiltered ClaimTask returns both across two calls", func(t *testing.T) {
		store, _ := newTestStore(t)

		t1, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "t1",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Priority:       20,
			Payload:        json.RawMessage(`{"prompt":"p1","provider":"codex","model":"gemini-3.8-flash-high"}`),
		})
		if err != nil {
			t.Fatalf("SubmitTask t1: %v", err)
		}
		t2, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "t2",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Priority:       10,
			Payload:        json.RawMessage(`{"prompt":"p2","model":"gemini-3.8-flash-high"}`),
		})
		if err != nil {
			t.Fatalf("SubmitTask t2: %v", err)
		}

		c1, err := store.ClaimTask(ctx, "w1", 60)
		if err != nil || c1 == nil {
			t.Fatalf("ClaimTask 1: c1=%v err=%v", c1, err)
		}
		c2, err := store.ClaimTask(ctx, "w1", 60)
		if err != nil || c2 == nil {
			t.Fatalf("ClaimTask 2: c2=%v err=%v", c2, err)
		}

		claimedIDs := map[string]bool{c1.TaskID: true, c2.TaskID: true}
		if !claimedIDs[t1.TaskID] || !claimedIDs[t2.TaskID] {
			t.Fatalf("ClaimTask across two calls did not return both tasks: got %s, %s", c1.TaskID, c2.TaskID)
		}
	})

	t.Run("worker-filtered claim of an empty queue returns no task", func(t *testing.T) {
		store, _ := newTestStore(t)
		claimed, err := store.ClaimTaskProvider(ctx, "worker-1", 60, "codex")
		if err != nil {
			t.Fatalf("ClaimTaskProvider on empty queue: %v", err)
		}
		if claimed != nil {
			t.Fatalf("expected nil task, got %v", claimed)
		}
	})

	t.Run("empty provider string delegates to ClaimTask logic", func(t *testing.T) {
		store, _ := newTestStore(t)
		t1, err := store.SubmitTask(ctx, SubmitTaskRequest{
			IdempotencyKey: "t-empty-delegation",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(`{"prompt":"p","model":"gemini-3.8-flash-high"}`),
		})
		if err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}

		claimed, err := store.ClaimTaskProvider(ctx, "w1", 60, "")
		if err != nil {
			t.Fatalf("ClaimTaskProvider with empty provider: %v", err)
		}
		if claimed == nil || claimed.TaskID != t1.TaskID {
			t.Fatalf("expected to claim %s, got %v", t1.TaskID, claimed)
		}
	})
}
