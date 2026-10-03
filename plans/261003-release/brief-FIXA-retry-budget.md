# Task: FIX-A — close the red-cell findings in the retry/resubmit surface (issue #517 D-01 tail)

Repo: g8s @ main. The release red cell (PR #536, suites
internal/autopilot/redtest_retry_test.go + cmd/g8s/redtest_resubmit_test.go)
proved these contracts broken. Fix the CODE so every BUG(REDTEST) skip in
those two files becomes a live passing assertion (flip each skip into a
real check; keep the finding comment as the test's rationale doc). Do not
weaken the tests to make them pass.

## Findings to close (from the redtest BUG markers)

1. **Lineage-wide retry cap.** The per-task cap (max 2) counts only
   DIRECT children, so resubmitting a child mints retry #3, #4, … by
   walking the lineage tree. Fix: ResubmitTask resolves the ROOT of the
   resubmission target's lineage (walk parent_task_id to origin) and
   counts ALL retry descendants of that root; refuse (typed error) when
   the cap is reached — no matter which descendant is resubmitted.
2. **Cycle/self-loop refusal.** parent_task_id == target id (and any
   ancestor cycle) is accepted today and drives GetTaskLineage to the
   1001-node CTE ceiling. Fix: refuse during the root walk — a cycle is
   a typed error, never a traversal.
3. **Backoff base when completed_at is NULL.** ResubmitTask used stale
   updated_at as the backoff base, placing not_before in the past.
   Fix: base = completed_at when present, otherwise NOW (never a stale
   timestamp).
4. **Derived-key canonicalization.** Identity games (`#R1`, `#r01`,
   trailing space) escaped budget counting and allowed parallel retries.
   Fix: canonicalize the derived key format at insert (trim, lowercase
   the `#rN` suffix, strict pattern `^<task-id>#r[1-9][0-9]*$` — refuse
   non-canonical shapes) AND count budget by lineage (finding 1), so
   key games are doubly dead.
5. **Permission casing.** `perm == "workspace_write"` lets
   "WORKSPACE_WRITE" bypass receipt enforcement. Fix: normalize with
   strings.EqualFold (or canonicalize on read) at every permission
   comparison in the resubmit/tick path.
6. **CLI receipt wiring.** executeResubmit drops `--receipt-id` (never
   reaches opts.ReceiptID → workspace_write resubmission impossible
   even with a fresh valid receipt). Fix the flag wiring, and make the
   CLI's own pre-check refuse CONSUMED receipts (the store already
   refuses — the CLI must not say yes then delegate a no; add the
   consumed check to the pre-validation, keeping the store as the
   enforcement).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/controlplane/*.go (the ResubmitTask/lineage/budget logic —
  smallest possible diff, no schema change; if a schema change feels
  REQUIRED, stop and report instead)
- cmd/g8s/resubmit.go (flag wiring + CLI pre-check)
- internal/autopilot/redtest_retry_test.go + cmd/g8s/redtest_resubmit_test.go
  (flip the BUG skips into live assertions; do not weaken assertions)

Do NOT run git commit. Do NOT touch internal/verifier, tools/,
internal/routing.

## Constraints

- `go test ./internal/controlplane/ ./internal/autopilot/ ./cmd/g8s/`
  green (including every un-skipped redtest), `go build ./...` green,
  gofumpt + golangci-lint clean on touched packages.
- Run `bash tools/ci_doc_contract_check.sh` — green before finishing.
- Report: per-finding fix (file:line), the canonical key pattern you
  enforced, and the lineage-count semantics in your own words.
