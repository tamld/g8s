# Plan: Fix Sanitizer Credential-Parser Assignment False Positive (#590)

- **Session type**: T2 (execution)
- **Issue**: `tamld/g8s#590` — `bug(sanitizer): v0.16 code artifact becomes invalid Python at credential-parser assignment`
- **Status**: Completed
- **Target Packages**: `internal/dispatch`, `internal/harness/probe`

## 1. Root Cause Analysis

In `internal/dispatch/dispatch.go`, `credentialAssignmentPattern` is:
```go
var credentialAssignmentPattern = regexp.MustCompile(`(?i)\b(password|credential|secret|token|api[_-]?key)(\s*[:=]\s*["']?)((?:\\.|\[(?:[^\[\]\r\n]|\[[^\]\r\n]*\])*\]|[^"'\s,})\]\\]){3,})`)
```
When `#537` (`#540`) added `)` to the negated character class `[^"'\s,})\]\\]` (so parameter type annotations like `def build(endpoint: str, token: str)` would stop before the closing `)`), it did not account for `(` on the right-hand side of an unquoted assignment:
1. For `api_key = parse_api_key(restored)`, Group 3 consumes `parse_api_key(restored` (because `(` is allowed by `[^"'\s,})\]\\]`) and stops immediately before `)`.
2. `redactCredentialAssignments` does not recognize unquoted call expressions (`parse_api_key(...)`), subscript expressions (`buf[4:36]`, `restored["api_key"]`), attribute access (`self.api_key`, `config.api_key`), or self-referential pass-throughs (`api_key=api_key`) as public code syntax.
3. Consequently, `api_key = parse_api_key(restored)` is rewritten to `api_key=<REDACTED>)`, introducing a Python `SyntaxError: invalid syntax` and falsely marking `artifact_altered=true`.

## 2. Solution Design

1. **Balanced Parentheses & Brackets in `credentialAssignmentPattern`**:
   - Extend Group 3 of `credentialAssignmentPattern` to match balanced parentheses `\((?:[^()\r\n]|\([^()\r\n]*\))*\)` atomically (mirroring the existing balanced bracket group `\[(?:[^\[\]\r\n]|\[[^\]\r\n]*\])*\]`) while adding `(` to the single-character negated class `[^"'\s,(){}\[\]\\]` so an unbalanced `(` is never partially consumed.
2. **Syntactic Context Recognition in `redactCredentialAssignments`**:
   - Explicitly distinguish **quoted** credential values (`sep` contains `"` or `'`, or `rhs` begins with `\"` or `\'`) from **unquoted** code expressions. Quoted values assigned to credential keywords are always redacted (`api_key = "hunter2"` -> `api_key=<REDACTED>`).
   - For **unquoted** assignments, preserve public code expressions byte-for-byte:
     - **Public literals** (`#373`): `True`, `False`, `None`, `null`, `nil`, numeric literals.
     - **Public type annotations** (`#537`): `str`, `int`, `float`, `bool`, `bytes`, `bytearray`, `memoryview`, `object`, `Any`, `None`, `dict`, `list`, `tuple`, `set`, `Optional`, `Union`, `Callable`, `Sequence`, `Mapping`, `Iterable`, `Path`, etc.
     - **Unquoted call expressions** (`#590`): `<ident_or_attr_chain>(...)` (e.g., `parse_api_key(restored)`, `self._extract(buf)`, `os.environ.get("API_KEY")`). Recursively apply `redactCredentialAssignments` to the interior of `(...)` so any hardcoded credential keyword argument nested inside a call (`fetch(password="hunter2")`) is still redacted while public call syntax (`parse_api_key(restored)`) passes through byte-identical.
     - **Unquoted subscript / slice expressions**: `<ident_or_attr_chain>[...]` (e.g., `restored["api_key"]`, `buf[4:36]`).
     - **Unquoted dotted attribute expressions**: `<ident>.<ident>(.<ident>)*` (e.g., `self.api_key`, `config.api_key`, `user.password`).
     - **Unquoted variable pass-throughs**: `strings.EqualFold(core, lhs)` (e.g., `Client(api_key=api_key)`, `{"api_key": api_key}`) or snake_case variable identifiers referencing credential nouns without digits (`parsed_api_key`, `restored_key`, `auth_token`, `raw_secret`, `user_password`).
3. **Regression & Fuzz Tests**:
   - Add `TestSanitizeCredentialParserAssignment590` in `internal/dispatch/dispatch_test.go` covering:
     - Plain Python source (`api_key = parse_api_key(restored)`), multi-arg calls, chained calls, subscript/slice assignments, attribute access, keyword-arg pass-through (`Client(api_key=api_key)`), JSON `{"code": "..."}`, JSON `{"source_lines": [...]}`, and nested JSONL transport, asserting both byte-identity (`out == in`) and Python `ast.parse` compilation success.
     - Real secrets (`api_key = "hunter2"`, `password=secret123`, `token: sk-live-abc123`, `password: p@ss(123)`, `api_key = fetch(password="hunter2")`) continuing to redact.
   - Add `#590` seed and public-expression idempotence cases to `internal/dispatch/sanitize_fuzz_test.go` and `internal/harness/probe/selfaudit.go`.
