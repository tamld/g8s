# Report A — Testing & Quality CI/CD World Practice Audit (2025-2026)

Subject: `g8s` (Go 1.26 CLI + MCP server, agent-infrastructure orchestrator) vs. leading-practice testing/quality CI.
Date: 2026-11-01. Team A (Testing & Quality). Method: ck:research, 5 web searches, claims cross-referenced across >=2 sources where possible.

---

## Executive Summary

1. **g8s is already at or above world-class baseline for test-tier structure** (per-lane CI, nightly battery, weekly CodeQL, bounded fuzz, -race, coverage ratchet, merge-guard). The 2025-2026 frontier is not more tiers — it is (a) **flake detection + quarantine ahead of the merge queue**, (b) **diff-coverage added to the existing ratchet**, and (c) **deterministic simulation of the queue/lease state machine**.
2. **Mutation testing on Go remains the weakest ecosystem spot**: zimmski/go-mutesting is unmaintained; maintained forks exist (avito-tech, buzyka — Apr 2025). No source confirms the Go 1.26 panic is fixed in any fork — keep it manual/quarterly until proven.
3. **AI/agent eval-in-CI has converged on a pattern g8s partially already has**: golden sets + pass-rate thresholds instead of exact-match assertions, version-controlled eval fixtures, security evals (Garak/PyRIT-class) shifted left into CI.
4. **Anti-pattern verdict**: coverage theater (raw %, especially Go's misleading multi-package totals), flaky required checks, retry-as-band-aid, unversioned evals. g8s's ratchet avoids most theater; its main exposure is unversioned red-test prompt fixtures.

---

## Methodology

- 5 web searches (2025-11 → 2026-11 recency window): (1) Go mutation testing maintenance status; (2) deterministic simulation testing (DST) adoption; (3) LLM evals in CI; (4) flaky tests + merge queues + test selection; (5) coverage gate strategies + Go sharding/gotestsum.
- ~40 candidate sources triaged; every major claim below is backed by a URL; consensus/controversy flagged inline. Limitation: no direct access to g8s CI logs/workflow files in this run — recommendations map from the stated context only.

---

## Key Findings

### 1. The 2025-2026 world-class CI test workflow: tiers by blast radius, not by ambition

- **Consensus tiering**: PR = fast unit + lint + build (minutes); integration/E2E = sharded, gated; nightly = full battery incl. soak; weekly/quarterly = fuzz, mutation, mutation-adjacent deep checks. g8s's per-lane + nightly + quarterly-mutation structure matches this exactly.
- **gotestsum is the de-facto `go test` replacement in CI** (JUnit output, package coverage aggregation); HashiCorp uses it in production workflows ([go-version workflow](https://github.com/hashicorp/go-version/blob/main/.github/workflows/go-tests.yml), [Stack Overflow on aggregate Go coverage](https://stackoverflow.com/questions/71492978/how-to-get-an-overall-coverage-value-from-go-test-cover)).
- **Deterministic sharding of Go packages across CI jobs** is the accepted scale-out pattern; [unkeyed's shard-test](https://pkg.go.dev/github.com/unkeyed/unkey/go/scripts/shard-test) deterministically assigns packages to shards to avoid imbalance flakes. Merge per-shard coverprofiles with `go tool covdata textfmt -i=...` (Go 1.20+).
- **Test-selection/affected-only**: mainstream for monorepos; for a single-repo CLI like g8s the win is small — consensus is package-level sharding + path filters beats fine-grained dependency graphs at this repo size (inference from sharding sources above; no strong single citation).
- **Self-hosted vs hosted**: no consensus found in this run's sources; keep hosted unless nightly battery cost forces a move.

### 2. Flake detection and quarantine must run BEFORE the merge queue — this is the strongest consensus in the entire research

- One flaky test at 10%+ frequency seriously disrupts merge-queue throughput and stalls every PR behind it ([Aviator, Jan 2025](https://www.aviator.co/blog/impact-of-flaky-test-in-merge-queue), [BuildPulse](https://buildpulse.io/blog/merge-queue-flaky-tests-amplification)).
- 2025 practice is **automatic detection + quarantine** (exclude from required status, track for triage), not retry-masking ([Trunk.io, Mar 2025](https://trunk.io/blog/stop-flaky-tests-from-sabotaging-your-merge-queue), [minware guide](https://www.minware.com/guide/best-practices/flaky-test-quarantine), [mill-build, Jan 2025](https://mill-build.org/blog/4-flaky-tests.html), [Datadog flaky management](https://docs.datadoghq.com/tests/flaky_management)).
- **Controversy/nuance**: retries. Tooling vendors tolerate auto-retry; data-driven analysis of 378M test records argues retries hide root causes and the fix is root-cause repair ([ContextQA](https://contextqa.com/blog/flaky-tests-in-ci-cd-without-retries)). Mature position: quarantine first, retry only for known-infra-noise.
- Tooling landscape: Trunk Flaky Tests, BuildPulse, Mergify, Datadog CI Visibility ([Mergify comparison](https://mergify.com/compare/flaky-test-tools)). For OSS/single-maintainer scale, a homegrown nightly `-count=5` re-run + auto-issue is the pragmatic equivalent.

### 3. Coverage gates: the converged answer is a hybrid — diff coverage + ratchet + slow-moving floor

- **Diff coverage (new/changed lines) is considered the fairest PR gate** — holds contributors accountable only for code they touch ([Medium/TDS](https://medium.com/data-science/test-and-cover-your-code-today-e80c27d08dab)).
- **Ratchet (baseline, fail-on-decrease, auto-update upward)** is the standard anti-regression complement ([vue-stripe#510 proposal](https://github.com/vue-stripe/vue-stripe/issues/510), [flisk#1381 per-area ratchets](https://github.com/flisk-sh/flisk/issues/1381)).
- **Raw overall % is misleading, especially in Go**: `go test -cover` totals mislead when only some packages have tests ([golang/go#76789](https://github.com/golang/go/issues/76789)); 80-90% is "strong" but 100% statement coverage guarantees nothing about logic ([LaunchDarkly](https://launchdarkly.com/blog/code-coverage-what-it-is-and-why-it-matters), [Qt](https://www.qt.io/software-insights/is-70-80-90-or-100-code-coverage-good-enough)).
- Concrete tool: [vladopajic/go-test-coverage](https://github.com/vladopajic/go-test-coverage) implements thresholds + diff-vs-baseline-branch in one Go-native gate.

### 4. Mutation testing on Go in 2025-2026: ecosystem fragmented, no clear winner, cadence stays off-PR

- zimmski/go-mutesting (the classic) is **largely unmaintained**; active forks: [avito-tech/go-mutesting](https://context7.com/avito-tech/go-mutesting) and [buzyka/go-mutesting](https://pkg.go.dev/github.com/buzyka/go-mutesting) (pkg.go.dev shows Apr 30 2025 update; adds mutation-ignore files via MD5).
- **Gremlins** remains the friendliest CLI; the original k3rn31 repo is inactive and maintenance continues under the [go-gremlins org](https://github.com/go-gremlins) ([gremlins.dev](https://gremlins.dev), [original announcement thread](https://www.reddit.com/r/golang/comments/vxv0uw/github_k3rn31gremlins_a_mutation_testing_tool_for)).
- CI-integration feature comparison (caching, sharding, exit codes, editor support, Claude Code plugin): [Gooze comparison page](https://gooze.dev/docs/comparison). Note Gooze is a commercial vendor — treat its self-serving framing accordingly.
- **Open/unverified**: no source in this run confirms the Go 1.26 panic in go-mutesting was fixed, nor that any fork is Go 1.26-clean. This must be tested directly against the forks before automating. Consensus regardless: mutation testing is nightly/weekly/quarterly and NEVER a required PR check (runtime cost).

### 5. DST (deterministic simulation testing) went mainstream in 2025 — but for g8s the adoptable form is the *pattern*, not the tooling

- DST = run the whole system in a simulated, deterministic environment so every bug reproduces exactly ([Antithesis docs](https://antithesis.com/docs/resources/deterministic_simulation_testing)). FoundationDB simulates an entire cluster single-threaded in-process ([pierrezemb.fr, Apr 2025](https://pierrezemb.fr/posts/learn-about-dst)); TigerBeetle and WarpStream (full-SaaS simulation) follow the same school ([WarpStream blog](https://www.warpstream.com/blog/deterministic-simulation-testing-for-our-entire-saas), [awesome-deterministic-simulation-testing list](https://github.com/ivanyu/awesome-deterministic-simulation-testing)).
- 2025 signals: [Amplify Partners DST primer](https://www.amplifypartners.com/blog-posts/a-dst-primer-for-unit-test-maxxers) frames DST as "for the masses"; SE Radio #685 with Antithesis CEO ([se-radio.net](https://se-radio.net/2025/09/se-radio-685-will-wilson-on-deterministic-simulation-testing)); Antithesis explicitly argues AI-generated code raises the stakes for reproducible fault injection ([Rootly interview](https://rootly.com/humans-of-reliability/will-wilson-antithesis)).
- **Reality check**: Antithesis is commercial, priced for database-scale systems. The adoptable pattern for a Go CLI orchestrator: seed-driven simulation of the queue/lease/receipt/CAS state machine with injected faults (time jumps, process kills, WAL truncation), asserting invariants — implementable with stdlib + `testing/quick` + seeded `math/rand`. Soak/chaos for SQLite WAL: nightly, long-duration, fault-injected.

### 6. AI/agent eval-in-CI (2025-2026): golden sets + pass-rate thresholds, version-controlled evals, security evals shifted left

- Converged pattern: score changes against golden examples before shipping; regressions surface as **pass-rate deltas, not exact-match failures** ([G2 Promptfoo blueprint](https://ai.g2.com/blueprint/whvsNw2CwqPANOt), [Softech Infra case study — 200 prompt changes/week](https://www.softechinfra.com/blog/promptfoo-prompt-eval-pipeline-200-changes-week-talkdrill)).
- Evals as CI quality gates are becoming the norm for agent code ([nhimg](https://nhimg.org/community/ai-beyond-identity/ai-evals-in-ci-cd-pipelines-are-your-quality-gates-enough)); eval fixtures/models/config must be **version-controlled** or the gate silently rots ([nhimg FAQ](https://nhimg.org/faq/what-breaks-when-llm-evaluation-is-not-version-controlled-across-models-data-and)).
- Security evals in CI: Promptfoo, Garak, PyRIT, DeepTeam wired into pipelines for prompt-injection/jailbreak regression ([BeyondScale](https://beyondscale.tech/blog/llm-security-testing-cicd-pipeline)) — directly relevant to g8s's red-test suites (prompt injection, telemetry poisoning, model substitution, rogue MCP tools).
- **Controversy/unverified**: reports that Promptfoo was acquired by OpenAI while staying open source ([agent-eval README](https://github.com/RudrenduPaul/agent-eval)) — verify license/governance before betting the stack on it. Statistical (not boolean) agent regression testing is an emerging alternative ([agent-eval](https://github.com/RudrenduPaul/agent-eval)).

### 7. Anti-patterns (quality theater) called out by 2025-2026 sources

- **Coverage theater**: chasing a global % number (esp. Go's misleading aggregate totals) instead of behavior ([golang/go#76789](https://github.com/golang/go/issues/76789), [Qt](https://www.qt.io/software-insights/is-70-80-90-or-100-code-coverage-good-enough)).
- **Flaky required checks**: a required check with nonzero flake rate converts the merge queue into a random walk ([Aviator](https://www.aviator.co/blog/impact-of-flaky-test-in-merge-queue), [Trunk](https://trunk.io/blog/stop-flaky-tests-from-sabotaging-your-merge-queue)).
- **Retry-as-band-aid**: green-by-retry masks real defects; root-cause flaky tests instead ([ContextQA](https://contextqa.com/blog/flaky-tests-in-ci-cd-without-retries)).
- **Unversioned evals**: eval gates that reference mutable models/datasets/config drift into theater ([nhimg FAQ](https://nhimg.org/faq/what-breaks-when-llm-evaluation-is-not-version-controlled-across-models-data-and)).
- **Required mutation/fuzz gates on PR**: runtime cost makes them noise generators; keep bounded fuzz on PR only for parsers, full fuzz nightly (g8s already does this correctly).

---

## Comparative Table

| Practice | Who does it (2025-26) | Maturity | g8s status |
|---|---|---|---|
| Tiered PR/nightly/weekly battery | HashiCorp, WarpStream, FDB-lineage shops | Mature | Already have |
| gotestsum + JUnit + sharded Go packages | HashiCorp go-version, unkeyed | Mature | Missing |
| Flake detection + auto-quarantine pre-queue | Trunk, BuildPulse, Mergify, Datadog customers | Mature (tooling), young (OSS adoption) | Missing |
| Merge queue with clean required-checks | GitHub merge queue / Aviator / Mergify users | Mature | Have doctrine, hygiene TBD |
| Diff-coverage gate + ratchet hybrid | vladopajic/go-test-coverage adopters; TDS-style shops | Converging | Ratchet only |
| Mutation testing (Go) | avito-tech/buzyka fork users, go-gremlins | Immature/fragmented | Quarterly manual (correct given Go 1.26 panics) |
| Bounded fuzz on parsers, full nightly | Go security team guidance; OSS norm | Mature | Already have (4 parsers) |
| DST (in-process, seeded, fault-injected) | FDB, TigerBeetle, WarpStream, Antithesis customers | Mainstream for DBs; pattern-portable | Missing (testing/quick is a seed) |
| Golden-set eval gates w/ pass-rate thresholds | Promptfoo users (Softech: 200 chg/wk) | Emerging→converging | Partial (red-tests exist; pass-rate scoring TBD) |
| Security evals in CI (Garak/PyRIT-class) | AppSec teams shifting left (BeyondScale) | Emerging | Red-test suites are the in-house analog |

---

## Recommendations for g8s (prioritized)

### P1 — do now (low cost, high signal)

1. **Adopt `gotestsum` + JUnit output in CI; add deterministic package sharding for the nightly battery** (pattern: [unkeyed shard-test](https://pkg.go.dev/github.com/unkeyed/unkey/go/scripts/shard-test); [HashiCorp example](https://github.com/hashicorp/go-version/blob/main/.github/workflows/go-tests.yml)). Cost: ~0.5 day. Enables per-shard timings and JUnit-based flake analytics.
2. **Build a homegrown flake quarantine loop**: nightly re-run of any failed test with `-count=5`; >=1 non-deterministic failure auto-files a `flake/` issue and moves the test to a non-required quarantine lane with a TTL (re-promote when green N consecutive nights). Cost: ~1 day. Rationale: [Aviator](https://www.aviator.co/blog/impact-of-flaky-test-in-merge-queue), [Trunk](https://trunk.io/blog/stop-flaky-tests-from-sabotaging-your-merge-queue). Do NOT add blanket retries ([ContextQA](https://contextqa.com/blog/flaky-tests-in-ci-cd-without-retries)).
3. **Add diff-coverage vs base branch to the existing threshold+ratchet gate** (e.g., [vladopajic/go-test-coverage](https://github.com/vladopajic/go-test-coverage) supports both). Gate changed lines at ~80%, keep ratchet as regression floor, ignore global-% cosmetics. Cost: ~0.5 day.

### P2 — next quarter (medium cost)

4. **Port DST to the orchestrator core**: seeded-deterministic simulation harness for queue claim / CAS lease / receipt issuance / WAL behavior, with injected faults (clock jumps, kill -9, partial writes) and invariant assertions; run nightly. Pattern per [Antithesis](https://antithesis.com/docs/resources/deterministic_simulation_testing) / [FDB simulation](https://pierrezemb.fr/posts/learn-about-dst); build with stdlib `testing/quick` + seeded RNG. Cost: ~3-5 days for a minimal invariant harness.
5. **Re-evaluate mutation tooling**: trial [avito-tech/go-mutesting](https://context7.com/avito-tech/go-mutesting) and the [go-gremlins](https://github.com/go-gremlins) fork **against Go 1.26 specifically** (panic check is the acceptance test); if clean, automate quarterly with scoped packages (parsers + state machine) instead of manual runs. Cost: ~1 day trial + ongoing quarterly compute.
6. **Upgrade red-test suites to pass-rate-scored golden evals**: version-control all prompt/eval fixtures; score on pass-rate deltas with a threshold (no exact-match), per [G2 blueprint](https://ai.g2.com/blueprint/whvsNw2CwqPANOt) / [Softech case study](https://www.softechinfra.com/blog/promptfoo-prompt-eval-pipeline-200-changes-week-talkdrill). Decide on Promptfoo (verify the [OpenAI acquisition claim](https://github.com/RudrenduPaul/agent-eval)) vs. extending the in-house Go harness. Cost: ~2-3 days.

### P3 — opportunistic (high cost or exploratory)

7. **Soak testing for the worker queue**: nightly multi-hour run with continuous submit/claim/kill cycles + SQLite WAL stress; assert no lease leak/receipt drift. Cost: ~2 days harness + nightly runner hours.
8. **Chaos/fault-injection lane**: replicate kill-process-group and worktree-corruption scenarios the crash-survival E2E already covers, but randomized nightly. Cost: ~1-2 days on top of #4.
9. **Commercial DST (Antithesis) / hosted flake analytics (Trunk, Datadog)**: not recommended at current scale — priced for database vendors and monorepo fleets; revisit only if g8s grows heavy multi-tenant usage.

---

## Open Questions

1. Does any maintained go-mutesting fork (avito-tech, buzyka) actually run clean on Go 1.26? No published evidence found; needs a 1-day spike.
2. Promptfoo governance post-acquisition (license, data flow) — verify before adopting as g8s's eval runner.
3. What is g8s's current required-check flake rate? (Needed to size P1.2; measure via nightly re-run before building quarantine tooling.)
4. Is GitHub merge queue worth enabling for a low-PR-velocity single-maintainer repo, or does the merge-guard rollup doctrine already capture the benefit at zero queue-stall risk?
5. DST scope: which invariants are load-bearing enough to justify the 3-5 day harness (lease single-winner? receipt path containment? both?).

---

## Sources

- [Aviator — Impact of Flaky Tests in Merge Queue (Jan 2025)](https://www.aviator.co/blog/impact-of-flaky-test-in-merge-queue)
- [Trunk.io — Stop flaky tests from sabotaging your merge queue (Mar 2025)](https://trunk.io/blog/stop-flaky-tests-from-sabotaging-your-merge-queue)
- [BuildPulse — Merge Queues and Flaky Tests](https://buildpulse.io/blog/merge-queue-flaky-tests-amplification)
- [Mergify — Flaky Test Tools Compared](https://mergify.com/compare/flaky-test-tools)
- [minware — Flaky Test Quarantine](https://www.minware.com/guide/best-practices/flaky-test-quarantine)
- [mill-build — Flaky Tests (Jan 2025)](https://mill-build.org/blog/4-flaky-tests.html)
- [Datadog — Flaky Tests Management](https://docs.datadoghq.com/tests/flaky_management)
- [ContextQA — Flaky Tests: Fix Without Retries](https://contextqa.com/blog/flaky-tests-in-ci-cd-without-retries)
- [Medium/TDS — Test and Cover Your Code Today](https://medium.com/data-science/test-and-cover-your-code-today-e80c27d08dab)
- [golang/go#76789 — misleading go test -cover totals](https://github.com/golang/go/issues/76789)
- [vladopajic/go-test-coverage](https://github.com/vladopajic/go-test-coverage)
- [LaunchDarkly — Code Coverage](https://launchdarkly.com/blog/code-coverage-what-it-is-and-why-it-matters) / [Qt — Is 70/80/90/100% good enough](https://www.qt.io/software-insights/is-70-80-90-or-100-code-coverage-good-enough)
- [vue-stripe#510 — ratchet autoUpdate proposal](https://github.com/vue-stripe/vue-stripe/issues/510) / [flisk#1381 — per-area ratchets](https://github.com/flisk-sh/flisk/issues/1381)
- [HashiCorp go-version test workflow (gotestsum)](https://github.com/hashicorp/go-version/blob/main/.github/workflows/go-tests.yml) / [SO — aggregate Go coverage](https://stackoverflow.com/questions/71492978/how-to-get-an-overall-coverage-value-from-go-test-cover)
- [unkeyed shard-test](https://pkg.go.dev/github.com/unkeyed/unkey/go/scripts/shard-test)
- [Gremlins](https://gremlins.dev) / [go-gremlins org](https://github.com/go-gremlins) / [buzyka/go-mutesting (Apr 2025)](https://pkg.go.dev/github.com/buzyka/go-mutesting) / [avito-tech/go-mutesting](https://context7.com/avito-tech/go-mutesting) / [Gooze comparison](https://gooze.dev/docs/comparison) / [original Gremlins thread](https://www.reddit.com/r/golang/comments/vxv0uw/github_k3rn31gremlins_a_mutation_testing_tool_for)
- [Antithesis — DST docs](https://antithesis.com/docs/resources/deterministic_simulation_testing) / [Amplify DST primer (Nov 2025)](https://www.amplifypartners.com/blog-posts/a-dst-primer-for-unit-test-maxxers) / [pierrezemb — Learn about DST (Apr 2025)](https://pierrezemb.fr/posts/learn-about-dst) / [WarpStream full-SaaS DST](https://www.warpstream.com/blog/deterministic-simulation-testing-for-our-entire-saas) / [SE Radio #685 (Sep 2025)](https://se-radio.net/2025/09/se-radio-685-will-wilson-on-deterministic-simulation-testing) / [Rootly — Will Wilson interview](https://rootly.com/humans-of-reliability/will-wilson-antithesis) / [awesome-DST list](https://github.com/ivanyu/awesome-deterministic-simulation-testing)
- [G2 — Promptfoo LLM Evaluation Pipelines](https://ai.g2.com/blueprint/whvsNw2CwqPANOt) / [Softech Infra — Prompt eval pipeline case study](https://www.softechinfra.com/blog/promptfoo-prompt-eval-pipeline-200-changes-week-talkdrill) / [BeyondScale — LLM security testing in CI/CD](https://beyondscale.tech/blog/llm-security-testing-cicd-pipeline) / [nhimg — AI evals as quality gates](https://nhimg.org/community/ai-beyond-identity/ai-evals-in-ci-cd-pipelines-are-your-quality-gates-enough) / [nhimg — unversioned evals FAQ](https://nhimg.org/faq/what-breaks-when-llm-evaluation-is-not-version-controlled-across-models-data-and) / [agent-eval (statistical agent regression)](https://github.com/RudrenduPaul/agent-eval)
