# 08 — Dispatch Wrapper Spec (DELTA-08)

Port of `reference/python/scripts/agy_dispatch.py` to a Pure-Go package
`internal/dispatch`. The wrapper runs ONE bounded AGY CLI job behind the
harness gate and returns a structured, sanitized result.

## ADDED Requirements

### Requirement: Binary resolution without host assumptions

`ResolveBinary(explicit string, opts ResolveOptions) (string, error)` SHALL
resolve the AGY executable with injectable seams (`EnvLookup`, `Platform`,
`Home`, `Which`, `Exists`) and the precedence:

1. Explicit reference (caller-provided path or command name).
2. `AGY_BIN` environment override.
3. `Which("agy")` PATH lookup.
4. Home fallbacks in order: `<home>/.local/bin/agy`,
   `<home>/AppData/Local/Programs/agy/agy`, `<home>/AppData/Roaming/npm/agy`.

#### Scenario: explicit beats env beats PATH beats home
- Given an explicit reference that exists, resolution returns it even when
  `AGY_BIN` and PATH also contain a match.
- When no explicit reference is set but `AGY_BIN` exists, `AGY_BIN` wins over PATH.

#### Scenario: Windows suffix expansion
- On platform `windows`, a suffix-less reference expands candidates
  `<ref>.exe`, `<ref>.cmd`, `<ref>.bat` before falling back to `Which`.
- On non-Windows platforms no suffix candidates are probed.

#### Scenario: home fallback on Windows npm layout
- With `Home` set and only `<home>/AppData/Roaming/npm/agy.cmd` existing,
  resolution returns that file after PATH lookup misses.

#### Scenario: not found is an error
- When every seam misses, `ResolveBinary` returns a descriptive error; the
  envelope surfaces it before any process spawn.

### Requirement: Command construction keeps sandbox by default

`BuildCommand` SHALL emit `[bin, --prompt, <prompt>, --model, <model>,
--print-timeout, <timeout>]`, then append `--dangerously-skip-permissions`
only when skip-permissions is requested, append `--sandbox` unless
no-sandbox is requested, then one `--add-dir <expanded>` pair per scope dir
with `~` expansion.

#### Scenario: skip-permissions keeps sandbox
- Requesting skip-permissions alone yields both `--dangerously-skip-permissions`
  and `--sandbox`.

#### Scenario: no-sandbox omits sandbox flag
- Requesting no-sandbox yields neither `--sandbox` nor any permission flag.

### Requirement: Read-only contract violation detection

`DetectReadOnlyContractViolations(stdout, stderr)` SHALL scan the combined,
non-empty output for six violation classes ported verbatim from the Python
baseline: `wiki_mutation_command`, `wiki_mutation_report`,
`wiki_reflect_side_effect` (`session logged to log.md`),
`wiki_write_side_effect` (`note written:`), `git_mutation_command`
(line-start git add/commit/checkout/reset/merge/rebase/push/rm/mv), and
`git_commit_side_effect` (`[branch abc1234] ...` lines). Each hit records a
type plus a ±96-rune snippet whose newlines are escaped and which is passed
through output sanitization. Negative instructions ("Do not run wiki.py
reflect") MUST NOT match because mutation verbs require command/report shape.

#### Scenario: negative instruction is ignored
- Output containing "Do not run wiki.py reflect" produces zero violations.

#### Scenario: session-log side effect is flagged
- Output containing "Session logged to log.md" yields exactly one violation
  of type `wiki_reflect_side_effect`.

### Requirement: Sensitive output sanitization

`SanitizeOutput` SHALL redact, in order: full `postgresql://...` URLs to
`postgresql://<REDACTED>`; generic `scheme://user:pass@` credentials to
`://<REDACTED>:<REDACTED>@`; backtick payloads after "specifically" to
`` specifically `<REDACTED>` ``; and any password/credential/secret sentence
fragment (≤160 trailing chars) keeping only the matched keyword plus
`<REDACTED>`, case-insensitively.

#### Scenario: postgres credential line is redacted
- `postgresql://user:password@host/db` becomes `postgresql://<REDACTED>`.

### Requirement: Bounded capture with lossless-enough degradation

Capture SHALL decode subprocess output with invalid UTF-8 bytes replaced by
U+FFFD. When raw size exceeds `MaxCaptureBytes` (2 MiB), capture SHALL keep
the first half and last half of the budget joined by a `\n<OUTPUT_TRUNCATED>\n`
marker instead of failing or deadlocking on huge streams.

#### Scenario: oversize probe truncates with marker
- Feeding ~3 MiB through the bounded capture yields output containing
  `<OUTPUT_TRUNCATED>` and total length within `MaxCaptureBytes` + marker.

#### Scenario: invalid UTF-8 is replaced
- Raw bytes containing `0xFF` decode to U+FFFD without error.

### Requirement: Dispatch envelope

`Run(opts RunOptions) (Result, error)` SHALL: resolve the binary; enforce the
harness gate via `harness.ValidateRequest` (gate failure = returned error,
no execution); build the contract prompt via `harness.BuildContractPrompt`;
execute through an injectable runner (tests substitute fakes); run violation
detection ONLY when the resolved permission profile forbids mutation; map a
zero exit code WITH violations to `HarnessReturnCode == ReadOnlyContractExit (3)`
and `OK == false`; sanitize stdout/stderr in the result; and report model,
role, permission, binary, add-dirs, duration (injectable clock), sanitized
stdout/stderr, and optional `contract_violation {policy, exit_code, violations}`.
Mutation-allowed profiles never run detection regardless of output content.

#### Scenario: clean read-only success
- Runner returns code 0 with benign output; result reports OK true,
  harness return code 0, no violation block.

#### Scenario: side effect under read-only fails closed
- Runner returns code 0 whose stdout logs "Session logged to log.md";
  result reports returncode 0, harness returncode 3, OK false, and a
  contract_violation block with policy `read_only`.

#### Scenario: workspace_write skips detection
- Same violating output under a mutation-allowed profile leaves
  harness returncode equal to the runner code with no violation block.

## Constraints inherited from constitution

- Zero-CGO: standard library only (`os/exec`, `regexp`, `bytes`).
- Determinism: envelope duration uses an injectable clock; resolver and
  capture are pure functions over injected seams.
- Spec-first: this delta precedes implementation; naming follows repo Go
  conventions rather than Python identifiers.
