# Phase 2 — CLI Subcommand `g8s search` & Receipt Scope Gate (`cmd/g8s`)

**Session type**: T2 (execution)

## Context Links

- Issue: `tamld/g8s#591`
- Depends on: [Phase 1](./phase-01-internal-search-engine.md)
- Related Code: `cmd/g8s/main.go`, `cmd/g8s/deliver.go`

## Overview

- **Priority**: P1
- **Status**: Pending
- **Description**: Wire `g8s search` into `cmd/g8s/main.go` and implement `cmd/g8s/search.go` + `cmd/g8s/search_test.go` supporting `ripgrep`-compatible flags (`-F`, `-i`, `-q`, `-g`, `--files`, `--hidden`, `--no-ignore`, `--scope-root`) plus `g8s`'s exclusive `--receipt-id <id>` Zero-Trust scope filter and `--json` / `--jsonl` envelope output.

## Requirements

1. **CLI Flags (`g8s search`)**:
   - `-F` / `--fixed-strings`: Treat pattern as a literal string instead of a regular expression.
   - `-i` / `--ignore-case`: Case-insensitive matching.
   - `-q` / `--quiet`: Exit `0` immediately when at least one match (or file in `--files` mode) is found, or exit `1` when no matches are found (drop-in replacement for `rg -q` / `grep -q` in shell scripts).
   - `-g` / `--glob <glob>` (repeatable): Include or exclude files/directories matching glob (e.g. `-g "*.go" -g "!*_test.go"`).
   - `--files`: Print/return matching files without searching file contents (replaces `rg --files` and `find`).
   - `--hidden`: Search hidden files and directories (still skips `.git` unless `--no-ignore` is also passed).
   - `--no-ignore` / `-u`: Do not respect `.gitignore` / `.ignore` rules or default `node_modules`/`vendor` exclusions.
   - `--scope-root <dir>`: Base directory for relative path resolution and receipt scope evaluation.
   - `--receipt-id <id>`: Load the specified `WriteReceipt` from the receipt store (`VerifyReceipt`) and restrict search/file listing strictly to `rc.AllowedPaths`.
   - `--max-results <n>`: Cap returned matches/files (default `1000`).
2. **Envelope & Exit Codes**:
   - In `--json` / `--jsonl` mode (when `-q` is not set), emit `v: 1, kind: "search", cmd: "search"` containing `matches`, `files`, `files_searched`, `duration_ms`, `engine`, and optional `receipt_id`.
   - In `-q` mode, exit `0` on match and exit `1` on zero matches without stdout noise.

## Related Code Files

- **Create**: `cmd/g8s/search.go`, `cmd/g8s/search_test.go`
- **Modify**: `cmd/g8s/main.go`

## Success Criteria

- `go test -v ./cmd/g8s -run TestSearch` passes all CLI scenarios (literal, regex, `--files`, `-q`, `-g`, `--receipt-id`, invalid regex error handling).
