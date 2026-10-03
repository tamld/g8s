# Task: D3 — coverage lift: autopilot + provider + review (each >= 80% on LINUX)

Repo: g8s @ main. CI-Linux per-package coverage: autopilot 62.7,
provider 66.5, review 67.0. Target: each >= 80%.

## Verify in the EXACT CI environment (the session's hard lesson)
darwin numbers LIE about Linux. Measure and verify with:
`docker run --rm -v $G8S_REPO_ROOT:/repo -w /repo -e CGO_ENABLED=0 golang:1.26.0 sh -c "go test -count=1 -cover ./internal/autopilot/ ./internal/provider/ ./internal/review/ 2>&1 | tail -3"`
Iterate until the container reports >= 80 for all three.

## Delivery protocol
Scratch worktree. Write ONLY to: internal/autopilot/*_test.go,
internal/provider/*_test.go, internal/review/*_test.go (new or extend).
Do NOT touch production code. Do NOT run git commit.

## Method
1. Measure per-function: `go tool cover -func` (locally is fine for
   discovery, but the PASS condition is the container number).
2. For each function < 80%: table-driven stdlib tests, hermetic,
   matching each package's existing test style.
3. Real bug -> `// BUG(...)` skipped seed + report (do not fix).

## Done
- Container coverage: all three >= 80%; all existing tests green;
  report the per-package before/after + per-function table.
