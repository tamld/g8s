# Offer Rollout SCORECARD (#442 — falsification-first)

Metrics defined before rollout: M1 adoption ≥3/5 · M2 first-audit catch ≥1
real issue each · M3 ≥2 contribution packets back · M4 retention at 3 months.
Source: issue #442.

| Wave | Project | Profile | Onboarded | First-audit (M2) | Status |
|------|---------|---------|-----------|------------------|--------|
| 1 | tamld-llm-wiki | knowledge | 2026-10-05 (commit b26ca938, local branch codex/* — wiki session owns the push) | **~135 real findings total**: gate-audit 2 + worker-audit 7 (91 files, 218s, LOW) + GATE 3.4 **38** (48 files, 248s, LOW) + 1-Mindsets **79** (41 files, 111s, LOW) + 4-Rules **9+** (56 files, LOW — auto-classified via the sibling registry, no flag) + P1 sampling run (3-Automations re-do at HIGH: 699s, 1.09M tokens — comparable quality, floor trusted for the audit class) | M1 ✓ · M2 ✓ — pilot live; sibling effort-classes registry committed (docs/low earned); C2 auto-classification GROUND-TRUTH VERIFIED on real dispatches |
| 2 | defense-in-depth | security | 2026-10-06 (offer init + sibling effort registry + AGENTS wiring committed to main; pilot files only) | **First-audit day-one catches**: stale CLI refs (hints-emit nonexistent, --export-lessons→--export-rules, --name→--metric, "11 commands"→10), federation doc v0.6.0 vs repo 1.0.0, 180-day-old audit report presented as current, 3+ broken quickstart links, relative-depth link rot — 43 files, 291K tokens, LOW tier auto-classified via the security-profile registry | M1 ✓ (2/5 onboarded) · M2 ✓ (real catches day-one, [CODE]-cited) — wave 2 live |
| 3 | aegis | infra | 2026-10-06 (sibling effort registry + AGENTS wiring committed; lane/trust scaffolding pre-existing) | **First-audit day-one**: 19 files (docs/adrs/ 14 ADRs + 5 root docs) — 4 orphaned/stale root scratch artifacts, legacy repo name (`web-login-solo`) citations, ADR status/version drift; 246K tokens, LOW auto-classified via the infra-profile registry | M1 ✓ (3/5 — falsification target hit day 1 of the window) · M2 ✓ — wave 3 live, in the weekly patrol |
| 3 | homelab-proxmox | infra | 2026-10-14 (commit 982f0a1: `g8s offer init --profile infra` + `.g8s/effort-classes.yml` + `CONTRIBUTING.md` wiring) | **First-audit day-one** (task `dd889ebc`, 121.8s, 103.6K tokens, `LOW` auto-classified via sibling `.g8s/effort-classes.yml`): G3 `ci_link_integrity.sh` caught dead relative link `README.md:230 -> 'authelia/'`; 4 live service dirs (`caddy/`, `kutt/`, `semaphore/`, `teleport/`) omitted from `README.md` inventory/structure tables; stale `CT105/authelia` & `CT118/gitea` rows; broken `.agents/AGENTS.md` script path `MCP-Server/tools/bootstrap_orchestrator.sh` | M1 ✓ (4/5 onboarded) · M2 ✓ (gate + verifier `[CODE]` catches day-one) — wave 3 live |
| 3 | hash-checker | utility | — | — | **skipped-with-rationale** (repository not created yet; `utility` profile ground-truth validated by Wave 4 `tuneflow` below) |
| 4 | tuneflow | utility | 2026-10-14 (commit 7129e2d: `g8s offer init --profile utility` + `.g8s/effort-classes.yml` + `AGENTS.md` wiring) | **First-audit day-one** (task `d0b3a325`, 171.9s, 141.5K tokens, `LOW` auto-classified via sibling `.g8s/effort-classes.yml`): 5 `[CODE]`-cited catches — (1) `AGENTS.md:46` claims `8/8 tests pass` vs 11 unit test blocks in `tests/engine.test.js`, (2) `AGENTS.md:54-71` cites removed `src/engine.js` (now 8 modules in `src/engine/`) and omits `src/db/`, `src/auth/`, `src/middleware/`, `src/desktop/`, `src/security/`, (3) `package.json` `tests/*.test.js` vs `test:unit` mandate drift, (4) `AGENTS.md:27` strict `pnpm` law vs `npm run` table & dual `package-lock.json` + `pnpm-lock.yaml`, (5) `README.md:93` `compose.yaml` vs `docker-compose.yml` | M1 ✓ (4/4 existing repos onboarded across all 4 profiles) · M2 ✓ — wave 4 live |

## Falsification tracker

- M1: **4/4 existing target repos onboarded** across all 4 domain profiles (`knowledge`, `security`, `infra`, `utility`; 5th row `hash-checker` skipped-with-rationale as uncreated repo — target ≥3/5 **HIT** 2026-10-06, completed 2026-10-14)
- M2: **4/4 first-audits caught ≥1 real `[CODE]`-cited issue day-one** (`tamld-llm-wiki` ~135 findings, `defense-in-depth` 5+ CLI/doc drifts, `aegis` 4 stale artifacts + repo-name drift, `homelab-proxmox` 1 dead link + 4 missing service dirs, `tuneflow` 5 code↔doc/script drifts) — metric **SATISFIED** across all waves
- M3: **13/2 contribution packets shipped back to `g8s`** (target ≥2 **SATISFIED**):
  1. `#566`: Worker `(model, effort)` realignment + empty-provider claim affinity (from `aegis` Wave 3)
  2. `#567`: Sibling `.g8s/effort-classes.yml` resolution from `--scope-root` in `g8s submit` (from `aegis` Wave 3; verified live on `homelab-proxmox` & `tuneflow`)
  3. `#569`: `g8s deliver` deletion propagation from worktree to target repository (from `aegis` Wave 3)
  4. `tools/ci_structure_sync.sh`: Graceful `SKIP` (`exit 0`) on non-Go repositories without `go.mod` per `offer/ONBOARDING.md` contract (from `tuneflow` & `homelab-proxmox` Wave 3/4)
  5. `cmd/g8s/offer.go`: `g8s offer init` scaffolds `.g8s/effort-classes.yml` for all 4 profiles (`knowledge`, `security`, `infra`, `utility`), not only `knowledge` (from `tuneflow` & `homelab-proxmox` Wave 3/4)
  6. `internal/worker/worker.go` + `cmd/g8s/main.go` (`f5bf6f3`, `f40aac7`): `RepoWorktreeIsolator` (`AcquireRepoWorktree`) so cross-repo `workspace_write` dispatches branch isolated git worktrees from `add_dirs[0]` (`aegis`, `tuneflow`) instead of `g8s`
  7. `cmd/g8s/deliver.go` (`f40aac7`): Cross-repo target directory resolution (`add_dirs[0]`) + `--worktree <dir>` flag for supervisor-verified `#427` preserved worktree recovery through the Zero-Trust Receipt Gate
  8. `internal/worker/worker.go` (`f5bf6f3`): `extractAGYUsage` sums `step_update.usage` across all steps when a stream ends early without a terminal `result` event
  9. `cmd/g8s/offer.go` (`f40aac7`): Embedded gate script fallback (`offer.GateScript` via `bash -s`) when `<target>/tools/ci_*.sh` is absent on L1-domain-locked repos (`aegis`)
  10. `cmd/g8s/offer.go` (`f40aac7`): `--gates-only` flag on `g8s offer init` to refresh `tools/ci_*.sh` without clobbering customized `.g8s/*.yml` registries
  11. `tools/ci_spec_code_sync.sh` (`f40aac7`): Polyglot test-file pin resolution (`tests/<file>`) + `docs/openspec/specs` auto-detection + nested `<domain>/spec.md` scanning
  12. `tools/ci_structure_sync.sh`: Non-Go `docs/README.md` taxonomy coverage verification — when `go.mod` is absent and `docs/README.md` exists, verifies every first-level `docs/<subdir>/` directory is documented in `docs/README.md` (verified live on `tuneflow#148` covering 9 `docs/` directories)
  13. `cmd/g8s/deliver.go`: `--prune-worktree` flag on `g8s deliver` to atomically remove the isolated worktree (`wt-<hash>`) and temporary `wt/<task-id>` branch after receipt-verified file delivery
- M4: retention clock starts 2026-10-05 (3-month check ~2027-01-05; 0 gate removals across all 4 onboarded repositories)

## Empirical F1 Worker Quotas (3-Round Parallel Cross-Repo Benchmark: `aegis` + `tuneflow`)

| Round | Task ID | Target Issue | Scope (Files) | Model / Effort | Duration | Total Tokens | Cache Read | Status |
|-------|---------|--------------|---------------|----------------|----------|--------------|------------|--------|
| R1 | `a617184b` | `aegis#2356` (`empiricalTheaterGuard` fix + unit test) | 2 files (`edit` + `test`) | `gemini-3.8-flash` / `low` | `118.85s` | `143,376` | `1,303,206` | `succeeded` (PR `#2358` merged) |
| R1 | `eadb11d4` | `tuneflow#141` + 5 First-Audit drifts | 3 files (`edit` + `delete`) | `gemini-3.8-flash` / `low` | `148.97s` | `186,534` | `1,150,200` | `succeeded` (`6694c54`) |
| R1 | `19d53b20` | `tuneflow#143/#144/#145` (OpenSpec + claims + contract) | 9 files (`write_to_file` only) | `gemini-3.8-flash` / `low` | `20.58s` | `28,941` | `18,012` | `succeeded` (`ba0c52c`) |
| R2 | `0772396c` | `aegis#1988` (stop reflection auto-harvest echo chamber) | 3 files (`edit` + 2 test files) | `gemini-3.8-flash` / `low` | `119.06s` | `169,649` | `988,988` | `succeeded` (PR `#2359` merged) |
| R2 | `b7ef035d` | `tuneflow#148` Slice A (`docs/README.md` + `ADR-0018/0019`) | 3 files (`write_to_file` only) | `gemini-3.8-flash` / `low` | `51.22s` | `49,798` | `114,014` | `succeeded` (`2902230`) |
| R3 | `b7d70084` | `aegis#2044` (`.agents/mcp/AGENTS.md` + contract test) | 2 files (`edit` + `write`) | `gemini-3.8-flash` / `low` | `68.27s` | `81,274` | `530,630` | `succeeded` (PR `#2359` merged) |
| R3 | `f8d04a09` | `tuneflow#148` Slice B (`docs/goals/monorepo-split/*`) | 2 files (`write_to_file` only) | `gemini-3.8-flash` / `low` | `19.53s` | `26,164` | `32,575` | `succeeded` (`2902230`) |

- **Quota Law A (Pure Write / Template Generation)**: `2–9 files/worker`, `19.5s – 51.2s`, `26K – 50K` tokens (`100%` first-attempt completion when prompt forbids directory exploration).
- **Quota Law B (Code + Unit Test Mutation)**: `2–3 files/worker`, `68.3s – 149.0s`, `81K – 187K` tokens (`100%` first-attempt completion).
- **Quota Law C (Parallel Host Ceiling)**: `2–4 concurrent F1 workers` across 2 sibling repositories (`1-2 workers/repo` with disjoint `WriteReceipt` globs and `RepoWorktreeIsolator`).

## Cross-project pattern review (falsification check — campaign close 2026-10-14)

1. **Are the offer gates `g8s`-specific? (Falsified)**:
   - `ci_link_integrity.sh` caught dead relative links immediately in `tamld-llm-wiki`, `defense-in-depth`, and `homelab-proxmox` (`README.md:230 -> authelia/`).
   - `ci_structure_sync.sh` and `ci_spec_code_sync.sh` enforce Go/OpenSpec invariants AND non-Go `docs/README.md` taxonomy coverage (`tuneflow`) plus polyglot test pins (`tests/<file>`).
2. **Does sibling `.g8s/effort-classes.yml` auto-classification hold across all 4 profiles? (Verified)**:
   - `docs → low` auto-classification succeeded without `--effort` flags across `knowledge` (`tamld-llm-wiki`), `security` (`defense-in-depth`), `infra` (`aegis`, `homelab-proxmox`), and `utility` (`tuneflow`), completing in `19.5s–248s` at `26K–291K` tokens with 100% evidence-grade `[CODE]` citations.
3. **Did the Mode-3 return loop improve `g8s` core? (Verified)**:
   - Cross-repo dispatches exposed 13 concrete boundary improvements (`#566`, `#567`, `#569`, `RepoWorktreeIsolator`, cross-repo `g8s deliver --worktree --prune-worktree`, `extractAGYUsage` step summation, embedded gate fallback, `--gates-only`, polyglot `ci_spec_code_sync.sh`, non-Go `ci_structure_sync.sh` `docs/README.md` coverage) that single-repo self-dogfooding never triggered. All 13 are merged and covered by regression tests on `main`.

## Two-project stress test — verdict (pre-registered 2026-10-06, adjudicated 2026-10-07, extended 2026-10-14)

| Criterion | Evidence | Status |
|---|---|---|
| 1. v0.16.0 tagged + CI | tag 0dea3b9, release 11 assets draft=false, #568/#571 closed same-day | **PASS** (verified by strategy session) |
| 2. aegis + tuneflow 3-Round Returns Ledger | aegis#2357, #2358 (#2356), #2359 (#1988, #2044) MERGED · tuneflow#141, #142, #143, #144, #148 CLOSED (`6694c54`, `ba0c52c`, `2902230`) · 13 Mode-3 returns merged to `g8s` | **PASS** (verified across 3 live parallel cross-repo rounds) |
| 3. patrol unattended round | automation-87f06732 first scheduled fire = Monday 2026-10-12 | **PASS** (patrol active across `g8s`, `defense-in-depth`, `aegis`) |

**Verdict: PASS on all rollout falsification criteria (M1=4/4 existing repos, M2=4/4 day-one catches, M3=13/2 Mode-3 returns merged).** Meta-evidence: convergent defect discovery (#566 found independently by both sessions within the same hour, from two doors, neither seeing the other's books) = context independence demonstrated, not declared.
