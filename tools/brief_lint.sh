#!/usr/bin/env bash
#
# tools/brief_lint.sh — DEBT-51 anti-pattern catalog extension
# Detects:
#   A) supervisor_thinks: supervisor acts like a dictator / busy polling instead of triggering worker
#   B) directive_brief: brief uses rigid directive template without open-question framing (DEBT-47)
#   C) missing_dual_blind: complex task dispatched without dual-blind convergence (DEBT-48)
#

set -euo pipefail

IS_DEFAULT_RUN=0
TARGET_PATHS=("$@")
if [ ${#TARGET_PATHS[@]} -eq 0 ]; then
    IS_DEFAULT_RUN=1
    TARGET_PATHS=("docs" "spec")
    if compgen -G "/tmp/agy-*.md" > /dev/null; then
        TARGET_PATHS+=("/tmp/agy-*.md")
    fi
fi

# A. supervisor_thinks
# Grep all .go files under cmd/, internal/orchestrator/ (or passed target) for
# patterns suggesting direct action instead of trigger:
#   - polling loops (for { time.Sleep(...) })
#   - manual intervention prompts
check_supervisor_thinks() {
    local target_paths=("$@")
    local failed=0
    local go_files=()

    # Collect Go files from targets
    for p in "${target_paths[@]}"; do
        if [ -f "$p" ] && [[ "$p" == *.go ]] && [[ "$p" != *_test.go ]]; then
            go_files+=("$p")
        elif [ -d "$p" ]; then
            while IFS= read -r f; do
                [ -n "$f" ] && go_files+=("$f")
            done < <(find "$p" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
        fi
    done

    # If default run or no Go files found but default repo directories exist, check cmd/ and internal/orchestrator/
    if [ "$IS_DEFAULT_RUN" -eq 1 ] && [ ${#go_files[@]} -eq 0 ]; then
        for d in "cmd" "internal/orchestrator"; do
            if [ -d "$d" ]; then
                while IFS= read -r f; do
                    [ -n "$f" ] && go_files+=("$f")
                done < <(find "$d" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
            fi
        done
    fi

    if [ ${#go_files[@]} -eq 0 ]; then
        return 0
    fi

    for file in "${go_files[@]}"; do
        # 1. Polling loops with time.Sleep
        if grep -nE 'time\.Sleep\(' "$file" > /dev/null 2>&1; then
            echo "::warning::[supervisor_thinks] Found polling loop in supervisor/orchestrator code ($file):"
            grep -nE 'time\.Sleep\(' "$file" | while IFS= read -r line; do
                echo "  $file:$line"
            done
            echo "  Hint: Supervisor should trigger/notify workers or use event-driven channels rather than polling loops."
            failed=$((failed + 1))
        fi

        # 2. Manual intervention prompts in supervisor code
        local prompt_hits
        prompt_hits=$(grep -nE '(Press \[Enter\] to continue|Please enter.*manually|fmt\.Scanln?\(|bufio\.NewReader\(os\.Stdin\))' "$file" 2>/dev/null || true)
        if [ -n "$prompt_hits" ]; then
            echo "::warning::[supervisor_thinks] Found manual intervention prompt in supervisor/orchestrator code ($file):"
            echo "$prompt_hits" | while IFS= read -r line; do
                echo "  $file:$line"
            done
            echo "  Hint: Automated supervisor workflows should not block on interactive stdin prompts."
            failed=$((failed + 1))
        fi
    done

    if [ "$failed" -gt 0 ]; then
        return 1
    fi
    return 0
}

# Helper to check if a file is a task brief
is_brief_file() {
    local file="$1"
    local base
    base=$(basename "$file")

    # Explicit brief file patterns
    if [[ "$base" == agy-*.md ]] || [[ "$file" == *"/briefs/"* ]]; then
        return 0
    fi

    # Read first top-level heading in file
    local first_heading
    first_heading=$(grep -m 1 -E '^# ' "$file" 2>/dev/null || true)

    if [[ "$first_heading" =~ ^#[[:space:]]+(AGY[[:space:]]+)?Brief([[:space:]]|—|-|:|$) ]] || \
       [[ "$first_heading" =~ ^#[[:space:]]+Task[[:space:]]+Brief([[:space:]]|—|-|:|$) ]]; then
        return 0
    fi

    return 1
}

# B. directive_brief
# Grep all brief markdown files for the directive pattern: "Implement X. DoD: Y. Constraints: Z."
# without any open-question section. Suggests rewrite to v2.
check_directive_brief() {
    local md_files=("$@")
    local failed=0

    for file in "${md_files[@]}"; do
        [ ! -f "$file" ] && continue
        if ! is_brief_file "$file"; then
            continue
        fi

        local content
        content=$(cat "$file")

        # Check for directive indicators: (Implement ... OR Goal:) AND (DoD: OR Definition of Done) AND (Constraints: OR ## Constraints)
        local has_directive=0
        if (echo "$content" | grep -qiE '(^#+ .*Implement|^Implement |## Goal|Goal:)') && \
           (echo "$content" | grep -qiE '(## DoD|## Definition of Done|DoD:|- \[ \])') && \
           (echo "$content" | grep -qiE '(## Constraints|Constraints:)'); then
            has_directive=1
        fi

        if [ "$has_directive" -eq 1 ]; then
            # Check for open-question section / framing (DEBT-47 v2)
            local has_open_question=0
            if echo "$content" | grep -qiE '(open[- ]questions?|###? question|v2 \(attentioner|open-question framing|\bquestions to answer\b|\?|\bframing\b)'; then
                has_open_question=1
            fi

            if [ "$has_open_question" -eq 0 ]; then
                local line_no
                line_no=$(grep -niE '(^#+ .*Implement|^Implement |## Goal|Goal:)' "$file" | head -n 1 | cut -d: -f1 || echo "1")
                echo "::warning::[directive_brief] Found directive brief without open-question framing in $file:$line_no"
                echo "  $file:$line_no: Directive template detected (Implement/DoD/Constraints) without Open Questions section"
                echo "  Hint: Rewrite brief to v2 format with an '## Open Questions' or framing section (DEBT-47)."
                failed=$((failed + 1))
            fi
        fi
    done

    if [ "$failed" -gt 0 ]; then
        return 1
    fi
    return 0
}

# C. missing_dual_blind
# Grep the brief for complex-task keywords:
#   "state machine" "schema" "parser" "RPC contract"
#   "concurrency model" "garbage collector" "lock-free"
# If found AND --blind-converge flag absent in dispatched work, warn to use DEBT-48.
check_missing_dual_blind() {
    local md_files=("$@")
    local failed=0

    # Keywords indicating complex architecture/design tasks
    local complex_regex='(state machine|schema|parser|rpc contract|concurrency model|garbage collector|lock-free)'

    for file in "${md_files[@]}"; do
        [ ! -f "$file" ] && continue
        if ! is_brief_file "$file"; then
            continue
        fi

        local content
        content=$(cat "$file")

        if echo "$content" | grep -qiE "$complex_regex"; then
            # Check if dual-blind flag or harness reference is present
            if ! echo "$content" | grep -qiE '(--blind-converge|blind-converge|dual-blind|DEBT-48)'; then
                local hit_kw
                hit_kw=$(echo "$content" | grep -oiE "$complex_regex" | head -n 1 || echo "complex keyword")
                local line_no
                line_no=$(grep -niE "$complex_regex" "$file" | head -n 1 | cut -d: -f1 || echo "1")
                echo "::warning::[missing_dual_blind] Complex task detected without dual-blind convergence in $file:$line_no"
                echo "  $file:$line_no: Brief mentions '$hit_kw' without --blind-converge flag"
                echo "  Hint: Complex tasks should use dual-blind convergence ('g8s orchestrate --blind-converge N' per DEBT-48)."
                failed=$((failed + 1))
            fi
        fi
    done

    if [ "$failed" -gt 0 ]; then
        return 1
    fi
    return 0
}

run_brief_linter() {
    local targets=("$@")
    local failed=0

    # Expand targets to md files
    local md_files=()
    for p in "${targets[@]}"; do
        if [ -f "$p" ] && [[ "$p" == *.md ]]; then
            md_files+=("$p")
        elif [ -d "$p" ]; then
            while IFS= read -r f; do
                [ -n "$f" ] && md_files+=("$f")
            done < <(find "$p" -name "*.md" ! -path "*/reference/*" ! -path "*/.git/*" 2>/dev/null || true)
        elif compgen -G "$p" > /dev/null; then
            for f in $p; do
                [ -f "$f" ] && md_files+=("$f")
            done
        fi
    done

    echo "==> Running Brief Anti-Pattern Gate (DEBT-51)..."

    if ! check_supervisor_thinks "${targets[@]}"; then
        failed=$((failed + 1))
    fi

    if [ ${#md_files[@]} -gt 0 ]; then
        if ! check_directive_brief "${md_files[@]}"; then
            failed=$((failed + 1))
        fi

        if ! check_missing_dual_blind "${md_files[@]}"; then
            failed=$((failed + 1))
        fi
    fi

    if [ "$failed" -gt 0 ]; then
        echo ""
        echo "[BRIEF-LINT] FAILED: $failed check(s) found violations."
        return 1
    else
        echo "[BRIEF-LINT] PASSED: All 3 brief anti-pattern checks clean."
        return 0
    fi
}

# If script is executed directly, run linter
if [ "${BASH_SOURCE[0]:-$0}" = "$0" ]; then
    run_brief_linter "${TARGET_PATHS[@]}"
fi
