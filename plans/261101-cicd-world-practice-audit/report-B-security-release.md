# CI/CD World Practice Audit — Team B: Security, Packaging & Release (2025–2026)

**Project framing:** `g8s` — Go 1.26 CLI + MCP server, GitHub Actions, GoReleaser + SBOM + checksums, gitleaks full-history, CodeQL weekly, govulncheck gate, OWASP agentic/LLM red-tests in CI; operator homelab (self-hosted fleet, LAN deploy targets).
**Date:** 2026-11-01 · **Researcher:** Team B (ck:research, budget 5 searches + primary-doc fetches)

---

## 1. Executive Summary

1. **SLSA Build L3 is now practically achievable on GitHub Actions** without third-party builders, via native artifact attestations (`actions/attest` v4, Sigstore keyless, `gh attestation verify`). One caveat: attestations for private/internal repos require GitHub Enterprise Cloud — relevant if any g8s fork stays private.
2. **GoReleaser v2.x has consolidated the whole distribution problem**: checksums, SBOM, attestations, macOS notarization (cross-platform via quill), and publishing to Homebrew, winget, scoop, nix, chocolatey, AUR, krew, **npm** and the **MCP Registry** from one config. g8s's MCP-server identity makes the MCP Registry publisher a differentiating, low-cost win.
3. **govulncheck as blocking gate on reachable symbols only** is the 2025–2026 consensus; OSV-Scanner/Trivy/Grype run advisory. Scanner output divergence is large (one study: 66.8% of findings reported by only one of three scanners), so depth beats tool count.
4. **Secrets scanning consensus is "both tools, different lanes"**: gitleaks for fast pre-commit/PR gates, TruffleHog for verified findings in CI (800+ detectors, live credential validation). GitHub-native push protection complements both.
5. **OWASP Agentic AI Top 10 is final (Dec 2025)**, a 2026 LLM Top 10 was unveiled Sept 2026, and OWASP shipped an Agent Control Standard (ACS). Prompt-injection regression in CI is now a real pattern: mcp-scan (tool-manifest audits) + promptfoo (MCP plugin) + garak (probes) are the reusable OSS stack.
6. **Windows signing's cheapest solid answer is Azure Artifact Signing** (renamed from Trusted Signing, ~US$10/mo); **macOS notarization** costs the US$99/yr Apple Developer membership and GoReleaser can do it from Linux runners.
7. For CLI distribution, **"canary" means channels (stable/rc/nightly), not progressive rollout**; automated rollback applies to server components (Argo Rollouts/Flagger pattern, or fleet-side pinning for a homelab).

---

## 2. Methodology

- **5 web searches** (SLSA/attestations, OWASP-agentic/MCP tooling, Go vuln scanning, secrets scanning, Go release signing/distribution) + **8 primary-doc fetches** (docs.github.com, github.com/ossf/scorecard, goreleaser.com, slsa.dev, genai.owasp.org, github.com/actions/attest-build-provenance, github.com/googleapis/release-please).
- **Recency window:** Nov 2025 – Nov 2026 prioritized; older only for stable specs (SLSA v1.2, Scorecard checks).
- **Cross-reference rule:** ≥2 sources for major claims; single-source items flagged "(single study)".
- **Sources** are cited inline as `[S#]` and listed in §7.

---

## 3. Key Findings

### 3.1 SLSA, attestations & Scorecard (Q1)

**F1 — SLSA v1.2 is the current spec (Approved)**; it defines levels + tracks and recommended attestation formats including provenance [S1]. Build L3 = hardened, tamper-resistant builds (isolated/ephemeral environments) — the L2→L3 jump is the one that matters in practitioner write-ups [S2].

**F2 — GitHub-native attestations reached "Build L3-grade" in practice.** GitHub positions Artifact Attestations as the path to SLSA Build Level 3 [S3]; generated via `actions/attest` v4 / `attest-build-provenance` (v4 is now a wrapper over `attest`), in-toto provenance bound with a **short-lived Sigstore-issued certificate** (public-good Sigstore for public repos, GitHub's private instance for Enterprise) and verified with `gh attestation verify` [S4][S5]. Requires `id-token: write`, `attestations: write`; public repos on all current plans, **private/internal repos need Enterprise Cloud** [S5].
*Controversy:* the SLSA community still distinguishes provenance quality from builder isolation (hosted runners are not fully isolated); strict-L3 shops keep the Google `slsa-github-generator` reusable-workflow path [S2][S6]. Consensus: for a normal OSS Go CLI, native attestations + checksums are the accepted 2025–2026 floor.

**F3 — What maintained Go CLIs actually ship today:** SHA256 checksums (baseline), GitHub Release, native provenance attestations, and increasingly SBOM attestations; cosign signing dominates for **container** artifacts, attestation-style provenance for binaries. GoReleaser natively supports checksums, SBOMs, and attestations sections [S7], so "checksum + provenance" is one config flag away.

**F4 — OpenSSF Scorecard checks GitHub-native tooling does NOT cover** [S8]: `Pinned-Dependencies` (pin actions by SHA), `Token-Permissions` (read-only GITHUB_TOKEN default), `Dangerous-Workflow` (pull_request_target/script-injection detection), `Binary-Artifacts`, `Branch-Protection`, `Signed-Releases` (looks for `.sig`/`.sigstore`/`.intoto.jsonl` in last 5 releases; SLSA provenance scores max), `SBOM`, `Security-Policy` (SECURITY.md), `Dependency-Update-Tool`, `CII-Best-Practices`. CodeQL alone satisfies only `SAST`.

### 3.2 Release engineering (Q2)

**F5 — Changelog/versioning consensus for Go: release-please.** Release Please (googleapis) parses Conventional Commits and opens a **Release PR**; merging it bumps versions, updates CHANGELOG.md, tags, and creates the GitHub Release; a `go` release type exists; monorepo via manifest config [S9]. It does **not** publish artifacts — pair with GoReleaser. Alternatives: semantic-release (fully automated, heavier, JS-first) and changesets (multi-package JS-centric). Consensus for Go CLIs: release-please + GoReleaser on trunk; release trains are mainly for SDK/platform shops.

**F6 — Pre-release channels & safe rollout.** GoReleaser supports `nightly` and snapshot builds [S7]; kubectl models the channel pattern (`dl.k8s.io/release/stable.txt` fetched by installers) [S10]. For **CLI** distribution there is no canary — channels + fast patch releases are the pattern; for **server** components canary + automated rollback is standard via Argo Rollouts or Flagger [S11][S12]. For a self-hosted homelab fleet, the equivalent is version-pinned channels + scheduled/verified upgrade tooling. HashiCorp-style checksum+signature verification UX (SHA256SUMS + signature file, user verifies before install) remains the reference for infra CLIs [S13].

**F7 — Binary signing, cheapest solid setup (2025–2026):**
- **macOS:** GoReleaser `notarize.macos` cross-platform path embeds quill — signs + notarizes from **any OS** with a Developer ID `.p12` + App Store Connect API key (`.p8`); Apple Developer Program US$99/yr; binaries/universal binaries only (App Bundles need GoReleaser Pro native notarytool path) [S14][S15].
- **Windows:** **Azure Artifact Signing** (renamed from Azure Trusted Signing, Mar 2025 reports) is the recognized cheapest modern option for OSS code signing (basic tier ≈ US$10/mo, price to re-verify at contract time) [S16][S17]. Known friction: runner-image issues signing large batches on windows-2025 runners [S18].

### 3.3 Go supply-chain hygiene in CI (Q3)

**F8 — govulncheck is the authoritative blocking gate.** Its call-graph analysis reports only vulnerabilities in symbols actually reachable, cutting false positives vs manifest scanners [S19][S20]. Emerging pattern: **fail on reachable findings, advisory for the rest**; runs as a nightly/weekly job plus PR gate [S21]. Caveat: it only sees *effective* (used) dependencies — a deliberately unused vulnerable dep won't appear [S22].

**F9 — OSV-Scanner / Trivy / Grype: all actively maintained in 2026.** OSV-Scanner (Google, osv.dev) for lockfiles/manifests [S23]; Trivy (Aqua) and Grype (Anchore) for images/distro/IaC breadth [S23][S24]. Trivy has documented Go-binary consistency gaps vs govulncheck [S24]. One 2025 study: 66.8% of findings reported by only one of Trivy/Grype/OSV-Scanner, 2.7% by all three (single study) [S25]. Recommendation reflected across write-ups: govulncheck (Go, blocking) + exactly one broad scanner (advisory) [S23].

**F10 — Dependency hygiene:** both Renovate and Dependabot satisfy Scorecard's `Dependency-Update-Tool` [S8]; Renovate is preferred by maintainers who need grouping/automerge/lockfile-maintenance [S26]. Module verification (GONOSUMCHECK/GONOSUMDB) is effectively legacy — the module proxy + `go.sum` + GONOSUMDB opt-outs are rarely touched; `GOFLAGS=-mod=readonly` and pinned tool deps (`tools.go` / `go run tool@version`) are the norm. GitHub Dependency Review Action gates PRs on new vulnerable/licence-problematic deps (native).

### 3.4 Packaging & distribution channels (Q4)

**F11 — GoReleaser v2.x is the one-config distribution hub** (v2.19 current in docs): Homebrew (brews + new declarative cask hooks), **winget, scoop, chocolatey, AUR, nix (+NUR), krew, npm, and MCP Registry**, plus Docker v2, ko, Blob storage, SBOM, attestations, checksums, changelog [S7]. This removes the need for hand-maintained tap/winget PRs.

**F12 — npm-wrap pattern:** ship platform-specific binaries as `optionalDependencies` with **no postinstall lifecycle scripts** (esbuild's model), with npm provenance attestations enabled. Rationale is the Sept 2025 `postmark-mcp` malicious npm MCP server incident — the same event OWASP cites to motivate MCP supply-chain checks [S27]. Lifecycle-script-free installs are now the expected hygiene for npm-distributed native binaries.

**F13 — `curl | sh` install scripts remain the dominant bootstrap** (uv, deno, kubectl, Homebrew) but 2025–2026 expectation is: pinned/derived version, **checksum verification inside the script**, and brew/scoop/etc. as documented alternatives [S10][S28]. Consensus: acceptable with verification; never the only install path.

**F14 — OCI images for the server side:** ko (Go-native, reproducible, distroless-by-default) or GoReleaser Docker v2 → ghcr.io, cosign-signed, with SBOM + provenance attestations [S7]. This is the mature default for the `g8s serve` container.

### 3.5 AI / agent-infrastructure security in CI (Q5)

**F15 — Standards status:** OWASP **LLM Top 10 2025** is final (Nov 2024) and a **2026 Top 10 for LLM Applications** was unveiled Sept 2026 together with the new **Agent Control Standard (ACS)** [S29]; the **Top 10 for Agentic Applications was released Dec 2025** (agent goal hijacking, tool misuse, identity/privilege abuse, memory/context manipulation, supply-chain compromise) [S27][S30][S31].

**F16 — Automatable in CI vs runtime-only:**
- *CI-automatable:* MCP tool-manifest auditing & tool-poisoning detection (**mcp-scan** — hash-pins every MCP tool description, "npm-audit for MCP", three-line CI check [S32][S33]), harness/skill/prompt-injection scanning (**snyk agent-scan** [S34]), prompt-injection & tool-abuse **regression suites** via **promptfoo red-teaming (dedicated MCP plugin)** [S35] and **garak (NVIDIA) probe batteries** [S36]; plus static gates aligned to OWASP DevSecOps security-gate principles [S37].
- *Runtime-only (cannot be a CI check):* live agent goal hijacking, memory/context manipulation, identity/privilege abuse in execution — these need dispatch-time gates and telemetry (maps directly onto g8s's Jev reflex lanes / receipt model).

**F17 — MCP-specific tooling is young but real:** mcp-scan (Invariant), Snyk agent-scan, commercial MCP scanner comparisons (Brandefense) [S33][S34][S38]; academic tooling for automated MCP vulnerability detection appeared Oct 2025 [S39]. Publishing g8s's MCP server to the official registry (GoReleaser support [S7]) with pinned server metadata is the supply-chain counterpart.

### 3.6 Secrets & policy-as-code (Q6)

**F18 — gitleaks vs TruffleHog: no single winner; role split is the consensus.** gitleaks: single binary, MIT, offline, <1s — the pre-commit/PR blocking gate (most-starred secrets scanner) [S40][S41]. TruffleHog: 800+ detectors with **live secret verification**, CI/CD + docker/cloud-source scanning — the CI deep-scan layer with far fewer false positives [S40][S41][S42]. GitHub push protection (native secret scanning) blocks the most common tokens at push time [S43].

**F19 — Policy-as-code by stage:** PR time → OPA/**conftest** on config/IaC diffs + **GitHub rulesets** (successor to branch protection; REST-manageable, includes required checks/commit-signing/deletion rules) [S44][S45]. Deploy time → artifact admission policies over attestations/SBOM (verify `gh attestation verify` results or cosign policy before fleet rollout). Mature OSS projects keep PR-time policy in-repo and deploy-time policy minimal unless running a registry with policy enforcement.

---

## 4. Comparative Table

| Practice | 2025–2026 current standard | Key tools (version) | Maturity |
|---|---|---|---|
| Provenance attestations | Native GitHub attestations, Sigstore keyless | `actions/attest` v4 [S4][S5] | High (private repos need E. Cloud) |
| SLSA spec | v1.2 (Approved) | slsa.dev [S1] | Stable |
| Strict Build L3 | Isolated reusable-workflow builds | slsa-github-generator [S6] | Niche / controversial |
| Release scoring | OpenSSF Scorecard incl. workflow-hygiene checks | scorecard-action [S8] | High |
| Changelog/versioning | Conventional Commits → Release PR | release-please (go type) [S9] | High |
| Binary packaging | One GoReleaser config → brew/winget/scoop/nix/choco/AUR/npm/MCP Registry | GoReleaser v2.19 [S7] | High |
| macOS notarize | Cross-platform (quill) + ACS API key, $99/yr | GoReleaser notarize [S14] | High |
| Windows codesign | Azure Artifact Signing (~$10/mo) | artifact-signing-action [S16] | Medium (runner friction) |
| Go vuln gate | govulncheck blocking on reachable only | govulncheck v2 (x/vuln) [S19][S20] | High consensus |
| Broad vuln scan | One advisory scanner, scheduled | OSV-Scanner v2 / Trivy / Grype [S23] | High (output diverges) |
| Dep updates | Renovate (grouped/automerge) or Dependabot | Renovate [S26] | High |
| Secrets — fast gate | gitleaks pre-commit + PR | gitleaks v8 [S40] | High |
| Secrets — verified CI scan | TruffleHog live verification | TruffleHog v3 [S41] | High |
| MCP security CI check | Tool-manifest pinning + poisoning scan | mcp-scan, snyk agent-scan [S32][S34] | Emerging |
| Prompt-injection regression | Red-team suites as CI tests | promptfoo (MCP plugin), garak [S35][S36] | Emerging |
| Agent risk taxonomy | OWASP Agentic Top 10 (Dec 2025) + LLM Top 10 2026 + ACS | genai.owasp.org [S29][S27] | Fresh (practice lags) |
| CLI rollout | Channels (stable/rc/nightly), not canary | GoReleaser nightlies; kubectl stable.txt [S10] | High |
| Server rollout | Canary + automated rollback | Argo Rollouts / Flagger [S11][S12] | High (K8s contexts) |
| Server packaging | ko / Docker v2 → ghcr + cosign + SBOM | ko, GoReleaser [S7] | High |
| Policy-as-code | conftest/OPA at PR; rulesets for repo governance | conftest, rulesets [S44][S45] | High |

---

## 5. Recommendations for g8s (prioritized)

**P1 — low cost, do now (hours each)**
1. Add `actions/attest-build-provenance` (+ SBOM attestation) to the release workflow; publish checksums `.sigstore`/provenance so Scorecard `Signed-Releases` maxes [S5][S8]. *(Cost: ~1–2 h)*
2. Pin all third-party Actions by full SHA + default `permissions: contents: read` job-scoped writes → fixes Scorecard `Pinned-Dependencies` + `Token-Permissions` + `Dangerous-Workflow` [S8]. *(1–2 h)*
3. Make govulncheck **blocking on reachable findings** in PR CI + weekly advisory run [S19][S21]. *(1 h)*
4. Add SECURITY.md, wire Scorecard action (badge), keep gitleaks full-history in CI; add TruffleHog verified scan weekly [S8][S41][S42]. *(2 h)*
5. Adopt release-please (`go` type) with Conventional Commits; GoReleaser consumes the tag [S9][S7]. *(0.5 d incl. convention migration)*

**P2 — medium cost (days)**
6. Publish via GoReleaser to **Homebrew tap + winget + npm (no-postinstall wrap) + MCP Registry** — the last is identity-differentiating for g8s [S7]. *(1–2 d)*
7. Add **mcp-scan to CI** (pin/poisoning check of the g8s MCP server manifest) + **promptfoo red-team suite** covering OWASP Agentic categories (tool misuse, goal hijacking) as nightly regression [S32][S35]. *(2–3 d incl. scenario authoring)*
8. macOS notarization via GoReleaser quill path (needs US$99/yr Apple Developer); Windows via Azure Artifact Signing (~US$10/mo) once binaries ship to Windows users [S14][S16]. *(1 d + procurement)*
9. Add Renovate with grouped minor/patch automerge + lockfile maintenance; keep Dependabot alerts on [S26][S8]. *(0.5 d)*

**P3 — larger / conditional**
10. ko/cosign OCI pipeline for `g8s serve` with SBOM + provenance, verified at LAN-fleet deploy time (attestation check before rollout — deploy-time policy replaces CI-only enforcement) [S7][S45]. *(3–5 d)*
11. Channel model (stable/rc/nightly) for CLI; if g8s server becomes fleet-resident, adopt Argo Rollouts/Flagger-style canary or homelab-side pinned-channel upgrades [S10][S11]. *(3–5 d)*
12. Evaluate garak probes + OWASP ACS conformance once the 2026 LLM Top 10 list's test mappings stabilize [S29][S36]. *(open-ended)*

---

## 6. Open Questions

1. Exact current Azure Artifact Signing pricing/quota for open-source (listed ~US$10/mo basic; verify identity-validation requirements for personal Microsoft accounts) [S16][S17].
2. Does g8s remain public-only on GitHub? If any private fork needs attestation verification, Enterprise Cloud is required [S5].
3. Scorecard `Signed-Releases` accepts SLSA provenance for max score — confirm `.intoto.jsonl` from `attest` counts without also shipping cosign `.sig` files [S8].
4. mcp-scan maintenance trajectory post-Invariant/Snyk consolidation — single-vendor risk; is snyk agent-scan the durable bet? [S32][S34]
5. OWASP ACS: is a conformance test-suite emerging that can run in CI, or is it advisory-only for 2026? [S29]

---

## 7. Sources

- [S1] https://slsa.dev/spec/v1.2/ — SLSA v1.2 spec (current, Approved)
- [S2] https://echoai.com/glossary/slsa (L2→L3 explainer) + https://sonarsource.com (SLSA overview) — search-surfaced explainers
- [S3] https://github.blog/tag/github-artifact-attestations — GitHub: attestations path to SLSA Build L3
- [S4] https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds — actions/attest v4 usage, `gh attestation verify`
- [S5] https://github.com/actions/attest-build-provenance — Sigstore keyless, plan limitations
- [S6] https://github.com/slsa-framework/slsa-github-generator — strict-L3 isolated builds
- [S7] https://goreleaser.com/customization/ — GoReleaser v2.19 targets (brews/casks, winget, scoop, nix, choco, AUR, npm, MCP Registry, SBOM, attestations, notarize, nightlies)
- [S8] https://github.com/ossf/scorecard/blob/main/docs/checks.md — full Scorecard check list
- [S9] https://github.com/googleapis/release-please — Release PR flow, `go` release type
- [S10] https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/ — stable.txt channel pattern
- [S11] https://argoproj.github.io/argo-rollouts/ — canary + automated rollback
- [S12] https://flagger.app — progressive delivery
- [S13] https://developer.hashicorp.com/terraform/downloads — checksum+signature verification UX reference
- [S14] https://goreleaser.com/customization/notarize/ — quill cross-platform notarization, ACS API key
- [S15] https://gist.github.com/Jolg42/201f75a3699f5742a9605170dc50bfbc — signing/notarization checklist
- [S16] https://forum.xojo.com/t/windows-code-signing-installer-creation-with-innosetup/84732?page=2 — Azure Trusted Signing → "Azure Artifact Signing" rename, cheapest option
- [S17] https://github.com/Azure/artifact-signing-action — official action
- [S18] https://github.com/actions/runner-images/issues/13538 — windows-2025 signing friction
- [S19] https://semaphore.io/blog/govulncheck — call-graph reachability
- [S20] https://github.com/draugr-dev/draugr/blob/main/internal/scanners/govulncheck.md — reachable-only pattern
- [S21] https://owasp.github.io/DevSecOpsGuideline/2-3-Build/2-3-5-Security-Gates — blocking-gate principle
- [S22] https://dev.to/oleg_sydorov/the-subtleties-of-vulnerability-scanning-in-go-projects-26fm — effective-deps caveat (Jul 2025)
- [S23] https://safeguard.sh/resources/blog/open-source-vulnerability-management-tools-compared — scanner positioning
- [S24] https://github.com/aquasecurity/trivy/issues/2698 + https://github.com/aquasecurity/trivy/discussions/6669 — Trivy Go-binary gaps
- [S25] https://www.linkedin.com/posts/olegpasichnyk_vulnerabilities-secrets-and-misconfiguration-activity-7493371291442503682-QXDp — 66.8% divergence study (single study)
- [S26] https://docs.renovatebot.com/ — Renovate grouping/automerge/lockfile maintenance
- [S27] https://www.polygraf.ai/?p=28067 + https://medium.com/@vito.rallo/owasp-dropped-a-top-10-for-agentic-ai-heres-what-actually-matters-a0b95ce32c16 — Agentic Top 10 (Dec 2025) incl. postmark-mcp incident
- [S28] https://docs.astral.sh/uv/getting-started/installation/ — checksum-verified installer example
- [S29] https://genai.owasp.org/ — LLM Top 10 2025; 2026 Top 10 + Agent Control Standard (ACS, Sept 2026)
- [S30] https://zenity.io/resources/white-papers/owasp-top-10-for-agentic-app-security — agentic risk categories
- [S31] https://www.darkreading.com/cyber-risk/owasp-agentic-ai-top-10-a-practitioner-s-guide — practitioner guide
- [S32] https://invariantlabs.ai/blog/introducing-mcp-scan — tool-poisoning detection
- [S33] https://stytch.com/blog/mcp-scan — "npm-audit for MCP", CI check pattern
- [S34] https://github.com/snyk/agent-scan — harness/MCP/skills injection scanning
- [S35] https://www.promptfoo.dev/docs/red-team/plugins/mcp — promptfoo MCP red-team plugin
- [S36] https://github.com/leondz/garak — NVIDIA LLM probe scanner
- [S37] https://owasp.github.io/DevSecOpsGuideline/ — DevSecOps gates
- [S38] https://brandefense.io/blog/mcp-scanner-audit-mcp-servers — MCP scanner landscape
- [S39] https://arxiv.org/html/2510.23673v1 — automated MCP vuln detection (Oct 2025)
- [S40] https://appsecsanta.com/secret-scanning-tools/gitleaks-vs-trufflehog + https://rafter.so/blog/secrets/secret-scanning-tools-comparison — role split
- [S41] https://secrails.com/blog/trufflehog-vs-gitleaks-github-secret-scanning-guide — TruffleHog verification, 800+ detectors
- [S42] https://safeguard.sh/resources/blog/best-secrets-scanning-tools-for-cicd-pipelines — CI/CD secrets tooling
- [S43] https://docs.github.com/en/code-security/secret-scanning/push-protection-for-repositories-and-organizations — native push protection
- [S44] https://www.conftest.dev — OPA policy testing for config
- [S45] https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets — rulesets vs branch protection
