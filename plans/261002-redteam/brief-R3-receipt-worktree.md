# Task: Red-test R3 — refresh the receipt/worktree adversarial suite against the current state-dir layout

Repo: g8s @ main. Tracking: D-02 self-audit doctrine; this refreshes
the classic containment probes against the POST-#468/#491 layout
(state-dir worktree roots, InstanceID, lease resilience) which the
original probes never targeted. Adversarial verification; findings
reported, never fixed here.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/receipt/redtest_ttl_test.go (new)
- internal/orchestrator/redtest_worktree_test.go (new)

Do NOT run git commit. Do NOT modify production code. Other workers
own controlplane/ and routing/ in parallel.

## Guarantees to verify

1. **Receipt TTL boundaries under clock manipulation.** Consume at
   exactly expires_at - 1ns (must pass), exactly expires_at (must
   fail — verify the comparison operator), 1s after (must fail);
   a receipt extended by re-issuing with the same path (old receipt
   must stay independently consumable/expired); two receipts same
   path different TTLs — consuming one must not touch the other.
2. **Single-use holds under concurrency.** Two goroutines consume the
   same receipt simultaneously — exactly one succeeds (verify the
   guard; if it relies on SQLite serialization, document that the
   guarantee is transactional).
3. **Path scope resists traversal on the current layout.** Craft
   allowed_paths with: `../` escapes, symlinked directory inside the
   scope pointing outside, absolute vs relative mixes, `.` and empty
   segments, case-insensitive filesystem assumptions (document the
   platform behavior), a path that IS allowed but is itself a symlink
   to outside — does consumption validate the FINAL target or the
   recorded path?
4. **Worktree collision-refusal holds for the state-dir root.**
   Re-run the collision scenario (#477's `.git` marker rule) against
   the NEW default root (`<state_dir>/worktrees`): pre-place a
   directory with a `.git` marker at the would-be worktree path —
   verify Acquire refuses + retries fresh shortID instead of
   RemoveAll; orphan sweep against a REGISTERED preserved worktree
   (the sa-002 Windows-chain scenario, POSIX-simulated) — verify no
   removal.
5. **InstanceID partitions the blast radius.** Two stores on two
   state dirs, one with a stale heartbeat mimicking the other's
   worker PID (the aegis kill vector, POSIX simulation): verify the
   ghost sweep does NOT kill the other instance's live worker
   corroboration path (identity-scoped kill contract, #468).

## Constraints

- stdlib testing; hermetic; POSIX-only tests carry the standard
  runtime.GOOS skip; fake clocks for TTL math.
- Guarantee failures: `t.Skip` + `// BUG(REDTEST):` + repro; list
  FIRST in the report.
- Report: per-guarantee verdict (HELD/FAILED), the constructed
  inputs, and any platform-specific behavior observed (document —
  do not generalize from one OS).
