#!/usr/bin/env bash
#
# Test suite for tools/pre_tag.sh fail-closed semantics (D-11, #986).
# Validates:
#   (1) gh CLI absent   -> check_ci_passes FAILS (release gate, fail-closed; was warn+skip)
#   (2) gh CLI present  -> check_ci_passes passes when workflows report success
#   (3) bare --force with existing remote tag  -> check_tag_not_exists FAILS (env ack required)
#   (4) --force + G8S_RELEASE_FORCE_TAG=1      -> check_tag_not_exists passes with loud acknowledgment
#   (5) no --force with existing remote tag    -> check_tag_not_exists FAILS (regression guard)
#   (6) check_winget_manifest (D-09 wiring): valid manifest passes, placeholder manifest fails
#   (7) full gate run with bare --force        -> exit 1 + typed acknowledgment error
#
# Mechanics: pre_tag.sh is sourced with G8S_PRE_TAG_SOURCE_ONLY=1 so individual
# check functions run without executing main().

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PRE_TAG="$SCRIPT_DIR/pre_tag.sh"

if [ ! -f "$PRE_TAG" ]; then
    echo "ERROR: pre_tag.sh not found at $PRE_TAG"
    exit 1
fi

echo "==> Running Pre-Tag Gate Fail-Closed Test Suite..."

FAILS=0

# --- fixture repo -----------------------------------------------------------

TMP="$(mktemp -d)"
trap 'chmod -R u+rwX "$TMP" 2>/dev/null; rm -rf "$TMP"' EXIT

FIXTURE="$TMP/fixture"
mkdir -p "$FIXTURE/cmd/g8s" "$FIXTURE/tools" "$FIXTURE/packaging/winget"

cat > "$FIXTURE/cmd/g8s/version.go" <<'EOF'
package main

var (
	Version   = "0.12.0"
	Commit    = "unknown"
	BuildTime = "unknown"
)
EOF

cat > "$FIXTURE/CHANGELOG.md" <<'EOF'
# Changelog

## [Unreleased]

## [0.12.0] - 2026-11-28

### Added
- Fixture release notes for 0.12.0
EOF

GOOD_SHA="b9eaf4c262096125b98c8d69e6af1459115c0343c6ee003440cfe61dd2101547"
cat > "$FIXTURE/packaging/winget/manifest.yaml" <<EOF
PackageIdentifier: TamLD.g8s
PackageVersion: 0.12.0
Installers:
  - Architecture: x64
    InstallerType: zip
    InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.12.0/g8s_0.12.0_windows_amd64.zip
    InstallerSha256: ${GOOD_SHA}
EOF

# Copy the winget gate so check_winget_manifest can run inside the fixture
cp "$SCRIPT_DIR/winget_manifest_check.sh" "$FIXTURE/tools/winget_manifest_check.sh"

# Bare origin remote carrying tag v0.12.0 (so the remote-tag branches trigger)
(
    cd "$FIXTURE" || exit 1
    git init -q
    git config user.email "test@example.com"
    git config user.name "Test User"
    git checkout -q -b main 2>/dev/null || git checkout -q -B main
    git add .
    git commit -q -m "chore: pre_tag fixture"
    git tag -a "v0.12.0" -m "Release 0.12.0"
    git clone --bare -q . "$TMP/origin.git"
    git remote add origin "$TMP/origin.git"
    git push -q origin main v0.12.0
) >/dev/null 2>&1

if [ ! -d "$TMP/origin.git" ]; then
    echo "ERROR: failed to prepare fixture git repo"
    exit 1
fi

# Stub gh that reports every workflow successful (for the gh-present pass case)
mkdir -p "$TMP/fakebin"
cat > "$TMP/fakebin/gh" <<'EOF'
#!/usr/bin/env bash
echo "success"
EOF
chmod +x "$TMP/fakebin/gh"

# run_check <name> <check-fn> [source-args...] — sources pre_tag.sh in the
# fixture and invokes one check function, echoing CHECK_RC=<rc> plus output.
run_check() {
    local fn="$1"
    shift
    local src_args=("$@")
    (
        cd "$FIXTURE" || exit 1
        export G8S_PRE_TAG_SOURCE_ONLY=1
        # shellcheck disable=SC1090
        source "$PRE_TAG" "${src_args[@]+"${src_args[@]}"}"
        rc=0
        "$fn" || rc=$?
        echo "CHECK_RC=$rc"
    ) 2>&1
}

# (1) gh absent -> check_ci_passes FAILS (D-11: release gate, fail-closed)
echo "Testing: (1) gh CLI absent -> check_ci_passes fails closed"
OUT1=$(PATH=/usr/bin:/bin run_check check_ci_passes)
RC1=$(printf '%s' "$OUT1" | sed -n 's/^CHECK_RC=//p' | tail -1)
if [ "${RC1:-}" = "1" ] && printf '%s' "$OUT1" | grep -q "gh CLI not available"; then
    echo "  ok (rc=1, typed fail-closed error emitted)"
else
    echo "  FAIL: expected CHECK_RC=1 + 'gh CLI not available', got rc='${RC1:-unset}'"
    printf '%s\n' "$OUT1" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

# (2) gh present (stub reporting success) -> check_ci_passes passes
echo "Testing: (2) gh CLI present with successful workflows -> check_ci_passes passes"
OUT2=$(PATH="$TMP/fakebin:/usr/bin:/bin" run_check check_ci_passes)
RC2=$(printf '%s' "$OUT2" | sed -n 's/^CHECK_RC=//p' | tail -1)
if [ "${RC2:-}" = "0" ]; then
    echo "  ok (rc=0, all workflows success)"
else
    echo "  FAIL: expected CHECK_RC=0, got rc='${RC2:-unset}'"
    printf '%s\n' "$OUT2" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

# (3) bare --force with existing remote tag -> FAILS (env acknowledgment required)
echo "Testing: (3) bare --force with existing remote tag -> check_tag_not_exists fails"
OUT3=$(run_check check_tag_not_exists --force)
RC3=$(printf '%s' "$OUT3" | sed -n 's/^CHECK_RC=//p' | tail -1)
if [ "${RC3:-}" = "1" ] && printf '%s' "$OUT3" | grep -q "G8S_RELEASE_FORCE_TAG=1 to acknowledge"; then
    echo "  ok (rc=1, typed acknowledgment error emitted)"
else
    echo "  FAIL: expected CHECK_RC=1 + acknowledgment error, got rc='${RC3:-unset}'"
    printf '%s\n' "$OUT3" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

# (4) --force + G8S_RELEASE_FORCE_TAG=1 -> passes with loud acknowledgment
echo "Testing: (4) --force with G8S_RELEASE_FORCE_TAG=1 -> passes with loud warning"
OUT4=$(G8S_RELEASE_FORCE_TAG=1 run_check check_tag_not_exists --force)
RC4=$(printf '%s' "$OUT4" | sed -n 's/^CHECK_RC=//p' | tail -1)
if [ "${RC4:-}" = "0" ] && printf '%s' "$OUT4" | grep -q "FORCED TAG RE-PUSH ACKNOWLEDGED"; then
    echo "  ok (rc=0, loud forced re-push warning emitted)"
else
    echo "  FAIL: expected CHECK_RC=0 + 'FORCED TAG RE-PUSH ACKNOWLEDGED', got rc='${RC4:-unset}'"
    printf '%s\n' "$OUT4" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

# (5) no --force with existing remote tag -> FAILS (pre-existing behavior)
echo "Testing: (5) no --force with existing remote tag -> check_tag_not_exists fails"
OUT5=$(run_check check_tag_not_exists)
RC5=$(printf '%s' "$OUT5" | sed -n 's/^CHECK_RC=//p' | tail -1)
if [ "${RC5:-}" = "1" ]; then
    echo "  ok (rc=1, existing fail-closed behavior retained)"
else
    echo "  FAIL: expected CHECK_RC=1, got rc='${RC5:-unset}'"
    printf '%s\n' "$OUT5" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

# (6) check_winget_manifest: valid fixture manifest passes, placeholder fails
echo "Testing: (6a) check_winget_manifest with valid manifest -> passes"
OUT6A=$(run_check check_winget_manifest)
RC6A=$(printf '%s' "$OUT6A" | sed -n 's/^CHECK_RC=//p' | tail -1)
if [ "${RC6A:-}" = "0" ]; then
    echo "  ok (rc=0)"
else
    echo "  FAIL: expected CHECK_RC=0, got rc='${RC6A:-unset}'"
    printf '%s\n' "$OUT6A" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

echo "Testing: (6b) check_winget_manifest with placeholder manifest -> fails"
cat > "$FIXTURE/packaging/winget/manifest.yaml" <<EOF
PackageIdentifier: TamLD.g8s
PackageVersion: 0.12.0
Installers:
  - Architecture: x64
    InstallerType: zip
    InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.12.0/g8s_0.12.0_windows_amd64.zip
    InstallerSha256: <from checksums.txt>
EOF
OUT6B=$(run_check check_winget_manifest)
RC6B=$(printf '%s' "$OUT6B" | sed -n 's/^CHECK_RC=//p' | tail -1)
if [ "${RC6B:-}" = "1" ] && printf '%s' "$OUT6B" | grep -q "must be replaced from release checksums.txt"; then
    echo "  ok (rc=1, placeholder rejected)"
else
    echo "  FAIL: expected CHECK_RC=1 + placeholder rejection, got rc='${RC6B:-unset}'"
    printf '%s\n' "$OUT6B" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

# (7) full gate run with bare --force (no env) -> exit 1 + typed error
echo "Testing: (7) full pre_tag.sh run with bare --force -> exit 1 with typed error"
OUT7=""
RC7=0
OUT7=$( (cd "$FIXTURE" && unset G8S_RELEASE_FORCE_TAG G8S_PRE_TAG_SOURCE_ONLY; bash "$PRE_TAG" --force) 2>&1 ) || RC7=$?
if [ "$RC7" -ne 0 ] && printf '%s' "$OUT7" | grep -q "G8S_RELEASE_FORCE_TAG=1 to acknowledge"; then
    echo "  ok (exit $RC7, typed acknowledgment error surfaced in full gate run)"
else
    echo "  FAIL: expected exit 1 + typed acknowledgment error, got exit $RC7"
    printf '%s\n' "$OUT7" | sed 's/^/    /'
    FAILS=$((FAILS + 1))
fi

if [ "$FAILS" -eq 0 ]; then
    echo ""
    echo "PRE-TAG GATE FAIL-CLOSED TEST SUITE: all tests passed."
    exit 0
fi
echo ""
echo "PRE-TAG GATE FAIL-CLOSED TEST SUITE: $FAILS failure(s)."
exit 1
