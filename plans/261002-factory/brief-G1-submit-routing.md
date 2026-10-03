# Task: Wave G1 — submit-time routing, `--route auto|manual` (issue #513, ADR-0030, SCORECARD S-5)

Repo: g8s @ main. Design: docs/decisions/0030-context-router.md
(ACCEPTED — §3 injection point is the contract). The library exists
(internal/routing, 98.7% covered): you are wiring it into the user
surface.

## Delivery protocol

Scratch worktree. Write ONLY to:

- cmd/g8s/submit.go (the --route flag + wiring + request fields)
- cmd/g8s/submit_route_test.go (new)
- cmd/g8s/main.go (usage line ONLY if needed)
- docs/user-guide/routing.md (new) + docs/user-guide/cli-reference.md
  (submit section touch, if the page lists flags)
- internal/routing/router.go ONLY IF a small exported helper is
  genuinely missing (justify in report; prefer zero changes)

Do NOT run git commit. Do NOT touch internal/lane/ (G2 owns it),
internal/controlplane/, internal/worker/.

## Required implementation

1. **Flag**: `-route string` values `auto|manual`, default `manual`.
   `manual` ⇒ the EXACT current code path (golden test: same request
   JSON bytes with and without the flag present-but-manual).
2. **auto path**: build routing.RouteRequest{Prompt, Paths (from
   add_dirs + any target paths derivable), Manifest from the same
   providers.json wiring the worker path uses (main.go:1488-1546)}
   → Route() → set payloadMap["provider"], ["model"], ["role"] from
   the Decision; add payloadMap["route_source"], ["route_reason"].
   Route() error ⇒ clean E_* envelope, no fallback-to-manual silently
   (an auto route that cannot decide is a USAGE error, surfaced).
3. **Determinism guard**: with G8S_ROUTER_MODE unset and no keys,
   auto routing must produce Source=deterministic decisions with zero
   network (the library guarantee — assert it through the CLI test).
4. **Golden/byte-identity test** + a table: docs-paths payload →
   summarizer class; code payload → default class; --route absent →
   byte-identical request_json to today.
5. **docs/user-guide/routing.md**: the flag, the two layers, the
   manifest source, exit/error behavior, G8S_ROUTER_MODE.

## Constraints

- stdlib; match submit.go style; envelope errors via cli helpers.
- `go test ./cmd/g8s/` green; build green; GOOS=windows compiles.
- Report: flag semantics, the golden-test proof, fixtures used.
