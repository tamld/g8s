package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestDomainAndClosedDBErrorsWithBusyTextDoNotRetry(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	var backoffCalls atomic.Int32
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 5
	busyBackoff = func(int) time.Duration {
		backoffCalls.Add(1)
		return 0
	}

	store, _ := newTestStore(t)
	ctx := context.Background()

	_, err := store.FinishAttempt("SQLITE_BUSY", "w", "tok", FinishAttemptParams{})
	if !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("FinishAttempt unknown task: %v", err)
	}
	if errors.Is(err, ErrBusy) {
		t.Fatal("FinishAttempt must not surface ErrBusy for unknown task")
	}

	err = store.RecordReceiptExport(ctx, "database is locked", ReceiptExportStatus{})
	if !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("RecordReceiptExport unknown task: %v", err)
	}
	if errors.Is(err, ErrBusy) {
		t.Fatal("RecordReceiptExport must not surface ErrBusy for unknown task")
	}

	err = store.AddErrorCall(ctx, "SQLITE_BUSY", ErrorCallRecord{})
	if !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("AddErrorCall unknown task: %v", err)
	}
	if errors.Is(err, ErrBusy) {
		t.Fatal("AddErrorCall must not surface ErrBusy for unknown task")
	}

	if backoffCalls.Load() != 0 {
		t.Fatalf("domain errors must not invoke backoff, got %d calls", backoffCalls.Load())
	}

	closedStore, closedPath := newTestStore(t)
	if err := closedStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	_ = closedPath
	backoffCalls.Store(0)

	err = closedStore.RecordReceiptExport(ctx, "SQLITE_BUSY", ReceiptExportStatus{})
	if err == nil {
		t.Fatal("expected error on closed store")
	}
	if errors.Is(err, ErrBusy) {
		t.Fatal("closed store must not surface ErrBusy")
	}

	_, err = closedStore.ClaimTask(ctx, "SQLITE_BUSY", 60)
	if err == nil {
		t.Fatal("expected error on closed store claim")
	}
	if errors.Is(err, ErrBusy) {
		t.Fatal("closed store claim must not surface ErrBusy")
	}

	err = closedStore.ValidateResult(ctx, "database is locked", ResultValidation{})
	if err == nil {
		t.Fatal("expected error on closed store validate")
	}
	if errors.Is(err, ErrBusy) {
		t.Fatal("closed store validate must not surface ErrBusy")
	}

	if backoffCalls.Load() != 0 {
		t.Fatalf("closed-db errors must not invoke backoff, got %d calls", backoffCalls.Load())
	}
}

// runRecoveryProbe holds an external BEGIN IMMEDIATE write lock until the first
// busyBackoff(attempt==0) closes releaseCh, then rolls back deterministically.
func runRecoveryProbe(t *testing.T, dbPath string, backoffCalls *atomic.Int32, fn func()) {
	t.Helper()

	lockedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	doneCh := make(chan struct{})
	var lockErr error
	var lockedOnce sync.Once
	signalLocked := func() { lockedOnce.Do(func() { close(lockedCh) }) }

	go func() {
		defer close(doneCh)

		dsn := fmt.Sprintf("file:%s?_txlock=immediate", sqlitePathEscape(dbPath))
		lockerDB, err := sql.Open("sqlite", dsn)
		if err != nil {
			lockErr = err
			signalLocked()
			return
		}
		defer lockerDB.Close()

		conn, err := lockerDB.Conn(context.Background())
		if err != nil {
			lockErr = err
			signalLocked()
			return
		}
		defer conn.Close()

		if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
			lockErr = err
			signalLocked()
			return
		}
		signalLocked()

		select {
		case <-releaseCh:
		case <-time.After(5 * time.Second):
			lockErr = errors.New("external write lock: release not signaled within 5s")
		}
		if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil && lockErr == nil {
			lockErr = err
		}
	}()

	<-lockedCh

	origBackoff := busyBackoff
	var releaseOnce sync.Once
	busyBackoff = func(attempt int) time.Duration {
		backoffCalls.Add(1)
		if attempt == 0 {
			releaseOnce.Do(func() { close(releaseCh) })
			<-doneCh
		}
		return 0
	}
	defer func() { busyBackoff = origBackoff }()

	fn()

	if lockErr != nil {
		t.Fatalf("external lock failed: %v", lockErr)
	}
}

// runExhaustionProbe holds an external BEGIN IMMEDIATE lock for the duration of fn();
// the caller must close releaseCh and wait on doneCh after assertions (see negative probe).
func runExhaustionProbe(t *testing.T, dbPath string) (releaseCh chan struct{}, doneCh <-chan struct{}, waitLocked func()) {
	t.Helper()

	lockedCh := make(chan struct{})
	releaseCh = make(chan struct{})
	done := make(chan struct{})
	doneCh = done
	var lockErr error
	var lockedOnce sync.Once
	signalLocked := func() { lockedOnce.Do(func() { close(lockedCh) }) }

	go func() {
		defer close(done)

		dsn := fmt.Sprintf("file:%s?_txlock=immediate", sqlitePathEscape(dbPath))
		lockerDB, err := sql.Open("sqlite", dsn)
		if err != nil {
			lockErr = err
			signalLocked()
			return
		}
		defer lockerDB.Close()

		conn, err := lockerDB.Conn(context.Background())
		if err != nil {
			lockErr = err
			signalLocked()
			return
		}
		defer conn.Close()

		if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
			lockErr = err
			signalLocked()
			return
		}
		signalLocked()

		select {
		case <-releaseCh:
		case <-time.After(5 * time.Second):
			lockErr = errors.New("external write lock: release not signaled within 5s")
		}
		if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil && lockErr == nil {
			lockErr = err
		}
	}()

	waitLocked = func() {
		<-lockedCh
		if lockErr != nil {
			t.Fatalf("external lock failed: %v", lockErr)
		}
	}
	return releaseCh, doneCh, waitLocked
}

func TestBusyRetryRecoversWithoutDuplicateSideEffects(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})

	store, dbPath := newTestStore(t)
	ctx := context.Background()

	raw := openRawDB(t, dbPath)
	var journalMode string
	if err := raw.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	store.db.SetMaxOpenConns(1)
	if _, err := store.db.Exec("PRAGMA busy_timeout = 1"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}

	var backoffCalls atomic.Int32
	busyRetryAttempts = 5
	busyBackoff = func(int) time.Duration {
		backoffCalls.Add(1)
		return 0
	}

	task := submitFixture(t, store, "busy-recover", 1)
	signalPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")

	assertRetried := func(op string, before int32) {
		t.Helper()
		if backoffCalls.Load()-before < 1 {
			t.Fatalf("%s: expected backoff increment, calls=%d before=%d", op, backoffCalls.Load(), before)
		}
	}

	before := backoffCalls.Load()
	runRecoveryProbe(t, dbPath, &backoffCalls, func() {
		claimed, err := store.ClaimTask(ctx, "w-busy", 60)
		if err != nil || claimed == nil {
			t.Fatalf("ClaimTask under lock: %v %+v", err, claimed)
		}
	})
	assertRetried("ClaimTask", before)

	claimed, err := store.GetTask(ctx, task.TaskID)
	if err != nil || claimed == nil {
		t.Fatalf("get claimed: %v", err)
	}
	token := deref(claimed.LeaseToken)

	before = backoffCalls.Load()
	runRecoveryProbe(t, dbPath, &backoffCalls, func() {
		if !store.StartTask(task.TaskID, "w-busy", token) {
			t.Fatal("StartTask failed under lock")
		}
	})
	assertRetried("StartTask", before)

	before = backoffCalls.Load()
	runRecoveryProbe(t, dbPath, &backoffCalls, func() {
		if err := store.AddErrorCall(ctx, task.TaskID, ErrorCallRecord{
			WorkerID:  "w-busy",
			Attempt:   1,
			ErrorType: "test",
			ErrorMsg:  "busy probe",
		}); err != nil {
			t.Fatalf("AddErrorCall: %v", err)
		}
	})
	assertRetried("AddErrorCall", before)

	before = backoffCalls.Load()
	runRecoveryProbe(t, dbPath, &backoffCalls, func() {
		if err := store.ValidateResult(ctx, task.TaskID, ResultValidation{
			Valid:       true,
			ValidatedBy: "w-busy",
		}); err != nil {
			t.Fatalf("ValidateResult: %v", err)
		}
	})
	assertRetried("ValidateResult", before)

	before = backoffCalls.Load()
	runRecoveryProbe(t, dbPath, &backoffCalls, func() {
		if err := store.ValidateContract(ctx, task.TaskID, ContractValidation{
			Valid:       true,
			ValidatedBy: "w-busy",
		}); err != nil {
			t.Fatalf("ValidateContract: %v", err)
		}
	})
	assertRetried("ValidateContract", before)

	before = backoffCalls.Load()
	runRecoveryProbe(t, dbPath, &backoffCalls, func() {
		if err := store.RecordReceiptExport(ctx, task.TaskID, ReceiptExportStatus{Success: true}); err != nil {
			t.Fatalf("RecordReceiptExport: %v", err)
		}
	})
	assertRetried("RecordReceiptExport", before)

	before = backoffCalls.Load()
	runRecoveryProbe(t, dbPath, &backoffCalls, func() {
		_, err := store.FinishAttempt(task.TaskID, "w-busy", token, FinishAttemptParams{
			Result:  []byte(`{"ok":true,"status":"succeeded"}`),
			Success: true,
		})
		if err != nil {
			t.Fatalf("FinishAttempt: %v", err)
		}
	})
	assertRetried("FinishAttempt", before)

	finalTask, err := store.GetTask(ctx, task.TaskID)
	if err != nil || finalTask == nil {
		t.Fatalf("final get task: %v", err)
	}
	if len(finalTask.ErrorCallHistory) != 1 {
		t.Fatalf("error_call_history len = %d, want 1", len(finalTask.ErrorCallHistory))
	}

	lines := readSignalLines(t, signalPath)
	var wc int
	for _, line := range lines {
		if line.TaskID != task.TaskID {
			continue
		}
		if line.From == StateRunning && line.To == StateWorkerCompleted {
			wc++
		}
	}
	if wc != 1 {
		t.Fatalf("expected exactly one RUNNING->WORKER_COMPLETED signal, got %d (%v)", wc, lines)
	}

	// Negative sensitivity: single attempt cannot outwait a sustained write lock.
	backoffCalls.Store(0)
	busyRetryAttempts = 1
	busyBackoff = func(int) time.Duration {
		backoffCalls.Add(1)
		return 0
	}

	retryTask := submitFixture(t, store, "busy-fail", 1)
	claimed2, err := store.ClaimTask(ctx, "w-busy2", 60)
	if err != nil || claimed2 == nil {
		t.Fatalf("claim retry task: %v", err)
	}
	if !store.StartTask(retryTask.TaskID, "w-busy2", deref(claimed2.LeaseToken)) {
		t.Fatal("start retry task")
	}

	releaseCh, doneCh, waitLocked := runExhaustionProbe(t, dbPath)
	waitLocked()
	err = store.ValidateResult(ctx, retryTask.TaskID, ResultValidation{Valid: true, ValidatedBy: "w-busy2"})
	if err == nil {
		t.Fatal("expected busy exhaustion error")
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	close(releaseCh)
	<-doneCh
}

// runRecoveryProbeAdvanceClock holds an external write lock like runRecoveryProbe
// and advances the injectable store clock during the first backoff sleep.
func runRecoveryProbeAdvanceClock(t *testing.T, dbPath string, clock *fakeClock, advance time.Duration, backoffCalls *atomic.Int32, fn func()) {
	t.Helper()

	lockedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	doneCh := make(chan struct{})
	var lockErr error
	var lockedOnce sync.Once
	signalLocked := func() { lockedOnce.Do(func() { close(lockedCh) }) }

	go func() {
		defer close(doneCh)

		dsn := fmt.Sprintf("file:%s?_txlock=immediate", sqlitePathEscape(dbPath))
		lockerDB, err := sql.Open("sqlite", dsn)
		if err != nil {
			lockErr = err
			signalLocked()
			return
		}
		defer lockerDB.Close()

		conn, err := lockerDB.Conn(context.Background())
		if err != nil {
			lockErr = err
			signalLocked()
			return
		}
		defer conn.Close()

		if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
			lockErr = err
			signalLocked()
			return
		}
		signalLocked()

		select {
		case <-releaseCh:
		case <-time.After(5 * time.Second):
			lockErr = errors.New("external write lock: release not signaled within 5s")
		}
		if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil && lockErr == nil {
			lockErr = err
		}
	}()

	<-lockedCh

	origBackoff := busyBackoff
	var releaseOnce sync.Once
	busyBackoff = func(attempt int) time.Duration {
		backoffCalls.Add(1)
		if attempt == 0 {
			clock.Advance(advance)
			releaseOnce.Do(func() { close(releaseCh) })
			<-doneCh
		}
		return 0
	}
	defer func() { busyBackoff = origBackoff }()

	fn()

	if lockErr != nil {
		t.Fatalf("external lock failed: %v", lockErr)
	}
}

func TestBusyRetryClaimResamplesClockAfterBackoff(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})

	clock := newFakeClock()
	t0 := clock.Now()
	store, dbPath := newTestStoreWithClock(t, clock)
	ctx := context.Background()

	store.db.SetMaxOpenConns(1)
	if _, err := store.db.Exec("PRAGMA busy_timeout = 1"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}

	busyRetryAttempts = 5
	_ = submitFixture(t, store, "clock-busy-claim", 1)

	const leaseSec = 60
	const backoffSec = 120.0
	wantExpires := float64(t0.Unix()) + backoffSec + leaseSec

	var backoffCalls atomic.Int32
	runRecoveryProbeAdvanceClock(t, dbPath, clock, 120*time.Second, &backoffCalls, func() {
		claimed, err := store.ClaimTask(ctx, "w-clock", leaseSec)
		if err != nil || claimed == nil {
			t.Fatalf("ClaimTask under lock: %v %+v", err, claimed)
		}
		if claimed.LeaseExpiresAt == nil {
			t.Fatal("lease_expires_at unset")
		}
		clockNow := clock.Now()
		if *claimed.LeaseExpiresAt <= float64(clockNow.Unix()) {
			t.Fatalf("lease expired on arrival: expires_at=%v clock_now=%v", *claimed.LeaseExpiresAt, float64(clockNow.Unix()))
		}
		if *claimed.LeaseExpiresAt != wantExpires {
			t.Fatalf("lease_expires_at = %v, want %v (t0 + backoff + lease)", *claimed.LeaseExpiresAt, wantExpires)
		}
	})
	if backoffCalls.Load() < 1 {
		t.Fatalf("expected backoff during claim recovery, got %d calls", backoffCalls.Load())
	}
}

func TestAddErrorCallConcurrentNoLostUpdates(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	task := submitFixture(t, store, "concurrent-error-calls", 1)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	release := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-release
			errs[i] = store.AddErrorCall(ctx, task.TaskID, ErrorCallRecord{
				WorkerID:  fmt.Sprintf("w-%d", i),
				Attempt:   i,
				ErrorType: "concurrent",
				ErrorMsg:  fmt.Sprintf("msg-%d", i),
			})
		}(i)
	}
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("AddErrorCall goroutine %d: %v", i, err)
		}
	}

	got, err := store.GetTask(ctx, task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(got.ErrorCallHistory) != n {
		t.Fatalf("len(got.ErrorCallHistory) = %d, want %d", len(got.ErrorCallHistory), n)
	}
}
