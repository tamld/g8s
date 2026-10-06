# Offer Rollout SCORECARD (#442 — falsification-first)

Metrics defined before rollout: M1 adoption ≥3/5 · M2 first-audit catch ≥1
real issue each · M3 ≥2 contribution packets back · M4 retention at 3 months.
Source: issue #442.

| Wave | Project | Profile | Onboarded | First-audit (M2) | Status |
|------|---------|---------|-----------|------------------|--------|
| 1 | tamld-llm-wiki | knowledge | 2026-10-05 (commit b26ca938, local branch codex/* — wiki session owns the push) | **~126 real findings total**: gate-audit 2 + worker-audit 7 (91 files, 218s, LOW) + GATE 3.4 **38** (48 files, 248s, LOW) + 1-Mindsets **79** (4 stale, 30 date-rot, 7 link-rot, 38 MoC-orphans; 41 files, 111s, LOW) + P1 sampling run (3-Automations re-do at HIGH: 699s, 1.09M tokens — comparable quality, floor trusted for the audit class) | M1 ✓ · M2 ✓ — pilot live; effort machinery LANDED (#556-#559, #561); P1 audit-sampling gauge FIRST READING recorded (#550): low tier floor TRUSTED for docs/audit class |
| 2 | defense-in-depth | security | — | — | pending |
| 3 | aegis | infra | — | — | pending |
| 3 | homelab-proxmox | infra/utility | — | — | pending |
| 3 | hash-checker | utility | — | — | pending (repo not created yet) |

## Falsification tracker

- M1: 1/5 onboarded (target ≥3/5)
- M2: wiki first-audit caught 2 real issues (structure markers + links) — metric SATISFIED for wave 1
- M3: 0/2 contribution packets
- M4: retention clock starts 2026-10-05 (3-month check ~2027-01-05)
