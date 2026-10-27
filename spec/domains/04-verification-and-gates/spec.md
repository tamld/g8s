# Living Domain Spec 04 — Mechanical Verification, Blind Review & F0 Commitment Gates

- **Domain ID**: `04-verification-and-gates`
- **Collision Domains**: `verification`, `supervision-gates`
- **Feature IDs**: `FEAT-VERIFY-01`, `FEAT-BLIND-VERIFY-01`, `FEAT-F0-GATE-01`
- **Constitutional Laws**: Law 6 (Evidence over Verdicts)

---

### R1 — Worktree-Targeted Verification, Blind Review & F0 Gate Check {#R1-f0-gate-and-verify}

<!-- inherited-from: DELTA-21, DELTA-22, DELTA-24, DELTA-28, DELTA-35, DELTA-37, DELTA-42 -->
<!-- feature_id: FEAT-F0-GATE-01 -->

F0 commitment gates (`g8s f0 gate check --json`) and post-run L3 output verification (`g8s reflex verify --json`) MUST mechanically verify repository state, markdown CLI claim integrity, and durable queue hygiene before merge or publication.

#### Scenario: F0 gate check validates repository and markdown CLI claims
<!-- tests: TestFiveAxisScorecardAndF0Gate -->
- **GIVEN** the `g8s` repository root
- **WHEN** `g8s f0 gate check --repo ../.. --json` is executed
- **THEN** it returns `verdict: "PASS"` inside an `f0_gate_check` envelope
