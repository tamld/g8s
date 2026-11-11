#!/usr/bin/env bash
#
# tools/pre_push_test.sh — Test suite for tools/pre_push.sh
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRE_PUSH="${REPO_ROOT}/tools/pre_push.sh"

echo "==> Testing tools/pre_push.sh harness..."

# Test 1: Help flag
echo "--> Test 1: Verify --help flag works and documents --impacted..."
HELP_OUT=$("$PRE_PUSH" --help)
if ! printf '%s\n' "$HELP_OUT" | grep -q -- "--impacted"; then
    echo "FAIL: --help output missing --impacted documentation"
    exit 1
fi
echo "  [PASS] --help flag works"

# Test 1b: --impacted-plan resolution against .g8s/feature-map.yml
if [ -x "${REPO_ROOT}/bin/g8s" ]; then
    echo "--> Test 1b: Verify --impacted-plan scopes leaf feature and fails closed to FULL on all edge cases..."
    PLAN_REACH=$("$PRE_PUSH" --impacted-plan --paths "internal/reach/registry.go")
    if [[ "$PLAN_REACH" != SCOPED\|*./internal/reach/...* ]]; then
        echo "FAIL: expected SCOPED plan containing ./internal/reach/..., got: $PLAN_REACH"
        exit 1
    fi
    PLAN_EMPTY=$("$PRE_PUSH" --impacted-plan --paths "__EMPTY__")
    if [[ "$PLAN_EMPTY" != "FULL|no diff vs origin/main" ]]; then
        echo "FAIL: expected FULL fallback on empty diff, got: $PLAN_EMPTY"
        exit 1
    fi
    PLAN_ROOT=$("$PRE_PUSH" --impacted-plan --paths "offer.go")
    if [[ "$PLAN_ROOT" != FULL\|root\ build/module\ file\ changed* ]]; then
        echo "FAIL: expected FULL fallback for root Go file offer.go, got: $PLAN_ROOT"
        exit 1
    fi
    PLAN_CORE=$("$PRE_PUSH" --impacted-plan --paths "internal/controlplane/store.go")
    if [[ "$PLAN_CORE" != FULL\|* ]]; then
        echo "FAIL: expected FULL fallback for core feature FEAT-CONTROLPLANE-01, got: $PLAN_CORE"
        exit 1
    fi
    PLAN_HOTSPOT=$("$PRE_PUSH" --impacted-plan --paths "tools/pre_push.sh")
    if [[ "$PLAN_HOTSPOT" != FULL\|hotspot\ touched* ]]; then
        echo "FAIL: expected FULL fallback for HOT-BUILD-CI hotspot, got: $PLAN_HOTSPOT"
        exit 1
    fi
    PLAN_ANY_HOTSPOT=$("$PRE_PUSH" --impacted-plan --paths "internal/reach/registry.go,manifest.json")
    if [[ "$PLAN_ANY_HOTSPOT" != FULL\|hotspot\ touched* ]]; then
        echo "FAIL: expected FULL fallback when any hotspot (HOT-MANIFEST) accompanies leaf feature, got: $PLAN_ANY_HOTSPOT"
        exit 1
    fi
    PLAN_UNMAPPED=$("$PRE_PUSH" --impacted-plan --paths "unknown/unmapped.go")
    if [[ "$PLAN_UNMAPPED" != FULL\|* ]]; then
        echo "FAIL: expected FULL fallback for unmapped path, got: $PLAN_UNMAPPED"
        exit 1
    fi

    MOCK_TMP="$(mktemp -d)"
    # Verify --no-renames is passed on git diff calls so core->leaf renames include the deleted core path
    cat > "${MOCK_TMP}/mock-git-rename.sh" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "diff" ]]; then
    if [[ "$*" != *"--no-renames"* ]]; then
        # Simulate git rename collapsing source+dest to destination only when --no-renames is missing
        echo "internal/reach/store.go"
        exit 0
    fi
    if [[ "$*" == *"origin/main...HEAD"* ]]; then
        printf "internal/controlplane/store.go\ninternal/reach/store.go\n"
    fi
    exit 0
fi
if [[ "$1" == "ls-files" ]]; then
    exit 0
fi
exit 1
EOF
    chmod +x "${MOCK_TMP}/mock-git-rename.sh"
    PLAN_RENAME=$(G8S_IMPACTED_GIT_BIN="${MOCK_TMP}/mock-git-rename.sh" "$PRE_PUSH" --impacted-plan)
    if [[ "$PLAN_RENAME" != FULL\|core\ shared\ feature\ touched* ]]; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: expected FULL fallback on core->leaf rename via --no-renames, got: $PLAN_RENAME"
        exit 1
    fi
    # Mock git that fails working-tree diff while succeeding on base diff
    cat > "${MOCK_TMP}/mock-git.sh" <<'EOF'
#!/usr/bin/env bash
if [[ "$*" == *"origin/main...HEAD"* ]]; then
    echo "internal/reach/registry.go"
    exit 0
fi
exit 1
EOF
    chmod +x "${MOCK_TMP}/mock-git.sh"
    PLAN_GIT_FAIL=$(G8S_IMPACTED_GIT_BIN="${MOCK_TMP}/mock-git.sh" "$PRE_PUSH" --impacted-plan)
    if [[ "$PLAN_GIT_FAIL" != "FULL|git changed-path discovery failed" ]]; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: expected FULL fallback on partial git diff failure, got: $PLAN_GIT_FAIL"
        exit 1
    fi

    # Mock g8s returning empty JSON {} for collisions / inspect
    cat > "${MOCK_TMP}/mock-g8s-empty.sh" <<'EOF'
#!/usr/bin/env bash
if [[ "$*" == *"feature help"* ]]; then exit 0; fi
echo "{}"
EOF
    chmod +x "${MOCK_TMP}/mock-g8s-empty.sh"
    PLAN_MALFORMED=$(G8S_IMPACTED_G8S_BIN="${MOCK_TMP}/mock-g8s-empty.sh" "$PRE_PUSH" --impacted-plan --paths "internal/reach/registry.go")
    if [[ "$PLAN_MALFORMED" != "FULL|malformed collisions schema" ]]; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: expected FULL fallback on malformed collisions JSON {}, got: $PLAN_MALFORMED"
        exit 1
    fi

    # Mock g8s returning valid collisions but empty JSON {} for inspect
    cat > "${MOCK_TMP}/mock-g8s-badinspect.sh" <<'EOF'
#!/usr/bin/env bash
if [[ "$*" == *"feature help"* ]]; then exit 0; fi
if [[ "$*" == *"feature collisions"* ]]; then
    echo '{"paths":["internal/reach/registry.go"],"features":["FEAT-REACH-01"],"hotspots":[],"touches":[{"path":"internal/reach/registry.go","features":["FEAT-REACH-01"]}]}'
    exit 0
fi
echo "{}"
EOF
    chmod +x "${MOCK_TMP}/mock-g8s-badinspect.sh"
    PLAN_BAD_INSPECT=$(G8S_IMPACTED_G8S_BIN="${MOCK_TMP}/mock-g8s-badinspect.sh" "$PRE_PUSH" --impacted-plan --paths "internal/reach/registry.go")
    if [[ "$PLAN_BAD_INSPECT" != "FULL|malformed inspect schema for FEAT-REACH-01" ]]; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: expected FULL fallback on malformed inspect JSON {}, got: $PLAN_BAD_INSPECT"
        exit 1
    fi

    # Mock g8s returning valid collisions for a non-existent Go package target (vacuous/deleted package guard)
    cat > "${MOCK_TMP}/mock-g8s-ghostpkg.sh" <<'EOF'
#!/usr/bin/env bash
if [[ "$*" == *"feature help"* ]]; then exit 0; fi
if [[ "$*" == *"feature collisions"* ]]; then
    echo '{"paths":["internal/ghost_nonexistent_pkg/x.go"],"features":["FEAT-GHOST-01"],"hotspots":[],"touches":[{"path":"internal/ghost_nonexistent_pkg/x.go","features":["FEAT-GHOST-01"]}]}'
    exit 0
fi
if [[ "$*" == *"feature inspect"* ]]; then
    echo '{"feature":{"id":"FEAT-GHOST-01","interfaces":{"go_packages":["internal/ghost_nonexistent_pkg"]},"visible_tests":[]},"transitive_consumers":[]}'
    exit 0
fi
exit 1
EOF
    chmod +x "${MOCK_TMP}/mock-g8s-ghostpkg.sh"
    PLAN_GHOST=$(G8S_IMPACTED_G8S_BIN="${MOCK_TMP}/mock-g8s-ghostpkg.sh" "$PRE_PUSH" --impacted-plan --paths "internal/ghost_nonexistent_pkg/x.go")
    if [[ "$PLAN_GHOST" != FULL\|scoped\ package\ target* ]]; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: expected FULL fallback on ghost/non-existent package target, got: $PLAN_GHOST"
        exit 1
    fi

    # Verify verify_scoped_test_log parses structured `go test -json` events:
    # 1) rejects forged "--- PASS:" inside t.Log (Action=="output") when test actually skipped
    cat > "${MOCK_TMP}/forged-skip.jsonl" <<'EOF'
{"Action":"run","Package":"github.com/tamld/g8s/internal/reach","Test":"TestForgedSkip"}
{"Action":"output","Package":"github.com/tamld/g8s/internal/reach","Test":"TestForgedSkip","Output":"=== RUN   TestForgedSkip\n"}
{"Action":"output","Package":"github.com/tamld/g8s/internal/reach","Test":"TestForgedSkip","Output":"    --- PASS: Forged (0.00s)\n"}
{"Action":"skip","Package":"github.com/tamld/g8s/internal/reach","Test":"TestForgedSkip","Elapsed":0}
{"Action":"output","Package":"github.com/tamld/g8s/internal/reach","Output":"PASS\nok  \tgithub.com/tamld/g8s/internal/reach\t0.002s\n"}
{"Action":"pass","Package":"github.com/tamld/g8s/internal/reach","Elapsed":0.002}
EOF
    if "$PRE_PUSH" --verify-scoped-log "${MOCK_TMP}/forged-skip.jsonl" "github.com/tamld/g8s/internal/reach" >/dev/null 2>&1; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: --verify-scoped-log accepted forged t.Log('--- PASS:') in all-SKIP JSON stream"
        exit 1
    fi

    # 2) rejects multi-package run where one expected test package had all tests skipped
    cat > "${MOCK_TMP}/partial-skip.jsonl" <<'EOF'
{"Action":"run","Package":"pkg/a","Test":"TestA"}
{"Action":"pass","Package":"pkg/a","Test":"TestA","Elapsed":0.001}
{"Action":"pass","Package":"pkg/a","Elapsed":0.002}
{"Action":"run","Package":"pkg/b","Test":"TestB"}
{"Action":"skip","Package":"pkg/b","Test":"TestB","Elapsed":0}
{"Action":"pass","Package":"pkg/b","Elapsed":0.001}
EOF
    if "$PRE_PUSH" --verify-scoped-log "${MOCK_TMP}/partial-skip.jsonl" "pkg/a" "pkg/b" >/dev/null 2>&1; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: --verify-scoped-log accepted stream where pkg/b skipped all tests"
        exit 1
    fi

    # 3) rejects non-JSON matched-no-packages output
    cat > "${MOCK_TMP}/no-pkg.log" <<'EOF'
go: warning: "./internal/ghost/..." matched no packages
EOF
    if "$PRE_PUSH" --verify-scoped-log "${MOCK_TMP}/no-pkg.log" >/dev/null 2>&1; then
        rm -rf "$MOCK_TMP"
        echo "FAIL: --verify-scoped-log accepted a non-JSON matched-no-packages log"
        exit 1
    fi

    # 4) accepts real `go test -json` stream on ./internal/reach/... with per-package requirement
    CGO_ENABLED=0 go test -json -count=1 ./internal/reach/... > "${MOCK_TMP}/real-pass.jsonl" 2>&1
    REAL_VERIFY=$("$PRE_PUSH" --verify-scoped-log "${MOCK_TMP}/real-pass.jsonl" "github.com/tamld/g8s/internal/reach")
    rm -rf "$MOCK_TMP"
    if [[ "$REAL_VERIFY" != PASS_COUNT=* ]]; then
        echo "FAIL: --verify-scoped-log rejected real passing go test -json stream: $REAL_VERIFY"
        exit 1
    fi

    echo "  [PASS] --impacted-plan leaf scoping, --no-renames, hotspot, and structured go test -json non-vacuity guards verified"
fi

if [ "${1:-}" = "--unit-only" ]; then
    echo ""
    echo "[PRE-PUSH UNIT HARNESS TESTS PASSED]"
    exit 0
fi

# Test 2: Fast mode on current codebase
echo "--> Test 2: Verify --fast mode passes on current clean codebase..."
if ! "$PRE_PUSH" --fast; then
    echo "FAIL: --fast mode failed on clean codebase"
    exit 1
fi
echo "  [PASS] --fast mode passed"

echo ""
echo "[ALL PRE-PUSH HARNESS TESTS PASSED]"
exit 0
