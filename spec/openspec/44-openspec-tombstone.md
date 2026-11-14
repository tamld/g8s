# DELTA-44: OpenSpec Numbering Tombstone (Reserved Gap)

* **Specification ID**: `DELTA-44`
* **Title**: OpenSpec Numbering Tombstone (Reserved Gap)
* **Status**: `RESERVED` (Tombstone — numbering gap between `DELTA-43` and `DELTA-45`)
* **Milestone**: M6 (Governance)
* **Target Package**: — (no implementation)

---

## Summary

`DELTA-44` was skipped during the `v0.20.0`–`v0.21.0` OpenSpec sequence (the numbering jumped from `DELTA-43` to `DELTA-45`). This tombstone specification permanently reserves the `DELTA-44` identifier so that existing `DELTA-45` through `DELTA-48` identifiers never renumber, preserving contiguous OpenSpec index integrity across documentation, tooling, and CI gates.

No code changes, schema additions, or behavioral modifications are associated with this reservation.

---

## Verification

#### Scenario: DELTA-44 tombstone preserves contiguous OpenSpec index integrity
<!-- tests: process-enforced -->
- **GIVEN** the `spec/openspec/` directory and `manifest.json` specification registry
- **WHEN** `tools/ci_doc_contract_check.sh` and `tools/ci_spec_code_sync.sh` inspect all OpenSpec deltas
- **THEN** `44-openspec-tombstone.md` is registered with `RESERVED` status and zero unpinned scenario warnings.