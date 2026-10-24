#!/usr/bin/env bash
#
# tools/ci_doc_contract_check.sh — Doc↔Code Contract Gate (Issue #208)
#
# Enforces that documentation and architecture specifications do not drift
# from the reality of the codebase:
#   1. Orchestrator FSM state contract: checks all 8 states in internal/state and internal/orchestrator
#   2. Task FSM state contract: checks all 8 task states in internal/state and internal/controlplane
#   3. Go toolchain version contract: checks go.mod vs docs
#   4. Receipt verification contract: checks presence of ReceiptVerifier & StdoutEnvelopeVerifier
#   5. Unowned TODO/FIXME scanning in docs
#   6. OpenSpec registry file existence
#   7. Session-type markers on campaign ledgers (ADR-0022 §6)
#   8. Spec↔code sync: delta scenarios pinned to real Go tests (ADR-0024 G1)
#   9. README structure sync: Project Structure matches the filesystem (ADR-0024 G2)
#   10. Root hygiene (no tracked build artifacts or scratch)
#   11. SAST Triad & Linter Configuration Contract
#   12. Zero-Stale Manifest Contract
#   13. Declarative Feature Map contract (#649)
#   14. Verification Skills Suite contract (#651)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

FAILED=0

echo "==> Running Documentation ↔ Code Contract Checks..."

# 1. Orchestrator FSM States Contract
echo "--> [1/14] Verifying Orchestrator FSM states contract..."
EXPECTED_ORCH_STATES=("PLAN" "SPAWN" "MONITOR" "RECEIPT" "MERGE" "ESCALATE" "CANCEL" "CONFLICT")
for state in "${EXPECTED_ORCH_STATES[@]}"; do
    if ! grep -qi "OrchestratorState$state" internal/state/state.go; then
        echo "::error::Orchestrator state $state missing in internal/state/state.go"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -qi "State$state" internal/orchestrator/fsm.go; then
        echo "::error::Orchestrator state $state missing in internal/orchestrator/fsm.go"
        FAILED=$((FAILED + 1))
    fi
done

# 2. Task FSM States Contract
echo "--> [2/14] Verifying Task FSM states contract..."
EXPECTED_TASK_STATES=("QUEUED" "LEASED" "RUNNING" "NEEDS_INFO" "BLOCKED" "SUCCEEDED" "FAILED" "CANCELLED")
for state in "${EXPECTED_TASK_STATES[@]}"; do
    clean_state="${state//_/}"
    if ! grep -qiE "TaskState($state|$clean_state)" internal/state/state.go; then
        echo "::error::Task state $state missing in internal/state/state.go"
        FAILED=$((FAILED + 1))
    fi
done

# 3. Go Version Contract
echo "--> [3/14] Verifying Go version consistency across SSoT docs..."
GO_MOD_VER=$(grep -E '^go ' go.mod | awk '{print $2}')
if [ -z "$GO_MOD_VER" ]; then
    echo "::error::Unable to determine go version in go.mod"
    FAILED=$((FAILED + 1))
else
    if ! grep -q "$GO_MOD_VER" docs/ARCHITECTURE_ROADMAP.md; then
        echo "::error::docs/ARCHITECTURE_ROADMAP.md does not reference current go version $GO_MOD_VER"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -q "$GO_MOD_VER" spec/constitution.md; then
        echo "::error::spec/constitution.md does not reference current go version $GO_MOD_VER"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -q "\"go_version\": \"$GO_MOD_VER\"" manifest.json; then
        echo "::error::manifest.json does not reference current go version $GO_MOD_VER"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -q "$GO_MOD_VER" README.md; then
        echo "::error::README.md does not reference current go version $GO_MOD_VER"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -q "$GO_MOD_VER" README.vi.md; then
        echo "::error::README.vi.md does not reference current go version $GO_MOD_VER"
        FAILED=$((FAILED + 1))
    fi
fi

# 4. Receipt Verification Contract
echo "--> [4/14] Verifying Receipt Verifier layer contract..."
if [ ! -f "internal/orchestrator/verify.go" ]; then
    echo "::error::internal/orchestrator/verify.go missing (ReceiptVerifier layer contract)"
    FAILED=$((FAILED + 1))
fi
if ! grep -q "ReceiptVerifier" internal/orchestrator/verify.go; then
    echo "::error::ReceiptVerifier interface missing in internal/orchestrator/"
    FAILED=$((FAILED + 1))
fi
if ! grep -q "StdoutEnvelopeVerifier" internal/orchestrator/verify.go; then
    echo "::error::StdoutEnvelopeVerifier missing in internal/orchestrator/"
    FAILED=$((FAILED + 1))
fi

# 5. Doc Unowned TODO Check
echo "--> [5/14] Checking for unowned TODO/FIXME in documentation..."
UNOWNED_DOC_TODOS=$(grep -rnE '(^|[[:space:]])//\s*(TODO|FIXME|XXX)' docs/ spec/ 2>/dev/null | grep -v 'OWNER=' | grep -v '`//' || true)
if [ -n "$UNOWNED_DOC_TODOS" ]; then
    echo "::error::Found unowned TODO/FIXME in documentation:"
    echo "$UNOWNED_DOC_TODOS"
    FAILED=$((FAILED + 1))
fi

# 6. OpenSpec File Link & Registry Bi-Directional Consistency
echo "--> [6/14] Verifying OpenSpec registry bi-directional consistency..."
while IFS= read -r link; do
    if [ ! -f "spec/openspec/$link" ]; then
        echo "::error::spec/openspec/README.md references missing spec file: spec/openspec/$link"
        FAILED=$((FAILED + 1))
    fi
done < <(grep -oE '\([0-9A-Za-z_-]+\.md\)' spec/openspec/README.md | tr -d '()')

for spec_file in spec/openspec/[0-9]*.md; do
    [ -e "$spec_file" ] || continue
    base_spec=$(basename "$spec_file")
    if ! grep -qF "($base_spec)" spec/openspec/README.md; then
        echo "::error::$spec_file is not registered in spec/openspec/README.md"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -qF "\"$spec_file\"" manifest.json; then
        echo "::error::$spec_file is not registered in manifest.json"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -qF "$base_spec" docs/AGENTS_FULL.md; then
        echo "::error::$spec_file is not registered in docs/AGENTS_FULL.md (anti-stale contract)"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -qE '^(\* )?\*\*Status\*\*:' "$spec_file"; then
        echo "::error::$spec_file is missing an explicit '**Status**:' header"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -qE '^#### Scenario:' "$spec_file"; then
        echo "::error::$spec_file is missing at least one '#### Scenario:' verification block"
        FAILED=$((FAILED + 1))
    fi
done

# 7. Session-type marker contract (ADR-0022 §6, #397): every campaign ledger
#    declares which session type produced it — T1 (strategy) or T2 (execution).
echo "--> [7/14] Verifying session-type markers (ADR-0022)..."
for ledger in plans/*/plan.md plans/handoffs/*.md; do
    [ -e "$ledger" ] || continue
    if ! grep -qE '\*\*Session type\*\*: *T[12]' "$ledger"; then
        echo "::error::$ledger missing session-type marker (ADR-0022 §6)"
        echo "        add '**Session type**: T1 (strategy)' or '**Session type**: T2 (execution)' to the ledger header"
        FAILED=$((FAILED + 1))
    fi
done

# 8. Spec↔code sync contract (S6-1 G1, #420): every scenario in an
#    APPLIED/ACCEPTED delta must be pinned to a real Go test with zero
#    grandfathered warnings.
echo "--> [8/14] Verifying spec↔code sync (ADR-0024 G1)..."
SYNC_OUT=$(bash "$SCRIPT_DIR/ci_spec_code_sync.sh" 2>&1) || {
    echo "$SYNC_OUT"
    FAILED=$((FAILED + 1))
}
echo "$SYNC_OUT"
if printf '%s\n' "$SYNC_OUT" | grep -qF '[grandfathered]'; then
    echo "::error::spec-code sync: unpinned scenarios detected (0 grandfathered warnings allowed)"
    FAILED=$((FAILED + 1))
fi

# 9. Structure regen contract (S6-1 G2, #420): the README Project Structure
#    region must match a fresh render from the live filesystem.
echo "--> [9/14] Verifying README structure sync (ADR-0024 G2)..."
if ! bash "$SCRIPT_DIR/ci_structure_sync.sh" 2>&1; then
    FAILED=$((FAILED + 1))
fi

# 10. Root hygiene (2026-10-02): build artifacts and session scratch must
#     never be TRACKED at the repository root — the tree a fresh clone sees
#     is the product surface. Local-only junk is a session cleanup matter;
#     committed junk fails this gate.
echo "--> [10/14] Verifying root & agent-runtime hygiene (no tracked build artifacts or local agent configs)..."
HYGIENE_VIOLATIONS=$( { git ls-files '*.out' '*.test' '*.exe' 'g8s.exe' \
    '*.patch' '*.orig' '*.bak' 'go.work.bak' '.g8s-drain-*' 'worktrees/*' 'dist/*' \
    '.opencode/*' '.jules/*' '.claude/*' '.sisyphus/*' '.superpowers/*' \
    'docs/superpowers/*' 'docs/goals/*' 'reference/python/scripts/scratch/*'; } 2>/dev/null)
if [ -n "$HYGIENE_VIOLATIONS" ]; then
    echo "::error::root hygiene: tracked build artifacts / scratch / local agent configs:"
    echo "$HYGIENE_VIOLATIONS"
    FAILED=$((FAILED + 1))
fi

# 11. SAST Triad & Linter Configuration Contract (Anti-Stale Guard):
#     Ensures gosec remains enabled in .golangci.yml, Semgrep rules and gate
#     script remain wired in pre_push.sh & quality.yml, and CodeQL workflow exists.
echo "--> [11/14] Verifying SAST Triad (Gosec + Semgrep + CodeQL) contract..."
for req_linter in errcheck gosec govet ineffassign staticcheck unused; do
    if ! grep -qE "^[[:space:]]*-[[:space:]]*${req_linter}\b" .golangci.yml; then
        echo "::error::.golangci.yml is missing required enabled linter: ${req_linter}"
        FAILED=$((FAILED + 1))
    fi
done
for sast_file in .semgrep/g8s-rules.yml tools/ci_semgrep_check.sh tools/ci_semgrep_check_test.sh .github/workflows/codeql.yml; do
    if [ ! -f "$sast_file" ]; then
        echo "::error::Missing required SAST Triad file: $sast_file"
        FAILED=$((FAILED + 1))
    fi
done
if ! grep -qF "ci_semgrep_check.sh" .github/workflows/quality.yml; then
    echo "::error::.github/workflows/quality.yml does not invoke tools/ci_semgrep_check.sh"
    FAILED=$((FAILED + 1))
fi
if ! grep -qF "ci_semgrep_check.sh" tools/pre_push.sh; then
    echo "::error::tools/pre_push.sh does not invoke tools/ci_semgrep_check.sh"
    FAILED=$((FAILED + 1))
fi

# 12. Zero-Stale Manifest Contract (Issue #638):
#     Ensures every path referenced in manifest.json exists on disk.
echo "--> [12/14] Verifying manifest.json path-existence (zero-stale contract)..."
while IFS= read -r fpath; do
    if [ ! -e "$fpath" ]; then
        echo "::error::manifest.json references non-existent path: $fpath"
        FAILED=$((FAILED + 1))
    fi
done < <(python3 -c "
import json
data = json.load(open('manifest.json'))
for section in ['governance', 'declarative_registries', 'documentation_index', 'schemas']:
    for p in data.get(section, {}).values():
        print(p)
for s in data.get('specifications', []):
    if 'path' in s:
        print(s['path'])
")

# 13. Declarative Feature Map Contract (Issue #649):
echo "--> [13/14] Verifying Declarative Feature Map (.g8s/feature-map.yml) contract (#649)..."
if ! bash "$SCRIPT_DIR/ci_feature_map_sync.sh" >/dev/null 2>&1; then
    echo "::error::Declarative Feature Map check (ci_feature_map_sync.sh) failed"
    FAILED=$((FAILED + 1))
fi

# 14. Verification Skills Suite Contract (Issue #651):
echo "--> [14/14] Verifying Verification Skills Suite (skills/manifest.json) contract (#651)..."
if ! python3 -c "
import json, os, sys

manifest_path = 'skills/manifest.json'
if not os.path.exists(manifest_path):
    print(f'::error::{manifest_path} does not exist')
    sys.exit(1)

with open(manifest_path, 'r', encoding='utf-8') as f:
    data = json.load(f)

skills = data.get('skills', [])
skill_names = {s.get('name') for s in skills if isinstance(s, dict)}
required_skills = [
    'g8s-blind-verifier',
    'g8s-red-prove',
    'g8s-scenario-matrix',
    'g8s-ux-visual-gate',
    'g8s-sec-gate',
    'g8s-epistemic-audit',
]

err = False
for req in required_skills:
    if req not in skill_names:
        print(f'::error::skills/manifest.json is missing required verification skill: {req}')
        err = True

for s in skills:
    if isinstance(s, dict):
        entry = s.get('entry')
        if not entry:
            print(f'::error::Skill {s.get(\"name\", \"unknown\")} missing entry path')
            err = True
        elif not os.path.exists(entry):
            print(f'::error::Skill {s.get(\"name\", \"unknown\")} entry does not exist on disk: {entry}')
            err = True

if err:
    sys.exit(1)
"; then
    FAILED=$((FAILED + 1))
fi

if [ "$FAILED" -gt 0 ]; then
    echo ""
    echo "[DOC-CONTRACT] FAILED: $FAILED contract violation(s) found."
    exit 1
fi

echo "[DOC-CONTRACT] PASSED: All documentation-code contracts synchronized."
exit 0
