# Plan — 261120-trust-compliance (v0.27 COMPLIANCE wave)

**Session type**: T2 (execution)

- **Date**: 2026-11-20 · coordinator F0 (ZCode session, post-v0.26.0 reload)
- **Operator approval**: TamLD duyệt 2026-11-20 ("duyệt, em làm đi. mở issues xong rồi báo anh")
- **Provenance**: F0-direct — issue triage + verification (workers not used for issue filing)
- **Inputs**: Codex mesh reviews (83/100 audit A–E + Context Authority Firewall + Enterprise/MCP), GLM static+runtime verification, ck:predict 5-persona debate (plan 8→7), Jev live triage (5×escalate_hitl — degenerate, logged)

## Wave structure (7 issues, red-test-first, DEBT-34 layer split)

| # | Issue | Priority | Evidence class |
|---|-------|----------|----------------|
| 1 | S0 nonce store cross-process replay + nil-store invariant guard | P0 | measured (static) — trust_root.go:66,109-120 |
| 2 | deliver TOCTOU digest↔apply window — read-once-into-memory | P0 | measured (static) — deliver.go:989 vs fence :599 |
| 3 | Gate epistemology: CAPABILITY_DETECTED vs ENFORCED (additive) | P1 | measured (doctor) — trust_assurance.go:259,333 |
| 4 | S0 custody operational: runbook + doctor hint | P1 | measured (doctor) — s0_key_provisioned=False |
| 5 | MCP protocol conformance fix (notification + handshake) | P1 | **measured (runtime on 0.26.0 binary)** |
| 6 | Context Authority Firewall — staged PR-1 (threat model + map + corpus) | P1 | founding instance = #5 measured finding |
| 7 | Policy Epoch & Revocation consistency audit (read-only) | P1 | hypothesis → audit |

## Ground rules

- Red-test-first per issue; worker dispatch via EXPLICIT-TRIPLE; disjoint receipts (1↔2 share deliver.go → sequence, not parallel); TCB-touching slices (1,2,3) need S0 approval token after duyệt (H2 hook flow).
- Burn-in anchor: awaiting operator (proposal: v0.27 cut date) — gates issue-6 milestone honesty.
- Post-wave: wave-2 hooks H9-H12 + weekly routing eval job; 2 wiki notes (MCP authority laundering measured; hook design laws).

## Verification log (session 2026-11-20)

- Binary 0.26.0 rebuilt at 16ebb22, doctor binary_matches_source=True.
- MCP runtime probe (isolated G8S_STATE_DIR): receipt minted without handshake ×2; notification replied with error -32601.
- Hooks eval 18/18 distinct checks; H1 replays the 2026-11-20 miss exactly.
- Jev degeneracy measured 5/5 escalate_hitl; router misfire 1× (governance → ui-ux).
