# Task: Fix #433 — release download matrix names unavailable assets

Repo: g8s. Tracking: tamld/g8s#433 (P1, field-found on v0.12.0).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (absolute paths):

- /Users/tamld/Documents/github/g8s/.goreleaser.yaml
- /Users/tamld/Documents/github/g8s/tools/release_matrix_check.sh
- /Users/tamld/Documents/github/g8s/tools/release_matrix_check_test.sh
- /Users/tamld/Documents/github/g8s/.github/workflows/dist-validation.yml

Do NOT run git commit. Do NOT modify any other file. Another worker is
operating on unrelated files in parallel — touching anything outside the
list above corrupts their delivery.

## Problem

The GoReleaser release header in `.goreleaser.yaml` (### Downloads &
Platform Matrix table, around lines 76-84) advertises
`g8s_{{ .Version }}_darwin_arm64.tar.gz` and
`g8s_{{ .Version }}_darwin_amd64.tar.gz` for macOS, but the actual build
configuration produces a single `darwin_all` archive
(`archives:` → `- id: darwin_all`). The v0.12.0 release page therefore
names two assets that do not exist — a recurrence of #338 (v0.10.1).

## Required repair

1. **Fix the table** in `.goreleaser.yaml`: macOS rows must reference the
   real artifact name pattern actually produced by the `darwin_all` archive
   id (check the `archives` `name_template` to derive the exact filename;
   if the template is `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_all`
   style, use the resulting name; keep Linux/Windows rows unchanged —
   verify they match the config too and fix them only if they are wrong).
2. **Add a recurrence guard**: new `tools/release_matrix_check.sh` that
   fails (exit 1) when any filename in the release header table does not
   match the set of archives the goreleaser config actually produces.
   Derive the expected set by parsing `.goreleaser.yaml` (archives ids +
   name_template + formats), not by running goreleaser. Keep it POSIX sh,
   no dependencies beyond grep/sed/awk.
3. **Test for the guard**: new `tools/release_matrix_check_test.sh`
   following the existing `tools/*_test.sh` convention (look at
   `tools/ci_version_sync_check.sh` peers for the pattern): (a) a fixture
   config whose table matches its archives → check exits 0; (b) a fixture
   whose table names a missing asset → exits 1 and prints the offending
   row.
4. **Wire the guard** into `.github/workflows/dist-validation.yml` as an
   additional step after the existing goreleaser steps (only when the
   file exists, so the workflow stays green before the script lands in
   the same PR — it lands in this same PR, so call it unconditionally).

## Constraints

- No version bump, no CHANGELOG edit.
- Scope limited to the four files listed above.
- `bash tools/release_matrix_check_test.sh` must pass.
- `bash tools/release_matrix_check.sh` against the repaired
  `.goreleaser.yaml` must exit 0.

## Deliverables (report back)

1. List of changed files.
2. Tail of `bash tools/release_matrix_check_test.sh` output.
3. Tail of `bash tools/release_matrix_check.sh` output (exit 0).
4. One sentence stating what the repaired macOS rows now say.
