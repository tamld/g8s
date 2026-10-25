# Industry Security Standards Mapping: OWASP Agentic, SLSA & SBOM

> **Scope**: Formal verification matrix mapping `g8s` architectural controls, runtime invariants, and release supply-chain artifacts against **OWASP Top 10 for LLM & Agentic Applications (2025)**, **SLSA v1.0 Build Provenance**, and **SPDX/CycloneDX Software Bill of Materials (SBOM)** (Issue `#639`).

---

## 1. OWASP Top 10 for LLM & Agentic Applications (2025) Control Matrix

| OWASP ID | Threat Category | `g8s` Architectural Mitigation | Primary Code Surface | Automated Verification Evidence |
| :--- | :--- | :--- | :--- | :--- |
| **LLM01 / ASI01** | **Prompt Injection & Indirect Tool Hijacking** | Deterministic Pre-Execution Gate (`internal/harness/safety.go`), `BlindReviewPacket` stripping worker self-report (`internal/verifier/blind_packet.go`), and Pre-AST Secret/Prompt Sanitizer (`internal/contextpack/sanitizer.go`). | `internal/harness`, `internal/verifier`, `internal/contextpack` | `internal/harness/safety_test.go`, `internal/verifier/blind_packet_test.go` |
| **LLM02 / ASI02** | **Sensitive Information Disclosure (DLP)** | Fail-closed DLP scanner (`internal/fsops/dlp.go`) blocking private keys, tokens (`ghp_`, `sk-`, `AKIA`), and LAN topology before file writes, MCP responses, or ContextPack output. | `internal/fsops`, `internal/contextpack` | `internal/fsops/dlp_test.go`, `internal/contextpack/contextpack_test.go` |
| **LLM03 / ASI03** | **Supply Chain & Skill Poisoning (TOCTOU)** | Digest-pinned `skill://g8s/<tier>/<name>` registry (`skills/manifest.json`) verified at hydration time via SHA-256 `expected_digest` (`E_SKILL_DIGEST_MISMATCH`). | `internal/mcp/skills_tools.go` | `internal/mcp/stratum_skills_test.go` |
| **LLM04** | **Data & Model Poisoning / Degradation** | Continuous Model Scorecard & Reliability Governor (`internal/routing/scorecard.go`) computing rolling PRI (`0.40*Pass + 0.30*Schema + 0.20*FirstPass + 0.10*1{P0==0}`) with automatic `QUARANTINED` circuit-breaker. | `internal/routing`, `cmd/g8s` | `internal/routing/scorecard_test.go` |
| **LLM05** | **Improper Output Handling** | Recursive JSON Schema Draft-7 validation (`--output-schema`) and mechanical `RoundReceiptSchema` firewall preventing raw unvalidated worker prose from mutating state. | `internal/cli`, `internal/orchestrator` | `internal/orchestrator/verify_test.go` |
| **LLM06 / ASI04** | **Excessive Agency & Privilege Escalation** | 2-Axis Orthogonal Containment (`F0/F1/F2` $\times$ `S0..S3` in `internal/charter`), single-use path-scoped `WriteReceipt` (`AllowedPaths`), and role/permission matrix (`read_only` cannot hold write receipts). | `internal/charter`, `internal/receipt`, `internal/harness` | `internal/charter/charter_test.go`, `internal/receipt/receipt_test.go` |
| **LLM07** | **System Prompt & Credential Leakage** | Strict environment scrubbing (`G8S_API_KEY` and provider credentials stripped from child worker environments and redacted from all JSON envelopes and error paths). | `internal/worker`, `cmd/g8s/receipt_export.go` | `internal/worker/worker_test.go` |
| **LLM08** | **Vector & Knowledge Vault Weakness** | Decoupled SQLite FTS5 Knowledge Vault (`internal/vault`) with L3 Promotion Gate preventing unverified worker claims from polluting durable memory. | `internal/vault`, `internal/lessons` | `internal/vault/vault_test.go` |
| **LLM09** | **Misinformation & Hallucinated Verdicts** | 6-Stage AI QA Pipeline (`internal/verifier/qapipeline.go`): Holdout partition isolation (`E_HOLDOUT_CONTAMINATION`), Seeded Mutant Canary (`E_CANARY_SURVIVED`), and Epistemic Audit (`E_PROSE_ONLY_COMPLETION`). | `internal/verifier` | `internal/verifier/qapipeline_test.go` |
| **LLM10 / ASI05** | **Unbounded Consumption & Runaway Loops** | Effort-scaled adaptive timeouts (`45s`/`90s`/`240s`), bounded `--max-turns <1..5>`, POSIX process group termination (`Setpgid`), and `DeviceLeasePool` (`1 emulator = 1 worker`). | `internal/process`, `internal/provider` | `internal/provider/device_lease_test.go` |

---

## 2. SLSA Build Provenance & Zero-CGO Reproducibility

`g8s` enforces deterministic, hermetic-grade binary builds across all target platforms (`darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`, `windows/amd64`):

1. **Zero-CGO Pure Go Invariant (`CGO_ENABLED=0`)**: Eliminates C-compiler toolchain drift and dynamic libc linkage vulnerabilities.
2. **Stripped & Stamped Ldflags**: `.goreleaser.yaml` compiles with `-s -w` and stamps immutable build metadata (`main.Version`, `main.Commit`, `main.BuildTime`).
3. **Cryptographic Checksums & Fail-Closed Installer**:
   - Every release publishes `checksums.txt` (SHA-256 over all archives and packages).
   - `scripts/install.sh` enforces fail-closed SHA-256 verification via `sha256sum -c` / `shasum -a 256 -c` and aborts immediately on missing or mismatched digests.

---

## 3. Software Bill of Materials (SBOM) & Cross-Platform Packaging

1. **SPDX SBOM Generation (`.goreleaser.yaml`)**:
   - Configured under `sboms:` (`id: spdx`, `artifacts: archive`, `documents: ["{{ .ArtifactName }}.spdx.json"]`) to emit machine-readable SPDX 2.3 JSON SBOMs alongside every release archive.
2. **Cross-Platform Package Manifests**:
   - **Linux Packages (`.deb`, `.rpm`, `.apk`)**: Configured via `nfpms` in `.goreleaser.yaml`.
   - **macOS Homebrew Cask**: Configured via `homebrew_casks` in `.goreleaser.yaml`.
   - **Windows Winget & Chocolatey**: Validated manifests at `packaging/winget/manifest.yaml` and `packaging/chocolatey/g8s.nuspec` + `packaging/chocolatey/tools/chocolateyinstall.ps1` (enforcing `checksumType = 'sha256'`).
