#!/usr/bin/env bash
#
# tools/worker_boundary.sh — Runtime Enforcement of AGY Worker Scope (DEBT-62 / Issue #209)
#
# Prevents AGY workers from executing forbidden operations:
#   - git push origin main / git push --force origin main
#   - gh pr merge --auto (no auto-merging PRs)
#   - gh api -X PATCH /repos/.../branches/main/protection (no altering branch protections)
#   - git branch -D (no force-deleting branches)
#   - rm -rf outside cwd (no deleting foreign directories)
#
# Usage:
#   tools/worker_boundary.sh check "<command>"
#   tools/worker_boundary.sh install-hooks [worktree_path]
#   tools/worker_boundary.sh watchdog <PID> [DURATION]

set -euo pipefail

is_forbidden_command() {
    local cmd="$1"
    local cwd="${2:-$(pwd)}"

    # 1. Block push to main branch
    if echo "$cmd" | grep -E -q '(^|[[:space:]])git[[:space:]]+push[[:space:]]+.*(\borigin[[:space:]]+main\b|\bmain\b)'; then
        echo "FORBIDDEN: Direct push to main branch is blocked."
        return 0
    fi

    # 2. Block force push to main branch
    if echo "$cmd" | grep -E -q '(^|[[:space:]])git[[:space:]]+push[[:space:]]+.*(-f|--force).*(\borigin[[:space:]]+main\b|\bmain\b)'; then
        echo "FORBIDDEN: Force-push to main branch is blocked."
        return 0
    fi

    # 3. Block gh pr merge --auto or auto-merge
    if echo "$cmd" | grep -E -q '(^|[[:space:]])gh[[:space:]]+pr[[:space:]]+merge[[:space:]]+.*(--auto|\bauto\b)'; then
        echo "FORBIDDEN: Automated PR merging (gh pr merge --auto) is blocked."
        return 0
    fi

    # 4. Block branch protection tampering via gh api
    if echo "$cmd" | grep -E -q '(^|[[:space:]])gh[[:space:]]+api[[:space:]]+.*(/branches/main/protection|/branches/([^/]+)/protection)'; then
        echo "FORBIDDEN: Tampering with repository branch protection rules is blocked."
        return 0
    fi

    # 5. Block git branch -D (force delete)
    if echo "$cmd" | grep -E -q '(^|[[:space:]])git[[:space:]]+branch[[:space:]]+.*(-D\b|--delete[[:space:]]+--force)'; then
        echo "FORBIDDEN: Force-deleting branches (git branch -D) is blocked."
        return 0
    fi

    # 6. Block rm -rf outside current working directory or dangerous root targets
    if echo "$cmd" | grep -E -q '(^|[[:space:]])rm[[:space:]]+-[a-zA-Z]*r[a-zA-Z]*f?[[:space:]]+(/[[:space:]]*$|/tmp\b|/var\b|/etc\b|/home\b|/Users\b|~|\.\./|.*[[:space:]]+(/[[:space:]]*$|/tmp\b|/var\b|/etc\b|/home\b|/Users\b|~|\.\./))'; then
        echo "FORBIDDEN: rm -rf outside working directory scope is blocked."
        return 0
    fi

    return 1
}

cmd_check() {
    local cmd="$1"
    local cwd="${2:-$(pwd)}"

    if is_forbidden_command "$cmd" "$cwd"; then
        echo "::error::[worker_boundary] Command violated worker-scope boundary: $cmd" >&2
        return 1
    fi
    echo "[worker_boundary] Command permitted: $cmd"
    return 0
}

cmd_install_hooks() {
    local target_dir="${1:-.}"
    local git_dir
    if [ -d "$target_dir/.git" ]; then
        git_dir="$target_dir/.git"
    else
        git_dir=$(cd "$target_dir" && git rev-parse --git-dir 2>/dev/null || true)
    fi

    if [ -z "$git_dir" ] || [ ! -d "$git_dir" ]; then
        echo "::error::[worker_boundary] Not a git repository: $target_dir" >&2
        return 1
    fi

    local hook_dir="$git_dir/hooks"
    mkdir -p "$hook_dir"
    local hook_file="$hook_dir/pre-push"

    cat << 'EOF' > "$hook_file"
#!/usr/bin/env bash
# AGY Worker Pre-Push Safety Guard (DEBT-62)
set -euo pipefail

while read -r local_ref local_oid remote_ref remote_oid; do
    if [[ "$remote_ref" == "refs/heads/main" ]]; then
        echo "::error::[worker_boundary pre-push] Direct push to main branch is strictly prohibited for AGY workers." >&2
        echo "  Hint: Create a feature branch (e.g. git checkout -b feat/my-change) and open a pull request." >&2
        exit 1
    fi
done

exit 0
EOF
    chmod +x "$hook_file"
    echo "[worker_boundary] Installed pre-push safety hook in $hook_file"
    return 0
}

cmd_watchdog() {
    local pid="$1"
    local duration="${2:-300}"

    echo "[worker_boundary] Starting process watchdog for PID $pid (duration: ${duration}s)..."

    for _ in $(seq 1 "$duration"); do
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "[worker_boundary] Watched process $pid exited normally."
            return 0
        fi

        # Inspect process arguments or command line on Linux/macOS
        local proc_cmd=""
        if [ "$(uname)" = "Darwin" ]; then
            proc_cmd=$(ps -p "$pid" -o command= 2>/dev/null || true)
        elif [ "$(uname)" = "Linux" ]; then
            if [ -f "/proc/$pid/cmdline" ]; then
                proc_cmd=$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)
            fi
        fi

        if [ -n "$proc_cmd" ]; then
            if is_forbidden_command "$proc_cmd"; then
                echo "::error::[worker_boundary watchdog] Terminating process $pid for boundary violation: $proc_cmd" >&2
                kill -9 "$pid" 2>/dev/null || true
                return 1
            fi
        fi

        sleep 1
    done

    return 0
}

usage() {
    echo "Usage: tools/worker_boundary.sh <check|install-hooks|watchdog> [args...]"
    echo "  check '<cmd>'                 - Validates if a shell command violates worker boundaries"
    echo "  install-hooks [worktree_path] - Installs git pre-push hook preventing direct push to main"
    echo "  watchdog <PID> [duration_sec] - Monitors a running AGY process for forbidden commands"
    exit 1
}

ACTION="${1:-}"
case "$ACTION" in
    check)
        shift
        if [ $# -lt 1 ]; then
            echo "Error: 'check' requires command string argument" >&2
            exit 1
        fi
        cmd_check "$1" "${2:-$(pwd)}"
        ;;
    install-hooks)
        shift
        cmd_install_hooks "${1:-.}"
        ;;
    watchdog)
        shift
        if [ $# -lt 1 ]; then
            echo "Error: 'watchdog' requires PID argument" >&2
            exit 1
        fi
        cmd_watchdog "$1" "${2:-300}"
        ;;
    *)
        usage
        ;;
esac
