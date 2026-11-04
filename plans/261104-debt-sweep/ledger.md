# Debt Sweep — 2026-11-04

Coordinator: F0 (agy). Workers: F1 via `g8s submit` + `g8s worker` (cursor/composer-2.5), session `g8s-sess-debt-20261104`.

## Inventory & disposition

| Item | Kind | Disposition | Evidence |
| :--- | :--- | :--- | :--- |
| PR #738 | docs fix | Closed — superseded by #739 | Blind review task `65497dd0` (link-integrity red, conflicting) |
| PR #719 | deps (yaml v3 3.0.6) | Merged | Blind review task `269e3b76`, 18/18 CI |
| PR #729 | perf (zero-alloc prefix) | Merged | Blind review task `671b095f`, 18/18 CI |
| PR #731 | CLI `--allow-workspace-write` (closes #724, #730) | Rebased, merge on green | Blind review task `e3b69a2d` (2 minor follow-ups) |
| #720 | submit timeout validation | Closed — delivered by #727 | Live probe: malformed duration → `E_USAGE` at parse |
| #730 root cause | `Route()` scanned the scorecard ledger twice per call | Fix PR (this ledger) | F1 task `231dfbc0`: 2 → 1 `ComputeScorecard` calls, 20k-row parity test |
| #724 follow-up | rejection hint + `README.vi.md` still env-only | F1 worker after #731 | Review finding from task `e3b69a2d` |
| #725 | `submit -profile` presets | F1 worker after #731 | Brief: `-profile` resolution, E_USAGE on unknown, same gates |
| Probe hermeticity | `TestIASTDASTProbes_AllPass` fails when `G8S_RECEIPT_ID` is inherited (every F1 worker sees a false red) | New issue + F1 worker | F0 repro: only `G8S_RECEIPT_ID=deadbeef` flips the test to FAIL |

## Laws reinforced
- Diff firewall works: first route-perf attempt rejected (`E_DIFF_FIREWALL_OUT_OF_SCOPE`) for a test file outside the receipt. Briefs must name new files exactly.
- Workers must use repo-pinned linters (`$(go env GOPATH)/bin`); `go run` of other versions produces phantom issues.
- Tests touching scorecards or in-process edit guards must clear ambient worker env (`G8S_STATE_DIR`, `G8S_RECEIPT_ID`).
