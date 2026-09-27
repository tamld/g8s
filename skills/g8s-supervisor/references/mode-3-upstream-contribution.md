# Mode 3: g8s Upstream Contribution

> Use only for a reproduced g8s defect that materially blocks active project work.
> Read `hard-boundary.md` first.

## Scope rule

Contribution is a bounded interruption, not primary work. Stop after the smallest useful packet or
fix. Never use current project work as reason to refactor, update, configure, or release g8s.

## Classification

| Class | Evidence required | Next artifact |
|---|---|---|
| Fix | Reproduced `kind:"error"`; caller/provider/environment alternatives checked | Sanitized issue packet |
| Optimization | Reproduced g8s transport/contract behavior fails its own declared acceptance check | Sanitized issue packet |
| Proposal | Verified current behavior plus a distinct approved scope | Sanitized proposal packet |
| Evidence gap | Missing identity, schema, receipt, oracle, or reproduction | Block/retest; no upstream claim |

## Contribution loop

1. Capture executable identity: canonical path, SHA-256, self-reported version, immutable
   provenance, runtime capability evidence, and task/receipt identifiers when available.
2. Sanitize reproduction: expected/actual, affected scope, acceptance result, competing hypotheses,
   redactions. Exclude prompts, raw output, tokens, provider config, secrets, and sensitive paths.
3. Rule out malformed task packet, unsupported provider binding, permission mismatch, and local
   environment fault. If unresolved, keep `EVIDENCE_GAP`.
4. Build issue/PR packet from `assets/issue-packet.md.tmpl` or `proposal-packet.md.tmpl`.
5. Hand packet to PiC Agent. Public issue/PR creation needs current explicit authorization naming
   repository, destination, purpose, and draft/publish status. Never push or merge.

## Local code fix, only after PiC approval

PiC Agent must approve the named local fix scope before any g8s source edit. Then:

1. Read `g8s/AGENTS.md`, then `README.md`, `spec/constitution.md`,
   `docs/REFACTORING_PLAN.md`, and target OpenSpec delta.
2. Create/modify OpenSpec before code. Use owned isolated worktree; leave unrelated shared checkout
   state untouched.
3. Add focused table-driven test; implement smallest Pure-Go, Zero-CGO fix.
4. Run `CGO_ENABLED=0 go test -race ./...` and applicable lint/format checks.
5. Return diff, tests, risk, rollback, and updated packet to PiC Agent. Do not commit, push, merge,
   or publish without separately applicable authorization.

**Inversion:** if a caller/config/provider mismatch is reported as g8s defect, an upstream fix can
mask the real boundary failure. Reproduction must discriminate these causes first.
