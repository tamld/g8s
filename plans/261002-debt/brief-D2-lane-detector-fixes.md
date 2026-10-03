# Task: Wave D2' — lane detector hardening (F1-F3) + Test-matrix guard + main-push detection

Repo: g8s @ main. Inputs: issue #510 findings F1-F3 (the
BUG(REDTEST) seeds in tools/redtest_lane_detect_test.go are the
specs — un-skip each as it is fixed and flip to a real assertion),
ADR-0031 §1 deny-by-default, and the S-10 recorded edges.

## Delivery protocol

Scratch worktree. Write ONLY to:

- tools/ci_lane_detect.sh
- tools/ci_lane_detect_test.sh (un-skip + extend the red seeds)
- tools/redtest_lane_detect_test.go (un-skip the fixed seeds + flip
  to assertions)
- the workflow file(s) owning `Test macos-latest` / `Test windows-latest`

Do NOT run git commit. Do NOT touch other packages.

## Required

1. **F1 — symlink resolution**: changed paths that are symlinks must
   resolve to their FINAL target before classification (a `docs`
   symlink → dir with .go files ⇒ build). POSIX: use `readlink -f`
   guarded for availability; non-available ⇒ classify build
   (deny-by-default). Un-skip the red seed.
2. **F2 — extension requirement**: a path under docs/ is prose ONLY
   if its extension is prose (.md, .markdown, .txt, .adoc); any other
   extension under docs/ ⇒ build (`docs/evil.go` must be build).
   Un-skip the red seed.
3. **F3 — .github area**: anything under `.github/**` ⇒ build lane
   regardless of extension (workflow-adjacent). Un-skip the red seed.
4. **Test-matrix guard**: the workflow owning `Test macos-latest` /
   `Test windows-latest` gets the Detect CI Lane step (same pattern
   as quality.yml); docs lane skips go test steps with the explicit
   not-applicable notice; job names unchanged.
5. **Main-push detection**: extend ci_lane_detect.sh with an optional
   base-SHA argument; the push-event caller passes
   `github.event.before` when it is a real SHA (not all-zeros);
   empty before-range ⇒ build (deny-by-default preserved). Add
   fixtures.
6. Keep: detector exits 0 always; bash 3.2 compatible; job names
   exact; every docs-lane skip prints its not-applicable notice.

## Definition of done

- All detector tests green INCLUDING the previously-skipped red seeds
  (now asserting the fixed behavior).
- YAML parses on every touched workflow.
- Report: per-finding fix description + fixture matrix + the honest
  risk you still see.
