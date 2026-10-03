# Task: M3 benchmark runner — real-Jev vs deterministic routing (issue #518, SCORECARD S-4)

Repo: g8s @ main. The eval probe suite (routing_suite.go) benchmarks Jev
against an in-process MOCK. Issue #518 needs the SAME measurement against
the REAL TYPESAFE endpoint — so the delta claim (ADR-0030: "Jev earns its
distributor seat by measured delta") gets real numbers. You build the
RUNNER; the supervisor executes it with the keys (keys never enter the
repo, the worker env, or any committed artifact).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/harness/probe/m3_real_jev_test.go (new, env-guarded test)

Do NOT run git commit. Do NOT touch routing_suite.go, routing/, cmd/,
docs/. Never print or embed any key/secret — read them from env only.

## Required implementation

1. **Env guard**: the test runs ONLY when `G8S_M3_REAL_JEV=1` AND both
   `TYPESAFE_ENDPOINT` and (`TYPESAFE_API_KEYS` or `TYPESAFE_API_KEY`)
   are set — otherwise `t.Skip` stating exactly which variable is
   missing (hermetic by default; the CI battery must never hit the
   network through this file).
2. **Fixtures** (RouteRequest{Prompt, Paths, Summary} each):
   a. **Synthetic** (from RoutingProbes' shapes — redeclare compactly,
      do not refactor the suite): docs-only paths, trust-boundary paths
      (internal/receipt/*), test-file paths, mixed docs+code, empty
      paths + prose summary, unknown extensions, hotfix-marker summary,
      refactor-declared summary. Expected role: set ONLY where the
      deterministic rule is unambiguous (read internal/routing/router.go's
      rule table first); ambiguous = empty expectedRole (record-only).
   b. **Real campaign corpus** (the Wave G/H receipt path shapes, verbatim):
      - ["docs/user-guide/verifier-and-autonomy.md", "README.md"] (docs round)
      - ["internal/autopilot/redtest_retry_test.go", "cmd/g8s/redtest_resubmit_test.go"] (test slice)
      - ["internal/controlplane/*", "cmd/g8s/resubmit.go", "internal/autopilot/redtest_retry_test.go", "cmd/g8s/redtest_resubmit_test.go"] (fix slice)
      - ["internal/harness/*"] (harness slice)
      - ["internal/settings/settings.go", "internal/settings/settings_test.go", "tools/merger.sh", "tools/merger_test.sh"] (W+C slice)
      - ["internal/verifier/verifier.go", "cmd/g8s/verify.go", "internal/verifier/redtest_verifier_test.go", "tools/merger.sh", "tools/ci_lane_detect.sh"] (multi-slice)
      Prompts: one-line realistic dispatch summaries per shape (write
      them as the supervisor's briefs would — "Read the brief at …
      execute exactly…" style), expectedRole empty (record-only).
3. **Measurement per fixture** (both passes use the SAME manifest —
   routing.DefaultManifest()):
   - Deterministic pass: routing.Route(ctx, req) — record
     {source, provider, model, role, latency_ms, matches_expected_role}.
   - Real-Jev pass: routing.Route(routing.WithJevAssist(ctx, endpoint,
     key), req) with a 10s context timeout — record the same fields PLUS
     {jev_accepted (source=="jev"), is_fallback, placement_differs
     (provider or model differs from the deterministic pass)}.
   - On Jev transport error: record {jev_error: <err string>} and count
     the fixture as fallback (deterministic decision stands) — the
     benchmark measures the REAL system including its degraded mode; a
     transport error is a data point, not a test failure.
4. **Summary JSON** printed via t.Logf (one compact object):
   {fixtures, deterministic_mean_ms, real_jev_mean_ms, jev_acceptance_rate,
   fallback_rate, placement_differs_rate, wrong_role_rate (jev-accepted
   decisions whose role mismatches an expectedRole), errors: N}.
   Round latencies to whole ms. NO keys, NO raw prompts longer than 60
   chars in any output.
5. **Total runtime bound**: the whole benchmark must finish under 3
   minutes (10s per-Jev-call cap × fixtures is the ceiling; fixtures are
   ≤ 20).

## Constraints

- stdlib + existing internal packages; `go test ./internal/harness/probe/`
  green WITHOUT the env guard variables (the skip path); `go build ./...`
  green; gofumpt + golangci-lint clean; ci_doc_contract_check green.
- Report: the fixture list you implemented, the JSON summary field
  definitions in your own words, and anything you had to interpret.
