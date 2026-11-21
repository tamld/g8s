#!/usr/bin/env bash
# gofumpt_check_test.sh — negative/positive acceptance for the fail-closed
# gate (issue #1021, audit M-03). Self-contained: installs a pinned gofumpt
# into a temp GOBIN (tests 2-3), strips it from PATH (test 1).
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$SCRIPT_DIR/gofumpt_check.sh"
GOFUMPT_VERSION="v0.7.0"
FAILS=0

TMP=$(mktemp -d)
trap 'chmod -R u+rwX "$TMP" 2>/dev/null; rm -rf "$TMP"' EXIT
export GOBIN="$TMP/bin"
export GOPATH="$TMP/gopath"
export GOCACHE="$TMP/gocache"
export GOFLAGS=-mod=mod
mkdir -p "$GOBIN"

# Install the pinned gofumpt once (tests 2-3 need it on PATH).
if ! go install "mvdan.cc/gofumpt@$GOFUMPT_VERSION" >/dev/null 2>&1; then
    echo "SKIP: go install gofumpt failed (offline?) — install tests need network"
    exit 77
fi

# Test 1: tool absent from PATH → exit 1 + the install error line.
mkdir -p "$TMP/empty_repo"
touch "$TMP/empty_repo/x.go"
OUT1=$(env PATH=/usr/bin:/bin HOME="$HOME" bash "$GATE" "$TMP/empty_repo" 2>&1)
RC1=$?
if [ "$RC1" -ne 0 ] && echo "$OUT1" | grep -q "gofumpt is not installed"; then
    echo "PASS  missing-tool → exit 1 + install error"
else
    echo "FAIL  missing-tool (rc=$RC1 out=$OUT1)"; FAILS=$((FAILS+1))
fi

# Test 2: clean formatted tree → exit 0 (gofumpt on PATH).
mkdir -p "$TMP/clean/pkg"
printf 'package pkg\n\nfunc F() int {\n\treturn 1\n}\n' > "$TMP/clean/pkg/f.go"
OUT2=$(cd "$TMP/clean" && PATH="$GOBIN:$PATH" bash "$GATE" . 2>&1)
RC2=$?
if [ "$RC2" -eq 0 ]; then
    echo "PASS  clean tree → exit 0"
else
    echo "FAIL  clean tree (rc=$RC2 out=$OUT2)"; FAILS=$((FAILS+1))
fi

# Test 3: unformatted file → exit 1 + file listed.
mkdir -p "$TMP/dirty/pkg"
printf 'package pkg\nfunc G() int {\nreturn 2\n}\n' > "$TMP/dirty/pkg/g.go"
OUT3=$(PATH="$GOBIN:$PATH" bash "$GATE" "$TMP/dirty" 2>&1)
RC3=$?
if [ "$RC3" -ne 0 ] && echo "$OUT3" | grep -q "g.go"; then
    echo "PASS  unformatted → exit 1 + file listed"
else
    echo "FAIL  unformatted (rc=$RC3 out=$OUT3)"; FAILS=$((FAILS+1))
fi

if [ "$FAILS" -eq 0 ]; then
    echo "gofumpt_check_test: ALL TESTS PASSED"
    exit 0
fi
echo "gofumpt_check_test: $FAILS FAILURE(S)"
exit 1
