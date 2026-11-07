#!/usr/bin/env bash
# Compare shipped g8s --help commands against docs/user-guide/cli-reference.md headings.
# No network. Builds ./bin/g8s when missing unless BIN is set. See --help.
set -euo pipefail
export LC_ALL=C

usage() {
    printf '%s\n' \
        'Usage: bash tools/ci_cli_reference_check.sh' \
        'Env: BIN (default: ./bin/g8s), CLI_REF (default: docs/user-guide/cli-reference.md)' \
        'Exit 1: MISSING-ENTRY (shipped command absent from reference) or STALE-DOC (reference names a missing command).' \
        'Exit 2: usage/input error.'
}

case "${1:-}" in
    '')
        ;;
    --help)
        usage
        exit 0
        ;;
    *)
        printf 'Unknown argument: %s\n' "$1" >&2
        usage >&2
        exit 2
        ;;
esac
if (($# > 0)); then
    usage >&2
    exit 2
fi

cd "$(git rev-parse --show-toplevel)"
BIN="${BIN:-./bin/g8s}"
CLI_REF="${CLI_REF:-docs/user-guide/cli-reference.md}"

if [[ ! -f $CLI_REF ]]; then
    printf 'CLI reference not found: %s\n' "$CLI_REF" >&2
    exit 2
fi

if [[ ! -x $BIN ]]; then
    go build -o "$BIN" ./cmd/g8s
fi

# Top-level commands from root help (stop before the trailing blank line after Commands).
shipped=()
while IFS= read -r cmd; do
    [[ -n $cmd ]] && shipped+=("$cmd")
done < <(
    "$BIN" --help | awk '/^Commands:/{p=1;next} /^$/{if(p) exit} p && /^  [a-z]/ {print $1}'
)
if ((${#shipped[@]} == 0)); then
    printf 'No commands parsed from %s --help\n' "$BIN" >&2
    exit 2
fi

# Match ### heading convention: `g8s <cmd>` before `, /, &, closing backtick, <, or >.
doc_heading_line_matches() {
    local cmd=$1
    grep '^###' "$CLI_REF" | grep -qE "g8s ${cmd}([\`' /&<>]|$)"
}

missing=0
stale=0
for cmd in "${shipped[@]}"; do
    if ! doc_heading_line_matches "$cmd"; then
        printf 'MISSING-ENTRY: %s\n' "$cmd" >&2
        missing=$((missing + 1))
    fi
done

# Stale: every top-level command token on a ### line must exist in the binary help list.
lookup_shipped() {
    local needle=$1 i
    for ((i = 0; i < ${#shipped[@]}; i++)); do
        [[ ${shipped[i]} == "$needle" ]] && return 0
    done
    return 1
}

while IFS= read -r token; do
    [[ -z $token ]] && continue
    if ! lookup_shipped "$token"; then
        printf 'STALE-DOC: %s\n' "$token" >&2
        stale=$((stale + 1))
    fi
done < <(grep '^###' "$CLI_REF" | grep -oE 'g8s [a-z0-9-]+' | awk '{print $2}' | sort -u)

if ((missing > 0 || stale > 0)); then
    printf 'cli-reference check failed: missing=%d stale=%d (doc=%s)\n' "$missing" "$stale" "$CLI_REF" >&2
    exit 1
fi

printf 'cli-reference check OK: %d shipped commands documented in %s\n' "${#shipped[@]}" "$CLI_REF"
