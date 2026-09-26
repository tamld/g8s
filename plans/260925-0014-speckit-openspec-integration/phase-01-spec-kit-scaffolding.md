# Phase 1 — Scaffold Spec Kit & Unify Constitution

## Context Links
- `spec/constitution.md` — SSoT, already declares "Spec Kit Project Constitution Model"
- https://github.com/github/spec-kit — `specify` CLI, `/speckit.*` command chain
- `docs/DOD_DOR.md`, `docs/RULES.md` — repo governance artifacts referenced by templates

## Overview
- **Priority**: P1 | **Status**: Pending
- Install Spec Kit scaffolding **without replacing OpenSpec**: `.specify/` + per-agent command files, constitution unified under one editing surface.

## Key Insights
- The repo already converged toward Spec Kit *vocabulary* (constitution) — this phase completes the *toolchain*.
- `.specify/memory/constitution.md` must be a GENERATED copy: `spec/constitution.md` stays the single editing surface. No symlinks (Windows support per Constitution Axiom 3/4).

## Requirements
- **Functional**: `specify init --here` scaffolding committed; `/speckit.*` command files for Claude Code in `.claude/commands/`; spec template customized to emit an "OpenSpec Delta Mapping" section (which DELTA-XX the feature extends or adds).
- **Non-functional**: dev-time tooling only; zero runtime deps in the shipped binary; every committed file readable without the CLI (offline fallback).

## Architecture
```
spec/constitution.md  (SSoT, humans edit here)
        │  generated copy, sha256-pinned
        ▼
.specify/memory/constitution.md  (GENERATED header — never edit directly)
.specify/templates/*  (customized: delta mapping + PROPOSED→…→APPLIED lifecycle)
.claude/commands/speckit.*.md  (agent command surface)
```

## Related Code Files
- **Create**: `.specify/**` (scripts, templates, `memory/constitution.md`), `.claude/commands/speckit.*.md`
- **Modify**: none
- **Delete**: none

## Implementation Steps
1. `uvx --from git+https://github.com/github/spec-kit.git specify init . --here --ai claude --force` (if `uv`/`pipx` unavailable: vendor templates manually per spec-kit repo layout).
2. Review generated `.specify/scripts/` (bash + pwsh) before committing — dev-only, no network at runtime.
3. Replace `.specify/memory/constitution.md` with a copy of `spec/constitution.md` prefixed by a `<!-- GENERATED from spec/constitution.md — edit the source, then regenerate -->` header.
4. Customize `.specify/templates/spec-template.md`: add **Delta Mapping** section + OpenSpec lifecycle states.
5. Verify: `ls .claude/commands/speckit.*.md` non-empty; header present; `git status` shows only intended additions.

## Todo List
- [ ] specify init scaffolding committed
- [ ] constitution generated copy + GENERATED header
- [ ] spec template customized (delta mapping + lifecycle)
- [ ] offline fallback verified (no CLI needed to read artifacts)

## Success Criteria
- `/speckit.*` commands discoverable by agents; `.specify/` complete and committed; constitution copy hash matches source (foundation for the Phase 3 guard).

## Risk Assessment
| Risk | Mitigation |
|------|-----------|
| `uv`/`pipx` absent on contributor machines | Vendor templates manually; document optional install |
| Upstream template churn | Pin the spec-kit version used in `docs/CHANGELOG.md` entry |

## Security Considerations
- Scripts are dev-only; reviewed before execution; no secrets, no runtime network.

## Next Steps
- Phase 2 wires the routing contract and repairs the registry.
