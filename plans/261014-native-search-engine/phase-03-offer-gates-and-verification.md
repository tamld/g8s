# Phase 3 — Offer Gate Integration, README Sync & Adversarial Review

**Session type**: T2 (execution)

## Context Links

- Issue: `tamld/g8s#591`
- Depends on: [Phase 1](./phase-01-internal-search-engine.md), [Phase 2](./phase-02-cli-g8s-search-and-doctor.md)
- Skill: `ck:code-review` (`~/.agents/skills/code-review/SKILL.md`)

## Overview

- **Priority**: P1
- **Status**: Pending
- **Description**: Wire `g8s search` into `tools/ci_spec_code_sync.sh` and `offer/SEARCH_GUIDE.md`, render the updated `README.md` project structure (`tools/ci_structure_sync.sh --render`), and execute the 3-stage `ck:code-review` (Spec Compliance, Code Quality + Edge Case Scouting, Adversarial Red-Team Review).

## Requirements

1. **Offer Gate Native Priority (`tools/ci_spec_code_sync.sh`)**:
   - Priority chain: `g8s search` (when `g8s` or `./bin/g8s` is available) → `rg` → POSIX `grep`/`find`.
2. **Structure Sync (`README.md`)**:
   - Run `bash tools/ci_structure_sync.sh --render` so `internal/search/` appears in `README.md` between `<!-- structure:start -->` and `<!-- structure:end -->`.
3. **Three-Stage `ck:code-review`**:
   - Stage 1: Spec Compliance Verification against Phase 1–3 requirements.
   - Stage 2: Code Quality, Race Detector (`CGO_ENABLED=1 go test -race ./internal/search/...`), and Cross-Platform Compilation (`make verify-cross-platform`).
   - Stage 3: Adversarial Red-Team Review recorded in `plans/261014-native-search-engine/reports/code-review-report.md`.

## Related Code Files

- **Modify**: `tools/ci_spec_code_sync.sh`, `offer/SEARCH_GUIDE.md`, `README.md`
- **Create**: `plans/261014-native-search-engine/reports/code-review-report.md`

## Success Criteria

- `bash tools/ci_structure_sync.sh` and `bash tools/ci_spec_code_sync_test.sh` pass.
- All 3 stages of `ck:code-review` pass with 0 unresolved Critical/High findings.
- `GOTOOLCHAIN=go1.26.9 git push origin main` succeeds and Issue `#591` is closed with full verification evidence.
