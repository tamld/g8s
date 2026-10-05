# Offer Rollout SCORECARD (#442 — falsification-first)

Metrics defined before rollout: M1 adoption ≥3/5 · M2 first-audit catch ≥1
real issue each · M3 ≥2 contribution packets back · M4 retention at 3 months.
Source: issue #442.

| Wave | Project | Profile | Onboarded | First-audit (M2) | Status |
|------|---------|---------|-----------|------------------|--------|
| 1 | tamld-llm-wiki | knowledge | 2026-10-05 (commit b26ca938, local branch codex/* — wiki session owns the push) | **2 real findings day-one**: README missing structure-sync markers (G2) + link-integrity FAIL | M1 ✓ · M2 ✓ — pilot live |
| 2 | defense-in-depth | security | — | — | pending |
| 3 | aegis | infra | — | — | pending |
| 3 | homelab-proxmox | infra/utility | — | — | pending |
| 3 | hash-checker | utility | — | — | pending (repo not created yet) |

## Falsification tracker

- M1: 1/5 onboarded (target ≥3/5)
- M2: wiki first-audit caught 2 real issues (structure markers + links) — metric SATISFIED for wave 1
- M3: 0/2 contribution packets
- M4: retention clock starts 2026-10-05 (3-month check ~2027-01-05)
