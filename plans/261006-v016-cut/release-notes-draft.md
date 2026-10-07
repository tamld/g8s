# G5 release-notes draft (final content shipped in CHANGELOG ## [0.16.0] — 2026-10-07)

## [0.16.0] - 2026-10-07

### Added

- Effort manifest + agent-models.v1 catalog loader (#556): `internal/config` loads per-model effort metadata (style, supported_efforts, default_effort, mandatory) from `.g8s/agent-models.yml` — provider-neutral, fail-open on a missing catalog.
- Effort dispatch adapter + `submit --effort` knob (#557): requested→applied translation per catalog style (named pass-through / nearest-supported with tie→lower / toggle / budget / baked-name), typed hard-refuse when a mandatory provider is asked for `none`, parse-time validation (default medium).
- Effort-class registry + sibling classes (#558/#562): `.g8s/effort-classes.yml` maps task write-scope paths to class effort defaults — first match by priority wins, unregistered fails open to medium (registry glob semantics for nested paths are reworked in v0.17, #570).
- Token telemetry + submit wiring (#558/#559): agy usage capture and payload fields `effort_requested` / `effort_applied` / `effort_class` / `effort_budget_tokens` / `effort_mismatch` / `effort_override` for per-class cost aggregation.
- Signal-based effort resolution, pass-3 (#564/#565): declared signals (blast radius, LOC estimate) demote path-class defaults — matrix v1.1 rule: blast=low declares low at any LOC.
- Quality ladder machinery + CLI (#561): rung lineage with `g8s ladder status|advance|gauges`, policy thresholds, HITL evidence packets (`--out`).
- Ladder diagnosis dispatch + ground-truth fixes (#563): diagnosis rungs generate their own prompts (never synthesized), re-do rungs require a prompt, `Timeout` carries the payload timeout instead of a request hash.
- User documentation: `docs/user-guide/effort.md` (knob, signals, baked-name ownership/realignment, telemetry, ladder) + cli-reference rows (d30605c).

### Fixed

- Baked-name realignment fires end-to-end (#568): a four-link shadowing chain (family-vs-variant catalog ids, style-less manifest entries shadowing the catalog, the manifest's 3.7 entry shadowing the 3.8 default, and provider-less submits refusing the baked question) left mismatched `(model, effort)` combos reaching the worker CLI — `invalid model selection` — and every class below `high` was undeliverable on the default model. The catalog declares the three observed agy variants; the manifest can no longer shadow the catalog; provider-less submits decide from the model id. Verified live: docs-scope dispatch realigned flash-high→flash-low at low effort and completed (task 5c44b300).
- Ladder integrity + gauges hardening (red-cell findings, #570): EvaluateNextRung refuses double-advance and non-continuous histories; evidence packets validate required fields; evidence `WriteToFile` refuses path escapes; gauges reject backwards token counters, dedup HITL events per root task, and never leak unfiltered events into filtered totals.
- Patrol residue (#570/G2): VERSIONING.md cadence + milestone rows current at v0.15.0; cosign sample and CLI banner samples updated (ef4b103).

### Measured (self-measuring release, #548 standard — every number cites its task)

Live v0.16 gate wave on the default provider (gemini-3.8-flash family, token totals from final worker usage events):

| dispatch | recorded class / effort | total tokens | thinking tokens | task |
|---|---|---|---|---|
| red-cell R1 | unregistered / high (override) | 376,645 | 53,759 | 53be18bf |
| red-cell R2 | unregistered / high (override) | 396,269 | 52,304 | 95e52eef |
| red-cell R3 | unregistered / high (override) | 492,478 | 45,244 | 885a9abd |
| red-cell R4 | unregistered / high (override) | 946,509 | 64,259 | 057bde25 |
| residue G2 | unregistered / low (signal-demoted, realigned flash-low) | 79,127 | 0 | 5c44b300 |
| claims G3 | unregistered / low (signal-demoted, realigned flash-low) | 304,182 | 0 | dfe71e88 |

low-tier dispatches ran zero thinking tokens (2/2) at 4–12x lower total cost than the high tier. The full evidence row and its caveats live on #570.

<!-- Keep the [Unreleased] heading above. -->
