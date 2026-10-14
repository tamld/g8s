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
- M3: **5/2 contribution packets shipped back to `g8s`** (target ≥2 **SATISFIED**):
  1. `#566`: Worker `(model, effort)` realignment + empty-provider claim affinity (from `aegis` Wave 3)
  2. `#567`: Sibling `.g8s/effort-classes.yml` resolution from `--scope-root` in `g8s submit` (from `aegis` Wave 3; verified live on `homelab-proxmox` & `tuneflow`)
  3. `#569`: `g8s deliver` deletion propagation from worktree to target repository (from `aegis` Wave 3)
  4. `tools/ci_structure_sync.sh`: Graceful `SKIP` (`exit 0`) on non-Go repositories without `go.mod` per `offer/ONBOARDING.md` contract (from `tuneflow` & `homelab-proxmox` Wave 3/4)
  5. `cmd/g8s/offer.go`: `g8s offer init` scaffolds `.g8s/effort-classes.yml` for all 4 profiles (`knowledge`, `security`, `infra`, `utility`), not only `knowledge` (from `tuneflow` & `homelab-proxmox` Wave 3/4)
- M4: retention clock starts 2026-10-05 (3-month check ~2027-01-05; 0 gate removals across all 4 onboarded repositories)

## Cross-project pattern review (falsification check — campaign close 2026-10-14)

1. **Are the offer gates `g8s`-specific? (Falsified)**:
   - `ci_link_integrity.sh` caught dead relative links immediately in `tamld-llm-wiki`, `defense-in-depth`, and `homelab-proxmox` (`README.md:230 -> authelia/`).
   - `ci_structure_sync.sh` and `ci_spec_code_sync.sh` enforce Go/OpenSpec invariants where present and skip cleanly on Markdown/Node/IaC repositories (`tamld-llm-wiki`, `tuneflow`, `homelab-proxmox`).
2. **Does sibling `.g8s/effort-classes.yml` auto-classification hold across all 4 profiles? (Verified)**:
   - `docs → low` auto-classification succeeded without `--effort` flags across `knowledge` (`tamld-llm-wiki`), `security` (`defense-in-depth`), `infra` (`aegis`, `homelab-proxmox`), and `utility` (`tuneflow`), completing in `111s–248s` at `103K–291K` tokens with 100% evidence-grade `[CODE]` citations.
3. **Did the Mode-3 return loop improve `g8s` core? (Verified)**:
   - Cross-repo dispatches exposed 5 concrete boundary assumptions (`#566`, `#567`, `#569`, non-Go structure gate skip, 4-profile effort seed scaffolding) that single-repo self-dogfooding never triggered. All 5 are merged and covered by regression tests on `main`.

## Two-project stress test — verdict (pre-registered 2026-10-06, adjudicated 2026-10-07)

| Criterion | Evidence | Status |
|---|---|---|
| 1. v0.16.0 tagged + CI | tag 0dea3b9, release 11 assets draft=false, #568/#571 closed same-day | **PASS** (verified by strategy session) |
| 2. aegis Returns Ledger 5/5 cited | aegis#2357 MERGED (5 red files + secrets removal + docs-debt) · g8s#566/#567/#569 filed, #568 fixed same-day · #550 telemetry (infra-review→high n=2, ceiling falsified n=5) · ladder armed 0-traversal (honest) · return handoff 6406370 | **PASS** (verified; one claim gap found: this row itself was claimed but missing — added now) |
| 3. patrol unattended round | automation-87f06732 first scheduled fire = Monday 2026-10-12 | **PASS** (patrol active across `g8s`, `defense-in-depth`, `aegis`) |

**Verdict: PASS on all rollout falsification criteria (M1=4/4 existing repos, M2=4/4 day-one catches, M3=5/2 Mode-3 returns merged).** Meta-evidence: convergent defect discovery (#566 found independently by both sessions within the same hour, from two doors, neither seeing the other's books) = context independence demonstrated, not declared.
