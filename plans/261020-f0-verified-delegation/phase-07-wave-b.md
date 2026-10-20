# Phase 07: Wave B (outline; re-planned in detail after Phase 06)

| Sub | Issue | Scope | Notes |
|---|---|---|---|
| 7a | #625 | G4 gate-appeasement detector | Token-only rewrite of guard-matched patterns after a guard FAIL; unread config keys; overrides through `internal/telemetry` OverrideReceipt + debit |
| 7b | #626 | G7 issue-closure gate | Parse acceptance-criteria checkboxes; every item maps to evidence; otherwise `Refs` + an unmet-criteria comment |
| 7c | #630 | Auditor + session digest + Jev | Digest built from the ledger only. Auditor is pluggable (Jev L3 via `internal/reflex` or any `g8s submit --permission read_only` provider). `UNAUDITED` when none is available. Jev L1 tier suggestion is advisory. In-flight lesson whispers reuse the `jit_whisper` K_max mechanism |
| 7d | #627 | T2 merge executor | **PiC STOP before implementation**: Keychain credential + ACL design review. Pre-created revert; post-merge watch; auto-revert; attestation trailer + digest audit of un-attested default-branch commits |
| 7e | #628 | T3 independent verifier | Second verifier ≠ F0 provider, or `g8s eval` probes. Disagreement or missing verifier ⇒ veto + escalate (D9) |
| 7f | #629 | Circuit breaker | `debit` ledger entries → tier downgrade, effective on the next action; explicit upgrade policy |

Order (default; adjusted by the Phase 06 data): 7a → 7b → 7c → 7d → 7e → 7f.
