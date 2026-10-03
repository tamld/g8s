# Task: FIX-B — strictness hardening from the red cell: verifier, merger, lane detector (issue #517 D-01 tail)

Repo: g8s @ main. The release red cell (PR #536) proved four strictness
gaps. Fix the CODE so each BUG(REDTEST) skip in
internal/verifier/redtest_verifier_test.go, tools/redtest_merger_test.go,
and tools/redtest_lane_fresh_test.go becomes a live passing assertion
(flip skips into real checks; keep the finding comments as rationale).
Do not weaken the tests.

## Findings to close

1. **Citation anchoring (internal/verifier).** `status: hard` accepted
   multi-citation strings ("#123 #456"). Fix isValidCitation: after
   trimming, the string must be exactly one citation — `^#\d+$` or
   `^incident:[a-z0-9][a-z0-9-]*$` — optionally followed by ONE space
   and free-text rationale (so "#208 (docs drifted…)" still loads).
   Two citation tokens, leading/trailing junk, or "#" alone ⇒ refuse
   (the class degrades to unregistered — fail-closed floor unchanged).
2. **Self-grade normalization (internal/verifier).** The
   caller==target refusal is bypassed by case/whitespace variants.
   Fix: normalize both IDs (strings.TrimSpace + strings.EqualFold)
   before comparison — in the package AND the CLI `--as-task` path.
3. **Merger autonomy trim (tools/merger.sh).** The autonomy gate
   accepted "1 " (trailing space) as a DIFFERENT value than the exact
   match it requires — trace which comparison let it through and make
   the read strict: trim the config value, then compare exactly.
   ("01", "true", "one" must still refuse — the paired cases in
   redtest_merger_test.go pin that; keep them passing.)
4. **Lane detector rename suppression (tools/ci_lane_detect.sh).** A
   code→docs RENAME (internal/x.go → docs/y.md) shows only the
   destination when git rename tracking is on — the D path (code!)
   disappears and the diff classifies docs. Fix: disable rename
   detection on the detector's diff invocation (--no-renames, or
   diff.* config neutralization in the fixture-realistic path) so the
   deleted/renamed code path is visible and counts as code. The
   existing suite (redtest_lane_detect_test.go + the R2-era cases) must
   stay green.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/verifier/verifier.go + cmd/g8s/verify.go (normalization only)
- internal/verifier/redtest_verifier_test.go (flips)
- tools/merger.sh + tools/redtest_merger_test.go (flips)
- tools/ci_lane_detect.sh + tools/redtest_lane_fresh_test.go (flips)

Do NOT run git commit. Do NOT touch internal/controlplane, cmd/g8s/resubmit.go,
internal/autopilot (another worker owns them this wave).

## Constraints

- `go test ./internal/verifier/ ./cmd/g8s/ ./tools/` green, `go build
  ./...` green, gofumpt + golangci-lint clean, and
  `bash tools/ci_lane_detect_test.sh` green.
- Report: per-finding fix (file:line), the accepted citation grammar,
  and confirmation that every previously-skipped redtest in your scope
  now asserts live.
