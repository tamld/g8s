# Phase 1 (WF1): Core `internal/fsops` & Native JSON Selector (`internal/cli`)

- **Session type**: T1 (strategy)
- **Parent Plan**: [`plan.md`](plan.md)
- **Issue**: `tamld/g8s#593`
- **Allowed Paths**: `internal/fsops/**`, `internal/cli/**`

---

## 1. `internal/fsops` Package Specification

### 1.1. Smart Zero-Trust Scope Verification (`VerifyScope`)
- Input: `filePath string`, `scopeRoot string`, `allowedPaths []string`.
- Computes `relPath` relative to `scopeRoot` (defaults to `.`).
- Always rejects path traversal (`..`, `../`), leading `/`, and Windows drive paths (`C:`) via `search.MatchesAllowedPaths`.
- If `len(allowedPaths) > 0` (resolved from explicit `--receipt-id` or `G8S_RECEIPT_ID` env var), enforces `search.MatchesAllowedPaths(relPath, allowedPaths)`.

### 1.2. Bounded File Read (`Read`)
```go
type ReadOptions struct {
    Path         string
    ScopeRoot    string
    StartLine    int      // 1-indexed inclusive (0 or 1 means start of file)
    EndLine      int      // 1-indexed inclusive (0 means end of file)
    MaxBytes     int      // defaults to dispatch.MaxCaptureBytes (2 MiB)
    AllowedPaths []string
}

type LineEntry struct {
    Line int    `json:"line"`
    Text string `json:"text"`
}

type ReadResult struct {
    File       string      `json:"file"`
    TotalLines int         `json:"total_lines"`
    StartLine  int         `json:"start_line"`
    EndLine    int         `json:"end_line"`
    SHA256     string      `json:"sha256"`
    Truncated  bool        `json:"truncated,omitempty"`
    Lines      []LineEntry `json:"lines"`
    Content    string      `json:"content"`
}
```
- Rejects binary files (512-byte NUL check) with a clear error.
- Computes full-file `sha256` hex digest for optimistic concurrency (`--if-sha256` in `Edit`).
- Runs `dispatch.SanitizeOutput` on returned text lines/content.

### 1.3. Atomic File Edit (`Edit`)
```go
type EditOptions struct {
    Path         string
    ScopeRoot    string
    OldString    string
    NewString    string
    ReplaceAll   bool
    IfSHA256     string
    AllowedPaths []string
}

type EditResult struct {
    File         string `json:"file"`
    Replacements int    `json:"replacements"`
    OldSHA256    string `json:"old_sha256"`
    NewSHA256    string `json:"new_sha256"`
}
```
- Enforces `VerifyScope` **before** reading or writing disk.
- If `IfSHA256 != ""` and does not match the current file's SHA-256 digest, fails with conflict error.
- Preserves original file permissions (`os.Stat().Mode()`) and newline style (`\r\n` vs `\n`).
- Writes updated content to a temporary file in `filepath.Dir(Path)`, calls `Sync()`, `Close()`, and `os.Rename(tmpPath, Path)` for atomic replacement.

---

## 2. `internal/cli` JSON Dot-Path Selector (`SelectPath`)

Add `internal/cli/select.go` & `internal/cli/select_test.go`:
- `SelectPath(rawJSON []byte, expr string) (string, error)`:
  - Supports dot-separated segments: `.data.receipt_id`, `.data.state`, `.error.code`.
  - Supports array indexing: `.data.matches[0].file`.
  - Supports array projection: `.data.matches[].file` (joins extracted elements with `\n`).
  - Scalar strings are emitted raw (unquoted, matching `jq -r`); objects/arrays are emitted as compact JSON.
