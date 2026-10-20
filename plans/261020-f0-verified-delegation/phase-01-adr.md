# Phase 01: ADR-0033 F0 Verified Delegation (#621)

## Context
The design was approved in brainstorm B′ (2026-10-19). The ADR is the canonical record that later phases cite.

## Deliverable
`docs/decisions/0033-f0-verified-delegation.md`, following the ADR-0032 format: Status / Supersedes / Depends on / Context / Decision / Implementation audit / Falsification clocks / Consequences / Related.

## Required content
- **Status**: Accepted (2026-10-19, PiC).
- **Supersedes**: "PiC reviews first" in `skills/g8s-supervisor/references/hard-boundary.md`, for tiers T1–T3.
- **Depends on**: ADR-0020 (reflex / Jev), ADR-0029 D3 ("nothing automated mints receipts"; the mandate is PiC-issued, never automated), ADR-0032 (F0/F1/F2), #516 merger, #594–#596 differential governance.
- **Context**: C1–C10 from 2026-10-18, one line each, with the defect class. All came from F0. The prose rules already existed and were violated.
- **Decision**:
  - Axioms 1–6 from plan.md §1.
  - Tier table T0–T4.
  - Components: mandate, ledger, gate G1–G7, T2 executor, T3 verifier, breaker, auditor/digest, Jev roles (L1 triage, L3 auditor; advisory only).
  - D9: T3 veto is decided by verification (two independent verifiers; disagreement or missing verifier ⇒ veto + escalate), not by a timer.
- **Enforcement points**: agent-level hooks (`PreToolUse` deny on merge/T4/bypass, `Stop` continue on uncited claims, Phase 04b), the gate (G0–G6), external ledger anchors (`F0-Ledger-Head` trailer/comment), and `f0 audit` for after-the-fact detection. The ledger lives at fixed per-repo (`<git-common-dir>/f0/`) and global paths that ignore `G8S_STATE_DIR`.
- **Honest limitations**: on a single-user machine, Keychain ACL isolation is not absolute, and mandate issuance cannot be cryptographically separated from F0; the hash chain has no secret key (truncation is detected only via external anchors); F0 could spoof tool binaries on PATH or edit hooks.json (hooks deny shell edits; `f0 audit` detects un-attested merges). Attestation is a hash anchor, not a cryptographic signature. Therefore detection-after-the-fact (attestation trailer + digest) is required, and the mandate is an *accepted* boundary.
- **Falsification clocks**:
  - Wave A replay: ≥5/10 hard BLOCK and ≥2 FLAG of C1–C10, counted separately; Wave B: 10/10 BLOCK.
  - The median added time per commitment point is < 60s.
  - Synchronous PiC interrupts per campaign are limited to T4 actions and T3 vetoes.
- **Related**: #620–#631, plan `plans/261020-f0-verified-delegation/`.

Also add a one-line pointer to ADR-0033 in `skills/g8s-supervisor/SKILL.md` (Hard Boundary section).

## Success criteria
- `bash tools/ci_link_integrity.sh` and `bash tools/ci_doc_contract_check.sh` pass.
- The docs lane pre-push gate passes.
