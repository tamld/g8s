# Phase 1 — Core Pure-Go Search Package & Doctor Probe (`internal/search` + `internal/doctor`)

**Session type**: T2 (execution)

## Context Links

- Issue: `tamld/g8s#591`
- Reference: [`offer/SEARCH_GUIDE.md`](../../offer/SEARCH_GUIDE.md)
- Related Code: `internal/doctor/doctor.go`

## Overview

- **Priority**: P1
- **Status**: Pending
- **Description**: Implement `internal/search` providing cross-platform concurrent file discovery (`ListFiles`) and content search (`Search`) with `.gitignore` evaluation, 512-byte NUL binary skipping, glob filtering (`-g`), fixed-string SIMD (`bytes.Index`) and regex (`regexp`), canonical `/` path normalization, and `WriteReceipt` `AllowedPaths` filtering. Wire `checkSearchEngine()` into `internal/doctor/doctor.go`.

## Requirements

1. **Functional**:
   - `search.Options`: `Roots []string`, `Pattern string`, `FixedStrings bool`, `IgnoreCase bool`, `Globs []string` (supporting `!*` negative globs and `**` globstars), `FilesOnly bool`, `IncludeHidden bool`, `NoIgnore bool`, `AllowedPaths []string` (optional receipt scope), `MaxResults int`.
   - `search.Result`: `Matches []Match` (`File`, `Line`, `Column`, `Text`), `Files []string` (when `FilesOnly=true`), `FilesSearched int`, `DurationMs int64`, `Engine string` (`"native"`).
   - Automatic `.gitignore` / `.ignore` parsing (root + per-directory basic patterns, default skip of `.git/`, `node_modules/`, `vendor/` unless `NoIgnore=true`).
   - Binary detection: skip files containing `\x00` in the first 512 bytes.
   - Cross-platform path normalization: all returned paths use forward slashes (`filepath.ToSlash`) relative to the search root when possible.
   - `internal/doctor/doctor.go`: add `checkSearchEngine()` reporting `Search Engine` status (`OK` with native + optional `rg` binary path).
2. **Non-Functional**:
   - 100% Pure Go (`CGO_ENABLED=0`), zero external module dependencies.
   - Concurrent worker pool bounded by `runtime.NumCPU()`.
   - Zero `panic()`, zero ignored `Close()` errors, zero unchecked type assertions (`ai_lint.sh` clean).

## Related Code Files

- **Create**: `internal/search/search.go`, `internal/search/search_test.go`
- **Modify**: `internal/doctor/doctor.go`

## Implementation Steps

1. Create `internal/search/search.go` with package doc comment `// Package search implements a native cross-platform file discovery and content search engine.`
2. Implement glob matching (`MatchGlob` supporting `*`, `**`, and `!` negation) and receipt `AllowedPaths` scope filtering.
3. Implement concurrent directory traversal + worker pool scanning lines with `bytes.Index` (`FixedStrings`) or `regexp.Regexp` (`Regex`).
4. Add `checkSearchEngine()` to `internal/doctor/doctor.go`.
5. Write comprehensive unit tests in `internal/search/search_test.go` covering literal match, regex match, case-insensitivity, globs (`-g '*.go'`, `-g '!*_test.go'`), `.gitignore` & hidden file skipping, binary NUL skipping, and `AllowedPaths` receipt filtering.

## Success Criteria

- `CGO_ENABLED=0 go test -race ./internal/search/... ./internal/doctor/...` passes with `>= 85%` coverage on `internal/search`.
- `bash tools/ai_lint.sh` passes with 0 violations.
