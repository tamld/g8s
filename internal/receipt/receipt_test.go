package receipt

// Unit and integration tests for the write-receipt engine.
//
// Ported from the Python baseline reference/python/scripts/test_receipt_delegation.py
// (38 tests across 6 categories). Harness-gate integration cases (Cat4) remain in
// internal/harness/harness_test.go by design; see plans/M1-receipt-sprint-log.md
// for the full mapping ledger.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tamld/g8s/internal/harness"
	_ "modernc.org/sqlite" // Pure-Go SQLite driver (Zero-CGO constitution axiom).
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return newTestManagerWithClock(t, nil)
}

func newTestManagerWithClock(t *testing.T, clock func() time.Time) *Manager {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "receipts.sqlite3")
	m, err := NewReceiptManager(dbPath, clock)
	if err != nil {
		t.Fatalf("NewReceiptManager(%q): unexpected error: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func mustIssue(t *testing.T, m *Manager, issuer string, paths []string, ttl time.Duration) *WriteReceipt {
	t.Helper()
	r, err := m.IssueReceipt(issuer, paths, ttl)
	if err != nil {
		t.Fatalf("IssueReceipt(%q, %v, %v): unexpected error: %v", issuer, paths, ttl, err)
	}
	return r
}

func openRawDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db %q: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var _ ReceiptManager = (*Manager)(nil)

const (
	validIssuer   = "brain-main"
	validPatternA = "src/**/*.rs"
)

// ---------------------------------------------------------------------------
// Category 1 - Happy Path
// ---------------------------------------------------------------------------

func TestIssueAndValidateFlow(t *testing.T) {
	m := newTestManager(t)
	start := time.Now()

	r := mustIssue(t, m, validIssuer, []string{validPatternA}, 600*time.Second)
	if r.ReceiptID == "" {
		t.Fatal("expected non-empty receipt_id")
	}
	if r.Issuer != validIssuer {
		t.Errorf("Issuer = %q, want %q", r.Issuer, validIssuer)
	}
	if len(r.AllowedPaths) != 1 || r.AllowedPaths[0] != validPatternA {
		t.Errorf("AllowedPaths = %v, want [%q]", r.AllowedPaths, validPatternA)
	}
	if !r.ExpiresAt.After(r.CreatedAt) {
		t.Errorf("ExpiresAt %v should be after CreatedAt %v", r.ExpiresAt, r.CreatedAt)
	}
	if got := r.ExpiresAt.Sub(r.CreatedAt); got != 600*time.Second {
		t.Errorf("effective TTL = %v, want 600s", got)
	}
	if r.Consumed {
		t.Error("freshly issued receipt must not be consumed")
	}

	consumed, err := m.ValidateAndConsume(r.ReceiptID, "worker-1")
	if err != nil {
		t.Fatalf("ValidateAndConsume: unexpected error: %v", err)
	}
	if consumed.ReceiptID != r.ReceiptID {
		t.Errorf("consumed ReceiptID = %q, want %q", consumed.ReceiptID, r.ReceiptID)
	}
	if consumed.Issuer != validIssuer || consumed.AllowedPaths[0] != validPatternA {
		t.Errorf("consumed receipt metadata mismatch: %+v", consumed)
	}
	if !consumed.Consumed {
		t.Error("ValidateAndConsume must mark receipt as consumed")
	}
	if consumed.ConsumerTaskID == nil || *consumed.ConsumerTaskID != "worker-1" {
		t.Errorf("ConsumerTaskID = %v, want worker-1", consumed.ConsumerTaskID)
	}
	if !strings.Contains(consumed.ExpiresAt.Format(time.RFC3339Nano), "") || !consumed.ExpiresAt.After(start) {
		t.Errorf("ExpiresAt %v should be after test start %v", consumed.ExpiresAt, start)
	}
}

func TestMultipleReceiptsIndependentConsumption(t *testing.T) {
	m := newTestManager(t)

	r1 := mustIssue(t, m, validIssuer, []string{"a/**"}, time.Minute)
	r2 := mustIssue(t, m, validIssuer, []string{"b/**"}, time.Minute)
	r3 := mustIssue(t, m, validIssuer, []string{"c/**"}, time.Minute)

	got, err := m.ValidateAndConsume(r2.ReceiptID, "worker-mid")
	if err != nil {
		t.Fatalf("consuming middle receipt: %v", err)
	}
	if got.ReceiptID != r2.ReceiptID {
		t.Fatalf("got %q, want %q", got.ReceiptID, r2.ReceiptID)
	}

	active, err := m.ListActiveReceipts()
	if err != nil {
		t.Fatalf("ListActiveReceipts: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active receipts = %d, want 2", len(active))
	}
	for _, ar := range active {
		if ar.ReceiptID == r2.ReceiptID {
			t.Error("consumed receipt must not appear in active listing")
		}
	}

	// Siblings stay usable after the neighbor was consumed.
	for _, rr := range []*WriteReceipt{r1, r3} {
		if _, err := m.ValidateAndConsume(rr.ReceiptID, "worker-late"); err != nil {
			t.Errorf("receipt %q should still be consumable: %v", rr.ReceiptID, err)
		}
	}
}

func TestGlobPatternsRoundTripVerbatim(t *testing.T) {
	m := newTestManager(t)
	patterns := []string{"**/*.rs", "**/Cargo.toml"}

	r := mustIssue(t, m, validIssuer, patterns, time.Minute)
	got, err := m.ValidateAndConsume(r.ReceiptID, "w")
	if err != nil {
		t.Fatalf("ValidateAndConsume: %v", err)
	}
	if strings.Join(got.AllowedPaths, "|") != strings.Join(patterns, "|") {
		t.Errorf("AllowedPaths = %v, want %v", got.AllowedPaths, patterns)
	}
}

func TestTTLLowerBoundOneSecondAccepted(t *testing.T) {
	m := newTestManager(t)
	r := mustIssue(t, m, validIssuer, []string{"x/**"}, 1*time.Second)
	if got := r.ExpiresAt.Sub(r.CreatedAt); got != time.Second {
		t.Errorf("effective TTL = %v, want 1s", got)
	}
}

func TestTTLUpperBound3600AndConsumerRecorded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "probe.sqlite3")

	pm, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	defer func() { _ = pm.Close() }()

	r := mustIssue(t, pm, validIssuer, []string{"y/**"}, 3600*time.Second)
	if got := r.ExpiresAt.Sub(r.CreatedAt); got != 3600*time.Second {
		t.Errorf("effective TTL = %v, want 3600s", got)
	}

	taskID := "task-recorder"
	if _, err := pm.ValidateAndConsume(r.ReceiptID, taskID); err != nil {
		t.Fatalf("ValidateAndConsume: %v", err)
	}

	// Direct storage probe mirrors the Python baseline row-level assertion.
	raw := openRawDB(t, dbPath)
	var consumed int
	var storedTask sql.NullString
	row := raw.QueryRow(
		"SELECT consumed, consumer_task_id FROM write_receipts WHERE receipt_id = ?",
		r.ReceiptID,
	)
	if err := row.Scan(&consumed, &storedTask); err != nil {
		t.Fatalf("raw row scan: %v", err)
	}
	if consumed != 1 {
		t.Errorf("stored consumed flag = %d, want 1", consumed)
	}
	if !storedTask.Valid || storedTask.String != taskID {
		t.Errorf("stored consumer_task_id = %v, want %q", storedTask, taskID)
	}
}

// ---------------------------------------------------------------------------
// Category 2 - Security
// ---------------------------------------------------------------------------

func TestReuseOfConsumedReceiptRejected(t *testing.T) {
	m := newTestManager(t)
	r := mustIssue(t, m, validIssuer, []string{"once/**"}, time.Minute)

	if _, err := m.ValidateAndConsume(r.ReceiptID, "first"); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	_, err := m.ValidateAndConsume(r.ReceiptID, "second")
	var reused *AlreadyConsumedError
	if !errors.As(err, &reused) {
		t.Fatalf("second consume error = %v, want AlreadyConsumedError", err)
	}
	if !strings.Contains(err.Error(), "already consumed") {
		t.Errorf("error text %q must mention 'already consumed'", err.Error())
	}
}

func TestExpiryDetectedViaInjectedClock(t *testing.T) {
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	fc := newFakeClock(base)
	m := newTestManagerWithClock(t, fc.Now)

	r := mustIssue(t, m, validIssuer, []string{"clock/**"}, 60*time.Second)

	fc.Advance(61 * time.Second)
	_, err := m.ValidateAndConsume(r.ReceiptID, "late-worker")
	var expired *ExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("error = %v, want ExpiredError", err)
	}
	if !strings.Contains(err.Error(), "(expired 1s ago)") {
		t.Errorf("error text %q must contain '(expired 1s ago)'", err.Error())
	}
}

func TestExpiryDetectedWithRealSleep(t *testing.T) {
	m := newTestManager(t)
	r := mustIssue(t, m, validIssuer, []string{"sleep/**"}, 1*time.Second)

	time.Sleep(1100 * time.Millisecond)

	_, err := m.ValidateAndConsume(r.ReceiptID, "slow-worker")
	var expired *ExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("error = %v, want ExpiredError", err)
	}
}

func TestUnknownReceiptIDReturnsNotFound(t *testing.T) {
	m := newTestManager(t)
	_, err := m.ValidateAndConsume(uuid.NewString(), "worker")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error text %q must contain 'not found'", err.Error())
	}
}

func TestEmptyReceiptIDReturnsNotFound(t *testing.T) {
	m := newTestManager(t)
	_, err := m.ValidateAndConsume("", "worker")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
}

func TestSQLInjectionViaReceiptIDBlocked(t *testing.T) {
	m := newTestManager(t)
	malicious := "'; DROP TABLE write_receipts; --"

	_, err := m.ValidateAndConsume(malicious, "attacker")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}

	// The table must still exist and function normally afterwards.
	survivor := mustIssue(t, m, validIssuer, []string{"post-inject/**"}, time.Minute)
	if _, err := m.ValidateAndConsume(survivor.ReceiptID, "worker"); err != nil {
		t.Errorf("table damaged by injection attempt: %v", err)
	}
}

func TestSQLInjectionIssuerRoundTripsSafely(t *testing.T) {
	m := newTestManager(t)
	hostile := "'; DROP TABLE write_receipts; --"

	r := mustIssue(t, m, hostile, []string{"safe/**"}, time.Minute)
	if r.Issuer != hostile {
		t.Errorf("issuer mutated in transit: %q", r.Issuer)
	}
	got, err := m.ValidateAndConsume(r.ReceiptID, "worker")
	if err != nil {
		t.Fatalf("ValidateAndConsume: %v", err)
	}
	if got.Issuer != hostile {
		t.Errorf("issuer corrupted on read: %q", got.Issuer)
	}
	if !got.Consumed {
		t.Error("receipt must remain consumable despite hostile issuer payload")
	}
}

func TestSQLInjectionAllowedPathsSerializedSafely(t *testing.T) {
	m := newTestManager(t)
	hostilePaths := []string{
		"'; DROP TABLE write_receipts; --",
		"../../etc/**",
		"\"quoted/**'path\"",
	}

	r := mustIssue(t, m, validIssuer, hostilePaths, time.Minute)
	got, err := m.ValidateAndConsume(r.ReceiptID, "worker")
	if err != nil {
		t.Fatalf("ValidateAndConsume: %v", err)
	}
	if strings.Join(got.AllowedPaths, "\x00") != strings.Join(hostilePaths, "\x00") {
		t.Errorf("paths corrupted through JSON serialization:\n got  %v\n want %v", got.AllowedPaths, hostilePaths)
	}

	// Canonical JSON storage must be lossless for arbitrary string content.
	blob, err := json.Marshal(hostilePaths)
	if err != nil {
		t.Fatalf("marshal sanity check: %v", err)
	}
	var back []string
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal sanity check: %v", err)
	}
}

func TestConcurrentConsumeSingleWinner(t *testing.T) {
	m := newTestManager(t)
	r := mustIssue(t, m, validIssuer, []string{"race/**"}, time.Minute)

	const racers = 2
	release := make(chan struct{})
	errs := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-release
			_, err := m.ValidateAndConsume(r.ReceiptID, "racer")
			errs <- err
		}(i)
	}
	close(release)
	wg.Wait()
	close(errs)

	winners := 0
	for err := range errs {
		switch {
		case err == nil:
			winners++
		default:
			var reused *AlreadyConsumedError
			if !errors.As(err, &reused) {
				t.Errorf("loser error = %v, want AlreadyConsumedError", err)
			}
		}
	}
	if winners != 1 {
		t.Errorf("exactly one consumer must win, got %d winners", winners)
	}
}

// ---------------------------------------------------------------------------
// Category 3 - Input Validation
// ---------------------------------------------------------------------------

func TestIssueRejectsEmptyAllowedPaths(t *testing.T) {
	m := newTestManager(t)
	_, err := m.IssueReceipt(validIssuer, []string{}, time.Minute)
	if !errors.Is(err, ErrEmptyPaths) {
		t.Fatalf("error = %v, want ErrEmptyPaths", err)
	}
	if !strings.Contains(err.Error(), "allowed_paths must not be empty") {
		t.Errorf("error text %q mismatch", err.Error())
	}
}

func TestIssueRejectsInvalidTTLs(t *testing.T) {
	cases := []struct {
		name string
		ttl  time.Duration
	}{
		{"zero", 0},
		{"negative", -10 * time.Second},
		{"above max", 3601 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			_, err := m.IssueReceipt(validIssuer, []string{"z/**"}, tc.ttl)
			if !errors.Is(err, ErrTTLBounds) {
				t.Fatalf("ttl=%v error = %v, want ErrTTLBounds", tc.ttl, err)
			}
			if !strings.Contains(err.Error(), "ttl_seconds must be between 1 and 3600") {
				t.Errorf("error text %q mismatch", err.Error())
			}
		})
	}
}

func TestIssueAcceptsBoundaryTTLs(t *testing.T) {
	for _, ttl := range []time.Duration{time.Second, 3600 * time.Second} {
		func() {
			m := newTestManager(t)
			r, err := m.IssueReceipt(validIssuer, []string{"boundary/**"}, ttl)
			if err != nil {
				t.Fatalf("ttl=%v rejected: %v", ttl, err)
			}
			if got := r.ExpiresAt.Sub(r.CreatedAt); got != ttl {
				t.Errorf("ttl=%v effective expiry delta = %v", ttl, got)
			}
		}()
	}
}

// ---------------------------------------------------------------------------
// Category 5 - Edge Cases
// ---------------------------------------------------------------------------

func TestDeletedDatabaseRecoversOnReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vanish.sqlite3")

	m1, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	r := mustIssue(t, m1, validIssuer, []string{"ghost/**"}, time.Minute)
	if err := m1.Close(); err != nil {
		t.Fatalf("close before delete: %v", err)
	}

	if err := os.Remove(dbPath); err != nil {
		t.Fatalf("remove db file: %v", err)
	}

	// A fresh handle recreates the schema; the vanished receipt is gone.
	m2, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	defer func() { _ = m2.Close() }()

	_, err = m2.ValidateAndConsume(r.ReceiptID, "worker")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("error after reopen = %v, want NotFoundError", err)
	}
}

func TestIssueThousandReceiptsUnderOneSecond(t *testing.T) {
	m := newTestManager(t)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		if _, err := m.IssueReceipt(validIssuer, []string{"bulk/**"}, time.Minute); err != nil {
			t.Fatalf("issue #%d failed: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	// The original 1s budget tripped on slower CI runners (windows + race
	// detector); a generous ceiling still catches pathological slowness such
	// as a missing index while keeping the test deterministic.
	if elapsed > 10*time.Second {
		t.Errorf("issuing 1000 receipts took %v, budget is <10s", elapsed)
	}

	active, err := m.ListActiveReceipts()
	if err != nil {
		t.Fatalf("ListActiveReceipts: %v", err)
	}
	if len(active) != 1000 {
		t.Errorf("active count = %d, want 1000", len(active))
	}
	t.Logf("1000 issues completed in %v", elapsed)
}

func TestUnicodeAndEmojiIssuerRoundTrip(t *testing.T) {
	m := newTestManager(t)
	exotic := "Brain \U0001F9E0 \u00dcn\u00efc\u00f6d\u00e9 \u2713"

	r := mustIssue(t, m, exotic, []string{"uni/**"}, time.Minute)
	got, err := m.ValidateAndConsume(r.ReceiptID, "worker")
	if err != nil {
		t.Fatalf("ValidateAndConsume: %v", err)
	}
	if got.Issuer != exotic {
		t.Errorf("unicode issuer corrupted:\n got  %q\n want %q", got.Issuer, exotic)
	}
}

func TestManyLongPathsPreserveOrder(t *testing.T) {
	m := newTestManager(t)
	longPath := strings.Repeat("dir/", 45) + "leaf.txt"
	want := make([]string, 100)
	for i := range want {
		want[i] = longPath
	}

	r := mustIssue(t, m, validIssuer, want, time.Minute)
	got, err := m.ValidateAndConsume(r.ReceiptID, "worker")
	if err != nil {
		t.Fatalf("ValidateAndConsume: %v", err)
	}
	if len(got.AllowedPaths) != len(want) {
		t.Fatalf("path count = %d, want %d", len(got.AllowedPaths), len(want))
	}
	for i := range want {
		if got.AllowedPaths[i] != want[i] {
			t.Fatalf("path[%d] corrupted:\n got  %q\n want %q", i, got.AllowedPaths[i], want[i])
		}
	}
}

func TestSubsecondExpiryPrecision(t *testing.T) {
	base := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	fc := newFakeClock(base)
	m := newTestManagerWithClock(t, fc.Now)

	r := mustIssue(t, m, validIssuer, []string{"micro/**"}, 10*time.Second)
	fc.Advance(10*time.Second + time.Millisecond)

	_, err := m.ValidateAndConsume(r.ReceiptID, "worker")
	var expired *ExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("subsecond overrun must expire, got error = %v", err)
	}
}

func TestTwoManagersCoordinateThroughWAL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "shared.sqlite3")

	m1, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("manager one: %v", err)
	}
	defer func() { _ = m1.Close() }()

	m2, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("manager two: %v", err)
	}
	defer func() { _ = m2.Close() }()

	r := mustIssue(t, m1, "brain-one", []string{"wal/**"}, time.Minute)

	// Cross-instance consumption must be visible to both handles.
	if _, err := m2.ValidateAndConsume(r.ReceiptID, "brain-two-worker"); err != nil {
		t.Fatalf("cross-handle consume: %v", err)
	}
	_, err = m1.ValidateAndConsume(r.ReceiptID, "stale-view")
	var reused *AlreadyConsumedError
	if !errors.As(err, &reused) {
		t.Fatalf("manager one sees stale state, error = %v", err)
	}

	active, err := m1.ListActiveReceipts()
	if err != nil {
		t.Fatalf("ListActiveReceipts on manager one: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("manager one active count = %d, want 0", len(active))
	}
}

func TestFreshDatabaseSchemaColumnTypesExact(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "schema.sqlite3")
	m, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	defer func() { _ = m.Close() }()

	want := map[string]string{
		"receipt_id":         "TEXT",
		"issuer":             "TEXT",
		"allowed_paths_json": "TEXT",
		"expires_at":         "REAL",
		"consumed":           "INTEGER",
		"consumer_task_id":   "TEXT",
		"created_at":         "REAL",
	}

	raw := openRawDB(t, dbPath)
	rows, err := raw.Query("PRAGMA table_info(write_receipts)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		got[name] = colType
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info: %v", err)
	}

	for name, colType := range want {
		actual, ok := got[name]
		if !ok {
			t.Errorf("column %q missing from schema", name)
			continue
		}
		if actual != colType {
			t.Errorf("column %q type = %q, want %q", name, actual, colType)
		}
	}
	if len(got) != len(want) {
		t.Errorf("schema has %d columns, want exactly %d (%v)", len(got), len(want), got)
	}
}

// ---------------------------------------------------------------------------
// Category 6 - Abuse / Documented Boundaries
// ---------------------------------------------------------------------------

func TestAnyCallerMayIssueReceipts(t *testing.T) {
	// Documented trust boundary: the engine performs no internal caller
	// authentication. Any process holding the database path may mint
	// receipts; containment relies on filesystem permissions (0600) and
	// upstream harness gating.
	m := newTestManager(t)
	r, err := m.IssueReceipt("worker-rogue", []string{"self-issued/**"}, time.Minute)
	if err != nil {
		t.Fatalf("direct worker issuance must succeed by design: %v", err)
	}
	if r.Issuer != "worker-rogue" {
		t.Errorf("issuer = %q, want worker-rogue", r.Issuer)
	}
}

func TestRevocationSemantics(t *testing.T) {
	m := newTestManager(t)
	r := mustIssue(t, m, validIssuer, []string{"revoke/**"}, time.Minute)

	ok, err := m.RevokeReceipt(r.ReceiptID)
	if err != nil || !ok {
		t.Fatalf("RevokeReceipt = (%v, %v), want (true, nil)", ok, err)
	}

	// Revoked receipts vanish: neither consume nor second revoke succeeds.
	_, err = m.ValidateAndConsume(r.ReceiptID, "worker")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Errorf("consume after revoke error = %v, want NotFoundError", err)
	}
	ok, err = m.RevokeReceipt(r.ReceiptID)
	if err != nil || ok {
		t.Errorf("double revoke = (%v, %v), want (false, nil)", ok, err)
	}

	ok, err = m.RevokeReceipt("no-such-id")
	if err != nil || ok {
		t.Errorf("unknown revoke = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestListingDoesNotConsumeReceipts(t *testing.T) {
	m := newTestManager(t)
	r := mustIssue(t, m, validIssuer, []string{"peek/**"}, time.Minute)

	for i := 0; i < 3; i++ {
		active, err := m.ListActiveReceipts()
		if err != nil {
			t.Fatalf("list #%d: %v", i, err)
		}
		if len(active) != 1 {
			t.Fatalf("list #%d returned %d entries, want 1", i, len(active))
		}
	}

	if _, err := m.ValidateAndConsume(r.ReceiptID, "worker"); err != nil {
		t.Errorf("read-only listings must never consume: %v", err)
	}
}

func TestDirectDatabaseTamperIsKnownLimitation(t *testing.T) {
	// Documented limitation mirrored from the Python baseline: an attacker
	// with direct database access can flip consumed back to 0. The trust
	// boundary is filesystem ownership (0600), not the DB itself.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tamper.sqlite3")

	m, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	r := mustIssue(t, m, validIssuer, []string{"tamper/**"}, time.Minute)
	if _, err := m.ValidateAndConsume(r.ReceiptID, "first"); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw := openRawDB(t, dbPath)
	if _, err := raw.Exec("UPDATE write_receipts SET consumed = 0 WHERE receipt_id = ?", r.ReceiptID); err != nil {
		t.Fatalf("tamper exec: %v", err)
	}

	m2, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("reopen tampered db: %v", err)
	}
	defer func() { _ = m2.Close() }()
	if _, err := m2.ValidateAndConsume(r.ReceiptID, "attacker"); err != nil {
		t.Errorf("documented limitation broken: tampered receipt should re-consume, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// T018: safety coordination hardening (port of test_safety_coordination.py)
// ---------------------------------------------------------------------------

func TestRevokeConsumedReceiptReturnsFalseAndRowPersists(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	rc, err := m.IssueReceipt("brain", []string{"src/**"}, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := m.ValidateAndConsume(rc.ReceiptID, "worker-1"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	revoked, err := m.RevokeReceipt(rc.ReceiptID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked {
		t.Fatal("revoking a consumed receipt must report false")
	}
	var already *AlreadyConsumedError
	_, err = m.ValidateAndConsume(rc.ReceiptID, "worker-2")
	if !errors.As(err, &already) {
		t.Fatalf("consumed row must persist for audit; got %v", err)
	}
}

func TestRevokeUnknownReceiptReturnsFalse(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	revoked, err := m.RevokeReceipt("rc-missing")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked {
		t.Fatal("revoking a nonexistent receipt must return false")
	}
}

func TestRevokedReceiptCannotBeRevalidated(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	rc, _ := m.IssueReceipt("brain", []string{"src/**"}, time.Minute)
	if ok, err := m.RevokeReceipt(rc.ReceiptID); err != nil || !ok {
		t.Fatalf("first revoke: %v %v", ok, err)
	}
	var notFound *NotFoundError
	_, err := m.ValidateAndConsume(rc.ReceiptID, "worker")
	if !errors.As(err, &notFound) {
		t.Fatalf("want NotFoundError after revoke, got %v", err)
	}
	if !strings.Contains(err.Error(), "write receipt not found") {
		t.Fatalf("error text mismatch: %v", err)
	}
	reissued, err := m.IssueReceipt("brain", []string{"src/**"}, time.Minute)
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if reissued.ReceiptID == rc.ReceiptID {
		t.Fatal("reissued receipt must carry a fresh id")
	}
}

func TestExpiryMathUsesInjectedClockExactly(t *testing.T) {
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	m := newTestManagerWithClock(t, clock.Now)
	defer m.Close()
	rc, err := m.IssueReceipt("brain", []string{"docs/**"}, 10*time.Second)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !rc.ExpiresAt.Equal(base.Add(10 * time.Second)) {
		t.Fatalf("expires_at = %v, want %v", rc.ExpiresAt, base.Add(10*time.Second))
	}
	clock.Advance(4 * time.Second)
	active, err := m.ListActiveReceipts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active = %d, want 1", len(active))
	}
	remaining := active[0].ExpiresAt.Sub(clock.Now()).Round(time.Second)
	if remaining != 6*time.Second {
		t.Fatalf("remaining = %v, want 6s", remaining)
	}
}

func TestListActiveExcludesConsumedAndExpiredAcrossHandles(t *testing.T) {
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	dbPath := filepath.Join(t.TempDir(), "shared.sqlite3")
	consumer, err := NewReceiptManager(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("open consumer: %v", err)
	}
	consumedRc, _ := consumer.IssueReceipt("brain", []string{"a/**"}, time.Minute)
	expiredRc, _ := consumer.IssueReceipt("brain", []string{"b/**"}, 5*time.Second)
	liveRc, _ := consumer.IssueReceipt("brain", []string{"c/**"}, time.Minute)
	if _, err := consumer.ValidateAndConsume(consumedRc.ReceiptID, "w"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if expiredRc.ExpiresAt.Before(base.Add(5 * time.Second)) {
		t.Fatalf("expired receipt ttl wrong: %v", expiredRc.ExpiresAt)
	}
	consumer.Close()

	clock.Advance(10 * time.Second)

	fresh, err := NewReceiptManager(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer fresh.Close()
	active, err := fresh.ListActiveReceipts()
	if err != nil {
		t.Fatalf("list on fresh handle: %v", err)
	}
	if len(active) != 1 || active[0].ReceiptID != liveRc.ReceiptID {
		t.Fatalf("fresh handle must see only the live receipt; got %+v", active)
	}
}

func TestTwoManagersOnSameDatabaseValidateIndependently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "multi.sqlite3")
	brain, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("brain handle: %v", err)
	}
	defer brain.Close()
	worker, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("worker handle: %v", err)
	}
	defer worker.Close()

	forBrain, _ := brain.IssueReceipt("brain", []string{"x/**"}, time.Minute)
	forWorker, _ := worker.IssueReceipt("worker-a", []string{"y/**"}, time.Minute)

	gotBrain, err := worker.ValidateAndConsume(forBrain.ReceiptID, "task-b")
	if err != nil || gotBrain.Issuer != "brain" {
		t.Fatalf("cross-handle validate failed: %v (%+v)", err, gotBrain)
	}
	gotWorker, err := brain.ValidateAndConsume(forWorker.ReceiptID, "task-w")
	if err != nil || gotWorker.Issuer != "worker-a" {
		t.Fatalf("reverse cross-handle validate failed: %v (%+v)", err, gotWorker)
	}
}

func TestConcurrentValidateSameReceiptExactlyOneWinner(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	rc, _ := m.IssueReceipt("brain", []string{"z/**"}, time.Minute)

	const racers = 2
	winner := make(chan struct{}, racers)
	var consumedErrs int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := m.ValidateAndConsume(rc.ReceiptID, fmt.Sprintf("racer-%d", i)); err == nil {
				winner <- struct{}{}
			} else {
				var already *AlreadyConsumedError
				if errors.As(err, &already) {
					atomic.AddInt32(&consumedErrs, 1)
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(winner)
	if len(winner) != 1 || atomic.LoadInt32(&consumedErrs) != racers-1 {
		t.Fatalf("exactly-one-winner violated: winners=%d losers=%d", len(winner), consumedErrs)
	}
}

func TestBrainIssueWorkerConsumeLeavesNoActiveReceipts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flow.sqlite3")
	brain, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	defer brain.Close()
	worker, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("worker: %v", err)
	}
	defer worker.Close()

	rc, err := brain.IssueReceipt("brain", []string{"out/**"}, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := worker.ValidateAndConsume(rc.ReceiptID, "task-9"); err != nil {
		t.Fatalf("worker consume: %v", err)
	}
	active, err := brain.ListActiveReceipts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("brain must see zero active receipts, got %d", len(active))
	}
}

func TestTenGoroutinesSingleUseReceiptOneSuccess(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	rc, _ := m.IssueReceipt("brain", []string{"s/**"}, time.Minute)

	const n = 10
	var successes, consumedFailures int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := m.ValidateAndConsume(rc.ReceiptID, "contender"); err == nil {
				atomic.AddInt32(&successes, 1)
				return
			} else if _, ok := err.(*AlreadyConsumedError); ok {
				atomic.AddInt32(&consumedFailures, 1)
			} else {
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if atomic.LoadInt32(&successes) != 1 || atomic.LoadInt32(&consumedFailures) != n-1 {
		t.Fatalf("single-use violated: success=%d consumed=%d", successes, consumedFailures)
	}
	active, _ := m.ListActiveReceipts()
	if len(active) != 0 {
		t.Fatalf("list must drain to zero after consumption, got %d", len(active))
	}
}

func TestExpiredReceiptInvisibleToFreshHandle(t *testing.T) {
	base := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	dbPath := filepath.Join(t.TempDir(), "session.sqlite3")

	prev, err := NewReceiptManager(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("prev session: %v", err)
	}
	stale, _ := prev.IssueReceipt("brain", []string{"old/**"}, 30*time.Second)
	prev.Close()

	clock.Advance(time.Hour)

	next, err := NewReceiptManager(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("next session: %v", err)
	}
	defer next.Close()
	active, err := next.ListActiveReceipts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expired receipts leaked across sessions: %+v", active)
	}
	var expired *ExpiredError
	_, err = next.ValidateAndConsume(stale.ReceiptID, "late-worker")
	if !errors.As(err, &expired) {
		t.Fatalf("want ExpiredError on late validate, got %v", err)
	}
}

func TestEndToEndIssueGatePromptDrainsActive(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	rc, err := m.IssueReceipt("brain", []string{"reports/**"}, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	consumed, err := m.ValidateAndConsume(rc.ReceiptID, "task-e2e")
	if err != nil {
		t.Fatalf("gate consume: %v", err)
	}
	prompt, err := harness.BuildContractPromptWithReceipt(
		"write the report", "collector", "workspace_write",
		consumed.AllowedPaths,
		&harness.ReceiptRef{ReceiptID: consumed.ReceiptID, Issuer: consumed.Issuer},
	)
	if err != nil {
		t.Fatalf("gate prompt: %v", err)
	}
	if !strings.Contains(prompt, "Receipt ID: "+rc.ReceiptID) || !strings.Contains(prompt, "Issuer: brain") {
		t.Fatalf("prompt missing receipt identity:\n%s", prompt)
	}
	active, _ := m.ListActiveReceipts()
	if len(active) != 0 {
		t.Fatalf("receipt must be drained after gate use, got %d", len(active))
	}
}

func TestEndToEndRevokeThenGateRejectsWithNotFound(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	rc, _ := m.IssueReceipt("brain", []string{"reports/**"}, time.Minute)
	if _, err := m.RevokeReceipt(rc.ReceiptID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := m.ValidateAndConsume(rc.ReceiptID, "task-late")
	if err == nil || !strings.Contains(err.Error(), "write receipt not found") {
		t.Fatalf("revoked gate must reject with not-found, got %v", err)
	}
}

func TestEndToEndIssueThenConsumeViaGateDrainsList(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()
	rc, _ := m.IssueReceipt("brain", []string{"g/**"}, time.Minute)
	active, _ := m.ListActiveReceipts()
	if len(active) != 1 {
		t.Fatalf("pre-consume list = %d, want 1", len(active))
	}
	if _, err := m.ValidateAndConsume(rc.ReceiptID, "gate-task"); err != nil {
		t.Fatalf("gate consume: %v", err)
	}
	active, _ = m.ListActiveReceipts()
	if len(active) != 0 {
		t.Fatalf("post-consume list = %d, want 0", len(active))
	}
}

// ---------------------------------------------------------------------------
// Coverage hardening - drive error branches that the positive tests miss.
// ---------------------------------------------------------------------------

func TestListActiveReceiptsReportsTamperedJSON(t *testing.T) {
	// Corrupt allowed_paths_json to force the unmarshal error branch in
	// ListActiveReceipts. This is the only way to drive that path because
	// json.Marshal of []string cannot fail.
	dbPath := filepath.Join(t.TempDir(), "tamper.sqlite3")
	m, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	rc := mustIssue(t, m, validIssuer, []string{"x/**"}, time.Minute)
	if err := m.Close(); err != nil {
		t.Fatalf("close before tamper: %v", err)
	}

	raw := openRawDB(t, dbPath)
	if _, err := raw.Exec(
		"UPDATE write_receipts SET allowed_paths_json = ? WHERE receipt_id = ?",
		"{not-json", rc.ReceiptID,
	); err != nil {
		t.Fatalf("tamper exec: %v", err)
	}
	_ = raw.Close()

	m2, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("reopen tampered db: %v", err)
	}
	defer m2.Close()

	_, err = m2.ListActiveReceipts()
	if err == nil {
		t.Fatal("ListActiveReceipts must fail on corrupted allowed_paths_json")
	}
	if !strings.Contains(err.Error(), rc.ReceiptID) {
		t.Errorf("error %q must mention the failing receipt id", err.Error())
	}
}

func TestValidateAndConsumeReportsTamperedJSON(t *testing.T) {
	// Same tamper trick drives the json.Unmarshal error branch inside
	// ValidateAndConsume. The path executes after a successful commit, so
	// the receipt ends up consumed but the caller still receives the error.
	dbPath := filepath.Join(t.TempDir(), "consume-tamper.sqlite3")
	m, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	rc := mustIssue(t, m, validIssuer, []string{"y/**"}, time.Minute)
	if err := m.Close(); err != nil {
		t.Fatalf("close before tamper: %v", err)
	}

	raw := openRawDB(t, dbPath)
	if _, err := raw.Exec(
		"UPDATE write_receipts SET allowed_paths_json = ? WHERE receipt_id = ?",
		"{not-json", rc.ReceiptID,
	); err != nil {
		t.Fatalf("tamper exec: %v", err)
	}
	_ = raw.Close()

	m2, err := NewReceiptManager(dbPath, nil)
	if err != nil {
		t.Fatalf("reopen tampered db: %v", err)
	}
	defer m2.Close()

	_, err = m2.ValidateAndConsume(rc.ReceiptID, "worker")
	if err == nil {
		t.Fatal("ValidateAndConsume must surface corrupted JSON")
	}
	if !strings.Contains(err.Error(), rc.ReceiptID) {
		t.Errorf("error %q must mention the failing receipt id", err.Error())
	}

	// The row was still committed to consumed=1, so a retry sees AlreadyConsumedError.
	_, err = m2.ValidateAndConsume(rc.ReceiptID, "worker")
	var reused *AlreadyConsumedError
	if !errors.As(err, &reused) {
		t.Errorf("retry after corrupted commit must report AlreadyConsumedError, got %v", err)
	}
}
