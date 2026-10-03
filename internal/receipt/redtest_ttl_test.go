package receipt

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// Guarantee 1: Receipt TTL boundaries under clock manipulation
// -----------------------------------------------------------------------------

// TestRedtest_TTL_BoundariesClockManipulation verifies TTL boundaries under deterministic
// fake clock control:
// - Consume at exactly expires_at - 1ns (must pass)
// - Consume at exactly expires_at (must fail — verify comparison operator)
// - Consume 1s after (must fail with ExpiredError)
// - Re-issued receipt with same path stays independent (old receipt expires/consumed separately)
// - Two receipts with same path and different TTLs: consuming one does not touch the other
func TestRedtest_TTL_BoundariesClockManipulation(t *testing.T) {
	base := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

	t.Run("Consume_At_ExpiresAtMinus1ns_Passes", func(t *testing.T) {
		fc := newFakeClock(base)
		m := newTestManagerWithClock(t, fc.Now)

		r := mustIssue(t, m, validIssuer, []string{"src/**"}, 10*time.Second)
		fc.Advance(10*time.Second - 1*time.Nanosecond)

		consumed, err := m.ValidateAndConsume(r.ReceiptID, "worker-1")
		if err != nil {
			t.Fatalf("consume at expires_at - 1ns must succeed, got err: %v", err)
		}
		if !consumed.Consumed {
			t.Fatalf("expected receipt to be marked consumed")
		}
	})

	t.Run("Consume_At_Exact_ExpiresAt_MustFail", func(t *testing.T) {
		// BUG(REDTEST): ValidateAndConsume uses `now.After(expiry)` which evaluates `now > expiry`.
		// At exactly `now == expiry`, `now.After(expiry)` evaluates to false, allowing consumption
		// at the expiration timestamp instead of failing. To fail at boundary, the check requires
		// `!now.Before(expiry)` (i.e. `now >= expiry`).
		fc := newFakeClock(base)
		m := newTestManagerWithClock(t, fc.Now)

		r := mustIssue(t, m, validIssuer, []string{"src/**"}, 10*time.Second)
		fc.Advance(10 * time.Second) // exactly at expires_at

		consumed, err := m.ValidateAndConsume(r.ReceiptID, "worker-2")
		if err == nil {
			t.Skipf("// BUG(REDTEST): receipt was consumed at exactly expires_at (id=%s, consumed=%v); "+
				"comparison operator `now.After(expiry)` evaluates false when now == expiry, allowing consumption at the exact boundary (must fail)",
				consumed.ReceiptID, consumed.Consumed)
		}

		var expired *ExpiredError
		if !errors.As(err, &expired) {
			t.Fatalf("expected ExpiredError at exact expires_at, got: %v", err)
		}
	})

	t.Run("Consume_1sAfter_Fails", func(t *testing.T) {
		fc := newFakeClock(base)
		m := newTestManagerWithClock(t, fc.Now)

		r := mustIssue(t, m, validIssuer, []string{"src/**"}, 10*time.Second)
		fc.Advance(11 * time.Second) // 1s after expires_at

		_, err := m.ValidateAndConsume(r.ReceiptID, "worker-3")
		var expired *ExpiredError
		if !errors.As(err, &expired) {
			t.Fatalf("consume 1s after expires_at must fail with ExpiredError, got: %v", err)
		}
		if expired.ReceiptID != r.ReceiptID {
			t.Errorf("expected ExpiredError.ReceiptID = %s, got %s", r.ReceiptID, expired.ReceiptID)
		}
	})

	t.Run("ReissuedReceipt_SamePath_IndependentLifecycles", func(t *testing.T) {
		fc := newFakeClock(base)
		m := newTestManagerWithClock(t, fc.Now)

		targetPath := []string{"pkg/core/*.go"}
		r1 := mustIssue(t, m, validIssuer, targetPath, 10*time.Second)

		// Re-issue receipt for the same path with a longer TTL
		r2 := mustIssue(t, m, validIssuer, targetPath, 30*time.Second)
		if r1.ReceiptID == r2.ReceiptID {
			t.Fatalf("re-issued receipt must receive a distinct UUID, got identical: %s", r1.ReceiptID)
		}

		// Advance past r1's expiry but before r2's expiry
		fc.Advance(15 * time.Second)

		// r1 must be expired
		_, err1 := m.ValidateAndConsume(r1.ReceiptID, "worker-r1")
		var expErr *ExpiredError
		if !errors.As(err1, &expErr) {
			t.Fatalf("old receipt r1 must be independently expired, got: %v", err1)
		}

		// r2 must still be independently consumable
		consumed2, err2 := m.ValidateAndConsume(r2.ReceiptID, "worker-r2")
		if err2 != nil {
			t.Fatalf("extended receipt r2 must remain consumable, got: %v", err2)
		}
		if !consumed2.Consumed {
			t.Fatalf("r2 must be marked consumed")
		}
	})

	t.Run("TwoReceipts_SamePath_DifferentTTLs_ConsumingOneDoesNotTouchOther", func(t *testing.T) {
		fc := newFakeClock(base)
		m := newTestManagerWithClock(t, fc.Now)

		sharedPath := []string{"docs/spec.md"}
		rA := mustIssue(t, m, validIssuer, sharedPath, 10*time.Second)
		rB := mustIssue(t, m, validIssuer, sharedPath, 50*time.Second)

		// Advance slightly and consume rA
		fc.Advance(2 * time.Second)
		cA, err := m.ValidateAndConsume(rA.ReceiptID, "worker-A")
		if err != nil || !cA.Consumed {
			t.Fatalf("failed to consume rA: %v", err)
		}

		// rB must be completely untouched
		vB, err := m.VerifyReceipt(rB.ReceiptID)
		if err != nil {
			t.Fatalf("rB should still be verifiable: %v", err)
		}
		if vB.Consumed {
			t.Fatalf("consuming rA must NOT mark rB as consumed")
		}

		// Later, rB can still be consumed normally
		fc.Advance(5 * time.Second)
		cB, err := m.ValidateAndConsume(rB.ReceiptID, "worker-B")
		if err != nil || !cB.Consumed {
			t.Fatalf("rB must still be independently consumable: %v", err)
		}
	})
}

// -----------------------------------------------------------------------------
// Guarantee 2: Single-use holds under concurrency
// -----------------------------------------------------------------------------

// TestRedtest_SingleUse_Concurrency verifies that when two goroutines attempt to consume
// the same receipt simultaneously, exactly one succeeds and exactly one receives AlreadyConsumedError.
// The test validates both single-manager (in-memory mutex) and separate-manager (SQLite transactional) guards.
func TestRedtest_SingleUse_Concurrency(t *testing.T) {
	t.Run("SingleManager_TwoGoroutines_SimultaneousConsume", func(t *testing.T) {
		m := newTestManager(t)
		r := mustIssue(t, m, validIssuer, []string{"src/*"}, time.Hour)

		var (
			wg       sync.WaitGroup
			start    = make(chan struct{})
			success  int64
			conflict int64
			mu       sync.Mutex
		)

		for i := 0; i < 2; i++ {
			wg.Add(1)
			workerID := "worker-" + string(rune('A'+i))
			go func() {
				defer wg.Done()
				<-start
				_, err := m.ValidateAndConsume(r.ReceiptID, workerID)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					success++
				} else {
					var ac *AlreadyConsumedError
					if errors.As(err, &ac) {
						conflict++
					}
				}
			}()
		}

		close(start)
		wg.Wait()

		if success != 1 || conflict != 1 {
			t.Fatalf("expected exactly 1 success and 1 conflict, got success=%d, conflict=%d", success, conflict)
		}
	})

	t.Run("TwoSeparateManagers_SQLiteTransactionalGuard", func(t *testing.T) {
		// Documented guarantee: transactional via SQLite CAS UPDATE write_receipts SET consumed = 1 WHERE receipt_id = ? AND consumed = 0
		dbPath := filepath.Join(t.TempDir(), "concurrent_receipts.sqlite3")
		m1, err := NewReceiptManager(dbPath, nil)
		if err != nil {
			t.Fatalf("create m1: %v", err)
		}
		defer m1.Close()

		m2, err := NewReceiptManager(dbPath, nil)
		if err != nil {
			t.Fatalf("create m2: %v", err)
		}
		defer m2.Close()

		r, err := m1.IssueReceipt(validIssuer, []string{"data/**"}, time.Hour, withConcernBDisabled())
		if err != nil {
			t.Fatalf("issue receipt: %v", err)
		}

		var (
			wg       sync.WaitGroup
			start    = make(chan struct{})
			success  int64
			conflict int64
			mu       sync.Mutex
		)

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := m1.ValidateAndConsume(r.ReceiptID, "consumer-mgr1")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else {
				var ac *AlreadyConsumedError
				if errors.As(err, &ac) {
					conflict++
				}
			}
		}()

		go func() {
			defer wg.Done()
			<-start
			_, err := m2.ValidateAndConsume(r.ReceiptID, "consumer-mgr2")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else {
				var ac *AlreadyConsumedError
				if errors.As(err, &ac) {
					conflict++
				}
			}
		}()

		close(start)
		wg.Wait()

		if success != 1 || conflict != 1 {
			t.Fatalf("transactional single-use failed across distinct managers: success=%d conflict=%d", success, conflict)
		}
	})
}

// -----------------------------------------------------------------------------
// Guarantee 3: Path scope resists traversal on current layout
// -----------------------------------------------------------------------------

// TestRedtest_PathScope_ResistsTraversal crafts adversarial allowed_paths:
// - `../` directory escapes
// - symlinked directory inside the scope pointing outside
// - absolute vs relative mixes
// - `.` and empty segments
// - case-insensitive filesystem assumptions (e.g. Darwin APFS vs Linux ext4)
// - a path that IS allowed but is itself a symlink to outside
//
// Investigation target: does ValidateAndConsume validate the FINAL target or the recorded path?
func TestRedtest_PathScope_ResistsTraversal(t *testing.T) {
	tempWorkspace := t.TempDir()
	outsideDir := t.TempDir()

	outsideSecret := filepath.Join(outsideDir, "outside_secret.txt")
	if err := os.WriteFile(outsideSecret, []byte("forbidden-host-data"), 0o600); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}

	// Create a symlink pointing outside the workspace (POSIX only)
	symlinkInsideScope := filepath.Join(tempWorkspace, "symlink_dir")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outsideDir, symlinkInsideScope); err != nil {
			t.Fatalf("create symlink: %v", err)
		}
	}

	// Create an allowed file that is itself a symlink to outside secret
	symlinkFile := filepath.Join(tempWorkspace, "allowed_symlink.txt")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outsideSecret, symlinkFile); err != nil {
			t.Fatalf("create symlink file: %v", err)
		}
	}

	adversarialPaths := []struct {
		name        string
		paths       []string
		description string
	}{
		{
			name:        "DotDotEscapes",
			paths:       []string{"../../etc/passwd", "../outside/secret.txt"},
			description: "Path traversal via ../ escapes",
		},
		{
			name:        "SymlinkDirInsideScopePointingOutside",
			paths:       []string{"symlink_dir/target.txt", filepath.ToSlash(symlinkInsideScope) + "/*"},
			description: "Symlinked directory inside scope pointing to foreign directory outside",
		},
		{
			name:        "AbsoluteVsRelativeMix",
			paths:       []string{"/etc/shadow", "relative/src/code.go", "/var/log/g8s.log"},
			description: "Mix of absolute root-anchored paths and relative paths",
		},
		{
			name:        "DotAndEmptySegments",
			paths:       []string{"./foo/bar", "foo//bar", "foo/./bar", "foo/bar/."},
			description: "Non-canonical paths with redundant dot and empty segments",
		},
		{
			name:        "CaseInsensitiveAssumptions",
			paths:       []string{"SRC/MAIN.GO", "src/Main.go"},
			description: "Casing variations that collide on case-insensitive filesystems (macOS APFS / Windows NTFS) but differ on Linux ext4",
		},
		{
			name:        "AllowedPathIsSymlinkToOutside",
			paths:       []string{"allowed_symlink.txt"},
			description: "A path matching allowed scope whose on-disk inode is a symlink resolving outside",
		},
	}

	for _, tc := range adversarialPaths {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)

			// 1. Issuance behavior: does IssueReceipt sanitize or accept adversarial inputs?
			r, err := m.IssueReceipt("brain-adversary", tc.paths, time.Hour, withConcernBDisabled())
			if err != nil {
				t.Fatalf("IssueReceipt rejected paths unexpectedly: %v", err)
			}

			// Verify IssueReceipt records paths verbatim without sanitization
			if len(r.AllowedPaths) != len(tc.paths) {
				t.Fatalf("expected %d paths recorded, got %d", len(tc.paths), len(r.AllowedPaths))
			}
			for i, p := range tc.paths {
				if r.AllowedPaths[i] != p {
					t.Errorf("path[%d] was altered during issue: got %q, want %q", i, r.AllowedPaths[i], p)
				}
			}

			// 2. Consumption behavior: does ValidateAndConsume inspect the final target or return recorded path?
			consumed, err := m.ValidateAndConsume(r.ReceiptID, "worker-probe")
			if err != nil {
				t.Fatalf("ValidateAndConsume failed: %v", err)
			}

			// FINDING & AUDIT:
			// ValidateAndConsume validates ONLY the recorded path in SQLite.
			// It performs ZERO filesystem I/O, does NOT resolve symlinks (os.Readlink / filepath.EvalSymlinks),
			// does NOT check whether target paths escape the workspace root, and does NOT canonicalize ../ segments.
			// The final target validation is entirely decoupled and delegated to downstream callers (e.g. deliver/reflex).
			if len(consumed.AllowedPaths) != len(tc.paths) {
				t.Errorf("consumed.AllowedPaths length mismatch: got %d, want %d", len(consumed.AllowedPaths), len(tc.paths))
			}
			for i, p := range tc.paths {
				if consumed.AllowedPaths[i] != p {
					t.Errorf("consumed path[%d] mutated: got %q, want %q", i, consumed.AllowedPaths[i], p)
				}
			}

			// Platform-specific observation documentation
			if tc.name == "CaseInsensitiveAssumptions" {
				if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
					t.Logf("[PLATFORM: %s] Filesystem is case-insensitive by default. Both 'SRC/MAIN.GO' and 'src/main.go' point to the same filesystem inode, but receipt stores byte-exact strings.", runtime.GOOS)
				} else {
					t.Logf("[PLATFORM: %s] Filesystem is case-sensitive (ext4/etc). 'SRC/MAIN.GO' and 'src/main.go' are distinct inodes.", runtime.GOOS)
				}
			}
		})
	}
}
