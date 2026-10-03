# Task: Wave F7 — fix the provider-resolution false-negative (#505)

Repo: g8s @ main. Issue: #505 (real bug, live-reproduced): explicit
`--provider agy` fails with `provider "agy" not found among
platform_dispatch entries (available: agy, claude)` — the provider IS
registered but has no argv template in the operator's providers.json.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/worker/worker.go (the resolution branch ONLY — the
  NewProviderCommandResolver function, ~line 276-310)
- internal/worker/provider_resolution_test.go (new)

Do NOT run git commit. Do NOT touch cmd/, controlplane/, routing/.

## Root cause (grep-verified)

`NewProviderCommandResolver` (worker.go): with provider != "", the
lookup order is (1) api_call rejection, (2) ProviderTemplates[provider],
(3) error "not found among platform_dispatch entries". The operator's
providers.json registers agy under platform_dispatch WITHOUT an `Args`
template → ProviderTemplates has no "agy" key while
PlatformDispatchNames DOES contain "agy" → a REGISTERED provider
falls into the unknown-provider error. The empty-provider path
already handles the same situation correctly ("miss -> fallback to
default agy argv", worker.go comment at the provider=="" branch).

## Required implementation

1. In the not-found branch: if `provider` is present in
   `opts.PlatformDispatchNames` (registered) but has no template →
   return the DEFAULT agy argv (same fallback the empty-provider
   model-template miss uses — find how that default is built and
   reuse the exact same construction) so `--provider agy` on a
   template-less registration behaves like the documented default.
2. If `provider` is NOT in PlatformDispatchNames at all → keep the
   current unknown-provider error (message already lists available
   names).
3. Red test first (provider_resolution_test.go):
   - registered-without-template → default argv returned, no error;
   - registered-WITH-template → template argv (existing behavior);
   - unknown provider → error listing available names;
   - api_call provider → the existing api_call rejection error;
   - empty provider + known model → model template; empty provider +
     unknown model → default agy argv.
   Build ProviderResolverOptions fixtures inline (maps + names) —
   no manifest file needed.

## Constraints

- stdlib only; do not change function signatures; DELTA-10 R6
  resolution order stays: provider > model > default-agy.
- `go test ./internal/worker/` and `go build ./...` green;
  GOOS=windows go build ./internal/worker/ compiles.
- Report: the exact behavior change + the test table results.
