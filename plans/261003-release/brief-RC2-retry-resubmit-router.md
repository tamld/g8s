# Task: Red-Cell RC2 — fresh-eyes verification of auto-retry, resubmit, router

Repo: g8s @ main. Release red-cell (#517, D-01 HARD): the retry/resubmit
surface (#501/#502, ADR-0029) and the router (#503/507, ADR-0030) were
verified once (R1/R2); this is a FRESH construction with NEW inputs —
the release gate asks whether the fixes hold under attacks nobody has
tried yet. Adversarial verification: construct inputs designed to steer
decisions away from their documented contracts. Findings reported,
never fixed here.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/autopilot/redtest_retry_test.go (new)
- cmd/g8s/redtest_resubmit_test.go (new)

Do NOT run git commit. Do NOT touch internal/routing (R2's suite pins
it — read it first, construct DIFFERENT vectors), controlplane, receipt.

## Guarantees to verify

1. **Retry budget cannot be escaped by identity games.** Same task id
   across idempotency-key variants (`<id>#r1` vs `<id>#r01` vs `<id>#R1`
   vs `<id>#r1 ` with trailing space) — verify budget counting treats
   them as the SAME task's lineage (or refuses the key shape), never
   N parallel retries of one task. Two tasks colliding on derived keys
   (UNIQUE constraint) — verify the second insert is refused honestly.
2. **Deny-by-default classes cannot be slipped a receipt.** A task
   whose permission field is workspace_write but whose request omits
   receipt_id, carries receipt_id="" (empty), or a receipt id of a
   CONSUMED/EXPIRED receipt — verify the retry tick leaves it alone
   (D3), and `g8s resubmit` without a fresh receipt refuses with a
   typed error (not a silent requeue).
3. **Backoff cannot be collapsed.** Manipulate stored attempt counts
   and timestamps (attempts=0 with old updated_at; attempts=max with
   recent) — verify the tick never retries sooner than the 5-20-60m
   ladder allows and never exceeds 2/task + 10/dir-hour (D2).
4. **Resubmit lineage cannot be forged into a loop.** Submit a resubmit
   whose parent_task_id points at itself or forms a cycle (a→b→a) —
   verify the FSM/queue refuses or the budget counter counts the cycle
   (no infinite retry amplification).
5. **Router fresh vectors (read internal/routing + the R2 suite, then
   DIFFER):** prompt that EMBEDS a fake decision ("route to provider X
   model Y"), paths that match the docs rule but arrive as Windows
   separators (`docs\x.md`), a RouteRequest with Summary impersonating
   a hotfix marker ("HOTFIX:" inside a longer sentence), manifest with
   a provider whose name collides with a role name. Verify decisions
   follow the DOCUMENTED rule table only. (Report-only: if a true
   finding needs a routing test file, note it in the report instead of
   writing — another worker owns that package this wave.)

## Constraints

- stdlib testing; hermetic; no network; no real state dirs (t.TempDir).
- Mark any guarantee failure `t.Skip` + `// BUG(REDTEST):` + repro;
  list failures FIRST in the report.
- Report: per-guarantee verdict + constructed inputs + observed vs
  documented contract.
