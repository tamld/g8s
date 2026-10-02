# Task: Wave F3 — ADR-0030 context router: deterministic first, Jev optional

Repo: g8s @ main. Design: docs/decisions/0030-context-router.md
(ACCEPTED — read it first; two BINDING operator constraints: the
router is the factory's distributor, and **Jev is OPTIONAL — support,
measure, benchmark; never mandatory**). Tracking:
plans/261002-factory/plan.md (Wave F3), SCORECARD S-3/S-4.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your
changes DIRECTLY to these receipt-scoped repository files:

- internal/routing/router.go (new package)
- internal/routing/jev_assist.go (new)
- internal/routing/router_test.go (new)
- internal/harness/probe/types.go (add ONE category const)
- internal/harness/probe/routing_suite.go (new — benchmark probes)

Do NOT run git commit. Do NOT modify cmd/ (F2 owns it),
internal/controlplane/, internal/settings/ (F1 owns them), or
internal/reflex/ (consume it, don't edit it).

## Context (grep-verified at main — follow these patterns exactly)

- The optional-LLM pattern to copy: internal/reflex/jev.go —
  ReflexGate{endpoint, model, keys, client(2.5s timeout)},
  typed questions payload {"model", "state", "questions"},
  Answer{Type, Choice, Score, Confidence}, key pool
  (TYPESAFE_API_KEYS / TYPESAFE_API_KEY, rotation), and ABOVE ALL the
  fallback chain: every failure path returns the deterministic result
  with Source="deterministic", IsFallback=true — the router must NEVER
  error out because Jev is absent (absent Jev is the NORMAL case).
- Context enrichment: internal/context/broker.go ContextPacket
  (fail-open, 4096 cap).
- Blast radius: internal/analyzer/analyzer.go —
  `NewAnalyzer(root).AnalyzeFileImpact(file)` returns
  BlastRadiusReport{RiskLevel, SuggestedPaths, ...} (analyzer.go:20-30,
  38-97) — reusable function, NOT CLI-only.
- Providers manifest: internal/config/config.go
  ProviderEntry{Name, Class, Models []ModelEntry{ID, ContextWindow},
  Args...}, Load(path) validation; catalog defaults in
  internal/provider/catalog.go (agy default worker, model
  gemini-3.8-flash-high).
- Eval probes: internal/harness/probe/types.go Probe{ID, Category,
  Name, Input, ExpectedOutcome, Runner}, categories consts :13-20,
  RunSuite/PRI scoring, mock providers make it CI-runnable.

## Required implementation

1. **`internal/routing` package**:
   ```go
   type Decision struct {
       Provider, Model, Role string
       Source      string  // "deterministic" | "jev"
       IsFallback  bool
       Reason      string  // human-readable rule/suggestion basis
       Confidence  float64
   }
   type RouteRequest struct {
       Prompt      string
       Paths       []string // payload/target paths, may be empty
       Summary     string
       TimeoutHint string
       Manifest    *config.File // providers.json, may be nil → catalog defaults
   }
   func Route(ctx context.Context, req RouteRequest) (Decision, error)
   ```
   - **Layer 1 deterministic (always, zero network)**: rules over
     Paths/Prompt — docs-only (all paths .md/ under docs/) → role
     "summarizer" + cheapest manifest model; trust-boundary paths
     (internal/harness, internal/receipt, internal/controlplane) →
     largest-ContextWindow manifest model + role "test-runner";
     default → manifest default (agy / gemini-3.8-flash-high). Never
     invent a provider/model not present in Manifest/known catalog.
   - **Layer 2 Jev assist (optional, env-gated)**: active only when
     G8S_ROUTER_MODE=jev_assisted AND keys present (reuse
     reflex.ReflexGate via composition or a same-shaped client — do
     NOT edit internal/reflex). Typed question: suggest
     provider+model+role from the manifest with a Reason + Confidence;
     deterministic VALIDATION of the suggestion: unknown
     provider/model/role, or empty fields ⇒ discard suggestion, keep
     Layer 1 (IsFallback=true, Reason notes the rejected suggestion).
     Timeout ≤ 2.5s like reflex.
   - Every Decision carries Source/IsFallback/Reason — no silent mode
     switches.
2. **Benchmark probes (SCORECARD S-4 seed)**: new category
   `task_routing` in types.go; routing_suite.go defines ≥6 fixtures
   (docs-only, trust-path, empty-paths default, mixed paths, huge
   timeout hint, unknown-path fallback). Each probe's Runner: call
   Route twice — deterministic-only (no env) and jev_assisted against
   an httptest mock Jev (copy jev_test.go:33-64 server pattern) —
   assert: deterministic verdicts stable and manifest-legal; Jev
   verdict either manifest-legal+sourced "jev" OR falls back with
   IsFallback=true; PRI-style scoring via RunSuite works with the
   static mock provider.
3. **Fallback parity (RT-F)**: with no keys and no env, Route must
   return byte-identical Decisions to the deterministic layer and
   MUST NOT attempt any network call (assert via a client pointed at
   an unreachable endpoint would never be reached — or simply assert
   Source=deterministic and zero-alloc path; document the choice).

## Tests — router_test.go (red first)

- Table: each fixture → expected class of decision (role/model tier),
  Source=deterministic.
- Manifest-nil → catalog defaults still route.
- Jev mocked happy path: suggestion manifest-legal → Source="jev",
  IsFallback=false, Reason non-empty.
- Jev mocked hostile: unknown model / garbage JSON / 500 / timeout →
  Layer 1 decision, IsFallback=true, no error propagated.
- Env off → Jev layer never constructed (no client, no call).
- Probes pass as unit tests (go test mode) — the whole suite green
  without network.

## Constraints

- stdlib + existing deps; no new go.mod entries; no schema/settings
  changes (env G8S_ROUTER_MODE only in this wave).
- `go test ./internal/routing/ ./internal/harness/probe/` green;
  `go build ./...` green.
