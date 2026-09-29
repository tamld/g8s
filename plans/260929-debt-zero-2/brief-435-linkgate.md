# Task: Fix #435 — link-integrity gate must examine (and report) configured content roots

Repo: g8s @ main. Tracking: tamld/g8s#435 (P2; a gate reporting OK without
examining the content it was asked to audit is a gate-integrity defect).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- tools/ci_link_integrity.sh
- tools/ci_link_integrity_test.sh (new file — follow the style of
  tools/claims_check_test.sh)

Do NOT run git commit. Do NOT modify any other file. Two other workers are
operating in parallel on unrelated files (internal/runtime/,
internal/memory/) — touching anything outside the list above corrupts
their delivery.

## Context (verified at main)

tools/ci_link_integrity.sh discovers only README.md (line ~45) and
docs/**/*.md (line ~51). Reproduction from the issue (all executed against
the pinned script): a pillar-style vault with a dead link inside
`1-Knowledge/note.md` exits 0 with "OK (0 relative link(s) verified across
1 file(s))" — a knowledge audit reports blanket success while examining
zero knowledge files.

## Required implementation

1. **Configurable content roots** (shell, no new deps):
   - Accept roots via (precedence): repeated `--root`-style flag if the
     script already parses args, else env var `LINK_INTEGRITY_ROOTS`
     (colon-separated list of directories/files relative to $ROOT_DIR),
     else the current default (`README.md` + `docs/`).
   - $ROOT_DIR semantics unchanged.
   - Discovery: for each root, if it is a file include it; if a directory
     include all .md files under it recursively (sorted, deduplicated).
   - Default behavior (no roots configured) must remain byte-identical to
     today for README/docs consumers.

2. **Honest reporting**:
   - The summary line must include: examined file count, examined relative
     link count, and the roots actually scanned.
   - **Zero-examined guard**: when roots were explicitly configured
     (flag/env) and discovery yields 0 files, the script must NOT print
     OK — it must print a distinct `NOT EVALUATED: no markdown files found
     under configured root(s): <roots>` and exit 2 (a distinct code, not
     the dead-link exit 1). When NO roots are configured and the default
     discovery finds nothing, keep current behavior.

3. **Do not** scan excluded runtime/private directories indiscriminately:
   keep any existing exclusion behavior intact; only roots explicitly
   configured get scanned.

## Tests — ci_link_integrity_test.sh (red first)

Bash test script in the style of tools/claims_check_test.sh: build
temporary fixtures under mktemp -d, run the gate, assert exit codes and
key report substrings, clean up. Cases (from the issue, plus new):

1. Clean README.md; `1-Knowledge/note.md` with `[Dead Link](missing.md)`;
   roots configured to include `1-Knowledge` → exit 1, one dead link.
2. Same fixture, roots NOT configured (default) → exit 0 with current
   default behavior preserved (README/docs only).
3. README.md + docs/guide.md dead link, default roots → exit 1
   (regression guard for today's working case).
4. README links existing docs/guide.md, guide links back, default roots →
   exit 0, report mentions 2 examined links.
5. Roots configured pointing at an empty directory → exit 2 with the
   NOT EVALUATED line (no blanket OK).
6. Report line always contains examined file and link counts.

## Constraints

- Pure bash stdlib (the gate is a shell script; do not introduce
  Python/Go). Match the existing script's style (set -euo pipefail,
  colored output helpers if present).
- The script is pinned into offer bundles by consumers — keep its stdout
  summary format backward-compatible in shape (counts were already
  printed; you are adding roots + counts detail, not renaming fields
  consumers parse today — check what the report lines look like today and
  extend, don't replace).
