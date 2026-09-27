#!/usr/bin/env bash
#
# tools/run_audit_fanout.sh — Deep-audit fan-out runner for g8s (S6-5, #420, ADR-0024 Layer 4)
#
# Dispatches read-only verifier workers across 6 declared audit dimensions:
#   1. docs      - README structure match & link resolution
#   2. spec      - OpenSpec scenario to test pins sync
#   3. tests     - Zero-CGO test execution & failure reporting
#   4. security  - Secret & path leak scan on tracked files
#   5. lifecycle - Escalation pattern promotion candidates
#   6. evidence  - Session-type markers & PR squash merge references
#
# Usage:
#   run_audit_fanout.sh [--repo <dir>] [--dimensions docs,spec,tests,security,lifecycle,evidence] [--dry-run]
#
# Output:
#   --dry-run: prints the submit commands prefixed 'DRY:' without executing
#   non-dry-run: prints the submit command, executes it via 'g8s submit', and prints task_id
#   Exit 0 on success, exit 2 on invalid arguments / unknown dimension.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DEFAULT_DIMENSIONS="docs,spec,tests,security,lifecycle,evidence"
REPO_DIR="."
DIMENSIONS_ARG="$DEFAULT_DIMENSIONS"
DRY_RUN=0

usage() {
    echo "Usage: run_audit_fanout.sh [--repo <dir>] [--dimensions docs,spec,tests,security,lifecycle,evidence] [--dry-run]"
}

# Parse command-line options
while [ $# -gt 0 ]; do
    case "$1" in
        --repo)
            if [ $# -lt 2 ]; then
                echo "error: --repo requires a directory argument" >&2
                usage >&2
                exit 2
            fi
            REPO_DIR="$2"
            shift 2
            ;;
        --dimensions)
            if [ $# -lt 2 ]; then
                echo "error: --dimensions requires a comma-separated list argument" >&2
                usage >&2
                exit 2
            fi
            DIMENSIONS_ARG="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "error: unknown argument '$1'" >&2
            usage >&2
            exit 2
            ;;
    esac
done

# Validate repository directory
if [ ! -d "$REPO_DIR" ]; then
    echo "error: repository directory not found: '$REPO_DIR'" >&2
    exit 2
fi

# Validate dimensions argument is not empty
if [ -z "$DIMENSIONS_ARG" ]; then
    echo "error: --dimensions cannot be empty" >&2
    exit 2
fi

# Dimension verifier prompt definitions
get_dimension_prompt() {
    local dim="$1"
    case "$dim" in
        docs)
            echo 'Verify README.md Project Structure matches the filesystem (run tools/ci_structure_sync.sh) and all relative links in README.md + docs/ resolve. Report file:line for every drift.'
            ;;
        spec)
            echo 'Verify every #### Scenario in spec/openspec/*.md with Status APPLIED/ACCEPTED has a <!-- tests: ... --> pin mapping to an existing Go test (run tools/ci_spec_code_sync.sh). Report unpinned/ghost scenarios.'
            ;;
        tests)
            echo 'Run CGO_ENABLED=0 go test -count=1 ./... and report any failure with file:line.'
            ;;
        security)
            echo 'Scan tracked files for leaked absolute home paths (/Users/<name>, C:\Users), api-key shapes (sk-...), and raw .env content. Report file:line only — never paste the secret value.'
            ;;
        lifecycle)
            echo 'Check .g8s/escalations.jsonl for patterns with >= 2 escalations of the same directory shape. Report promote candidates.'
            ;;
        evidence)
            echo 'Verify every plans/*/plan.md has a **Session type** marker and every recent squash merge on main references its PR number. Report gaps.'
            ;;
        *)
            return 1
            ;;
    esac
}

# Parse and validate requested dimensions
REQUESTED_DIMENSIONS=()
IFS=',' read -r -a raw_dims <<< "$DIMENSIONS_ARG"

if [ "${#raw_dims[@]}" -eq 0 ]; then
    echo "error: no dimensions specified" >&2
    exit 2
fi

for raw in "${raw_dims[@]}"; do
    cleaned=$(printf '%s' "$raw" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
    if [ -z "$cleaned" ]; then
        echo "error: empty dimension name specified in '$DIMENSIONS_ARG'" >&2
        exit 2
    fi
    if ! get_dimension_prompt "$cleaned" >/dev/null 2>&1; then
        echo "error: unknown dimension '$cleaned'. Valid dimensions: docs, spec, tests, security, lifecycle, evidence" >&2
        exit 2
    fi
    REQUESTED_DIMENSIONS+=("$cleaned")
done

RUN_TS="$(date +%s)"
RUN_ID="${RUN_TS}-${RANDOM:-$$}"

# Helper to execute g8s submit
exec_g8s_submit() {
    local idem_key="$1"
    local prompt="$2"
    local extra_args=()
    if [ "$REPO_DIR" != "." ]; then
        extra_args+=(--add-dir "$REPO_DIR")
    fi

    local submit_args=(
        --idempotency-key "$idem_key"
        --role verifier
        --permission read_only
        --model gemini-3.7-flash-high
        --timeout 300s
        --prompt "$prompt"
    )
    if [ "${#extra_args[@]}" -gt 0 ]; then
        submit_args+=("${extra_args[@]}")
    fi

    if [ -n "${G8S_BIN:-}" ]; then
        "$G8S_BIN" submit "${submit_args[@]}"
    elif command -v g8s >/dev/null 2>&1; then
        g8s submit "${submit_args[@]}"
    elif [ -x "${SCRIPT_DIR}/../g8s" ]; then
        "${SCRIPT_DIR}/../g8s" submit "${submit_args[@]}"
    elif [ -x "./g8s" ]; then
        ./g8s submit "${submit_args[@]}"
    elif [ -f "${SCRIPT_DIR}/../cmd/g8s/main.go" ]; then
        (cd "${SCRIPT_DIR}/.." && go run ./cmd/g8s submit "${submit_args[@]}")
    else
        echo "error: g8s binary not found. Build g8s or add to PATH." >&2
        return 1
    fi
}

# Process each dimension
for dim in "${REQUESTED_DIMENSIONS[@]}"; do
    prompt=$(get_dimension_prompt "$dim")
    idem_key="audit-${dim}-${RUN_ID}"
    submit_cmd="g8s submit --idempotency-key ${idem_key} --role verifier --permission read_only --model gemini-3.7-flash-high --timeout 300s --prompt '${prompt}'"

    if [ "$DRY_RUN" -eq 1 ]; then
        echo "DRY: ${submit_cmd}"
    else
        echo "${submit_cmd}"
        output=$(exec_g8s_submit "$idem_key" "$prompt")
        task_id=$(printf '%s\n' "$output" | sed -n 's/.*"task_id":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
        if [ -n "$task_id" ]; then
            echo "task_id: ${task_id}"
        else
            echo "task_id: unknown"
        fi
    fi
done

exit 0
