# CI/CD Surface Inventory — `g8s` @ `abdb733` (2026-11-01)

**Scope:** READ-ONLY scout (`Team C`); checkout `HEAD=abdb733`.

**Lane model:** `tools/ci_lane_detect.sh` → `CI_LANE=docs|build` (ADR-0031). Docs lane: early-exit skips build-heavy steps; doc/contract gates still run where unguarded.

---

## 1. WORKFLOWS (13 files under `.github/workflows/`)

| Workflow file | `on:` triggers | Jobs (PR rollup names) | Gates / notes |
|---|---|---|---|
| `ci.yml:10-14` | `push`/`pull_request` → `main` | **Test ubuntu-latest**, **Test macos-latest**, **Test windows-latest** | Lane detect `:37-49`; docs skip `:51-57`. Build lane: gofmt `:65-74`, vet CGO=0 `:76-80`, test CGO=0 `:82-86`, `-race ./internal/...` `:88-92`, `go build ./...` `:94-96`, dogfood self-test `:98-111`, Windows NSIS/WiX `:113-132` (windows matrix only). |
| `quality.yml:17-21` | `push`/`pull_request` → `main` | **Scope Changes**, **Quality Gate**, **Quality Gate (windows-latest)**, **Cross-Platform Build**, **Verify Gate** | Path filter job `path-scope`. **Quality Gate** steps: golangci-lint action `:107-113`, gofumpt `:115-124`, layer DEBT-34 `:126-132`, vet `:134-138`, golangci-lint run `:140-142`, gocognit **warn** `:144-157`, Semgrep self-test+gate `:159-167`, **gitleaks** `:169-174`, ai/brief lint+self-tests `:176-190`, `ci_doc_contract_check.sh` `:192-194`, `claims_check.sh` `:196-198`, link+version sync `:200-206`, debug prints **warn** `:208-217`, docs early exit `:219-225`, coverage ≥80% `:227-277`, ratchet bot `:279-307`, dogfood brief roundtrip `:309-376`, **govulncheck** `:378-380`, fuzz 7 targets `:382-395`. Windows job `:404-451`; cross-compile `:453-495`; aggregator `:497-536`. |
| `codeql.yml:10-16` | `push`/`pull_request`/`schedule` Mon 03:00 UTC | **Analyze (go)**, **Analyze (actions)** | Docs skip `:66-71`; CodeQL init/analyze `:79-97`, `security-extended`, CGO=0 autobuild `:87-91`. |
| `version-sync.yml:3-7` | `push`/`pull_request` → `main` | **Version Sync Validation** | `version.go` vs `CHANGELOG.md` `:22-48`, packaging literals `:50-78`, go.mod vs workflows `:80-100`, `tools/ci_version_sync_check.sh` `:102-103`. |
| `hygiene-guard.yml:2-4` | `pull_request` opened/sync/reopen | **hygiene** (job id) | Worktree count `:20-26`, coverage artifacts in diff `:27-34`, untracked cap `:35-41`. |
| `block-bypass.yml:2-4` | `pull_request` opened/sync/reopen | **Bypass Guard** | Recent file churn heuristic `:23-33`, self-merge message ban `:35-46`. |
| `windows-e2e.yml:3-7` | `push`/`pull_request` → `main` | **Windows E2E Verification** | `go test -tags=windows` `:58-62`, CLI/service install path `:64-83`. |
| `crash-survival.yml:3-7` | `push`/`pull_request` → `main` | **Subprocess Crash-Survival & Supervisor Adoption** | SIGKILL worker `:64-73`, queue survival `:75-80`. |
| `golden-loop.yml:2-7` | `schedule` 06:00 UTC, `workflow_dispatch`, **all PRs to main** | **golden-loop** | Release vs HEAD binary loop `:51-80`. |
| `nightly.yml:8-11` | `schedule` 03:00 UTC, `dispatch` | **Nightly Full Battery** | No lane split; gofmt/gofumpt/vet/lint/test/race/fuzz `-fuzztime 10s` `:40-90`. |
| `dogfood.yml:14-23` | Mon 09:00 UTC, `dispatch`, PR **path-filtered** | **Weekly Self-Dogfooding Audit** | `make dogfood` `:50-54`; issue on failure `:65-98`. |
| `dist-validation.yml:4-7` | `pull_request` → `main`, `dispatch` | **GoReleaser Check & Snapshot** | `goreleaser check` `:53-58`, snapshot `--skip=publish,sign,nfpm` `:64-69`, `release_matrix_check.sh` `:71-73`. |
| `release.yml:13-17` | `push` tags `v*.*.*` | **Verify tag…**, **Build darwin…**, **Build windows…**, **Publish GitHub Release…**, notarize job **`if: false`** `:186-189` | Tag/version assert `:41+`, tests mirror CI, GoReleaser+Syft `:166-179`. |

**Not on every PR:** `Release` (tags only), `Nightly Full Battery`, `Dogfood` (unless path filter), `Dist Validation` (PR to main).

---

## 2. LOCAL GATES

| Artifact | Enforcement |
|---|---|
| `tools/pre_push.sh` | **Blocking** `set -e`; docs lane 6 gates `:95-158`; build lane 11 steps `:161-365`. |
| Docs lane (`:95-158`) | Hygiene blocking `:101-112`; ai_lint+brief_lint blocking `:114-124`; `ci_doc_contract_check.sh` `:127-130`; `claims_check.sh` `:133-138`; `ci_version_sync_check.sh` `:140-145`; `ci_link_integrity.sh` `:147-151`. |
| Build lane | Hygiene `:165-176`; gofmt/gofumpt blocking `:178-200`; layer check+self-test blocking `:202-208`; ai_lint blocking `:211-217`; claims `:220-225`; brief_lint `:227-234`; doc contract `:236-241`; version sync `:243-248`; vet `:250-255`; golangci-lint **skip if missing** `:257-274`; Semgrep blocking `:276-283`; tests+coverage≥80% `:285-332`; race **skipped in `--fast`** `:334-342`; `make dogfood` `:344-348`; cross-platform **skipped in `--fast`** `:351-360`. |
| `tools/setup_hooks.sh:18-41` | Installs `.git/hooks/pre-push` → **`pre_push.sh --fast`** (blocking push). |
| **Absent from pre_push vs Quality CI** | No gitleaks, govulncheck, native fuzz, coverage ratchet bot, dogfood CI shell in quality (uses inline script). |

---

## 3. SECURITY SCANNERS

| Capability | Where wired | Evidence |
|---|---|---|
| **gitleaks** | Quality workflow | `.gitleaks.toml:1`; step `quality.yml:169-174` (`--config .gitleaks.toml --exit-code 1`). |
| **Gosec** | golangci-lint | `quality.yml:140-142`; `.golangci.yml` (via linter bundle comment `:107`). |
| **Semgrep** | Quality + pre_push | `.semgrep/g8s-rules.yml`; `tools/ci_semgrep_check.sh` via `quality.yml:164-167`. |
| **CodeQL** | Dedicated workflow | `codeql.yml` (Go + Actions matrix). |
| **govulncheck** | Quality only (not separate workflow) | `quality.yml:378-380`. |
| **Fuzz** | Quality (1000x–10000x) + Nightly (10s) | `quality.yml:382-395`; `nightly.yml:81-90`. Targets: `FuzzConfigLoad`, `FuzzReceiptEnvelope`, `FuzzWorkerResultParse`, `FuzzSanitizeOutput`, `FuzzVerifyScope`, `FuzzReadAndEdit`, `FuzzSelectPath`. |
| **DAST tests** | **Test code only** (via `go test ./...`) | `internal/server/dast_test.go:18`; `internal/mcp/dast_test.go:14`; `internal/fsops/fuzz_test.go:154-156` (`TestDASTBoundaryVerification`). **No** `-run DAST` in workflows (grep `.github/` → no matches). |
| **IAST / probe suite** | **Test code only** | `internal/harness/probe/iast_dast_test.go:13+`; `internal/cli/select_fuzz_test.go:35-37`. Not isolated in CI. |
| **Red-team suites (OWASP)** | `go test` | `internal/routing/poisoning_red_test.go` (ASI08); `internal/routing/model_substitution_red_test.go` (LLM04/06); `internal/mcp/rogue_tool_red_test.go` (ASI02/04); plus 12 other `*redtest*` files under `tools/`, `cmd/g8s/`, `internal/`. |
| **Property tests** | `testing/quick` | `internal/fsops/scope_property_test.go`, `sanitize_property_test.go`; `internal/cli/envelope_property_test.go`. |

---

## 4. RELEASE TOOLING

| Item | Detail |
|---|---|
| **GoReleaser** | `.goreleaser.yaml`: platforms darwin/linux/windows amd64+arm64 `:15-36`; universal darwin `:44-48`; **checksums** `:66-67`; **SPDX SBOM** `:69-73`; nfpms deb/rpm/apk `:118-129`; homebrew cask `:131-142`. **No `sign:` block** in file (read through `:143`). |
| **Release workflow** | `release.yml:13-17` tag push; Syft install `:166-169`; `goreleaser release --clean --skip=homebrew` `:171-176`. |
| **Dist validation** | Snapshot explicitly `--skip=...sign...` `dist-validation.yml:69`. |
| **`tools/release.sh`** | Bump `cmd/g8s/version.go`, CHANGELOG, commit, tag, push `:9-14`, `:30-43`. |
| **Changelog** | Root `CHANGELOG.md` (Keep-a-Changelog sections; `[Unreleased]` used in version-sync). |
| **Version sync** | `version-sync.yml` + `tools/ci_version_sync_check.sh` (SBOM/syft parity check at `tools/ci_version_sync_check.sh:263-269`). |
| **cosign / GitHub attestations** | **ABSENT** — `rg 'cosign|attestation|provenance' .github/` → no matches. Docs defer cosign to v0.40.0 (`plans/261031-cicd-depth-wave/plan.md:48`). |

---

## 5. TEST TIERS

| Tier | Mechanism |
|---|---|
| **Coverage floor** | 80% aggregate (excl. `cmd/g8s`) `quality.yml:227-277`; mirrored locally `pre_push.sh:301-331`. |
| **Ratchet** | Baseline `.g8s/coverage-baseline` (tracked; current `82.21`); main push bot updates `quality.yml:279-300`; PRs use `tools/ci_coverage_ratchet.sh` (0.25% tolerance) `:302-303`, script header `:1-15`. |
| **Race** | CI `ci.yml:88-92`; Quality coverage uses CGO=1 `:229-230`; Nightly `:76-79`. |
| **Windows E2E** | Dedicated workflow + Quality windows job + CI windows matrix installers. |
| **Crash-survival** | `crash-survival.yml`. |
| **Golden-loop** | Daily + PR `golden-loop.yml`. |
| **Nightly** | `nightly.yml:10` cron 03:00 UTC. |
| **Mutation testing** | **Not in CI**; process-only `docs/directives.md:20` (D-08 quarterly hand-mutation; go-mutesting blocked on Go 1.26). |

---

## 6. GOVERNANCE HOOKS IN CI

| Gate | Path |
|---|---|
| **Feature map** | `tools/ci_feature_map_sync.sh` → `g8s feature verify`; invoked from `ci_doc_contract_check.sh:240-245` (Quality `:192-194`). |
| **OpenSpec / DELTA (G1)** | `tools/ci_spec_code_sync.sh` in `ci_doc_contract_check.sh:164+`; structure sync `:177`. |
| **Doc contracts** | `tools/ci_doc_contract_check.sh` (14 sub-checks incl. SAST triad contract `:198+`). |
| **ai_lint / brief_lint** | `quality.yml:176-190`; DEBT-21/49/51. |
| **Layer gate DEBT-34** | `tools/ci_layer_check.sh` + `ci_layer_check_test.sh`; Quality `:126-132`. |
| **A6 whitespace exemption** | Documented `tools/ci_layer_check.sh:14`; tested `tools/ci_layer_check_test.sh:199-211`. |
| **`TestCommands_Audit`** | `tools/f0/commands_test.go:291` — **unit test for `f0 audit`**, not a GitHub Actions job (`rg 'f0 audit|tools/f0' .github/` → no matches). |
| **Coverage baseline bot** | `quality.yml:279-300` (`contents: write` on Quality job `:81-82`). |
| **Quantitative claims** | `tools/claims_check.sh` `quality.yml:196-198`. |
| **Link integrity** | `tools/ci_link_integrity.sh` `quality.yml:200-202`. |

---

## 7. GAPS SELF-FLAGGED (docs vs wiring)

| Claim | Verdict @ `abdb733` |
|---|---|
| **gitleaks existed but unwired** (`plans/261031-cicd-depth-wave/plan.md:25`) | **CLOSED on main** — wired `quality.yml:169-174`. |
| **AGENTS_FULL DAST/IAST commands** (`docs/AGENTS_FULL.md:196-197`) | **Partial** — tests exist; **no workflow step** runs `-run DAST` / `-run TestIAST` (searched `.github/**`). |
| **README "govulncheck, fuzz" workflows** (`README.md:369` tree comment) | **Partial** — no standalone `govulncheck.yml`/`fuzz.yml`; both live under **Quality** (+ Nightly fuzz). |
| **cosign / SLSA attestation** (`docs/security/VERIFICATION_GUIDE.md:86+`, roadmap v0.40.0) | **Absent in CI/release automation** (no `.github` matches). |
| **Mutation CI** (`docs/ENTERPRISE_LEDGER.md:77`) | **Absent** — documented quarterly manual D-08 only. |

---

## Summary Table

| Capability | Status | Evidence path |
|---|---|---|
| Dual-lane CI (docs/build) | Present | `tools/ci_lane_detect.sh`; workflows using `CI_LANE` |
| PR quality gate bundle | Present | `.github/workflows/quality.yml` |
| gitleaks in CI | Present | `quality.yml:169-174`, `.gitleaks.toml` |
| SAST triad (gosec+Semgrep+CodeQL) | Present | `quality.yml`, `codeql.yml`, `.semgrep/g8s-rules.yml` |
| govulncheck | Present | `quality.yml:378-380` |
| DAST/IAST as dedicated CI steps | Partial | Tests in `internal/*/dast_test.go`, `probe/iast_dast_test.go`; not in workflows |
| Fuzz in CI | Present | `quality.yml:382-395`, `nightly.yml:81-90` |
| Coverage 80% + ratchet bot | Present | `quality.yml:227-307`, `.g8s/coverage-baseline`, `tools/ci_coverage_ratchet.sh` |
| Feature-map / spec sync gates | Present | `ci_doc_contract_check.sh:164-245` |
| Release SBOM + checksums | Present | `.goreleaser.yaml:66-73`, `release.yml:166-176` |
| cosign / artifact attestations | Absent | `rg` `.github/` + `.goreleaser.yaml` — no signing config |
| Mutation testing automation | Absent | `docs/directives.md:20` (manual quarterly) |
| pre_push parity with Quality | Partial | `tools/pre_push.sh` — no gitleaks/govulncheck/fuzz |
