# ADR-0040: Mechanized SSoT — Every Claim in the Registries Is a Check or It Is Deleted

- **Status**: Accepted (2026-11-08) — operator-ratified batch ("trả hết nợ kỹ thuật... rồi mới release")
- **Depends on**: ADR-0038 (fence — the enforcement-point pattern generalized), DELTA-46 (mesh ledger — claims bind to ledger rows), #792/#796/#798/#808 (the four implemented instances), ADR-0039 (provenance ledger — the same honesty standard applied to commits)
- **Evidence**: docs/research/35-g8s-reform-review.md findings F2/F3/F5/F7/F9; the 2026-11-08 release cycle where the checker suite caught three live drifts in its own release flow (manifest latest_release left at v0.22.0 ground truth, changelog stub-content split, stale local tag)

## Context

Reform review finding R2: the repo's registries (manifest.json, README, AGENTS_FULL, cli-reference, roadmap) claim things about the codebase, but nothing mechanically verified most of those claims — so they drifted silently (a release metadata block pointing at a 0.21-era commit, ~15 undocumented CLI commands, a CI gate advertised but wired nowhere, model exemplars two generations stale). The D9-S5 surface-inventory script proved the pattern works: **a claim that a check can read is a claim that can no longer rot silently.**

## Decision

**Every factual claim in a tracked registry must be machine-verifiable by a gate in this repository, or it must be removed from the registry.** "Verified" means a check that reads the claim's ground truth (live `--help` output, git refs, filesystem) and fails loudly on mismatch — not a check that re-reads the same prose.

### The unified gate contract (what each instance implements)

| Registry surface | Ground truth the gate reads | Instance |
|---|---|---|
| `manifest.json latest_release.{commit,date}` | `git rev-list -n1 <tag>` + tag date | #792 (checker extension + post-cut amendment step in release.sh) |
| `docs/user-guide/cli-reference.md` entries | live `./bin/g8s --help` command list (both directions: missing AND stale entries fail) | #796 (`tools/ci_cli_reference_check.sh`, nightly) |
| README/docs claims about CI wiring (gates, workflows, steps) | actual workflow files on disk | #781 `ci_surface_inventory.sh` + #798 (pre-tag gate — no tag without a fresh inventory receipt) |
| `pre_push.sh` gate claims | each check's actual enforcement behavior (negative-probed) | #808 (audit: fail-loud missing linter, honest CI-only header) |
| `feature-map.yml` ↔ filesystem | registered packages vs `internal/*` reality | feature-map sync gate (wired #798-cycle) |
| Roadmap "Done" rows | tag existence + release workflow + verification artifacts | release.sh flow + pre_tag gates |

### Rules

1. **New registry claim ⇒ new check in the same PR.** A PR that adds a manifest field, a registry row, or a docs claim without a gate reading it is rejected in review (the surface-inventory and version-sync checkers enforce the mechanical subset; review enforces the rest).
2. **Fail-loud, layout-tolerant.** Checkers read ground truth, not formatting (the version-sync awk is layout-tolerant by design after the v0.23.0 incident); an unverifiable state is a typed failure, never a silent pass.
3. **Post-cut amendment, not pre-knowledge.** Where a value is only final after an irreversible step (release-commit sha), the flow records it in a follow-up amendment commit and the checker compares against the tag — not against HEAD.
4. **The SSoT is not self-attested where a third party can attest it.** GitHub artifact attestations (SLSA Build L2, #818) and the OpenSSF Scorecard checks are preferred over internal claims for supply-chain facts; the internal inventory covers what third parties cannot see.

## Consequences

- **Easier**: drift is caught at PR time or at pre-tag time by named gates, not by archaeology (the release cycle proved all three catch-points live).
- **Harder**: every registry edit needs its gate touched in the same PR — the cost of adding a claim is now visible at authoring time.
- **Non-goals**: not every docs sentence becomes a check (narrative and rationale are exempt); only claims about machine-checkable state (versions, commands, wiring, file inventories).

## Falsification clocks

- If a quarter passes with zero checker-caught drifts AND zero checker false-positives, the checkers may be over-permissive — audit thresholds.
- If a release cycle lands without running the pre-tag surface inventory, this ADR is being violated by the release flow itself.

## Related

ADR-0038 (enforcement point), ADR-0039 (commit provenance), #787 (R2 tracking), #792/#796/#798/#808 (instances).
