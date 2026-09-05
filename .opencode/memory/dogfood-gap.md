# Dogfood Gap — OpenCode Session vs g8s submit

**Status**: Known compromise, tracked for v0.8.0+ resolution.

## What we say

`spec/constitution.md` and `docs/RELEASE_STRATEGY.md` state g8s dogfoods itself: every workflow that touches g8s should pass through `g8s submit` so worker dispatch is tracked, audited, and rate-limited.

## What actually happens

The OpenCode session running this work (Sisyphus + subagents) does NOT dispatch workers via `g8s submit`. Instead it uses MCP `agy-dispatch` to invoke the `agy` binary directly from the tool layer. The g8s supervisor, slot pool, telemetry events, and audit trail are all bypassed for routine dev work.

## Why

1. **Iteration speed**: `g8s submit` requires DB setup (`G8S_DB`), providers.json, build step. MCP `agy-dispatch` is a single `task()` call.
2. **Tool layer architecture**: OpenCode tool layer (MCP) is a primitive that g8s is built ON TOP of. Treating the tool layer as the dogfood layer conflates primitive with product.
3. **CI gap is the real problem**: workers ARE supposed to run through g8s for tracked dispatch — that path is exercised by the `g8s-dogfood` workflow on `windows-e2e.yml`, not by every dev commit.

## Compromise (v0.7.0)

- **Dev loop**: MCP agy-dispatch OK. Session-level, low-stakes, fast iteration.
- **CI gate**: `g8s version --json` and `g8s --help` exercised on every PR/push in `ci.yml` so binary regression is caught.
- **Production dogfood**: any task that needs audit trail goes through `g8s submit` (worker dispatch, batch jobs, scheduled runs).

## Trade-offs

| Concern | MCP bypass | g8s submit |
|---|---|---|
| Iteration speed | Fast | Slow |
| Audit trail | None | Full |
| Telemetry events | Skipped | Captured |
| Slot concurrency | Not enforced | Enforced |
| Receipt persistence | Not written | Written |

## Resolution path

**v0.8.0** (Concern A — supervisor package, T020) will introduce worker metrics + lease accounting at the supervisor layer. At that point, MCP agy-dispatch can be wrapped as a g8s-managed dispatch path that records the call in the receipt log without forcing the dev loop through `g8s submit` CLI overhead.

**v0.9.0** (M5 — telemetry, #253) will add worker trace telemetry that captures MCP agy-dispatch calls as well as `g8s submit` calls, unifying audit surface regardless of dispatch path.

## Action items

- [ ] v0.8.0: Wrap MCP agy-dispatch as g8s-tracked dispatch (Concern A deliverable).
- [ ] v0.9.0: Telemetry hook for MCP dispatch path (#253).
- [x] v0.7.0: Add CI gate `g8s version --json` to catch binary regressions on PR/push.
