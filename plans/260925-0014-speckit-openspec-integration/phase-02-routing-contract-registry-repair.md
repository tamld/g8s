# Phase 2 — Routing Contract & Registry Repair ("tối ưu")

## Context Links
- `AGENTS.md`, `docs/AGENTS_FULL.md`, `manifest.json`, `spec/openspec/README.md`, `README.md` (§ Documentation & Spec-Driven Development)

## Overview
- **Priority**: P1 | **Status**: Pending
- Fix live registry drift and define the **Speckit-vs-OpenSpec routing rule** so no agent ever has to guess which framework governs a change.

## Key Insights (drift evidence, verified 2026-09-25)
- `spec/openspec/README.md`: DELTA-08/09/10 rows link to filenames that do not exist on disk (`08-pty-streaming-spec.md`, `09-doctor-and-autorepair-spec.md`, `10-worker-supervisor-bridge-spec.md`).
- `README.md` claims DELTA-11 = "Orchestration Roadmap"; `spec/openspec/README.md` claims DELTA-11 = "Knowledge Vault" — conflicting identity for the same ID.
- Disk holds 15 spec files; `manifest.json` lists 13; `11-orchestration-roadmap-spec.md` is registered nowhere.
- `grep -i "spec-kit|speckit"` across `AGENTS.md`, `docs/AGENTS_FULL.md`, `manifest.json` → zero hits. No routing rule exists.

## Requirements
- **Routing rule** (one decision tree in AGENTS.md, mirrored in AGENTS_FULL.md):
  - Net-new capability → `/speckit.specify → clarify → plan → tasks → implement`, MUST end by registering an OpenSpec delta in `manifest.json`.
  - Capability amendment / bugfix on an existing DELTA → OpenSpec proposal → apply → archive directly.
  - Invariants always resolve to `spec/constitution.md` (single SSoT).
- **Registry repair**: fix stale links; register DELTA-18 and DELTA-20 rows; resolve DELTA-11 conflict (recommend `git mv 11-orchestration-roadmap-spec.md → 14-orchestration-roadmap-spec.md` as DELTA-14; Knowledge Vault keeps DELTA-11) — final numbering confirmed at review gate.
- **manifest.json**: add `governance.spec_workflow` block: `{ "feature_delivery": "spec-kit", "capability_registry": "openspec", "constitution_ssot": "spec/constitution.md", "sync_guard": "cmd/speccheck" }`.

## Architecture
- AGENTS.md stays the ONLY agent entry point; `manifest.json` is the machine-readable routing surface (`governance.spec_workflow`). Prose docs and JSON never disagree because Phase 3's guard enforces it.

## Related Code Files
- **Modify**: `AGENTS.md`, `docs/AGENTS_FULL.md`, `manifest.json`, `spec/openspec/README.md`, `README.md`
- **Rename (proposed)**: `spec/openspec/11-orchestration-roadmap-spec.md` → `14-orchestration-roadmap-spec.md`
- **Create/Delete**: none

## Implementation Steps
1. Resolve the DELTA-11 identity conflict (recommend renumber roadmap → DELTA-14) — confirm at cook's review gate before renaming.
2. `git mv` + grep sweep for stale references (`grep -rn "11-orchestration-roadmap" --exclude-dir=plans`).
3. Rewrite `spec/openspec/README.md` table to mirror `manifest.json.specifications` exactly (same rows, same statuses).
4. Add the routing decision tree to AGENTS.md §2 and AGENTS_FULL.md; add the `spec_workflow` block to `manifest.json`.
5. Update `README.md` § Documentation links; run a relative-link check across the touched files.

## Todo List
- [ ] DELTA-11 conflict resolved (renumber decision recorded)
- [ ] registry table == manifest.json.specifications (row-for-row)
- [ ] routing rule present in AGENTS.md + AGENTS_FULL.md + manifest block
- [ ] zero dangling relative links

## Success Criteria
- Registry row count == `len(manifest.json.specifications)`; link check clean; routing rule discoverable in both prose and machine-readable form.

## Risk Assessment
| Risk | Mitigation |
|------|-----------|
| Renumbering breaks external references | grep sweep + one-release note in CHANGELOG; git mv preserves history |
| Doc churn collides with in-progress plans | No overlap — diff-distiller plan touches `internal/` Go code only |

## Security Considerations
- Documentation-integrity only; no code paths, permissions, or receipts changed.

## Next Steps
- Phase 3 dogfoods the new workflow by building the guard that keeps this phase's invariant enforced forever.
