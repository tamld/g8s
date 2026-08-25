# OpenSpec DELTA-10: Two-Class Provider Architecture

**Status**: `PROPOSED`

**Milestone**: M4 — Provider Classes & Runtime Dogfooding

**Packages**: `internal/provider` (amend), `internal/worker` (amend), `internal/config` (new)

---

## 1. Goal

g8s must support, at minimum, **two provider resource classes**, per owner
architecture directive (2026-08-25):

1. **`api_call`** — operators *manually define* remote resource pools in
   configuration. Each entry names a proxy-fronted model endpoint
   (e.g. a 9router instance) with its base URL, auth source, model catalog,
   and concurrency slots. The system calls out over HTTP; no vendor-specific
   structs are hardcoded.
2. **`platform_dispatch`** — the system uses dispatch binaries **available on
   the local machine** (agy, opencode, codex, …) through the existing DELTA-08
   dispatch machinery. Type-2 dispatch against the real agy CLI v1.1.20 is
   already proven end-to-end (see docs/RELEASE_READINESS.md "Type-2 dispatch
   validation").

## 2. Added Requirements

### Requirement: Provider class taxonomy in the registry

The provider registry SHALL carry an explicit `Class` field on every provider
entry with exactly two allowed values:

- `api_call`
- `platform_dispatch`

Scenario: registry rejects unknown classes.

```gherkin
When a providers.yaml entry declares class: teleport
Then config load fails with error containing "unknown provider class"
And no provider is registered.
```

### Requirement: Config-driven api_call entries

Host declaration (`~/.config/g8s/providers.yaml`) SHALL accept entries of the
shape:

```yaml
providers:
  - name: nine-router
    class: api_call
    base_url: https://9router.example.com/v1
    auth_env: NINE_ROUTER_TOKEN      # env var holding the bearer token
    models:
      - id: gemini-3.7-flash-high
        context_window: 1000000
    slots: 4
```

Scenario: operator defines a proxy pool.

```gherkin
Given a providers.yaml with one api_call entry
When the registry discovers providers
Then the entry appears with status READY only after a successful health probe
And AcquireSlot respects the declared slot count.
```

Scenario: missing auth environment variable.

```gherkin
Given an api_call entry whose auth_env names an unset variable
When the registry discovers providers
Then the entry status is UNAVAILABLE with reason "auth_env X is not set"
And no HTTP request is issued.
```

### Requirement: platform_dispatch entries resolve locally

Entries of class `platform_dispatch` SHALL reference a binary resolvable via
the DELTA-08 resolver chain (explicit path > env var > PATH lookup > home
fallback). The worker execution path SHALL route these tasks through
`dispatch.Run`.

Scenario: local CLI resolution.

```gherkin
Given a platform_dispatch entry named agy with env AGY_BIN unset
When the registry discovers providers on a machine where agy is on PATH
Then the entry is READY and its BinaryPath equals the resolved absolute path.
```

### Requirement: Result-envelope adapter for real CLIs

The DELTA-09 worker supervisor SHALL accept real platform CLIs that do not
natively write the `{ok:true,...}` result envelope. Two adapter modes are
specified:

1. **wrapper mode** (default): the supervisor spawns
   `g8s internal wrap-exec --out <path> -- <child argv...>`; the wrapper runs
   the child, then writes `{ok: <exit==0>, exit_code: N}` to the result path.
2. **stdout mode** (opt-in per task payload `"result_mode": "stdout"`): if the
   child exits 0 without writing a result file, the supervisor synthesizes
   `{ok:true}` from the bounded stdout capture instead of failing with
   `invalid_result`.

Scenario: real agy completes under the supervisor.

```gherkin
Given a task whose request targets the agy platform_dispatch provider
When the child process finishes successfully but writes no result.json
Then the attempt finishes SUCCEEDED (wrapper or stdout mode)
And the exported receipt records exit code 0.
```

### Requirement: Class-aware selection flow

Task routing SHALL select providers by matching the requested model against
entries of either class, preferring `api_call` pools when both classes offer
the same model id, falling back to `platform_dispatch` when no pool serves it.

Scenario: preference order.

```gherkin
Given gemini-3.7-flash-high is served by both a nine-router api_call pool
  and a local agy platform_dispatch entry
When a task requests that model
Then the supervisor acquires a slot from the api_call pool first.
```

## 3. Non-goals

- No vendor-specific API adapters (OpenAI/Anthropic SDK shapes); type-1 stays
  generic proxy-pool semantics.
- No changes to receipt delegation semantics (DELTA-02/08 unchanged).
- Windows service backends remain deferred (DELTA-06A decision).

## 4. Verification gates

All implementation work behind the standing dual-pass gate
(CGO_ENABLED=0 vet+test; CGO_ENABLED=1 race) plus a live dogfood demo:
submit → worker claim → real agy execution → SUCCEEDED.
