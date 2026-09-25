# Implementation Plan - Issue #359: Reflex Scope Bypass and Globstar Support

## Context
During security auditing of the System 1 mutation triage gate (`internal/reflex/jev.go`), two path confinement vulnerabilities were identified:
1. `isWithinScope` verified whether `FilesModified` are within `AllowedPaths`, but only checked relative traversal (`../`). It did not reject absolute paths (`/etc/...`, `C:/...`) or Windows UNC network paths (`//server/share`), allowing out-of-workspace mutations to slip past if allowed patterns match or contain wildcards.
2. `pathMatches` used standard Go `filepath.Match`, which does not support recursive globstars (`**`). As a result, standard OpenSpec receipt patterns like `src/**` or `**/*.go` failed to match nested files.

## User Review Required
> [!NOTE]
> - All modified files submitted to `isWithinScope` must be workspace-relative paths. Any path that is absolute (POSIX leading slash, Windows drive letter, UNC path) or contains escaping relative traversal (`..`) is immediately rejected (fail-closed).
> - `pathMatches` introduces full recursive globstar (`**`) support without external third-party dependencies, adhering to standard globstar semantics.

## Proposed Changes

### Component 1: Path Confinement in `internal/reflex/jev.go`
- Implement `isPathAbsoluteOrEscaping(p string) bool` to strictly validate that input paths are relative to workspace.
- Reject paths with:
  - POSIX absolute `/...`
  - Windows absolute `C:/...`, `C:\...`
  - Windows UNC `//...`, `\\...`
  - Relative traversal `../...`, `..`
  - Empty or current-dir `""`, `.`
- Update `isWithinScope` to call `isPathAbsoluteOrEscaping` before evaluating pattern matching.

### Component 2: Recursive Globstar `**` in `internal/reflex/jev.go`
- Implement `matchGlob(cleanPattern, cleanFile string) bool` and `globToRegex(pattern string) string`.
- Correctly handle:
  - `**` matching entire path tree
  - `src/**` matching `src/a/b/c.go`
  - `**/*.go` matching `main.go`, `pkg/main.go`, `pkg/sub/main.go`
  - `docs/**/guide.md` matching `docs/guide.md` and `docs/sub/deep/guide.md`
- Integrate into `pathMatches(cleanFile, pattern string) bool`.

### Component 3: Unit Tests in `internal/reflex/jev_test.go`
- Add tests in `TestScopeBoundaryAbsoluteAndTraversingPaths` verifying rejection of `/etc/passwd`, `C:/Windows/...`, `\\server\share`, `../escape`.
- Add tests in `TestGlobstarRecursiveMatching` verifying `**`, `src/**`, `**/*.go`, and `docs/**/guide.md`.
- Verify full test suite across `./internal/reflex/...` and `go vet ./...`.
