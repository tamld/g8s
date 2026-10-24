# Phase 04 — Extend `Jev` into Gate-Lane (`0/D/R/F/S`), Effort (`low..xhigh`), and Skill Routing

- **Status**: `pending`
- **Targets**: `internal/routing/jev_assist.go`, `internal/routing/router.go`, `internal/routing/jev_assist_test.go`, `dotagents/skills/_shared/scripts/skill-router.py`

## Steps
- [x] **4.1 (Jev Gate-Lane & Effort Assist with Safety Monotonicity)**:
  - Add `SuggestedLane string` and `SuggestedEffort string` to `routing.Decision`.
  - In `jevAssist`, include optional `"lane"` (`0`, `D`, `R`, `F`, `S`) and `"effort"` (`low`, `medium`, `high`, `xhigh`) questions in the Jev request payload.
  - Enforce **Safety Monotonicity**: if Jev suggests a higher-rigor lane or higher effort than the deterministic baseline, adopt Jev's upgrade; never allow Jev to downgrade a deterministic trust-boundary lane (`S`) or lower effort below deterministic P0 minimums.
- [x] **4.2 (Jev-Assisted Skill Router in `skill-router.py`)**:
  - Add `--jev` flag (and `G8S_SKILL_ROUTER_MODE=jev_assisted` env support) to `~/.agents/skills/_shared/scripts/skill-router.py`.
  - When enabled and `TYPESAFE_API_KEY` / `TYPESAFE_API_KEYS` (from env or `.env`) is present, send the top candidate skills to `api.typesafe.ai/v1/systemone` to select the highest-relevance JIT skills within the `4000` token budget, recording `"router_source": "jev"` and `"is_fallback": false` (or falling back cleanly to `"router_source": "deterministic", "is_fallback": true` when offline).
