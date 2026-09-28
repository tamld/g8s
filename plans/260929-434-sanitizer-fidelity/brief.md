# Task: Repair output-fidelity regression #434 in the output redaction policy

Repo: this checkout (a per-attempt worktree of g8s @ fd10dbf). Tracking: tamld/g8s#434 (P1, field-found on v0.12.0).

## Context

`internal/dispatch/dispatch.go` normalizes credential-shaped assignments in
captured worker output (`SanitizeOutput`, policy: values that are public
literals like `False`/`0`/`None` survive; other values are normalized to
`<REDACTED>`). The assignment value class currently allows a lone backslash,
which breaks JSON-transported content in two verified ways:

1. **Public literal fidelity loss**: the value class captures `False\n` (the
   literal plus the JSON escape pair right after it). The anchored
   `publicLiteralPattern` (`^(?:true|false|none|null|nil|[-+]?\d+)$`) rejects
   that capture, so the whole match `token = False\n` becomes
   `token=<REDACTED>` — a public literal is replaced AND the `\n` escape is
   eaten. Reproduction (raw bytes; `\n` means backslash+n):

   Input : `{"code":"def public_fixture(u):\n    token = False\n    return u.password is None and token is False\n","quoted_data":"fixture-payload-0042"}`
   Buggy : `{"code":"def public_fixture(u):\n    token=<REDACTED>    return u.password is None and token is False\n","quoted_data":"fixture-payload-0042"}`
   Expect: byte-identical to input (nothing in it is credential-shaped).

2. **Escape-pair split → invalid JSONL**: a capture ending on a lone backslash
   (e.g. `abc\` from `abc\"def\"`) makes the replacement consume that
   backslash; the following `"` loses its escape and the nested JSON line no
   longer decodes. This matches the issue's "two stdout JSONL lines failed
   JSON decoding" observation.

## Required repair (implement exactly this; RE2 has NO lookahead/lookbehind)

**Part 1 — atomic escape pairs in the value group.** Change
`credentialAssignmentPattern` value group so `\X` pairs are consumed atomically
and a lone backslash can never end the capture:

```go
var credentialAssignmentPattern = regexp.MustCompile(`(?i)\b(password|credential|secret|token|api[_-]?key)(\s*[:=]\s*["']?)((?:\\.|[^"'\s,}\]\\]){3,})`)
```

**Part 2 — public-literal test on the literal core.** In
`redactCredentialAssignments`, test only the leading plain chars (before the
first backslash) against `publicLiteralPattern`; keep the whole match when it
is a public literal:

```go
core := subs[3]
if i := strings.IndexByte(subs[3], '\\'); i >= 0 {
    core = subs[3][:i]
}
if publicLiteralPattern.MatchString(core) {
    return m // public literal (possibly followed by transport escapes) — keep
}
return subs[1] + "=<REDACTED>"
```

**Part 3 — URL classes stop before escapes.** Add `\\` to the negated classes
so URL matches never consume into an escape pair (URLs are fully redacted
anyway, so this only preserves the trailing escape):

```go
{regexp.MustCompile(`postgresql://[^\s"\\\x60]+`), "postgresql://<REDACTED>"},
{regexp.MustCompile(`://[^\s"\\\x60/:]+:[^\s"\\\x60/@]+@`), "://<REDACTED>:<REDACTED>@"},
```

Do NOT touch the `specifically \x60...\x60` backtick pattern and do NOT
restructure anything else in the file.

## Test matrix — add table-driven `TestSanitizeJSONTransportFidelity434` in `internal/dispatch/dispatch_test.go` (place it near `TestSanitizePreservesPublicLiterals`)

| # | input (raw) | required outcome |
|---|---|---|
| 1 | the exact issue repro JSON above | `SanitizeOutput(in) == in` byte-exact |
| 2 | `{"type":"item.completed","text":"set password: abc\"def\" here"}` | contains `password=<REDACTED>`; `json.Valid([]byte(out))` is true |
| 3 | `{"source_lines":["def public_fixture(u):","    token = False","    return u.password is None and token is False"]}` | byte-exact unchanged |
| 4 | `{"code":"token = False\ngood = True"}` | byte-exact unchanged (public literal keeps following escape + content) |
| 5 | `{"code":"x = f(token=False, retries=0, timeout=None)"}` | byte-exact unchanged |
| 6 | `{"cred":"example-value-77\nnext chunk"}` | `cred=<REDACTED>` present; `example-value-77` gone; line stays `json.Valid` |
| 7 | `{"dsn":"postgresql://user:pass@db.example.com/sales\n"}` | URL fully redacted; trailing `\n` escape preserved; `json.Valid` true |
| 8 | `password:\"example-value-77\"` (escaped quotes around the value) | normalized to `password=<REDACTED>` (currently missed by the pattern — this repair must close that gap) |

Also assert in the same test that case 2's output contains no
`example-value-77` remainder. All EXISTING tests in the package must stay
green — in particular `TestSanitizePreservesPublicLiterals` (#373) and the
postgres/URL redaction tests.

## Constraints

- No version bump, no CHANGELOG edit (release tooling owns both).
- Do not widen scope: only `internal/dispatch/dispatch.go` and
  `internal/dispatch/dispatch_test.go` may change.
- gofmt clean; `go build ./...` clean.
- Do not weaken the redaction policy anywhere: every input that is normalized
  today must still be normalized (case 8 must improve, nothing may regress).

## Deliverables (report back)

1. List of changed files.
2. Tail of `go test ./internal/dispatch/ -run 'TestSanitize' -v` output.
3. Tail of `go test ./internal/dispatch/...` (full package) output.
4. One sentence stating which pre-existing behavior changed (case 8).
