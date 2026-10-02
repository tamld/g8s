package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubmitTaskThrottle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "controlplane.db")

	baseTime := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	currentTime := baseTime
	clock := func() time.Time {
		return currentTime
	}

	store, err := NewControlPlane(dbPath, clock)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	// Ensure limit is restored after test
	t.Cleanup(func() {
		SetSubmitRateLimitPerHour(0)
	})

	makeReq := func(key, actor string) SubmitTaskRequest {
		return SubmitTaskRequest{
			IdempotencyKey: key,
			Payload:        json.RawMessage(fmt.Sprintf(`{"prompt":"test","actor":%q}`, actor)),
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{dir},
		}
	}

	// 1. limit=0 (default) is unlimited
	SetSubmitRateLimitPerHour(0)
	for i := 0; i < 5; i++ {
		req := makeReq(fmt.Sprintf("unlimited-%d", i), "agent-default")
		if _, err := store.SubmitTask(ctx, req); err != nil {
			t.Fatalf("SubmitTask with limit=0 failed on submit %d: %v", i+1, err)
		}
	}

	// 2. limit=3 -> 4th submit in the hour fails with ErrSubmitRateLimited
	SetSubmitRateLimitPerHour(3)

	for i := 1; i <= 3; i++ {
		req := makeReq(fmt.Sprintf("alpha-%d", i), "agent-alpha")
		if _, err := store.SubmitTask(ctx, req); err != nil {
			t.Fatalf("SubmitTask alpha submit %d failed: %v", i, err)
		}
	}

	// 4th submit for agent-alpha must fail with ErrSubmitRateLimited
	req4 := makeReq("alpha-4", "agent-alpha")
	_, err = store.SubmitTask(ctx, req4)
	if err == nil {
		t.Fatalf("expected 4th submit for agent-alpha to fail, got nil error")
	}

	var rateLimited *ErrSubmitRateLimited
	if !errors.As(err, &rateLimited) {
		t.Fatalf("expected ErrSubmitRateLimited, got %T: %v", err, err)
	}
	if rateLimited.Actor != "agent-alpha" {
		t.Errorf("rateLimited.Actor = %q, want %q", rateLimited.Actor, "agent-alpha")
	}
	if rateLimited.Limit != 3 {
		t.Errorf("rateLimited.Limit = %d, want 3", rateLimited.Limit)
	}
	if rateLimited.Window != time.Hour {
		t.Errorf("rateLimited.Window = %v, want 1h", rateLimited.Window)
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "agent-alpha") {
		t.Errorf("error message should name actor, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "3") {
		t.Errorf("error message should name limit, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "1h") && !strings.Contains(errMsg, "hour") {
		t.Errorf("error message should name window, got: %s", errMsg)
	}

	// 3. Different actor unaffected
	reqBeta := makeReq("beta-1", "agent-beta")
	if _, err := store.SubmitTask(ctx, reqBeta); err != nil {
		t.Fatalf("different actor agent-beta should succeed, got: %v", err)
	}

	// 4. Window slides: submits at T-3700s do not count
	// Advance clock by 3700s (> 1 hour)
	currentTime = baseTime.Add(3700 * time.Second)

	reqAlphaAfterSlide := makeReq("alpha-slid-1", "agent-alpha")
	if _, err := store.SubmitTask(ctx, reqAlphaAfterSlide); err != nil {
		t.Fatalf("submit after window slide should succeed, got: %v", err)
	}
}
