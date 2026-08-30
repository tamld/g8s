#!/usr/bin/env bash
#
# g8s AI Anti-Pattern CI Gate (DEBT-21 / Issue #87)
# Enforces Constitution Axioms 1, 3, 4, 5 by detecting common AI-generated code smells:
#   1. check_no_panic: No panic("...") or panic(fmt.Sprintf(...)) in non-test code.
#   2. check_no_ignored_errors: No _ = ...Close() or defer ... _ = error swallowing.
#   3. check_no_type_assertion_in_library: No unchecked .(Type) downcasts in internal/.
#   4. check_todo_owner: No TODO/FIXME/XXX without OWNER= annotation.
#   5. check_no_ai_artifacts: No conversational LLM boilerplate in source code.
#

set -euo pipefail

TARGET_DIR="${1:-.}"

check_no_panic() {
    local target="$1"
    local files
    files=$(find "$target" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
    if [ -z "$files" ]; then
        return 0
    fi

    local hits
    hits=$(echo "$files" | xargs grep -rnE 'panic\(\s*("|\s*fmt\.Sprintf)' 2>/dev/null || true)
    if [ -n "$hits" ]; then
        echo "::error::[check_no_panic] Found panic() in non-test code (Constitution Axiom 1/4 violation):"
        echo "$hits"
        echo "  Hint: Return explicit errors using errors.New() or fmt.Errorf() instead of panicking."
        return 1
    fi
    return 0
}

check_no_ignored_errors() {
    local target="$1"
    local files
    files=$(find "$target" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
    if [ -z "$files" ]; then
        return 0
    fi

    local hits
    hits=$(echo "$files" | xargs grep -rnE '(_ =.*Close\(\)|defer.*_ =)' 2>/dev/null || true)
    if [ -n "$hits" ]; then
        echo "::error::[check_no_ignored_errors] Found silently ignored error pattern (_ = ...Close() or defer ... _ =):"
        echo "$hits"
        echo "  Hint: Use clean defer calls (e.g. defer f.Close()) or handle the error explicitly."
        return 1
    fi
    return 0
}

check_no_type_assertion_in_library() {
    local target="$1"
    local search_dir="$target"
    if [ -d "$target/internal" ]; then
        search_dir="$target/internal"
    fi

    local files
    files=$(find "$search_dir" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
    if [ -z "$files" ]; then
        return 0
    fi

    local raw_hits
    raw_hits=$(echo "$files" | xargs grep -rnE '\.\(\[*[*a-zA-Z]' 2>/dev/null || true)
    if [ -n "$raw_hits" ]; then
        local unchecked_hits
        unchecked_hits=$(echo "$raw_hits" | grep -vE '(,\s*[a-zA-Z0-9_]+\s*[:=]=|switch\s+.*=\s*.*\.(\(type\)|type)|\.\(type\)|\/\/)' || true)
        if [ -n "$unchecked_hits" ]; then
            echo "::error::[check_no_type_assertion_in_library] Found unchecked type assertion in internal/ library code:"
            echo "$unchecked_hits"
            echo "  Hint: Use checked type assertions with comma-ok idiom ('v, ok := x.(Type)') or type switches ('switch v := x.(type)')."
            return 1
        fi
    fi
    return 0
}

check_todo_owner() {
    local target="$1"
    local files
    files=$(find "$target" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
    if [ -z "$files" ]; then
        return 0
    fi

    local raw_hits
    raw_hits=$(echo "$files" | xargs grep -rnE '//\s*(TODO|FIXME|XXX)' 2>/dev/null || true)
    if [ -n "$raw_hits" ]; then
        local unowned_hits
        unowned_hits=$(echo "$raw_hits" | grep -v 'OWNER=' || true)
        if [ -n "$unowned_hits" ]; then
            echo "::error::[check_todo_owner] Found unassigned TODO/FIXME/XXX debt without OWNER annotation:"
            echo "$unowned_hits"
            echo "  Hint: Annotate with explicit owner, e.g. '// TODO(OWNER=username): ...' or resolve before commit."
            return 1
        fi
    fi
    return 0
}

check_no_ai_artifacts() {
    local target="$1"
    local files
    files=$(find "$target" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
    if [ -z "$files" ]; then
        return 0
    fi

    local hits
    hits=$(echo "$files" | xargs grep -rniE '(I am an AI|as an? (AI |large )?language model|certainly!|sure, (here|I can)|here is the (updated )?code|I hope this helps!)' 2>/dev/null || true)
    if [ -n "$hits" ]; then
        echo "::error::[check_no_ai_artifacts] Found AI conversational artifact(s) in source code:"
        echo "$hits"
        echo "  Hint: Remove conversational LLM boilerplate and remarks from source code."
        return 1
    fi
    return 0
}

check_test_pins_fabricated_symbol() {
    local target="$1"
    local doctor_out
    local repo_root
    repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

    doctor_out=$(cd "$repo_root" && go run ./cmd/g8s doctor --tdd-trap-check "$target" --json 2>&1) || true

    if [[ "$doctor_out" =~ \"category\":[[:space:]]*\"fabricated\" ]]; then
        echo "::error::[test-pins-fabricated-symbol] Test references fabricated/undefined symbols (DEBT-49):"
        echo "$doctor_out"
        echo "  Hint: Define symbols in production code before pinning them in unit tests."
        return 1
    fi
    return 0
}

check_test_locks_impl_detail() {
    local target="$1"
    local doctor_out
    local repo_root
    repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

    doctor_out=$(cd "$repo_root" && go run ./cmd/g8s doctor --tdd-trap-check "$target" --json 2>&1) || true

    if [[ "$doctor_out" =~ \"category\":[[:space:]]*\"locks-impl-detail\" ]]; then
        echo "::error::[test-locks-impl-detail] Test asserts on private implementation details (DEBT-49):"
        echo "$doctor_out"
        echo "  Hint: Assert on observable public behavior rather than internal private fields."
        return 1
    fi
    return 0
}

run_linter() {
    local target="$1"
    local failed=0

    echo "==> Running AI Anti-Pattern Gate against target: $target"

    if ! check_no_panic "$target"; then
        failed=$((failed + 1))
    fi

    if ! check_no_ignored_errors "$target"; then
        failed=$((failed + 1))
    fi

    if ! check_no_type_assertion_in_library "$target"; then
        failed=$((failed + 1))
    fi

    if ! check_todo_owner "$target"; then
        failed=$((failed + 1))
    fi

    if ! check_no_ai_artifacts "$target"; then
        failed=$((failed + 1))
    fi

    if ! check_test_pins_fabricated_symbol "$target"; then
        failed=$((failed + 1))
    fi

    if ! check_test_locks_impl_detail "$target"; then
        failed=$((failed + 1))
    fi

    if [ "$failed" -gt 0 ]; then
        echo ""
        echo "[AI-LINT] FAILED: $failed check(s) found violations."
        return 1
    else
        echo "[AI-LINT] PASSED: All 7 AI anti-pattern checks clean."
        return 0
    fi
}

# If script is executed directly, run linter
if [ "${BASH_SOURCE[0]:-$0}" = "$0" ]; then
    run_linter "$TARGET_DIR"
fi
