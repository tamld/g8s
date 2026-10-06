# Offer Rollout SCORECARD (#442 — falsification-first)

Metrics defined before rollout: M1 adoption ≥3/5 · M2 first-audit catch ≥1
real issue each · M3 ≥2 contribution packets back · M4 retention at 3 months.
Source: issue #442.

| Wave | Project | Profile | Onboarded | First-audit (M2) | Status |
|------|---------|---------|-----------|------------------|--------|
| 1 | tamld-llm-wiki | knowledge | 2026-10-05 (commit b26ca938, local branch codex/* — wiki session owns the push) | **~135 real findings total**: gate-audit 2 + worker-audit 7 (91 files, 218s, LOW) + GATE 3.4 **38** (48 files, 248s, LOW) + 1-Mindsets **79** (41 files, 111s, LOW) + 4-Rules **9+** (56 files, LOW — auto-classified via the sibling registry, no flag) + P1 sampling run (3-Automations re-do at HIGH: 699s, 1.09M tokens — comparable quality, floor trusted for the audit class) | M1 ✓ · M2 ✓ — pilot live; sibling effort-classes registry committed (docs/low earned); C2 auto-classification GROUND-TRUTH VERIFIED on real dispatches |
| 2 | defense-in-depth | security | 2026-10-06 (offer init + sibling effort registry + AGENTS wiring committed to main; pilot files only) | **First-audit day-one catches**: stale CLI refs (hints-emit nonexistent, --export-lessons→--export-rules, --name→--metric, "11 commands"→10), federation doc v0.6.0 vs repo 1.0.0, 180-day-old audit report presented as current, 3+ broken quickstart links, relative-depth link rot — 43 files, 291K tokens, LOW tier auto-classified via the security-profile registry | M1 ✓ (2/5 onboarded) · M2 ✓ (real catches day-one, [CODE]-cited) — wave 2 live |
| 3 | aegis | infra | — | — | pending |
| 3 | homelab-proxmox | infra/utility | — | — | pending |
| 3 | hash-checker | utility | — | — | pending (repo not created yet) |

## Falsification tracker

- M1: 2/5 onboarded (target ≥3/5; clock started 2026-10-05, 1 month window)
- M2: wiki first-audit caught 2 real issues (structure markers + links) — metric SATISFIED for wave 1
- M3: 0/2 contribution packets
- M4: retention clock starts 2026-10-05 (3-month check ~2027-01-05)
