# Debt Sweep — 2026-11-04

Coordinator: F0 (agy). Workers: F1 via `g8s submit` + `g8s worker` (cursor/composer-2.5), session `g8s-sess-debt-20261104`.
Status: **Closed.** Zero open PRs; every debt issue in scope closed with evidence.

## Inventory & disposition

| Item | Kind | Disposition | Evidence |
| :--- | :--- | :--- | :--- |
| PR #738 | docs fix | Closed — superseded by #739 | Blind review task `65497dd0` (link-integrity red, conflicting) |
| PR #719 | deps (yaml v3 3.0.6) | Merged | Blind review task `269e3b76`, 18/18 CI |
| PR #729 | perf (zero-alloc prefix) | Merged | Blind review task `671b095f`, 18/18 CI |
| PR #731 | CLI `--allow-workspace-write` (closes #724, #730) | Merged after rebase | Blind review task `e3b69a2d` (2 minor follow-ups → PR #746) |
| #720 | submit timeout validation | Closed — delivered by #727 | Live probe: malformed duration → `E_USAGE` at parse |
| PR #743 | `Route()` scanned the scorecard ledger twice per call (#730 root cause) | Merged | F1 `231dfbc0`; blind `ec2a1447` MERGE, red test proven (2 → 1 `ComputeScorecard` calls) |
| PR #745 → #742 | sa-006/007/008 probes not hermetic against inherited worker env; unbounded MCP stdio read (the Windows 10-minute hang) | Merged after 2 rounds | Round 1 blind `d1164ada` REQUEST_CHANGES (receipt-only jail); round 2 `jailProbeEnv` over all delegation vars, blind `c8bab18a` MERGE, red test proven |
| PR #746 | #724 follow-up: rejection hint names `--allow-workspace-write`; `README.vi.md`; `docs/claims.yml` | Merged | Blind `34880a69` MERGE; nits folded in round 2 (F1 `a047bc75`) |
| PR #747 → #725 | `submit -profile` presets | Merged after 2 rounds | Round 1 blind `8db646d0` REQUEST_CHANGES (stdout/JSON regression, vacuous red test); round 2 F1 `6028117a`, blind `9e0f3a1a` MERGE |
| PR #748 → #744 | six `cmd/g8s` tests failed under inherited `G8S_STRATUM` | Merged | F1 `ae2f729f`; blind `e7be8c65` MERGE, red test proven (`E_STRATUM_ADJACENCY_VIOLATION` without the scrub) |

## Eval ladder applied to every code PR
1. `g8s verify --task <id>` passes.
2. F0 reads the real diff on disk.
3. F0 reruns the battery, including a hostile inherited env.
4. A blind S3-A reviewer works in a `mktemp -d` clone and must prove the red test (revert the fix hunk, the new test fails).
5. CI is 18/18 green.

The blind reviewers caught three defects that F0 and the implementing worker both missed: the incomplete env jail (#745 round 1), the stdout pollution on bad flags (#747 round 1), and a vacuous override test (#747 round 1).

## Laws reinforced
- The diff firewall rejected three attempts. One worker invented a test-file name. In two round-2 briefs, files restored from a PR head differed from the worktree base. The receipt must cover every file that differs from the base, and a violation deletes the worktree.
- Workers must use repo-pinned linters (`$(go env GOPATH)/bin`). `go run` of other versions produces phantom issues, and a missing PATH silently skips the gate.
- Tests touching scorecards, receipts, or strata must clear the whole inherited worker env (`G8S_STATE_DIR`, `G8S_RECEIPT_ID`, `G8S_STRATUM`, `G8S_PERMISSION`, `G8S_TIER`, `G8S_SCOPE_ROOT`) once, centrally.
- Briefs must not contain `rm -rf`-like text. The harness blocks the submit with `E_HARNESS`.
