# Code Review Report (`ck:code-review`): Issue #590 Sanitizer Credential-Parser Assignment

- **Issue**: `tamld/g8s#590`
- **F1 Worker Task**: `3840df66-f827-46f3-8877-dd2adc2430f5` (Receipt `3d415745-e517-444f-bb33-863267df6534`)
- **Reviewed Files**:
  - `internal/dispatch/dispatch.go`
  - `internal/dispatch/dispatch_test.go`
  - `internal/dispatch/sanitize_fuzz_test.go`
  - `internal/harness/probe/selfaudit.go`
  - `internal/harness/probe/selfaudit_test.go`

## Stage 1 — Spec Compliance
- [x] Deterministic regression input `api_key = parse_api_key(restored)` passes through `SanitizeOutput` byte-for-byte identical (`out == in`) and compiles cleanly under Python `ast.parse`.
- [x] Multi-argument calls, method calls, subscript/slice lookups (`restored["api_key"]`, `buf[4:36]`), dotted attribute access (`self.api_key`), and variable pass-throughs (`Client(api_key=api_key)`, `api_key = parsed_api_key`) pass through byte-for-byte identical.
- [x] JSON `{"code": "..."}` and `{"source_lines": [...]}` artifacts preserve byte-identity and `json.Valid`, avoiding false-positive `artifact_altered=true` flags.
- [x] Real secrets (`api_key = "hunter2"`, `password=secret123`, `token: sk-live-abc123`, `password: p@ss(123)`) continue to redact completely.

## Stage 2 — Code Quality & Static Analysis
- [x] `gofmt` / `gofumpt` clean.
- [x] `GOTOOLCHAIN=go1.26.9 golangci-lint run ./internal/dispatch/... ./internal/harness/probe/...` reports `0 issues`.
- [x] `tools/ai_lint.sh` passes `8/8` checks.

## Stage 3 — Adversarial Red-Team Review & Fixes Applied
1. **RT-590-1 (Secrets nested after subscript expressions)**:
   - *Attack Vector*: `api_key = items[0].fetch(password="hunter2")` starts with a valid subscript expression `items[0]`, followed by a method call containing a hardcoded credential `password="hunter2"`.
   - *Fix*: Instead of returning `m` unchanged on `publicCodeSubscriptPattern`, `redactCredentialAssignments` preserves the prefix up to `[` and recursively calls `redactCredentialAssignments` on the remainder, redacting `password=<REDACTED>` while preserving `api_key = items[0].fetch(`.
2. **RT-590-2 (3+ level nested parentheses/brackets)**:
   - *Attack Vector*: Because Go RE2 does not support recursive regexes, `\((?:[^()\r\n]|\([^()\r\n]*\))*\)` matches up to 2 levels of parentheses (`f(g(x))`). A 3-level call `api_key = parse_api_key(bytes(memoryview(restored)))` caused the regex to stop at `parse_api_key` before `(`, where `subs[3]` lacked a trailing `(`.
   - *Fix*: Switched `redactCredentialAssignments` to `FindAllStringSubmatchIndex` and checked `matchEnd < len(value) && (value[matchEnd] == '(' || value[matchEnd] == '[') && publicCodeIdentChainPattern.MatchString(subs3)`, preserving call/subscript callees at arbitrary nesting depth while continuing to scan inside the arguments.
