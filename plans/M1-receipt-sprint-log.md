# M1 Receipt Engine Sprint Log (DELTA-02)

> Traceability log. Every phase records evidence, decisions, and deviations.
> Worktree: `worktrees/feat-receipt-engine` @ branch `feat/m1-receipt-engine`
> Base: `main` @ e6f01ec

## Goal (self-defined under delegated authority)

Port the Python receipt delegation baseline to Pure-Go `internal/receipt`, satisfying:

- DELTA-02 spec contract (`spec/openspec/02-receipt-delegation-spec.md`): `WriteReceipt` struct + `ReceiptManager` interface (IssueReceipt / ValidateAndConsume / RevokeReceipt / ListActiveReceipts).
- Constitution axioms: Zero-CGO (`modernc.org/sqlite` only), single-use TTL≤3600s receipts, injectable deterministic clock, atomic exclusive-tx consumption.
- Exit gate: `CGO_ENABLED=0 go test -v -race ./internal/receipt/...` passes 100%.

## Environment Decisions

| # | Decision | Evidence | Rationale |
|---|----------|----------|-----------|
| D1 | All Go commands run in Docker `golang:1.25` container (OrbStack), worktree mounted at `/workspace`, named volumes `g8s-gomod` + `g8s-gocache` | [RUNTIME] host has NO Go toolchain (checked /usr/local/go, brew, mise, asdf — all absent); Docker 29.4.0 present | Environment parity + host stays clean |
| D2 | `go.mod` bumped `go 1.22` → `go 1.25.0`; dep `modernc.org/sqlite v1.57.0` added | [RUNTIME] sqlite@latest requires go >= 1.25.0 (GOTOOLCHAIN=local blocked auto-switch on golang:1.22) | Repo is alpha; latest driver brings current fixes. Veto window open for anh. |
| D3 | Worktree via plain `git worktree add ../g8s-feat-receipt-engine -b feat/m1-receipt-engine` | [RUNTIME] ck:worktree helper script absent in repo | Skill conventions followed (feat prefix, sibling dir) |

## Phase Timeline

### Phase 0 — Setup (done)

- [x] Worktree created, tree clean at base commit.
- [x] Baseline green BEFORE dep add: `ok github.com/tamld/g8s/internal/harness 0.001s`.
- [x] Dep added, tidy, baseline re-verified green.

### Phase 1 — TDD Red

- [x] `internal/receipt/receipt_test.go` (~750 lines): port Python baseline (38 tests, 6 categories) into 28 Go test functions + added coverage for Revoke/ListActive/typed-errors/schema-contract.
- [x] RED verified [RUNTIME]: build failed with `undefined: Manager / NewReceiptManager / WriteReceipt / ReceiptManager` before implementation existed. Compiler also caught one real test bug (unused variable) which was fixed.

### Phase 2 — TDD Green

- [x] `internal/receipt/receipt.go` (~315 lines) implements `ReceiptManager`: exact schema column types, WAL + `busy_timeout(5000)` per-connection pragmas, exclusive-tx consumption guarded by `RowsAffected`, typed errors (`NotFoundError`/`AlreadyConsumedError`/`ExpiredError`) matching Python messages verbatim, float64 unix-time encoding preserving subsecond precision, `os.Chmod(dbPath, 0o600)` per containment axiom.

### Phase 3 — Exit Gate

- [x] Dual-pass gate (see D4): PASS1 `CGO_ENABLED=0 go vet ./... && go test -count=1` → `ok .../internal/receipt 2.751s`; PASS2 `CGO_ENABLED=1 go test -race -count=1` → `ok 3.788s`, zero race reports.
- [x] Verbose enumeration [RUNTIME]: 28/28 top-level tests PASS, 0 FAIL.
- [x] Full regression [RUNTIME]: `go vet ./...` clean; `CGO_ENABLED=0 go test ./...` → `harness ok`, `receipt ok`.

## Deviation Ledger (Python → Go)

| Python test | Go adaptation | Reason |
|---|---|---|
| `test_db_file_deleted_mid_session` (same live handle must raise) | Fresh-handle recovery: delete file, open NEW store, expect clean recreate + `ErrNotFound` | Go keeps an idiomatic persistent `*sql.DB` pool (old inode survives unlink); recovery property preserved, semantics documented |
| Category 4 harness-integration tests (validate_dispatch + receipt) | Covered by existing `internal/harness/harness_test.go` ("requires --receipt-id"); full store↔harness wiring lands with controlplane (Phase 2 of refactor plan) | Avoid duplicating harness scope inside receipt pkg |
| D4: Exit-criteria literal `CGO_ENABLED=0 go test -v -race` | Dual-pass gate: PASS1 CGO_ENABLED=0 (proves Zero-CGO build purity) + PASS2 CGO_ENABLED=1 -race (detector only; modernc driver remains pure Go) | [RUNTIME] `-race requires cgo` on every Go toolchain; the literal criteria is unsatisfiable. Candidate doc fix for AGENTS.md invariant 5 — veto window open for anh |

## Test Parity Ledger (Python 38 → Go 28)

38 Python test cases map onto 28 Go test functions (table-driven tests and multi-assert functions absorb several Python cases each; all 6 categories represented). Category 4 harness-gate cases are covered in `internal/harness/harness_test.go` per Deviation Ledger row 2.

## Evidence Appendix

(appended per phase below)

### Phase 1 — Red

- [RUNTIME] `go test ./internal/receipt/...` before implementation: build failure `undefined: Manager`, `undefined: NewReceiptManager`, `undefined: WriteReceipt`, `undefined: ReceiptManager`.

### Phase 3 — Gate

- [RUNTIME] PASS1 (CGO_ENABLED=0): `ok github.com/tamld/g8s/internal/receipt 2.751s`
- [RUNTIME] PASS2 (CGO_ENABLED=1 -race): `ok github.com/tamld/g8s/internal/receipt 3.788s`, zero race reports
- [RUNTIME] Verbose count: 28 `--- PASS`, 0 `--- FAIL`
- [RUNTIME] Regression: `go vet ./...` clean; harness `ok 0.001s`; receipt `ok 2.191s`
- [RUNTIME] `-race requires cgo` error reproduced when running `-race` under CGO_ENABLED=0 → basis for D4

## Authorization Record

2026-08-24, user directive (translated from Vietnamese, intent preserved):

1. Execute per self-defined goal — includes local commits on feature branches.
2. Conditional: publish as open source under MIT once MVP is achieved (design pre-approved by user).
3. Permitted: set up CI/CD + cross-platform release build testing through the public repo (higher Actions limits than private).
4. HARD CONSTRAINT: never harm, modify, or delete any files outside the project scope/folder.
