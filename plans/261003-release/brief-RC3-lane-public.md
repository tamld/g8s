# Task: Red-Cell RC3 — lane detector fresh construction + Gate 7 public-surface pass

Repo: g8s @ main. Release red-cell (#517, D-01 HARD): (a) the CI lane
detector post-#512 (F1-F3 fixes: symlink, extension, .github bypasses)
verified under FRESH constructions nobody has tried; (b) the Gate 7
public-surface pass (README, README.vi, offer bundle, CLI help —
release-note claims vs reality). Findings reported, never fixed here.

## Delivery protocol

Scratch worktree. Write ONLY to:

- tools/redtest_lane_fresh_test.go (new Go test shelling out to
  tools/ci_lane_detect.sh against fixture repos, hermetic t.TempDir)

Do NOT run git commit. Do NOT modify production code or
redtest_lane_detect_test.go (read it first; construct DIFFERENT vectors).

## Guarantees to verify

1. **Lane detector fresh constructions** (every case: exit 0, and any
   real code file ⇒ lane=build): a `.md` file whose CONTENT is a
   shell script (content must not matter); `docs.tar.gz` (double
   extension); a directory named `README.md/` (with children);
   `internal/x.go` renamed mid-diff (D path in git diff); a diff with
   ONLY deleted code files; a 0-byte `.go` file; `offer/x.md` +
   `main.go` in one commit; `assets/logo.SVG` vs `assets/logo.svg`
   (case); `.github/workflows/x.yml` + nothing else; a `plans/x.md`
   that is actually a git SYMLINK to `../../evil.go`.
2. **F1-F3 regression confirmation** — run the EXISTING
   tools/ci_lane_detect_test.sh + redtest_lane_detect_test.go first
   and CONFIRM they still pass (record output); then verify each F1-F3
   class with a NEW example (symlink dir → different target shape;
   extension trick → different double-extension; .github → different
   workflow filename).
3. **Gate 7 public-surface pass (report-only, no file writes for
   this part):** read README.md, README.vi.md, offer/**, and
   `bin/g8s <cmd> --help` output for the shipped commands (verify,
   resubmit, watch, deliver, lane-related) — cross-check every
   quantitative claim and every flag/example against the actual code
   on main. List: claims with no backing (→ claims.yml candidates),
   flags documented but absent, flags present but undocumented,
   version strings still saying 0.13.0 (expected pre-bump — list,
   don't fail). Verify the offer bundle files are present and
   non-empty and the pull-path instructions match the real repo layout.

## Constraints

- stdlib; hermetic fixture repos; bash 3.2 if you shell out via
  scripts; no network.
- Mark any guarantee failure `t.Skip` + `// BUG(REDTEST):` + repro;
  list failures FIRST in the report.
- Report: detector verdicts per construction, F1-F3 confirmation
  outputs, and the public-surface findings table (claim → file:line →
  observed reality).
