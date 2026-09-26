# Phase 3 — Pilot: `cmd/speccheck` Delivered via the Full Spec Kit Chain

## Context Links
- `.claude/commands/speckit.*.md` (from Phase 1), `spec/openspec/README.md` lifecycle, `.github/workflows/quality.yml`
- Style references: `cmd/g8s/main.go` (flag conventions), `internal/receipt/receipt_test.go` (table-driven, injectable clock)

## Overview
- **Priority**: P1 | **Status**: Pending
- Dogfood the integrated workflow end-to-end on a small, real capability: the registry sync guard. This is the pilot that proves the hybrid model before it becomes policy.

## Key Insights
- Constitution Axiom 5 (Self-Describing Executable): `speccheck` must expose `--help`/`--json`, explicit flag types/defaults/enums, and actionable machine-parseable exit codes: `0` = in sync, `1` = drift detected, `2` = usage error.
- Dual-pass CI is mandatory → `speccheck` needs its own table-driven tests, green under both `CGO_ENABLED=0` and `CGO_ENABLED=1 -race`.

## Requirements
- **Functional** checks:
  1. `manifest.json.specifications` ↔ files in `spec/openspec/*.md` ↔ README registry table (id / title / path / status, row-for-row).
  2. `.specify/memory/constitution.md` is an exact generated copy of `spec/constitution.md` (sha256 equality, GENERATED header present).
  3. Relative links inside touched registry docs resolve on disk.
- Flags: `--json` (drift report `{check, expected, actual}[]`), `--path <repo-root>` (default `.`). Report-only — no `--fix` in v1 (YAGNI).
- **Non-functional**: Pure Go, stdlib only (`encoding/json`, `crypto/sha256`); < 50 ms on this repo; zero new module dependencies.

## Architecture
```
cmd/speccheck/main.go
  ├─ parse manifest.json (encoding/json, strict)
  ├─ walk spec/openspec/*.md + parse README table (bounded regex, format pinned by header comment)
  ├─ sha256(spec/constitution.md) vs sha256(.specify/memory/constitution.md)
  ├─ emit drift list (human text | --json) → exit 0/1/2
```
Test-first: drift fixtures (missing file, stale link, hash mismatch, status mismatch) as table-driven cases.

## Related Code Files
- **Create**: `cmd/speccheck/main.go`, `cmd/speccheck/main_test.go`, `spec/openspec/21-spec-registry-guard-spec.md` (pilot delta, registered as DELTA-21)
- **Modify**: `manifest.json` + `spec/openspec/README.md` (add DELTA-21 — proving the Phase 2 routing rule)
- **Delete**: none

## Implementation Steps
1. Run the speckit chain: `/speckit.specify "spec-registry sync guard"` → `/speckit.clarify` → `/speckit.plan` → `/speckit.tasks` (artifacts committed under the speckit workspace).
2. Produce OpenSpec delta `21-spec-registry-guard-spec.md`; register DELTA-21 in `manifest.json` + registry table.
3. Write table-driven tests first (drift fixtures above).
4. Implement `main.go`; pass dual-pass gates.
5. Self-test: on a scratch branch, revert one Phase 2 fix (e.g., re-break a README link) → `speccheck` must flag exactly that drift with exit 1.

## Todo List
- [ ] speckit chain artifacts complete (spec/plan/tasks)
- [ ] DELTA-21 registered in manifest + README
- [ ] dual-pass tests green (CGO 0/1)
- [ ] drift-revert self-test passes (exit 1 on induced drift)

## Success Criteria
- g8s's own registry drift becomes machine-detectable; the integrated hybrid workflow has one complete, inspectable run.

## Risk Assessment
| Risk | Mitigation |
|------|-----------|
| README table parsing brittle | Pin table format via header comment in `spec/openspec/README.md`; manifest.json is the primary source, README secondary |
| Scope creep (auto-fix, links spidering) | Deferred — report-only v1 (YAGNI) |

## Security Considerations
- Read-only tool (read_only-equivalent; no receipts required); deterministic output; no network.

## Next Steps
- Phase 4 wires the guard into CI and ships the PR.
