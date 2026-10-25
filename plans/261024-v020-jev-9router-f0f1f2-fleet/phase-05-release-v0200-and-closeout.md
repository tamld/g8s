# Phase 05 — Release `g8s v0.20.0`, Sync Offer Bundle & Skills, and Final Fleet Closeout

- **Status**: `pending`
- **Targets**: `spec/openspec/32-bounded-agentic-loop-f1f2-fleet-jev-spec.md`, `manifest.json`, `.g8s/feature-map.yml`, `AGENTS.md`, `docs/AGENTS_FULL.md`, `CHANGELOG.md`, `skills/g8s-supervisor/SKILL.md`

## Steps
- [x] **5.1 (OpenSpec `DELTA-32` & Docs Sync)**: Write `spec/openspec/32-bounded-agentic-loop-f1f2-fleet-jev-spec.md`, register `DELTA-32` in `manifest.json`, `.g8s/feature-map.yml`, `AGENTS.md`, `docs/AGENTS_FULL.md`, and `CHANGELOG.md` (`0.20.0`).
- [x] **5.2 (Cut & Push `g8s v0.20.0`)**: Run `G8S_PREPUSH_FAST=1 ./tools/release.sh minor`, rebuild and install `./bin/g8s` to `~/.local/bin/g8s`.
- [x] **5.3 (Sync `dotagents` & `adopter-knowledge-01` + Final `g8s fleet status`)**: Commit and push `dotagents` (`skill-router.py` + `g8s-supervisor`) and `adopter-knowledge-01`, then verify `./bin/g8s fleet status --dir <workspace> --json` reports all 8 repositories clean and pushed.
