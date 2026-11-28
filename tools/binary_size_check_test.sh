#!/usr/bin/env bash
#
# Test suite for tools/binary_size_check.sh (D-6, #986).
# Validates the machine binding for the "<20 MiB per release architecture"
# README/claims.yml claim:
#   (1) --binary fixture within the default bound         -> exit 0
#   (2) --binary fixture above a tighter --limit-bytes    -> exit 1 + bound-exceeded message
#   (3a) strict bound: --limit-bytes == exact file size   -> exit 1 (must be strictly smaller)
#   (3b) strict bound: --limit-bytes == exact size + 1    -> exit 0
#   (4) --binary missing file                             -> exit 2
#   (5) unknown --target                                  -> exit 2 (release matrix enforced)
#   (6) unknown argument                                  -> exit 2
#   (7) real host build with the default 20 MiB bound     -> exit 0 (the claim itself; SKIP if no go)
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$SCRIPT_DIR/binary_size_check.sh"

if [ ! -f "$GATE" ]; then
    echo "ERROR: binary size gate script not found at $GATE"
    exit 1
fi

echo "==> Running Binary Size Check Test Suite..."

FAILS=0

run_case() {
    local desc="$1"
    local expect_code="$2"
    local pattern="$3"   # empty string = no output pattern assertion
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILS=$((FAILS + 1))
    elif [ -n "$pattern" ] && ! echo "$output" | grep -q "$pattern"; then
        echo "  FAIL: output did not match pattern '$pattern'"
        echo "$output" | sed 's/^/    /'
        FAILS=$((FAILS + 1))
    else
        echo "  ok (exit $exit_code${pattern:+, pattern matched})"
    fi
}

TMP="$(mktemp -d)"
trap 'chmod -R u+rwX "$TMP" 2>/dev/null; rm -rf "$TMP"' EXIT

# Fixtures: deterministic byte files
head -c 2048 /dev/zero > "$TMP/blob_2048.bin" 2>/dev/null || printf '%2048s' '' > "$TMP/blob_2048.bin"
head -c 100 /dev/zero > "$TMP/blob_100.bin" 2>/dev/null || printf '%100s' '' > "$TMP/blob_100.bin"

# (1) 2048-byte fixture vs default 20 MiB bound -> exit 0
run_case \
    "(1) small fixture within the default 20 MiB bound passes" 0 \
    "20971520 bytes" \
    bash "$GATE" --binary "$TMP/blob_2048.bin"

# (2) 2048-byte fixture vs tighter 1024-byte bound -> exit 1 + typed message
run_case \
    "(2) fixture above a tighter bound fails with typed message" 1 \
    "binary size bound exceeded" \
    bash "$GATE" --binary "$TMP/blob_2048.bin" --limit-bytes 1024

# (3a) strict '<' semantics: limit == exact size must FAIL
run_case \
    "(3a) strict bound: limit equal to exact size fails (must be strictly smaller)" 1 \
    "binary size bound exceeded" \
    bash "$GATE" --binary "$TMP/blob_100.bin" --limit-bytes 100

# (3b) strict '<' semantics: limit == exact size + 1 passes
run_case \
    "(3b) strict bound: limit of exact size + 1 passes" 0 \
    "bound" \
    bash "$GATE" --binary "$TMP/blob_100.bin" --limit-bytes 101

# (4) missing --binary file -> exit 2
run_case \
    "(4) missing binary file is a usage/file error" 2 \
    "binary file not found" \
    bash "$GATE" --binary "$TMP/does-not-exist.bin"

# (5) unknown --target -> exit 2 with the release matrix listed
run_case \
    "(5) unknown --target is a usage error listing the release matrix" 2 \
    "valid targets:" \
    bash "$GATE" --target plan9/riscv

# (6) unknown argument -> exit 2
run_case \
    "(6) unknown argument is a usage error" 2 \
    "unknown argument" \
    bash "$GATE" --bogus

# (7) real host build vs the default 20 MiB bound — the claim proof itself.
# Mirrors the gofumpt_check_test.sh SKIP convention when prerequisites are absent.
if ! command -v go >/dev/null 2>&1; then
    echo "SKIP (7): go toolchain not available — real-build claim proof needs go"
else
    run_case \
        "(7) real host-target release build stays under the 20 MiB bound (claim proof)" 0 \
        "bound" \
        bash "$GATE"
fi

if [ "$FAILS" -eq 0 ]; then
    echo ""
    echo "BINARY SIZE CHECK TEST SUITE: all tests passed."
    exit 0
fi
echo ""
echo "BINARY SIZE CHECK TEST SUITE: $FAILS failure(s)."
exit 1
