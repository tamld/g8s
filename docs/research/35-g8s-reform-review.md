# 35 — g8s Reform Review (2026-11-07): what to restructure, what to improve

Author: F0 coordinator review (deep sweep subagent + campaign evidence from 2026-11-01→07).
Scope: whole-repo review requested by the operator after the FlowAxon/g8s debt-closing campaign.
Method: evidence-first — every finding cites file:line or issue/PR; claims-vs-reality diffs preferred over opinions.

## 0. Health snapshot (2026-11-07)

| Metric | Value | Read |
|---|---|---|
| Open issues | 4, median age ~2.8d, oldest #714 (5.8d) | hygiene excellent |
| Issue throughput | 114 closed / 30d (3.8/day); #776 issue→fix→close in 51 min | responsive |
| PR cadence | 5.5 merges/day (60 in 11 days); ~9.5 commits/day | high |
| CI | 13 workflows / ~47 jobs; 10 per PR; ~150-250 CI-min/day; poles: race 13.5m, Win-E2E ~7m | heavy but effective |
| Tests | ~60 packages; coverage baseline 82.43% (DOWN from 84.36% 3 wks ago — see F2); testenv adopted in 8/60 pkgs | mixed |
| ADRs | 27 files; ADR-0038 Proposed; numbering gaps 0004-0013/0025/0036; DELTA-44 skipped | needs tombstones |
| Dogfooding | ~55% of substantive commits bypass the receipt/PR flow (F4) | the big one |

## 1. Top findings (ranked by reform value)

**F1 — Diff firewall fails open in 4 paths; contradicts the repo's own DELTA-26 doctrine.**
`internal/worker/diff_firewall.go:53-54` (empty dir → skip), `:56-57` (`.git` stat error → skip), `:62-63` (git status error → skip), `:103-104` (empty allowedPaths → skip). Inputs come from `resolveReceiptAllowedPaths` (`:120-143`) which returns `nil` on EVERY error path — an infra hiccup silently disables scope enforcement. DELTA-26 bans nil-isolator fallback for isolation; the firewall does the moral equivalent for scope. (Sits behind ADR-0038's own Context admission that mesh claims arbitrate at acquisition only.)
→ Reform R1.

**F2 — Coverage ratchet auto-lowers its own baseline.** `.github/workflows/quality.yml:275-299` unconditionally overwrites `.g8s/coverage-baseline` on every main push, up or down; 57 bot commits walked it 84.36% → 82.43% in 3 weeks. Only the 80% hard gate is real. → 2-line fix: commit baseline only when `avg > baseline`.

**F3 — manifest.json release metadata stale, no tool checks it.** `latest_release.commit: "32020e8"` (0.21-era) vs actual v0.22.0 tag `e7e11c7` (2026-11-02); `ci_version_sync_check.sh:202-231` validates `.tag` only, never `.commit`/`.date`. → repair + extend the checker.

**F4 — Dogfooding mandate is unfalsifiable.** Last 200 commits: ~45 substantive direct-to-main F0 commits; no PR branch matches the harness's own harvest convention (`wt-*`); AGENTS.md mandates g8s-mediated delegation but no metric can falsify compliance. The repo built its own anti-theater methodology — it should eat it. → Reform R3.

**F5 — cli-reference.md missing ~15/70 shipped commands** (`reach`, `scorecard`, `gates`, `audit`, `f0`, `context-pack`, `qa-report`, `claim`, `resubmit`, `offer`, `lesson`, `feedback`, `update`, `uninstall`…). manifest advertises this file as the CLI doc surface. → extend D9-S5's surface-inventory pattern to the CLI surface.

**F6 — Constitution/README name 2025-era models** ("Claude 3.7 Sonnet / GPT-4o / DeepSeek R1") vs registries routing `claude-sonnet-4.6` / `claude-opus-5-5` / `composer-2.5`; `agent-models.yml` still says "verified 2026-10-04 / catalog_version 1" despite the 2026-11-07 probe audit (#786). Exemplar rot in an anti-drift product. → refresh + bump catalog_version.

**F7 — Registry hygiene:** 27 ADRs, gaps without tombstones; DELTA-44 skipped; AGENTS_FULL says DELTA-25 "IMPLEMENTED" while manifest says "APPLIED" — the two SSoT registries disagree.

**F8 — hostile-env test pattern in only 8/60 packages** while #744/#764/#742 were the same inherited-env failure class fixed package-by-package. → shared TestMain promotion.

**F9 — CI duplication + skew:** go vet ×3, go test ×2, gofumpt ×3 across workflows; quality.yml step named "1.25" pinning 1.26.10; version-sync.yml is the only PR workflow without cancel-in-progress.

**F10 — Test-thin tail sits exactly where containment lives:** cmd/jevtriage 0 tests; internal/provider 13:6, conv/diffintel/premise 4:1 — the provider/CLI-adapter layer is the same layer #776 showed escapes containment.

## 2. Cải tổ — 3 structural reforms (one ADR each)

**R1 — Fail-Closed Unification of the delivery boundary.** Generalize DELTA-26's isolation law to ALL enforcement components: *any component that cannot obtain its inputs (git state, receipt scope, claim fence) aborts the attempt with a typed `E_*_UNVERIFIABLE`*. Audit every `return nil, nil` in internal/worker, internal/fsops, internal/mesh. Fold ADR-0038 ratification into this ADR's implementation. Closes the CLASS that #776 instance-closed.

**R2 — Mechanized SSoT: every manifest field and doc claim verified or deleted.** Extend the D9-S5 surface-inventory into a universal claims-vs-wiring gate: manifest `documentation_index` pointers resolve; `latest_release.{commit,date}` matches the tag; every `--help` command has a cli-reference entry or `undocumented: true`; `tools/release.sh` is the only writer of release metadata. Claim without a check → not in the SSoT.

**R3 — Dogfooding honesty ledger.** Every main-landing carries machine-readable provenance (receipt-id, or explicit `F0-direct:` tag); dogfood.yml publishes the monthly ratio; an ADR sets the target band. Either validates the harness against its own development (strongest possible evidence) or scopes the mandate honestly where F0-direct is genuinely right.

## 3. Cải tiến — 5 PR-sized fixes (immediately dispatchable)

1. Fail-loud diff firewall (4 skips → typed errors + receipt-lookup distinction) — ~40 lines + table tests
2. Coverage ratchet monotonicity (baseline commits only when avg > baseline)
3. Manifest release repair (commit `e711c7`… fix + sync-check extension)
4. cli-reference catch-up (15 stubs + help-vs-reference diff check)
5. Model-name refresh + registry status sync (constitution/README exemplars, agent-models catalog_version, DELTA-25 status, ADR tombstones)

## 4. Campaign evidence informing this review

- C13/C14/C15 (collision matrix, plans/261104-g8s-product-site/) — read_only escape, one-subagent-one-repo, header parity
- 9router combo audit (2026-11-07, #786): 8/8 core combos live; cu/* protocol-bound; NIM routing ids double-prefix — registries drifted from reality and only live probes caught it (F6/R2 pattern)
- Stale-binary lesson: a local gate ran against a pre-merge binary and passed — R2's claims-vs-wiring class
- Windows JSON-escape assertion bug (#785): platform-fair assertions law

## 5. Dispatch plan

- P1 (now): cải tiến 1-3 as worker tasks (bounded, test-covered)
- P2: ADR-R1 draft (fold ADR-0038 ratification) → operator ratification → implementation issues
- P3: R2 extension of surface-inventory + R3 provenance ledger (after S2/S3/S4 land — same reflection machinery serves them)
