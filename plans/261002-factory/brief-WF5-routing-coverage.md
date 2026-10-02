# Task: Wave F5 — routing package coverage lift (D-06 ratchet)

Repo: g8s @ branch feat/factory-router. Goal: raise
internal/routing package coverage from 74.8% to ≥ 90% so the CI
aggregate clears the 80.75% ratchet floor with margin.

## Delivery protocol

Scratch worktree. Write ONLY to this receipt-scoped file set:

- internal/routing/router_test.go (extend)
- internal/routing/jev_assist_test.go (new)

Do NOT touch any non-test file (router.go / jev_assist.go are
read-only for you). Do NOT run git commit. Another worker owns
internal/controlplane tests in parallel.

## Context (measure first)

Run `go test -count=1 -coverprofile=/tmp/r.cov ./internal/routing/ &&
go tool cover -func=/tmp/r.cov` in your worktree and target every
function below 90%. Likely gaps live in jev_assist.go: key-pool
rotation, 429 handling, timeout path, malformed-response path, the
env-gate (`G8S_ROUTER_MODE=jev_assisted`) branch, and the
validate-suggestion rejection paths. The mock-server pattern is
already in router_test.go (httptest with canned JevResponse bodies).

## Required

1. Cover each gap with table-driven stdlib tests; httptest servers for
   every Jev interaction (NO real network).
2. Keep every existing test green:
   `go test -count=1 ./internal/routing/ ./internal/harness/probe/`.
3. Report final package coverage % and the per-function table.
4. Real bug found → do not fix; record as `// BUG(...)` skipped seed +
   flag in report.

## Constraints

- No new deps, no production edits, deterministic (no sleeps — use
  injected/short timeouts like jev_test.go does), hermetic.
