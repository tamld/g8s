# Task: #480 items 1-2 — govulncheck step + native fuzzing for the four external-input parsers

Repo: g8s @ main. Tracking: tamld/g8s#480.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- .github/workflows/quality.yml (add govulncheck step + fuzz step)
- internal/config/fuzz_test.go (new)
- internal/receipt/fuzz_test.go (new — package path: verify with
  `ls internal/receipt/`; place where the envelope parser lives)
- internal/worker/result_fuzz_test.go (new)
- internal/dispatch/sanitize_fuzz_test.go (new)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
internal/controlplane/ and cmd/g8s/ in parallel.

## Context (grep-verified at main)

- Zero `func Fuzz` in the repo. quality.yml:9 claims "CVE in deps" and :93
  claims golangci-lint "bundles govulncheck" — neither is a real scan.
- The external-input parsers: providers.json load (`internal/config`
  config.Load), write-receipt envelope JSON (`internal/receipt`), worker
  result envelope parse (`internal/worker` taskRequest/workerResult
  decoding path), sanitizer transport (`internal/dispatch`
  SanitizeOutput).

## Required implementation

1. **govulncheck step** in quality.yml (ubuntu job, after tests):
   `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck
   ./...` — break the job on vulns that affect code (govulncheck's default
   exit code already does; do not add `|| true`). Keep it one step, no new
   job.
2. **Four fuzz tests** (native Go fuzzing, each with 3+ seeds drawn from
   the incident corpus where one exists):
   - FuzzConfigLoad (internal/config): fuzz `config.Load` over JSON bytes;
     seeds: minimal valid file, a two-class file, malformed JSON, wrong
     class value, missing required fields. Assertion: never panics;
     error/no-error matches the documented validation rules (spot-check
     one invariant: a `class: api_call` payload without base_url must
     error).
   - FuzzReceiptEnvelope (internal/receipt): fuzz the envelope JSON decode
     path over arbitrary bytes; seeds: a valid envelope, an escape-pair
     payload from the #434 class (escaped newline/quote soup), truncated
     JSON. Assertion: decode either succeeds with a struct whose required
     fields are consistent, or returns an error — never panics, never
     hangs (guard with the test's own deadline).
   - FuzzWorkerResultParse (internal/worker): fuzz the worker result /
     taskRequest decode path; seeds: the #443 echo-poisoning stream shape,
     fenced-JSON variants, empty input. Assertion: no panic; the
     sanitized/decoded output never re-reads as a refusal block for a
     clean final response (pin the #443 invariant).
   - FuzzSanitizeOutput (internal/dispatch): fuzz SanitizeOutput; seeds:
     escaped newline/quote pairs, nested JSONL, empty, very long. 
     Assertions: idempotence (sanitize(sanitize(x)) == sanitize(x)) for
     the public-literal cases and output never contains the raw poison
     literals from the #434 corpus after sanitization.
3. **Fuzz step in quality.yml**: a step running
   `go test -run '^$' -fuzz <each-target> -fuzztime 30s ./internal/config/
   ./internal/receipt/ ./internal/worker/ ./internal/dispatch/` (four
   short invocations or a loop; total ≤ 3 min). Note in a step comment:
   deeper fuzzing runs are manual/quarterly.

## Tests

- Each Fuzz test must pass its seed corpus in normal `go test` mode
  (seeds run as unit cases) — `go test ./internal/config/ ./internal/
  receipt/ ./internal/worker/ ./internal/dispatch/` green locally.
- quality.yml: YAML valid; steps mirror the file's existing style.

## Constraints

- stdlib only for the fuzz tests (native `testing/fuzz`... i.e.
  `testing.F`), no new dependencies.
- Do not modify production code — if a fuzz test exposes a REAL bug, do
  NOT fix it silently: record the failing input in the fuzz test as a
  skipped regression seed with a `// BUG(FoundAtRelease):` comment, note
  it in your report, and leave the fix to the supervisor.
