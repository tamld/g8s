# Task: Red-test R1 — verify the auto-retry + signal-file trust boundaries

Repo: g8s @ main. Tracking context: factory program Wave F hardening
(ADR-0029/0030), D-02 self-audit doctrine. You are writing ADVERSARIAL
verifications: attempt to construct inputs that defeat each stated
guarantee, then record what actually happens. If a guarantee FAILS,
that is a finding to report — do NOT fix production code.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/controlplane/redtest_retry_test.go (new)
- internal/controlplane/redtest_signal_test.go (new)

Do NOT run git commit. Do NOT modify production code. Other workers
own routing/ and receipt/ in parallel.

## Guarantees to verify (each = one or more red tests)

1. **Retry budget cannot be exceeded.** Attempt: (a) 3rd resubmit of
   the same original (cap 2) via direct ResubmitTask calls with fresh
   contexts; (b) 11 resubmits within an hour across 11 DIFFERENT
   original tasks (per-dir cap 10) — clock-injected; (c) budget
   counting via parent_task_id lineage when the caller forges
   `retry_of` metadata with a WRONG original id (does the count
   follow the real lineage or the forged metadata?).
2. **Non-retryable classes never resubmit.** Construct result_json
   payloads that a naive string-match classifier might misread:
   success markers embedded in error text, E_USAGE nested in allowed
   fields, a receipt-violation reason that CONTAINS the word
   "timeout", unicode/fullwidth look-alikes of class keywords. Verify
   ClassifyRetryable denies all of them.
3. **Idempotency keys resist collision.** Attempt: resubmit task A
   twice concurrently (goroutines) — exactly one new task; craft an
   original task whose id CONTAINS "#r1" already (a task submitted
   with idempotency-key "x#r1" — verify the derived-key scheme stays
   collision-free for the second generation: can task
   `<orig>#r1`'s own retry collide with `<orig>`'s retry #1?).
4. **Signal file is append-only in practice and cannot be spoofed
   through the store API.** Verify: non-terminal transitions write
   nothing; the store never rewrites/truncates existing lines; a
   signal line's from/to always matches the recorded transition in
   task_events for the same task (cross-check N lines against the
   event log).
5. **Signal file cannot fill the disk unboundedly.** Drive ~5000
   terminal transitions in a loop; verify the file stays
   line-per-transition (no duplication under retry), and REPORT the
   byte size per 1000 transitions — is there any rotation/cap
   mechanism at all? (Finding either way: expected = none, document
   the growth rate and who should own retention.)
6. **Crash-window honesty under adversarial timing.** chmod the
   signals dir unwritable MID-RUN (between transitions) on POSIX:
   transitions still commit, warns appear, no error escapes to the
   caller.

## Constraints

- stdlib testing; newTestStore helper; fake clocks where timing
  matters; hermetic (t.TempDir).
- Every test states the guarantee it verifies in a comment.
- A test that exposes a guarantee failure: mark `t.Skip` with
  `// BUG(REDTEST):` + one-line reproduction, and list it FIRST in
  your report (supervisor owns fixes).
- Report: per-guarantee verdict (HELD/FAILED), the constructed inputs
  for each attempt, and the measured signal-file growth rate.
