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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver (Zero-CGO constitution axiom).
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type fakeClock struct {
	mu sync.Mutex
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
	if elapsed > time.Second {
		t.Errorf("issuing 1000 receipts took %v, budget is <1s", elapsed)
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
		"receipt_id":        "TEXT",
		"issuer":            "TEXT",
		"allowed_paths_json": "TEXT",
		"expires_at":        "REAL",
		"consumed":          "INTEGER",
		"consumer_task_id":  "TEXT",
		"created_at":        "REAL",
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
