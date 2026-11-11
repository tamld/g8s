#!/usr/bin/env bash
#
# tools/pre_push.sh — Local Pre-Push CI/CD Quality Gate Harness
#
# Local pre-push parity with .github/workflows/quality.yml (ADR-0031 two lanes).
# Docs lane: hygiene, AI/brief lint, doc-contract, claims, version sync, link integrity.
# Build lane: above doc gates + format, layer, vet, golangci (gosec/staticcheck), semgrep,
#   tests/coverage (80%), optional -race, dogfood, optional cross-platform.
# CI-only (not claimed here): gitleaks, govulncheck, CodeQL, coverage ratchet, D-09 reflection.
#
# Usage:
#   ./tools/pre_push.sh              # Run full suite before push
#   ./tools/pre_push.sh --fast       # Fast mode (skips -race & cross-platform)
#   ./tools/pre_push.sh --impacted   # Scope Go tests via .g8s/feature-map.yml (fallback to full on hotspots/core)
#   ./tools/pre_push.sh --fix        # Auto-format with gofmt & gofumpt first
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Unset git hook environment variables so sub-tests (e.g. TestCleanupWorktrees) don't bleed into git repo
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_PREFIX

# Ensure tools in GOPATH/bin are available
GOPATH_BIN="$(go env GOPATH)/bin"
export PATH="$GOPATH_BIN:$PATH"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

FAST_MODE=0
AUTO_FIX=0
IMPACTED_MODE=0
IMPACTED_PLAN_ONLY=0
IMPACTED_OVERRIDE_PATHS=""
VERIFY_SCOPED_LOG=""
# Build-lane step count (--fast skips race + cross-platform sub-steps only).
BUILD_STEPS=15

while [ $# -gt 0 ]; do
    case "$1" in
        --fast|-f)
            FAST_MODE=1
            shift
            ;;
        --fix)
            AUTO_FIX=1
            shift
            ;;
        --impacted)
            IMPACTED_MODE=1
            shift
            ;;
        --impacted-plan)
            IMPACTED_MODE=1
            IMPACTED_PLAN_ONLY=1
            shift
            if [ $# -gt 0 ] && [ "${1:-}" = "--paths" ]; then
                if [ $# -lt 2 ]; then
                    echo "ERROR: --paths requires a comma-separated path list" >&2
                    exit 1
                fi
                IMPACTED_OVERRIDE_PATHS="$2"
                shift 2
            fi
            ;;
        --verify-scoped-log)
            if [ $# -lt 2 ]; then
                echo "ERROR: --verify-scoped-log requires a log file path" >&2
                exit 1
            fi
            VERIFY_SCOPED_LOG="$2"
            shift 2
            break
            ;;
        --help|-h)
            echo "Usage: $0 [--fast|-f] [--impacted] [--fix] [--help|-h]"
            echo ""
            echo "Options:"
            echo "  --fast, -f    Skip race detector and cross-platform compilation (faster run)"
            echo "  --impacted    Scope Go tests to impacted features/consumers via .g8s/feature-map.yml"
            echo "                (falls back to full ./... suite on hotspots, core/shared packages, or unmapped paths)"
            echo "  --fix         Auto-apply gofmt and gofumpt fixes before checking"
            echo "  --help, -h    Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown argument: $1"
            echo "Run '$0 --help' for usage."
            exit 1
            ;;
    esac
done

# resolve_impacted_packages prints either "FULL|<reason>" or "SCOPED|<space-separated ./pkg/... list>".
resolve_impacted_packages() {
    local override_csv="${1:-}"
    if [ ! -f ".g8s/feature-map.yml" ]; then
        echo "FULL|missing .g8s/feature-map.yml"
        return 0
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        echo "FULL|python3 unavailable"
        return 0
    fi
    local g8s_bin="${G8S_IMPACTED_G8S_BIN:-./bin/g8s}"
    if [ ! -x "$g8s_bin" ] || ! "$g8s_bin" feature help >/dev/null 2>&1; then
        echo "FULL|g8s binary unavailable ($g8s_bin)"
        return 0
    fi

    python3 - "$override_csv" "$g8s_bin" "${G8S_IMPACTED_GIT_BIN:-git}" <<'PY'
import glob
import json
import os
import subprocess
import sys

override_csv = sys.argv[1].strip()
g8s_bin = sys.argv[2]
git_bin = sys.argv[3]

def run_lines(cmd):
    try:
        proc = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    except OSError:
        return None
    if proc.returncode != 0:
        return None
    return [ln.strip() for ln in proc.stdout.splitlines() if ln.strip()]

if override_csv == "__EMPTY__":
    changed = []
elif override_csv:
    changed = [p.strip() for p in override_csv.split(",") if p.strip()]
else:
    base_lines = run_lines([git_bin, "diff", "--no-renames", "--name-only", "origin/main...HEAD"])
    wt_lines = run_lines([git_bin, "diff", "--no-renames", "--name-only"])
    st_lines = run_lines([git_bin, "diff", "--no-renames", "--cached", "--name-only"])
    ut_lines = run_lines([git_bin, "ls-files", "--others", "--exclude-standard"])
    if base_lines is None or wt_lines is None or st_lines is None or ut_lines is None:
        print("FULL|git changed-path discovery failed")
        sys.exit(0)
    changed = sorted(set(base_lines + wt_lines + st_lines + ut_lines))

if not changed:
    print("FULL|no diff vs origin/main")
    sys.exit(0)

full_trigger_exact = {"go.mod", "go.sum", "offer.go", "offer_test.go", "Makefile", ".golangci.yml"}
for p in changed:
    if "," in p:
        print("FULL|comma in changed path")
        sys.exit(0)
    if p in full_trigger_exact or ("/" not in p and p.endswith(".go")):
        print(f"FULL|root build/module file changed ({p})")
        sys.exit(0)

csv_arg = ",".join(changed)
col_proc = subprocess.run(
    [g8s_bin, "feature", "collisions", "--file", ".g8s/feature-map.yml", "--paths", csv_arg, "--json"],
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
)
if col_proc.returncode != 0:
    print("FULL|feature collisions query failed")
    sys.exit(0)

try:
    col = json.loads(col_proc.stdout)
except Exception:
    print("FULL|invalid collisions JSON")
    sys.exit(0)

if not isinstance(col, dict) or "features" not in col or "hotspots" not in col or "touches" not in col:
    print("FULL|malformed collisions schema")
    sys.exit(0)

if col.get("unmapped_paths"):
    print("FULL|unmapped paths present")
    sys.exit(0)

touches = col.get("touches")
if not isinstance(touches, list) or len(touches) != len(changed):
    print("FULL|incomplete collisions touch mapping")
    sys.exit(0)
for t_entry in touches:
    if not isinstance(t_entry, dict) or (not t_entry.get("features") and not t_entry.get("hotspots")):
        print("FULL|untouched/unowned path in collisions response")
        sys.exit(0)

for hs in col.get("hotspots") or []:
    hid = hs.get("id", "") if isinstance(hs, dict) else str(hs)
    print(f"FULL|hotspot touched ({hid or 'unknown'})")
    sys.exit(0)

seed_features = list(col.get("features") or [])
if not seed_features:
    print("FULL|no feature-owned paths in diff")
    sys.exit(0)

core_features = {"FEAT-CONTROLPLANE-01", "FEAT-HARNESS-CORE-01", "FEAT-FSOPS-01"}
for fid in seed_features:
    if fid in core_features:
        print(f"FULL|core shared feature touched ({fid})")
        sys.exit(0)

all_features = set(seed_features)
pkgs = set()

def add_pkg_from_path(rel):
    rel = rel.lstrip("./")
    if rel.startswith("cmd/g8s/") or rel == "cmd/g8s":
        pkgs.add("./cmd/g8s/...")
    elif rel.startswith("internal/"):
        parts = rel.split("/")
        if len(parts) >= 2 and parts[1] and "*" not in parts[1]:
            pkgs.add(f"./internal/{parts[1]}/...")

for p in changed:
    if p.endswith(".go"):
        add_pkg_from_path(p)

inspected = {}
queue = list(seed_features)
while queue:
    fid = queue.pop(0)
    if fid in inspected:
        continue
    iproc = subprocess.run(
        [g8s_bin, "feature", "inspect", fid, "--file", ".g8s/feature-map.yml", "--json"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if iproc.returncode != 0:
        print(f"FULL|feature inspect failed for {fid}")
        sys.exit(0)
    try:
        idata = json.loads(iproc.stdout)
    except Exception:
        print(f"FULL|invalid inspect JSON for {fid}")
        sys.exit(0)
    if not isinstance(idata, dict) or not isinstance(idata.get("feature"), dict):
        print(f"FULL|malformed inspect schema for {fid}")
        sys.exit(0)
    feat_obj = idata["feature"]
    if feat_obj.get("id") != fid or not isinstance(feat_obj.get("interfaces"), dict) or "transitive_consumers" not in idata:
        print(f"FULL|incomplete inspect payload for {fid}")
        sys.exit(0)
    inspected[fid] = idata
    for tc in idata.get("transitive_consumers") or []:
        if tc in core_features:
            print(f"FULL|core shared consumer impacted ({tc})")
            sys.exit(0)
        if tc not in all_features:
            all_features.add(tc)
            queue.append(tc)

if len(all_features) > 6:
    print(f"FULL|wide blast radius ({len(all_features)} features impacted)")
    sys.exit(0)

for fid, idata in inspected.items():
    feat = idata["feature"]
    ifaces = feat["interfaces"]
    for gp in ifaces.get("go_packages") or []:
        gp = gp.strip().lstrip("./").rstrip("/")
        if gp:
            pkgs.add(f"./{gp}/...")
    if fid in seed_features:
        for vpath in (feat.get("visible_tests") or []):
            add_pkg_from_path(vpath)

if not pkgs:
    print("FULL|no scoped Go packages resolved on build lane")
    sys.exit(0)

sorted_pkgs = sorted(pkgs)
list_proc = subprocess.run(
    ["go", "list", "-f", "{{.Dir}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}"] + sorted_pkgs,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
)
if list_proc.returncode != 0 or "matched no packages" in (list_proc.stderr or ""):
    print("FULL|scoped package target failed go list validation")
    sys.exit(0)

list_lines = [ln.strip() for ln in list_proc.stdout.splitlines() if ln.strip()]
if not list_lines:
    print("FULL|scoped package target resolved zero Go packages")
    sys.exit(0)

total_test_files = 0
for ln in list_lines:
    parts = ln.split("|")
    if len(parts) == 3:
        total_test_files += int(parts[1]) + int(parts[2])
if total_test_files == 0:
    print("FULL|scoped packages contain zero Go test files")
    sys.exit(0)

print("SCOPED|" + " ".join(sorted_pkgs))
PY
}

# verify_scoped_test_log validates structured `go test -json` event streams:
# - rejects any non-JSON line (e.g. "matched no packages" or build error text),
# - rejects any Action=="fail" event,
# - ignores arbitrary text inside Action=="output" (defeating forged "--- PASS:" via t.Log),
# - requires >=1 test-level Action=="pass" (non-empty Test) AND, for every expected test-bearing
#   import path passed after log_file, requires both package-level Action=="pass" and >=1 test-level Action=="pass".
verify_scoped_test_log() {
    local log_file="$1"
    shift || true
    if [ ! -f "$log_file" ]; then
        echo "missing test log: $log_file" >&2
        return 1
    fi
    python3 - "$log_file" "$@" <<'PY'
import json
import sys

log_path = sys.argv[1]
expected_test_pkgs = [p.strip() for p in sys.argv[2:] if p.strip()]

pkg_passed = set()
test_passes_by_pkg = {}
total_test_passes = 0
line_count = 0

with open(log_path, "r", encoding="utf-8", errors="replace") as fh:
    for idx, raw in enumerate(fh, 1):
        ln = raw.strip()
        if not ln:
            continue
        line_count += 1
        try:
            ev = json.loads(ln)
        except Exception:
            sys.stderr.write(f"non-JSON line {idx} in go test -json stream: {ln[:120]}\n")
            sys.exit(1)
        if not isinstance(ev, dict):
            sys.stderr.write(f"non-object JSON event on line {idx}\n")
            sys.exit(1)
        action = ev.get("Action")
        pkg = ev.get("Package")
        test_name = ev.get("Test")
        if action == "fail":
            sys.stderr.write(f"failing event detected for package={pkg} test={test_name}\n")
            sys.exit(1)
        if action == "pass" and isinstance(pkg, str) and pkg:
            if isinstance(test_name, str) and test_name:
                total_test_passes += 1
                test_passes_by_pkg[pkg] = test_passes_by_pkg.get(pkg, 0) + 1
            elif test_name is None or test_name == "":
                pkg_passed.add(pkg)

if line_count == 0 or not pkg_passed:
    sys.stderr.write("scoped go test -json stream produced zero passing package events\n")
    sys.exit(1)
if total_test_passes < 1:
    sys.stderr.write("scoped go test -json stream executed zero test-level Action=pass verdicts\n")
    sys.exit(1)

for req_pkg in expected_test_pkgs:
    if req_pkg not in pkg_passed:
        sys.stderr.write(f"expected test package did not emit package-level Action=pass: {req_pkg}\n")
        sys.exit(1)
    if test_passes_by_pkg.get(req_pkg, 0) < 1:
        sys.stderr.write(f"expected test package had zero test-level Action=pass verdicts (all skipped/vacuous): {req_pkg}\n")
        sys.exit(1)

print(f"PASS_COUNT={total_test_passes}")
PY
}

if [ -n "$VERIFY_SCOPED_LOG" ]; then
    verify_scoped_test_log "$VERIFY_SCOPED_LOG" "$@"
    exit $?
fi

if [ "$IMPACTED_PLAN_ONLY" -eq 1 ]; then
    resolve_impacted_packages "$IMPACTED_OVERRIDE_PATHS"
    exit 0
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m' # No Color

pass() {
    printf "  ${GREEN}✓${NC} %s\n" "$1"
}

fail() {
    printf "  ${RED}✗ [FAILED]${NC} %s\n" "$1"
    echo ""
    echo -e "${RED}${BOLD}Pre-push checks FAILED.${NC} Fix the issues above before pushing to remote."
    exit 1
}

step() {
    printf "\n${BLUE}${BOLD}[%s] %s${NC}\n" "$1" "$2"
}

# ADR-0031: CI/CD two-lane detection (Docs Lane vs Build Lane)
DETECTED_LANE_RAW="$(bash tools/ci_lane_detect.sh origin/main 2>/dev/null || echo "lane=build")"
if [ "$DETECTED_LANE_RAW" = "lane=docs" ] || [ "$DETECTED_LANE_RAW" = "docs" ]; then
    DETECTED_LANE="docs"
else
    DETECTED_LANE="build"
fi

if [ "$DETECTED_LANE" = "docs" ]; then
    echo -e "${BOLD}======================================================${NC}"
    echo -e "${BOLD}       g8s Pre-Push Verification Harness (DOCS)       ${NC}"
    echo -e "${BOLD}======================================================${NC}"
    echo "DOCS LANE: 6 gates"

    # 1. Git Hygiene Gate
    step "1/6" "Verifying Git Hygiene..."
    for f in coverage.out cover.out *.cover; do
        if git diff --cached --name-only | grep -q "^$f\$"; then
            fail "Test artifact $f is staged for commit. Remove it and add to .gitignore."
        fi
    done
    UNTRACKED=$(git status --porcelain -uall 2>&1 | grep -c '^??' || true)
    if [ "$UNTRACKED" -gt 15 ]; then
        fail "Too many untracked files ($UNTRACKED > 15). Clean up or gitignore."
    fi
    pass "Git hygiene clean"

    # 2. AI & Brief Anti-Pattern Gate (DEBT-21 + DEBT-51)
    step "2/6" "Running AI & Brief Anti-Pattern Gates..."
    bash tools/ai_lint_test.sh >/dev/null
    if ! bash tools/ai_lint.sh; then
        fail "AI anti-patterns detected in source code."
    fi
    bash tools/brief_lint_test.sh >/dev/null
    if ! bash tools/brief_lint.sh docs/ spec/ docs/decisions/ skills/; then
        fail "Brief anti-patterns detected."
    fi
    pass "AI and brief anti-pattern checks clean"

    # 3. Documentation ↔ Code Contract Gate (Issue #208)
    step "3/6" "Running Doc ↔ Code Contract Gate (Issue #208)..."
    if ! bash tools/ci_doc_contract_check.sh; then
        fail "Documentation and code contracts are out of sync."
    fi
    pass "Doc-code contracts synchronized"

    # 4. Quantitative Claims Gate (#447)
    step "4/6" "Running Quantitative Claims Gate (#447)..."
    if ! bash tools/claims_check.sh; then
        fail "Quantitative claims check failed."
    fi
    pass "Quantitative claims verified"

    # 5. Version Sync Gate
    step "5/6" "Running Version Sync Check..."
    if ! bash tools/ci_version_sync_check.sh; then
        fail "Version sync check failed."
    fi
    pass "Version sync verified"

    # 6. Link Integrity Gate
    step "6/6" "Running Link Integrity Gate..."
    if ! bash tools/ci_link_integrity.sh; then
        fail "Link integrity check failed."
    fi
    pass "Link integrity verified"

    echo ""
    echo -e "${GREEN}${BOLD}======================================================${NC}"
    echo -e "${GREEN}${BOLD}  ✓ ALL PRE-PUSH CHECKS PASSED (DOCS LANE)!           ${NC}"
    echo -e "${GREEN}${BOLD}======================================================${NC}"
    exit 0
fi

echo -e "${BOLD}======================================================${NC}"
echo -e "${BOLD}       g8s Pre-Push Verification Harness (BUILD)      ${NC}"
echo -e "${BOLD}======================================================${NC}"

# 1. Git Hygiene Gate
step "1/${BUILD_STEPS}" "Verifying Git Hygiene..."
for f in coverage.out cover.out *.cover; do
    if git diff --cached --name-only | grep -q "^$f\$"; then
        fail "Test artifact $f is staged for commit. Remove it and add to .gitignore."
    fi
done
UNTRACKED=$(git status --porcelain -uall 2>&1 | grep -c '^??' || true)
if [ "$UNTRACKED" -gt 15 ]; then
    fail "Too many untracked files ($UNTRACKED > 15). Clean up or gitignore."
fi
pass "Git hygiene clean"

# 2. Formatting Gate
step "2/${BUILD_STEPS}" "Verifying Code Formatting (gofmt & gofumpt)..."
if [ "$AUTO_FIX" -eq 1 ]; then
    gofmt -w .
    if command -v gofumpt >/dev/null 2>&1; then
        gofumpt -w .
    fi
fi
UNFORMATTED=$(gofmt -l . | grep -v '^reference/' || true)
if [ -n "$UNFORMATTED" ]; then
    echo "Unformatted files found by gofmt:"
    echo "$UNFORMATTED"
    fail "Files above are not gofmt-clean. Run 'gofmt -w <file>' or '$0 --fix'."
fi
if command -v gofumpt >/dev/null 2>&1; then
    UNFORMATTED_FUMPT=$(gofumpt -l . 2>/dev/null | grep -v '^reference/' || true)
    if [ -n "$UNFORMATTED_FUMPT" ]; then
        echo "Unformatted files found by gofumpt:"
        echo "$UNFORMATTED_FUMPT"
        fail "Files above are not gofumpt-clean. Run 'gofumpt -w <file>' or '$0 --fix'."
    fi
fi
pass "gofmt and gofumpt formatting clean"

# 3. Layer Ownership Gate
step "3/${BUILD_STEPS}" "Running Layer Ownership Gate (DEBT-34)..."
bash tools/ci_layer_check_test.sh >/dev/null
pass "Layer check self-tests passed"
if ! bash tools/ci_layer_check.sh; then
    fail "Layer ownership rules violated (DEBT-34)."
fi
pass "Layer ownership verified"

# 4. AI Anti-Pattern Gate
step "4/${BUILD_STEPS}" "Running AI Anti-Pattern Gate (DEBT-21)..."
bash tools/ai_lint_test.sh >/dev/null
pass "AI lint self-tests passed"
if ! bash tools/ai_lint.sh; then
    fail "AI anti-patterns detected in source code."
fi
pass "AI anti-pattern checks clean"

# 5. Brief Anti-Pattern Gate
step "5/${BUILD_STEPS}" "Running Brief Anti-Pattern Gate (DEBT-51)..."
bash tools/brief_lint_test.sh >/dev/null
pass "Brief lint self-tests passed"
if ! bash tools/brief_lint.sh docs/ spec/ docs/decisions/ skills/; then
    fail "Brief anti-patterns detected."
fi
pass "Brief anti-pattern checks clean"

# 6. Quantitative Claims Gate (#447) — quality.yml blocking step
step "6/${BUILD_STEPS}" "Running Quantitative Claims Gate (#447)..."
if ! bash tools/claims_check.sh; then
    fail "Quantitative claims check failed."
fi
pass "Quantitative claims verified"

# 7. Documentation ↔ Code Contract Gate
step "7/${BUILD_STEPS}" "Running Doc ↔ Code Contract Gate (Issue #208)..."
if ! bash tools/ci_doc_contract_check.sh; then
    fail "Documentation and code contracts are out of sync."
fi
pass "Doc-code contracts synchronized"

# 8. Version Sync Gate
step "8/${BUILD_STEPS}" "Running Version Sync Check..."
if ! bash tools/ci_version_sync_check.sh; then
    fail "Version sync check failed."
fi
pass "Version sync verified"

# 9. Link Integrity Gate (quality.yml — all lanes)
step "9/${BUILD_STEPS}" "Running Link Integrity Gate..."
if ! bash tools/ci_link_integrity.sh; then
    fail "Link integrity check failed."
fi
pass "Link integrity verified"

# 10. Pure-Go Vet Gate
step "10/${BUILD_STEPS}" "Running Pure-Go Vet Gate (CGO_ENABLED=0)..."
if ! CGO_ENABLED=0 go vet ./...; then
    fail "go vet reported errors."
fi
pass "go vet clean"

# 11. GolangCI-Lint & Gosec SAST Quality Gate
step "11/${BUILD_STEPS}" "Running GolangCI-Lint & Gosec SAST Quality Gate..."
LINTER_BIN=""
if [ -x "$GOPATH_BIN/golangci-lint" ]; then
    LINTER_BIN="$GOPATH_BIN/golangci-lint"
elif command -v golangci-lint >/dev/null 2>&1; then
    LINTER_BIN="golangci-lint"
fi

if [ -n "$LINTER_BIN" ]; then
    GO_MOD_TOOLCHAIN="go$(awk '/^go / {print $2}' go.mod)"
    if ! GOTOOLCHAIN="$GO_MOD_TOOLCHAIN" "$LINTER_BIN" run --timeout=10m; then
        fail "golangci-lint (including gosec) reported issues."
    fi
    pass "golangci-lint + gosec clean (zero issues)"
else
    fail "golangci-lint not found (install to $GOPATH_BIN or PATH). quality.yml requires gosec/staticcheck/errcheck."
fi

# 12. Semgrep Offline AST SAST Gate
step "12/${BUILD_STEPS}" "Running Semgrep AST Security Policy Gate (.semgrep/g8s-rules.yml)..."
bash tools/ci_semgrep_check_test.sh >/dev/null
pass "Semgrep SAST self-tests passed"
if ! bash tools/ci_semgrep_check.sh; then
    fail "Semgrep AST security policy check failed."
fi
pass "Semgrep AST security policy clean"

# 13. Dual-Pass Test Suite & Coverage Verification
step "13/${BUILD_STEPS}" "Running Dual-Pass Test Suite & Coverage Verification..."
SCOPED_RUN=0
TEST_TARGETS=("./...")
RACE_TARGETS=("./internal/...")
if [ "$IMPACTED_MODE" -eq 1 ]; then
    IMPACTED_PLAN="$(resolve_impacted_packages "")"
    IMPACTED_KIND="${IMPACTED_PLAN%%|*}"
    IMPACTED_DETAIL="${IMPACTED_PLAN#*|}"
    if [ "$IMPACTED_KIND" = "SCOPED" ] && [ -n "$IMPACTED_DETAIL" ]; then
        SCOPED_RUN=1
        read -r -a TEST_TARGETS <<< "$IMPACTED_DETAIL"
        RACE_TARGETS=()
        for pkg in "${TEST_TARGETS[@]}"; do
            if [[ "$pkg" == ./internal/* ]]; then
                RACE_TARGETS+=("$pkg")
            fi
        done
        echo "  -> Scoped (--impacted via .g8s/feature-map.yml): ${TEST_TARGETS[*]}"
    else
        echo "  -> --impacted fallback to full suite (${IMPACTED_DETAIL})"
    fi
fi

echo "  -> Pass 1: Pure-Go (Zero-CGO) with coverage..."
COV_FILE=$(mktemp)
trap 'rm -f "$COV_FILE"' EXIT
EXPECTED_TEST_PKGS=()
if [ "$SCOPED_RUN" -eq 1 ]; then
    while IFS= read -r tpkg; do
        [ -n "$tpkg" ] && EXPECTED_TEST_PKGS+=("$tpkg")
    done < <(CGO_ENABLED=0 go list -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' "${TEST_TARGETS[@]}")
    if [ "${#EXPECTED_TEST_PKGS[@]}" -eq 0 ]; then
        fail "Scoped --impacted targets resolved zero test-bearing Go packages."
    fi
    set +e
    CGO_ENABLED=0 go test -json -count=1 -cover "${TEST_TARGETS[@]}" >"$COV_FILE" 2>&1
    TEST_EXIT=$?
    set -e
else
    set +e
    CGO_ENABLED=0 go test -count=1 -v -cover "${TEST_TARGETS[@]}" >"$COV_FILE" 2>&1
    TEST_EXIT=$?
    set -e
fi
if [ "$TEST_EXIT" -ne 0 ]; then
    echo "  --- Failing Test Diagnostics ---" >&2
    grep -E "(--- FAIL:|^FAIL[[:space:]]|\"Action\":\"fail\"|panic:)" -B 1 -A 6 "$COV_FILE" >&2 || tail -n 40 "$COV_FILE" >&2
    fail "CGO_ENABLED=0 tests failed."
fi
pass "Pure-Go tests passed"

if [ "$SCOPED_RUN" -eq 0 ]; then
    echo "  -> Checking aggregate coverage >= 80% (matching CI Quality Gate)..."
    COV_RESULT=$(awk '
      /^ok[ \t]/ && !/cmd\/g8s/ {
        for (i = 1; i <= NF; i++) {
          if ($i ~ /^coverage:/) {
            pct = $(i+1)
            gsub(/%/, "", pct)
            total += pct
            count++
            break
          }
        }
      }
      END {
        if (count == 0) exit 1
        avg = total / count
        printf "%.2f %d", avg, count
      }
    ' "$COV_FILE")

    if [ -z "$COV_RESULT" ]; then
        fail "Coverage extraction failed."
    fi

    avg=$(echo "$COV_RESULT" | awk '{print $1}')
    count=$(echo "$COV_RESULT" | awk '{print $2}')
    echo "     Aggregate coverage: ${avg}% across ${count} packages (cmd/g8s excluded)"

    if awk -v a="$avg" 'BEGIN { exit !(a+0 < 80) }'; then
        fail "Aggregate coverage ${avg}% is below 80% threshold."
    fi
    pass "Coverage threshold met (${avg}% >= 80%)"
else
    SCOPED_CHECK_OUT="$(verify_scoped_test_log "$COV_FILE" "${EXPECTED_TEST_PKGS[@]}" 2>&1)" || fail "Scoped --impacted run was vacuous: ${SCOPED_CHECK_OUT}"
    SCOPED_PASS="${SCOPED_CHECK_OUT#PASS_COUNT=}"
    pass "Scoped --impacted non-vacuous JSON event stream verified (${SCOPED_PASS} passing test(s) across ${#EXPECTED_TEST_PKGS[@]} test package(s); whole-repo 80% aggregate runs in full pre_push / CI)"
fi

if [ "$FAST_MODE" -eq 0 ]; then
    if [ "${#RACE_TARGETS[@]}" -gt 0 ]; then
        echo "  -> Pass 2: Race Detector (CGO_ENABLED=1 -race)..."
        if ! CGO_ENABLED=1 go test -race -count=1 "${RACE_TARGETS[@]}"; then
            fail "Race detector reported data races."
        fi
        pass "Race detector clean (zero warnings)"
    else
        echo "  -> Pass 2: Skipped (no internal packages in scoped --impacted target)"
    fi
else
    echo "  -> Pass 2: Skipped (fast mode)"
fi

# 14. Dogfooding CI Roundtrip
step "14/${BUILD_STEPS}" "Running Dogfooding Roundtrip Gate..."
if ! make dogfood; then
    fail "Dogfooding roundtrip failed."
fi
pass "Dogfooding roundtrip verified"

# 15. Cross-Platform Compilation Gate
step "15/${BUILD_STEPS}" "Running Cross-Platform Build Gate..."
if [ "$FAST_MODE" -eq 0 ]; then
    if ! make verify-cross-platform; then
        fail "Cross-platform build failed."
    fi
    pass "Linux, macOS, and Windows compilation successful"
else
    echo "  -> Skipped (fast mode)"
fi

echo ""
echo -e "${GREEN}${BOLD}======================================================${NC}"
echo -e "${GREEN}${BOLD}  ✓ ALL PRE-PUSH CHECKS PASSED! Ready to push remote. ${NC}"
echo -e "${GREEN}${BOLD}======================================================${NC}"
exit 0
