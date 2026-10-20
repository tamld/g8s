# Phase 06: Wave A replay of C1–C10 and measurement (#620)

## Goal
Measure, not assume, what Wave A actually blocks. Expected (hypothesis, to be confirmed or falsified):
| C | Defect | Wave A check | Expected |
|---|---|---|---|
| C1 | merge while `linux` pending (direct `gh pr merge`) | hook pre-tool → G2 | BLOCK |
| C2 | merge with Trivy red | G2 | BLOCK |
| C3 | sed TODO→To-Do | needs G4 (Wave B) | not blocked |
| C4 | invented YAML keys | needs G4 | not blocked |
| C5 | unverified F1 doc claim ("8 commands") | G6 only if cited as fixed without evidence | FLAG (partial) |
| C6 | 12 fixtures dropped in squash | G3 dropped | BLOCK |
| C7 | `Closes #617` with criterion unmet | needs G7 | not blocked |
| C8 | wrong SCORECARD claims accepted | G6 partial | FLAG (partial) |
| C9 | F0 direct edits | G3 unattributed (manual tag escape costs a debit and is shown) | BLOCK |
| C10 | "CI GREEN" without a run ID | hook stop → G6 report mode | BLOCK |

Counting rule: BLOCK (hard) and FLAG (partial) are reported **separately**; partials never count as blocks.

## Deliverable
- `tools/f0/testdata/replay/` with synthetic fixtures per case: a tiny git repo built in the test, ledger entries, and a fake forge response.
- `tools/f0/replay_test.go` asserting the gate verdict for each case.
- A short results table appended to `plan.md` §5 with **actual** counts. If a hypothesis is wrong, record that honestly.

## Also measure
The added wall time of `f0 gate check` per commitment point (offline adapter). Target < 60s (ADR falsification clock).

## Success criteria
The replay test passes and encodes the actual expectations. The results are recorded. Wave B priorities are re-ranked from the data.
