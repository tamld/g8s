---
title: "F0 Skill-Vault Optimization — from ad-hoc craft to routed skill usage"
issues: []
related-plans: ["261020-f0-verified-delegation"]
status: in_progress
priority: P1
created: 2026-11-03
mode: auto

**Session type**: T2 (execution)
---

# F0 Skill-Vault Optimization

## Summary

The FlowAxon v5 campaign (2026-11-01→03) proved the F1-delegation machine works (7/7 workers ok=true, reports F0-verified). But the coordinator's own **skill hygiene failed**: a 163-skill vault was used organically at ~3% — including a dedicated media skill (`agentvid`) the vault's own router would have surfaced for the film workstream. This plan converts the failure into a routing ritual: **router-first inventory, skill-combo maps per workstream, plan→cook ritual, session-end usage ledger.**

## Evidence — eval of current coordination (this campaign)

| # | Finding | Evidence | Verdict |
|---|---|---|---|
| F1 | Vault has **163 skills**; Skill tool fired ~4× all campaign (browser-use, ck:plan) | session log | GAP |
| F2 | `skill-router.py` (the vault's JIT router, AGENTS.md §2) invoked **zero times** before today | session log | GAP |
| F3 | Router probe today: "cinematic product promo video" → **`agentvid` [media]** — never used; film was hand-rolled (video-shotcraft ~30% + numpy synth + edge-tts) | router run 2026-11-03 | GAP |
| F4 | video-shotcraft's own canon (`references/sound-design.md`, `music-beat-sync.md`, `sfx-sourcing.md`) ignored until the operator linked the repo — 158 synthesized clacks masked VO consonants (docs/research/23 autopsy) | docs/research/23 | GAP |
| F5 | Research waves hand-briefed each time (intake/scoring structures re-invented) — vault research skills unused | briefs-fxv5/ | GAP (quality survived, reuse didn't) |
| F6 | 24 `wiki-*` skills never invoked; wiki.py CLI driven from memory instead | campaign log | GAP |
| F7 | What WORKED: memory files, directive ledger, mechanical gates, F1 research-loop briefs (worker eval matrices matched F0 verification 7/7) | waves 2-4 | KEEP |
| F8 | ck:plan/ck:cook ritual never run until now — plans lived in todos, not `./plans/` | today | GAP |

Root cause (one line): **the coordinator plans tasks, not capabilities** — briefs are written before checking what the vault already knows.

## Proposals

### P1 — Router-first inventory law
Every workstream opens with `skill-router.py --strict "<task>"` + vault grep. Router output is a MANDATORY line in every F1 brief (`vault skills available: …`) and in every F0 plan. Cost: ~30s per wave.

### P2 — Plan→Cook ritual for non-trivial waves
Waves with ≥3 phases or ≥1 day effort go through `ck:plan` (persistent plan in `./plans/`) → `ck:cook` execution. This plan is the template run.

### P3 — Skill-combo map per workstream (authored in cook phase)

| Workstream | Vault combo | Repo/tool canon |
|---|---|---|
| Film / video | `agentvid`, `ck:remotion` | video-shotcraft `references/*` (beat-sync, sound-design, sfx-sourcing, pipeline) |
| Web craft / design | `impeccable`, `stop-slop`, `landing-page-design`, `design-taste-frontend`, `ui-motion-craft` | repo design docs + directive ledger |
| Research / web | `ck:research`, `web-audit` | WebSearch/WebFetch F0 |
| Knowledge | `wiki-commit-ritual`, `wiki-rule-author`, `wiki-verifier` | wiki.py CLI |
| Coordination eval | `ck:retro`, `ck:plan`, `ck:cook` | plans/ ledger |
| Infra / g8s | `g8s-supervisor`, `g8s-ux-visual-gate` | AGENTS.md §2 |

### P4 — Session-end skill-usage ledger
Each campaign plan gets a `## Skill Ledger` section: fired / missed (router diff) / promote-to-brief. Retro input for the next wave.

### P5 — Promote to vault knowledge
Distill P1-P4 into wiki rule `R-F0-Skill-Vault-Routing` (GATE E registration) + memory update.

## Phases

1. **Eval** (done — table above)
2. **Cook**: author `skill-combo-map.md` (this dir) + update F1 brief template with router line + draft wiki rule
3. **Apply**: next real wave (film v4 / rig v3 / imagery — standing duyệt queue) runs router-first + combo-cited briefs
4. **Close**: wiki rule registered, memory updated, ledger row in this plan

## Eval gates

- G1: `skill-combo-map.md` exists with ≥6 workstream rows
- G2: F1 brief template contains the router-inventory line
- G3: next wave's briefs cite ≥1 vault skill each (verifiable in brief files)
- G4: wiki rule registered in 4-Rules/README (GATE E) + committed

## Non-goals

- No changes to g8s harness code. No mass skill pruning (audit-only posture). No new skills authored (use what exists).

## Skill Ledger — campaign 261101-03 (FlowAxon v5 eval round)
fired: browser-use:control-browser, ck:plan, ck:cook, video-shotcraft (30%→canon read)
missed: agentvid (router hit for film task, unused), skill-router.py (0 invocations until 11-03), 24 wiki-* skills, ck:research, impeccable/stop-slop (memory-only)
promote-to-brief: agentvid, ck:remotion, impeccable, stop-slop, wiki-commit-ritual, landing-page-design (B7 motion law)
