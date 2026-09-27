#!/usr/bin/env bash
#
# ci_structure_sync.sh — G2 gate (S6-1, #420, ADR-0024 Layer 1)
#
# Renders the Project Structure tree from the live filesystem (Go package
# dirs + their doc comments, plus a curated static list of non-Go dirs)
# into the README's `<!-- structure:start -->` ... `<!-- structure:end -->`
# region. Gate mode fails when the committed region drifts from a fresh
# render — hand-edits and new packages without regen are both caught.
#
# Usage:
#   ci_structure_sync.sh [--repo-root .] [--readme README.md] [--render]

set -euo pipefail

MODE="gate"
REPO_ROOT="."
README="README.md"

while [ $# -gt 0 ]; do
    case "$1" in
        --repo-root) REPO_ROOT="$2"; shift 2 ;;
        --readme) README="$2"; shift 2 ;;
        --render) MODE="render"; shift ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done
cd "$REPO_ROOT"

START_MARKER="<!-- structure:start -->"
END_MARKER="<!-- structure:end -->"

# Static curated entries for non-Go directories (label column). Skipped
# when the directory does not exist on disk.
static_entry() {
    case "$1" in
        cmd) echo "├── cmd/                    # CLI entrypoint (stdlib flag-based, no cobra)" ;;
        skills) echo "├── skills/                 # Vendored agent skills (g8s-supervisor charter + manifest)" ;;
        packaging) echo "├── packaging/              # Windows NSIS/WiX, Chocolatey, winget" ;;
        docs) echo "├── docs/                   # User guide, ADRs, specs, security" ;;
        plans) echo "├── plans/                  # Campaign ledgers (dated, session-type marked)" ;;
        spec) echo "├── spec/openspec/          # OpenSpec deltas (DELTA-01..22)" ;;
        schemas) echo "├── schemas/                # JSON schemas (task, receipt, result)" ;;
        tools) echo "├── tools/                  # CI helper scripts (pre-push gates, release)" ;;
        workflows) echo "└── .github/workflows/      # CI/CD pipelines" ;;
        *) return 1 ;;
    esac
}

# doc_summary extracts the one-line package summary from the source files
# of a package directory: the first `// Package <name>` line, trimmed of
# the prefix, terminated at the first sentence boundary.
doc_summary() {
    local dir="$1" pkg="$2"
    local line
    line=$(grep -h "^// Package ${pkg}\b" "$dir"/*.go 2>/dev/null | head -1 || true)
    [ -n "$line" ] || { echo "(no package doc — add one)"; return; }
    line=${line#*Package ${pkg}}
    line=${line##*(—|:| - )}
    line=$(echo "$line" | sed 's/^\s*[-—:]*\s*//')
    # terminate at first sentence period
    line=${line%%.*}
    if [ "${#line}" -gt 90 ]; then
        line="${line:0:90}"
    fi
    [ -n "$(echo "$line" | tr -d ' ')" ] && echo "$line" || echo "(no package doc — add one)"
}

render() {
    echo '```'
    echo "g8s/"
    echo "├── cmd/g8s/                # CLI entrypoint (stdlib flag-based, no cobra)"
    echo "├── internal/               # All packages (private, not importable)"
    for dir in $(go list ./... 2>/dev/null | grep '/internal/' | sed 's|.*/internal/||; s|/.*||' | sort -u); do
        summary=$(doc_summary "internal/$dir" "$dir")
        echo "│   ├── $dir/ — $summary"
    done
    echo "│   └── ...                 # supporting packages"
    for entry in skills packaging docs plans spec schemas tools workflows; do
        case "$entry" in
            workflows) [ -d ".github/workflows" ] && static_entry workflows ;;
            *) [ -d "$entry" ] && static_entry "$entry" ;;
        esac
    done
    echo '```'
}

if [ "$MODE" = "render" ]; then
    if ! grep -q "$START_MARKER" "$README" || ! grep -q "$END_MARKER" "$README"; then
        echo "::error::structure sync: $README is missing the $START_MARKER / $END_MARKER markers" >&2
        exit 1
    fi
    body=$(render)
    export STRUCTURE_BODY="$body"
    awk -v start="$START_MARKER" -v end="$END_MARKER" '
        BEGIN { body = ENVIRON["STRUCTURE_BODY"] }
        $0 == start { print; print body; printing = 1; next }
        $0 == end { printing = 0 }
        printing != 1 { print }
    ' "$README" > "$README.new"
    mv "$README.new" "$README"
    echo "STRUCTURE SYNC: rendered fresh tree into $README."
    exit 0
fi

# --- gate mode -------------------------------------------------------------
if ! grep -q "$START_MARKER" "$README" || ! grep -q "$END_MARKER" "$README"; then
    echo "::error::structure sync: $README is missing the $START_MARKER / $END_MARKER markers (#420 G2)"
    exit 1
fi

current=$(awk -v start="$START_MARKER" -v end="$END_MARKER" '
    $0 == start { printing = 1; next }
    $0 == end { printing = 0 }
    printing == 1 { print }
' "$README")
fresh=$(render)

if [ "$current" != "$fresh" ]; then
    echo "::error::structure sync: README Project Structure drifted from the live filesystem (#420 G2) — run: tools/ci_structure_sync.sh --render"
    echo "--- committed region ---"
    echo "$current"
    echo "--- fresh render ---"
    echo "$fresh"
    exit 1
fi
echo "STRUCTURE SYNC GATE: OK — README tree matches the filesystem."
