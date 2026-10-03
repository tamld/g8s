# Task: FIX-C — sanitizer must not corrupt public type annotations (issue #537, fix before the v0.14.0 tag)

Repo: g8s @ main. Issue #537 (filed by the operator with a runtime repro):
the sanitizer redacts a Python parameter TYPE ANNOTATION as if it were a
credential value — `token: str` inside a def signature became
`token=<REDACTED>`, producing code that fails `ast.parse`. The
artifact_altered metadata correctly reported it — the bug is the
false-positive itself. Red-first: land the repro as a failing test, then
fix the sanitizer, then flip the test green.

## The repro (from #537, simplified to a unit fixture)

Input line:  `def build(endpoint: str, token: str) -> dict:`
Wrong output: `def build(endpoint: str, token=<REDACTED> -> dict:`
Required: byte-identical passthrough (public type annotations are not
secrets).

## Required behavior

1. **Red-first regression**: add a test in the sanitizer's existing test
   file (find it — internal/harness/) covering: Python def signatures
   with typed params (`token: str`, `password: str`, `api_key: str` as
   ANNOTATIONS — param names that LOOK secret), return annotations
   (`-> dict:`), and asserts BOTH `ast.parse` success (stdlib ast) and
   byte-identity. Also keep: every existing sanitizer test green —
   real credentials in the SAME shapes must still redact
   (`token: sk-live-abc123`, `api_key = "hunter2"`, `password: hunter2`
   in a YAML context, etc.).
2. **The fix (smallest correct change)**: the credential-assignment
   pattern must not fire when the captured "value" is a bare PUBLIC TYPE
   token — implement an allowlist of common type names (str, int,
   float, bool, bytes, object, Any, None, dict, list, tuple, set,
   Optional, Union, plus generic shapes like `list[str]`) checked on
   the captured value before redacting. If a REAL credential's value
   literally equals one of these tokens, the operator can still rely on
   the other layers (such values are not secret-shaped anyway).
   Document the allowlist on the sanitizer function.
3. **Do NOT** weaken any other redaction rule; do not change the
   artifact_altered metadata semantics; no schema changes.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/harness/<the sanitizer file>.go + its existing test file
  (+ redtest additions welcome in the same test file)

Do NOT run git commit. Do NOT touch controlplane, receipt, cmd/.

## Constraints

- stdlib only; `go test ./internal/harness/` green INCLUDING the new
  regression; `go build ./...` green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Report: the exact pattern that fired (file:line), the allowlist you
  implemented, and the before/after of the #537 repro.
