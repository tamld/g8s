# Phase 03: Campaign mandate (#623)

> Revised after red-team review: C3, m1.

## Files
`tools/f0/mandate.go`, `tools/f0/mandate_test.go`, `tools/f0/main.go` (dispatch).

## Source of truth = global ledger, not the file (red-team C3)
- `f0 mandate issue …` appends a `mandate-issue` entry to the **global ledger** (`~/.local/state/g8s/f0/global-ledger.jsonl`). The `extra` field holds the full mandate JSON. It also writes a cache file `~/.local/state/g8s/f0/mandate.json`.
- Every reader (`check`, gate G0, hooks) loads the mandate from the **latest `mandate-issue` ledger entry**. If the cache file exists and differs ⇒ FAIL with "mandate cache tampered".
- Schema: `{version:1, id:"md-<8hex>", issuer, issued_at, expires_at, campaign, repos:[{name, path, max_tier}], excluded_paths:[glob], error_budget:{limit}}`.
- Budget spent = number of `debit` entries in the global ledger since `issued_at` for that mandate id. Exhausted ⇒ effective max tier is T0.
- Issuance refuses `max_tier` T4.
- Self-issuance by F0 is an *accepted boundary* (ADR-0033 Honest limitations). The digest (Wave B) lists every `mandate-issue` entry, so PiC can see who issued what.

## Commands
- `f0 mandate issue --issuer <name> --campaign <text> --repo <name>=<abs-path>:<tier> … [--exclude <glob>] [--budget N] [--ttl 24h]`
- `f0 mandate show [--json]`
- `f0 mandate check --repo <path> --action <a> [--paths p1,p2]` ⇒ `{required_tier, max_tier, allowed, reason}`; exit 0 if allowed, 3 if not.

## Tier classification
| action | tier |
|---|---|
| push-branch, open-pr, comment, close-issue | T1 |
| merge | T2; **T3** if any path matches `excluded_paths` or the repo's trust boundaries |
| publish (release/tag), force-push, delete-ref, credential/config, edit `hooks.json` | T4 |

**Trust boundaries** (red-team m1): use the existing exported API `lane.NewRouter(lane.WithTrustBoundariesPath(<abs>/.g8s/trust-boundaries.yml))`, then `Route(lane.Input{Paths: …}) == lane.LaneSecurity` (`internal/lane/lane.go:157,199-239,257-262,364`).
- Always pass an **absolute** path (`findFile` otherwise walks up from cwd, `lane.go:739-760`).
- If the file is missing, do **not** use the g8s-specific defaults (`lane.go:120-127`). Treat it as "no trust boundaries" for sibling repos, and use defaults only when repo == g8s.

## Tests
- Classification table; T3 escalation via trust boundaries and via `excluded_paths`.
- Missing trust-boundaries file in a sibling repo ⇒ no escalation.
- Expiry, missing mandate, unlisted repo ⇒ T0.
- T4 refused at issuance.
- Cache-file tamper ⇒ FAIL.
- Budget from `debit` entries ⇒ T0 when exhausted.
