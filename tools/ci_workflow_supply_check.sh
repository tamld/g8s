#!/usr/bin/env bash
#
# tools/ci_workflow_supply_check.sh — CI supply-chain static gate (#983)
#
# Scans .github/workflows/*.y*ml for the regression classes fixed by issue
# #983 and fails closed on any hit:
#
#   1. Pipe-to-shell installs: `curl ... | sh` / `curl ... | sudo sh`
#      (incl. wget). Third-party install scripts must never be piped into a
#      shell — privileged jobs install tools via a pinned release download
#      with SHA-256 verification (tools/ci_fetch.sh).
#
#   2. Floating tool versions: `@latest`, `version: latest`, GoReleaser
#      constraint floats (`version: '~> vX.Y'`), and unpinned `npx`
#      invocations. Privileged jobs must pin exact versions.
#
#   3. Jobs without an explicit `timeout-minutes` (GitHub's default ceiling
#      is 360 minutes — a hung privileged job must not sit for 6 hours).
#
#   4. Workflow-level write permissions: `permissions: contents: write` (or
#      similar) granted at workflow scope leaks write to every job in the
#      file. Write scopes belong on the individual privileged job.
#
# Pure static analysis — no YAML parser dependency, no network access.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKFLOW_DIR="${REPO_ROOT}/.github/workflows"

failures=0

check_fail() {
    echo "::error::$1"
    failures=$((failures + 1))
}

if [ ! -d "$WORKFLOW_DIR" ]; then
    echo "ERROR: workflow dir not found: ${WORKFLOW_DIR}" >&2
    exit 1
fi

echo "== Workflow supply-chain gate (#983) =="

# --- 1. No pipe-to-shell downloads -----------------------------------------
# Pattern checks skip comment-only lines (`grep -vE ':[0-9]+:[[:space:]]*#'`
# strips them from grep -n output) so historical notes don't trip the gate;
# trailing inline comments after real code are still scanned.
pipe_hits=$(grep -rnE '(curl|wget)[^|]*\|[[:space:]]*(sudo[[:space:]]+)?(ba)?sh([[:space:]]|$)' "$WORKFLOW_DIR" | grep -vE ':[0-9]+:[[:space:]]*#' || true)
if [ -n "$pipe_hits" ]; then
    check_fail "pipe-to-shell download(s) found — install via pinned release + SHA-256 (tools/ci_fetch.sh):"
    printf '%s\n' "$pipe_hits" | sed 's/^/    /'
else
    echo "OK: no curl|sh / wget|sh pipe-to-shell installs"
fi

# --- 2. No floating tool versions -------------------------------------------
float_hits=$(grep -rnE '@latest|version:[[:space:]]+latest|version:[[:space:]]+['"'"'"]~>' "$WORKFLOW_DIR" | grep -vE ':[0-9]+:[[:space:]]*#' || true)
if [ -n "$float_hits" ]; then
    check_fail "floating tool version(s) found — pin an exact version:"
    printf '%s\n' "$float_hits" | sed 's/^/    /'
else
    echo "OK: no floating tool-version tokens outside comments"
fi

# Unpinned npx (any `npx <pkg>` without an explicit @X.Y[.Z] version).
npx_hits=""
while IFS= read -r line; do
    [ -z "$line" ] && continue
    if ! printf '%s' "${line#*:}" | grep -qE '@[0-9]+\.[0-9]+'; then
        npx_hits="${npx_hits}${line}
"
    fi
done < <(grep -rnE '(^|[[:space:]])npx[[:space:]]' "$WORKFLOW_DIR" || true)
if [ -n "$npx_hits" ]; then
    check_fail "unpinned npx invocation(s) found — pin an exact version (pkg@X.Y.Z):"
    printf '%s' "$npx_hits" | sed 's/^/    /'
else
    echo "OK: no unpinned npx invocations"
fi

# --- 3. Every job must declare timeout-minutes -------------------------------
missing_timeouts=$(awk '
    /^jobs:[ \t]*$/ { injobs = 1; next }
    injobs && /^[^ \t#]/ { injobs = 0; current = "" }
    injobs && /^  [A-Za-z0-9_-]+:[ \t]*$/ {
        current = $0
        sub(/^  /, "", current)
        sub(/:[ \t]*$/, "", current)
        jobs[jobcount++] = FILENAME "|" current
        next
    }
    injobs && /^[ \t]+timeout-minutes:/ && current != "" {
        has_timeout[FILENAME "|" current] = 1
    }
    END {
        for (i = 0; i < jobcount; i++) {
            if (!has_timeout[jobs[i]]) print jobs[i]
        }
    }
' "$WORKFLOW_DIR"/*.y*ml || true)
if [ -n "$missing_timeouts" ]; then
    check_fail "job(s) without explicit timeout-minutes (GitHub default is 360m):"
    printf '%s\n' "$missing_timeouts" | sed 's/^/    /'
else
    echo "OK: every job declares timeout-minutes"
fi

# --- 4. No workflow-level write permissions ----------------------------------
workflow_write=$(awk '
    /^permissions:[ \t]*$/ { inperm = 1; next }
    inperm && /^[^ \t#]/ { inperm = 0 }
    inperm && /^  [A-Za-z-]+:[ \t]*(write|publish)[ \t]*$/ {
        print FILENAME ": workflow-level `" $0 "`"
    }
' "$WORKFLOW_DIR"/*.y*ml || true)
if [ -n "$workflow_write" ]; then
    check_fail "workflow-level write permission(s) found — scope write to the privileged job only:"
    printf '%s\n' "$workflow_write" | sed 's/^/    /'
else
    echo "OK: no workflow-level write permissions"
fi

if [ "$failures" -ne 0 ]; then
    echo ""
    echo "RESULT: FAIL — ${failures} finding class(es) (see ::error:: markers above)"
    exit 1
fi

echo ""
echo "RESULT: PASS — workflow supply-chain gate clean"
