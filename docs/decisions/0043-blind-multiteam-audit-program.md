# ADR-0043: Blind Multi-Team Audit Program & Audit-Driven Finding Intake

- **Status**: Accepted (2026-11-20) — operator directive ("hãy thực hiện yêu cầu 'audit mù'… dispatch 3-5 đội worker… để audit")
- **Depends on**: [ADR-0039](0039-dogfooding-honesty-ledger.md) (provenance honesty), [ADR-0042](0042-verifiable-zero-trust-evolution-infrastructure.md) (TRUST-G3 independent evaluation, TRUST-G5 epistemic classes), Compliance-in-code doctrine (typed `E_*`, deviation tagging)
- **Artifacts**: `plans/261120-blind-audit/` (briefs, per-team result summaries, full 57-finding registries), issues #972–#978 (pre-audit wave) and the 11 audit-derived issues filed from this program

---

## Context

The v0.26.0 security re-audits (F01–F14, G8S-01..12 → ADR-0042) plus the external Codex mesh review and internal static/runtime verification caught several trust-boundary classes, but all of them share one epistemic weakness: reviewers who know the prior findings anchor on them. Two blind rediscoveries during this session — an external reviewer independently converging with the internal compliance-wave plan, and (below) a blind auditor rediscovering issue #972 from code alone — demonstrated that **blind re-derivation is the cheapest validation of prior findings and the only reliable source of genuinely new findings**.

The first execution of the program (2026-11-20) also measured the worker transport honestly: of 4 worker lanes dispatched (cursor ×2, 9router, opencode), **zero produced a real audit** — three distinct defect classes (inline-spawn cwd=/ isolation failure; silent `ok=false` without a typed error; phantom `ok=true` completing a 15-file audit in <2.3s). The program therefore also ratified a transport fallback doctrine.

## Decision

1. **Blind protocol is the standard for audit waves.** Each audit team receives: a disjoint file scope, a single lens, an explicit prohibition on consulting prior findings (issues, PRs, CHANGELOG, `docs/history/`, `plans/`), verification vocabulary framing (no attack narratives), a structured JSON output contract (`findings[]` with `file:line` evidence + `severity` + `coverage` disclosure), and a mandatory "verified positives" section. Findings are deduplicated across teams only AFTER collection; cross-team convergence of the same finding is recorded as a validation signal, not noise.

2. **Transport fallback ladder (recorded justification required).** Worker dispatch order for audit/verification workloads: (a) g8s worker lanes (EXPLICIT-TRIPLE, receipt-bounded), (b) if a lane fails with a measured defect → retry once through the documented drain pattern, (c) if still failing → ZCode read-only subagents on the main checkout, with the deviation reason recorded in the audit artifacts (dogfood-mandate exception path). Every fallback consumed is itself a finding candidate: transport defects are filed as first-class issues (they violate compliance-in-code: untyped errors, false-success envelopes).

3. **Finding intake.** Audit findings flow into the issue tracker through batched issues (grouped by severity and repair-theme, never one-issue-per-finding), each carrying SoW (red-test-first) + DoD (measurable, evidence-cited). Findings already tracked by open issues are recorded as rediscoveries, not duplicates. The full finding registry is committed to the repo under the audit wave directory (provenance per ADR-0039).

4. **Cadence & falsification clock.** Audit waves run per release-train (aligned with the standing verification program's quarterly clocks). A wave that produces zero NEW validated findings (beyond rediscoveries) across ≥3 waves must be re-scoped: either the lens set is wrong or the codebase is stable enough to demote the wave to on-demand.

## Consequences

- First wave (2026-11-20): **57 findings** (6 high, 18 medium, 33 low) across 4 lenses × 165 file-reviews, filed as 11 issues (2 P0 hot, 6 wave batches, 3 transport).
- The transport fallback consumed this wave produced 3 transport issues — the program audits its own execution by design.
- Rediscovery of #972 (S0 nonce race) by a blind auditor confirmed the compliance wave's P0 triage.
- Cost of the first wave: ~11.5M subagent tokens across 4 teams; acceptable for the finding density (1 high-class finding per ~2M tokens), revisited per wave.

## Non-goals

- No performance/profiling, CLI-ergonomics, or multi-node mesh lens in this program (single-host scope per ADR-0042 non-goals; each needs a dedicated brief + team if added later).
- Audit findings never bypass the normal issue → red-test → fix → verify → PR pipeline.
