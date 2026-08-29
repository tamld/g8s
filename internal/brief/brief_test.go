package brief

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(t time.Time) *mockClock {
	return &mockClock{now: t}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func setupTestStore(t *testing.T, clock func() time.Time) *controlplane.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test-briefs.db")
	store, err := controlplane.NewControlPlane(dbPath, clock)
	if err != nil {
		t.Fatalf("setup controlplane store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestBriefRoundtrip(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clk := newMockClock(now)
	store := setupTestStore(t, clk.Now)

	// 1. Issue a brief
	title := "DELTA-15 Orchestration Worker"
	payload := "# Overview\nImplement worker pool."
	dod := "- [ ] Unit tests pass\n- [ ] Coverage >= 90%"
	issuedBy := "sisyphus"
	ttl := 2 * time.Hour

	b, err := Issue(store, title, payload, dod, issuedBy, ttl)
	if err != nil {
		t.Fatalf("Issue() failed: %v", err)
	}

	if !strings.HasPrefix(b.ID, "brief-") {
		t.Errorf("expected ID prefix 'brief-', got %q", b.ID)
	}
	if b.Title != title {
		t.Errorf("Title = %q, want %q", b.Title, title)
	}
	if b.PayloadMD != payload {
		t.Errorf("PayloadMD = %q, want %q", b.PayloadMD, payload)
	}
	if b.DodMD != dod {
		t.Errorf("DodMD = %q, want %q", b.DodMD, dod)
	}
	if b.IssuedBy != issuedBy {
		t.Errorf("IssuedBy = %q, want %q", b.IssuedBy, issuedBy)
	}
	if b.Status != StatusActive {
		t.Errorf("Status = %q, want %q", b.Status, StatusActive)
	}
	if !b.IssuedAt.Equal(now) {
		t.Errorf("IssuedAt = %v, want %v", b.IssuedAt, now)
	}
	if !b.ExpiresAt.Equal(now.Add(ttl)) {
		t.Errorf("ExpiresAt = %v, want %v", b.ExpiresAt, now.Add(ttl))
	}

	// 2. Verify it is listed in ListActive
	active, err := ListActive(store)
	if err != nil {
		t.Fatalf("ListActive() failed: %v", err)
	}
	if len(active) != 1 || active[0].ID != b.ID {
		t.Fatalf("ListActive() returned %v, want [%s]", active, b.ID)
	}

	// 3. Consume the brief
	consumed, err := Consume(store, b.ID)
	if err != nil {
		t.Fatalf("Consume() failed: %v", err)
	}
	if consumed.Status != StatusConsumed {
		t.Errorf("consumed.Status = %q, want %q", consumed.Status, StatusConsumed)
	}
	if consumed.ID != b.ID {
		t.Errorf("consumed.ID = %q, want %q", consumed.ID, b.ID)
	}

	// 4. Verify ListActive excludes consumed brief
	activeAfter, err := ListActive(store)
	if err != nil {
		t.Fatalf("ListActive() after consume failed: %v", err)
	}
	if len(activeAfter) != 0 {
		t.Errorf("ListActive() after consume should be empty, got %d items", len(activeAfter))
	}

	// 5. Subsequent consume returns ErrAlreadyConsumed
	_, err = Consume(store, b.ID)
	if err == nil || !errors.Is(err, ErrAlreadyConsumed) {
		t.Errorf("second Consume() want ErrAlreadyConsumed, got %v", err)
	}
}

func TestBriefTTLExpiry(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clk := newMockClock(now)
	store := setupTestStore(t, clk.Now)

	ttl := 1 * time.Hour
	b, err := Issue(store, "Expiring Brief", "Payload", "DoD", "sisyphus", ttl)
	if err != nil {
		t.Fatalf("Issue() failed: %v", err)
	}

	// Advance clock past expiry
	clk.Advance(2 * time.Hour)

	// ListActive should exclude expired brief
	active, err := ListActive(store)
	if err != nil {
		t.Fatalf("ListActive() failed: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("ListActive() on expired brief want 0, got %d", len(active))
	}

	// Consume should return ErrExpired
	_, err = Consume(store, b.ID)
	if err == nil || !errors.Is(err, ErrExpired) {
		t.Errorf("Consume() on expired brief want ErrExpired, got %v", err)
	}
}

func TestBriefConsumeUnknownID(t *testing.T) {
	store := setupTestStore(t, nil)

	_, err := Consume(store, "non-existent-id")
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Errorf("Consume() non-existent ID want ErrNotFound, got %v", err)
	}
}

func TestBriefValidationErrors(t *testing.T) {
	store := setupTestStore(t, nil)

	// Nil store
	if _, err := Issue(nil, "t", "p", "d", "i", time.Hour); err == nil {
		t.Errorf("Issue with nil store should error")
	}
	if _, err := Consume(nil, "id"); err == nil {
		t.Errorf("Consume with nil store should error")
	}
	if _, err := ListActive(nil); err == nil {
		t.Errorf("ListActive with nil store should error")
	}

	// Empty fields in Issue
	if _, err := Issue(store, "", "p", "d", "i", time.Hour); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Issue with empty title want ErrInvalidInput, got %v", err)
	}
	if _, err := Issue(store, "t", "", "d", "i", time.Hour); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Issue with empty payload want ErrInvalidInput, got %v", err)
	}
	if _, err := Issue(store, "t", "p", "", "i", time.Hour); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Issue with empty dod want ErrInvalidInput, got %v", err)
	}
	if _, err := Issue(store, "t", "p", "d", "", time.Hour); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Issue with empty issued_by want ErrInvalidInput, got %v", err)
	}
	if _, err := Issue(store, "t", "p", "d", "i", 0); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Issue with 0 ttl want ErrInvalidInput, got %v", err)
	}
	if _, err := Issue(store, "t", "p", "d", "i", -time.Hour); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Issue with negative ttl want ErrInvalidInput, got %v", err)
	}

	// Empty ID in Consume
	if _, err := Consume(store, ""); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Consume with empty id want ErrInvalidInput, got %v", err)
	}
	if _, err := Consume(store, "   "); err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Consume with whitespace id want ErrInvalidInput, got %v", err)
	}
}

func TestListActiveOrderingAndFiltering(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clk := newMockClock(now)
	store := setupTestStore(t, clk.Now)

	// Issue b1
	b1, err := Issue(store, "Brief 1", "P1", "D1", "sisyphus", 2*time.Hour)
	if err != nil {
		t.Fatalf("Issue b1: %v", err)
	}

	clk.Advance(10 * time.Minute)

	// Issue b2
	b2, err := Issue(store, "Brief 2", "P2", "D2", "sisyphus", 2*time.Hour)
	if err != nil {
		t.Fatalf("Issue b2: %v", err)
	}

	clk.Advance(10 * time.Minute)

	// Issue b3 with short TTL
	b3, err := Issue(store, "Brief 3", "P3", "D3", "sisyphus", 15*time.Minute)
	if err != nil {
		t.Fatalf("Issue b3: %v", err)
	}

	// Consume b2
	if _, err := Consume(store, b2.ID); err != nil {
		t.Fatalf("Consume b2: %v", err)
	}

	// Advance clock past b3's expiry
	clk.Advance(20 * time.Minute)

	// ListActive should return only b1
	active, err := ListActive(store)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("ListActive len = %d, want 1", len(active))
	}
	if active[0].ID != b1.ID {
		t.Errorf("ListActive[0].ID = %s, want %s", active[0].ID, b1.ID)
	}

	// Attempting to consume b3 should now report ErrExpired
	if _, err := Consume(store, b3.ID); err == nil || !errors.Is(err, ErrExpired) {
		t.Errorf("Consume b3 want ErrExpired, got %v", err)
	}

	// Also test consuming a brief with status already set to 'expired'
	b4, err := Issue(store, "Brief 4", "P4", "D4", "sisyphus", 2*time.Hour)
	if err != nil {
		t.Fatalf("Issue b4: %v", err)
	}
	if err := store.UpdateBriefStatus(context.Background(), b4.ID, StatusExpired); err != nil {
		t.Fatalf("UpdateBriefStatus b4: %v", err)
	}
	if _, err := Consume(store, b4.ID); err == nil || !errors.Is(err, ErrExpired) {
		t.Errorf("Consume b4 with StatusExpired want ErrExpired, got %v", err)
	}
}

func TestBriefStoreErrors(t *testing.T) {
	store := setupTestStore(t, nil)
	// Close store to trigger db errors
	_ = store.Close()

	if _, err := Issue(store, "t", "p", "d", "i", time.Hour); err == nil {
		t.Errorf("Issue on closed store should error")
	}
	if _, err := Consume(store, "b1"); err == nil {
		t.Errorf("Consume on closed store should error")
	}
	if _, err := ListActive(store); err == nil {
		t.Errorf("ListActive on closed store should error")
	}
}
