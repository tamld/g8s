# Phase 02: Evidence ledger + `f0 run` + G5 vacuous-pass guard (#622)

> Revised after red-team review (2026-10-20): C2, M1, M2, M9, m2.

## Architecture
A new Go `main` package at `tools/f0/` (built with `go build -o bin/f0 ./tools/f0`). goreleaser only builds `./cmd/g8s` (`.goreleaser.yaml:11,27`), so this is not part of the product. Pure Go, `CGO_ENABLED=0`. Imports from `internal/` are legal (same module).

```
tools/f0/
  main.go        # subcommand dispatch
  ledger.go      # hash-chained JSONL store + anchors
  run.go         # f0 run -- <cmd...>
  parsers.go     # items_checked extractors
  *_test.go, testdata/parsers/
```

## Ledger locations (fixes red-team C2: no G8S_STATE_DIR escape)
- **Per-repo evidence ledger**: `$(git rev-parse --git-common-dir)/f0/ledger.jsonl`. It deliberately **ignores `G8S_STATE_DIR`**, so a fresh state dir cannot give F0 an empty ledger, and it is shared by every worktree of the repo.
- **Global ledger** (mandate, debits, waivers): `~/.local/state/g8s/f0/global-ledger.jsonl`, a fixed path that also ignores `G8S_STATE_DIR`.
- Both use the same entry format and chain rules.

## Entry
- Fields: `id` ("ev-"+8 hex), `seq`, `ts` (RFC3339 UTC), `kind`, `repo`, `branch`, `sha`, `dirty` (bool) + `dirty_hash` (sha256 of `git status --porcelain` + `git diff HEAD` when dirty), `cmd` ([]string), `exit`, `duration_ms`, `stdout_sha256`, `stderr_sha256`, `tail` (last 1 KiB, with secret-like patterns redacted), `items_checked` (-1 = unknown), `status` (PASS | FAIL | VACUOUS | UNKNOWN), `extra`, `prev_hash`, `hash`.
- `hash = sha256(prev_hash || canonical_json(entry without hash))`.
- **Writes**: `internal/lockfile` exposes only non-blocking `TryLock` (`lockfile_unix.go:19-39`), so wrap it in a retry loop with backoff (10ms→250ms, give up after 10s with a clear error). Read the last line (seq/prev_hash) *under the lock*, then append with O_APPEND and fsync.

## Anchoring (detects truncation and rollback, red-team C2)
- A hash chain without a secret key can be recomputed, so truncating the tail still verifies. To defend against this, every gate PASS produces an **anchor** `F0-Ledger-Head: <seq>:<hash>` that is published outside the file: as a commit trailer on the merge commit, or as a PR comment. The T2 executor and hooks write it; Phase 04 provides `f0 anchor`.
- `f0 ledger verify [--repo P]` (a) recomputes the chain, and (b) collects all `F0-Ledger-Head` anchors from `git log --format=%B` on the default branch. For each anchor it checks `ledger[seq].hash == anchored hash`. A missing seq (truncation) or a mismatch (rewrite) ⇒ exit 1.
- Optional (Wave B): an HMAC key held in the Keychain for per-entry MACs.

## `f0 run -- <cmd...>`
- Streams output live (tee) and buffers it; the exit code is passed through.
- At the end, prints to stderr: `[f0] evidence <id> <status> items=<n> sha=<short>[ DIRTY]`.
- Kind detection by argv: go-test | node-test | npm-test | cargo-test | did-verify | g8s-offer-check | g8s-deliver | g8s-verify | generic.
- **go-test**: `f0 run` rewrites the invocation to add `-json` if it is absent (it prints a notice). It counts `{"Action":"pass"|"fail","Test":"<non-empty>"}` events. A package reporting `[no tests to run]` contributes 0 (red-team M1).
- **g8s-deliver**: adds `--json` if it is absent. It records `extra.dry_run=true` when `--dry-run` is in argv; such entries never count as delivery (red-team M5). It parses `data.applied`, `data.deleted`, `data.task_id`, `data.receipt_id` (`deliver.go:33-34`, `cli/envelope.go:36`). It also stores `extra.blobs` = git blob hash of each applied file as written in the target checkout.
- Status rules: exit≠0 ⇒ FAIL; exit=0 ∧ items=0 ⇒ **VACUOUS**; items>0 ⇒ PASS; items=-1 ⇒ UNKNOWN.
- `extra.suspect_fast` is a warning only.

## Parsers
| kind | items_checked |
|---|---|
| go-test | test pass/fail events from `-json` |
| node-test / npm-test | sum of `ℹ tests N` / `# tests N` |
| cargo-test | Σ(N passed + M failed) over `test result:` lines |
| did-verify | number of file args after `--files` (0 ⇒ VACUOUS; regression 2026-10-18) |
| g8s-offer-check | number of `exit=` gate lines |
| g8s-deliver | len(applied)+len(deleted) |
| generic | -1 |

## Tests
- Chain verify; tamper a middle line ⇒ fail at that seq.
- **Truncation after an anchor ⇒ fail.**
- Concurrent writers (≥4 processes) ⇒ seq strictly increasing, no corrupt lines.
- `G8S_STATE_DIR` changes do not change the ledger path.
- go-test: `-run NONE` ⇒ VACUOUS; `-json` injection.
- did-verify with empty `--files` ⇒ VACUOUS.
- deliver `--dry-run` ⇒ `extra.dry_run`.
- A dirty worktree sets `dirty=true`.
- Exit-code passthrough.

## Success criteria
`CGO_ENABLED=0 go test ./tools/f0/...` passes and `go vet` is clean. There are no changes outside `tools/f0/**`.
