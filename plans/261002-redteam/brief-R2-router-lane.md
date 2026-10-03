# Task: Red-test R2 — verify the router and CI-lane detector under hostile inputs

Repo: g8s @ main. Tracking: ADR-0030/0031 hardening. Adversarial
verification: construct inputs designed to steer decisions away from
their documented contracts, record what actually happens. Findings are
reported, never fixed here.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/routing/redtest_router_test.go (new)
- tools/redtest_lane_detect_test.go (new)

Do NOT run git commit. Do NOT modify production code. Other workers
own controlplane/ and receipt/ in parallel.

## Guarantees to verify

1. **Jev suggestion cannot escape the manifest.** Feed the mocked Jev
   server (httptest, see jev_assist_test.go) suggestions that name:
   an unregistered provider, a registered provider with an
   UNREGISTERED model, a valid model with role "escalated"
   (non-existent role), empty provider with valid model, a provider
   whose name differs only by case/whitespace ("AGY", " agy"),
   extremely long strings (64KB provider/model), unicode look-alikes.
   Verify EVERY suggestion is either rejected to the deterministic
   fallback or resolved strictly within the manifest — never executed.
2. **Deterministic layer cannot be steered by prompt content.** Route
   requests whose Prompt contains path-like strings ("../../etc",
   "docs/../../../cmd/x.go"), absolute paths, NUL bytes, 1MB prompt,
   paths that exist in the repo vs paths that do not — verify the
   decision depends only on the DOCUMENTED rule inputs (payload
   class, manifest) and never leaks filesystem state through the
   prompt.
3. **Route() never touches the network when Jev is unconfigured.**
   Point the client at an unreachable endpoint (closed port) with
   G8S_ROUTER_MODE unset — assert zero errors, zero latency anomaly,
   Source=deterministic. Then set the env but remove keys — same.
4. **CI lane detector resists path tricks** (tools/ci_lane_detect.sh
   against fixture repos): a file literally named `evil.md.go`;
   `docs` as a SYMLINK to a directory containing .go files; a file
   named `README.md` that is a 100MB blob; paths with spaces/newlines
   in the name; a .md file inside .github/workflows/; case variants
   (`Docs/x.go`, `DOCS/x.md`); an empty commit. Verify: any real code
   file (by extension or location) anywhere in the diff ⇒ lane=build,
   and the detector never crashes (exit 0 always).
5. **Pre-push fast path cannot skip code gates.** Construct a diff
   that is docs files ONLY in origin/main...HEAD but has STAGED code
   changes (the cached fallback path) — verify the detector counts
   staged code and resolves build lane (not docs).

## Constraints

- stdlib testing; hermetic fixtures (t.TempDir git repos for the
  detector — see ci_lane_detect_test.sh's fixture style; keep bash
  3.2 compatible if you extend it, but prefer a Go test shelling out).
- Mark any guarantee failure `t.Skip` + `// BUG(REDTEST):` + repro;
  list failures FIRST in the report.
- Report: per-guarantee verdict + constructed inputs + (for #1) the
  full suggestion-rejection matrix.
