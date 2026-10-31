# ADR-0037: Reach Layer — Platform Data Acquisition for Research & Context

**Status**: PROPOSED (draft for peer co-sign — agy)
**Date**: 2026-10-31
**Deciders**: S0 (ratified direction), zcode (author), agy (co-sign pending)
**Tracking**: [#704](https://github.com/tamld/g8s/issues/704), release gate on v0.22.0

## Context

The Pillar 1 research gate (upstream success-case research before any design)
currently relies on agents' native browsing and citation strings — the gate
checks the TEXT of a citation, not whether the cited source was actually
fetched by machine. Login-walled platforms, video content (subtitles), and
non-English ecosystems (V2EX, Bilibili, Xiaohongshu) are unreachable.

[Panniantong/agent-reach](https://github.com/Panniantong/agent-reach) (107k★,
MIT) validated a pattern for this: per-platform **ordered fallback chains of
upstream tools**, isolating agents from upstream API churn, with a
fingerprinted health ledger and read-only discipline. Two read_only scouts
verified the mechanism (553 cited lines, 2026-10-31).

## Decision

1. **`internal/reach`** — a thin Go package, three concerns:
   - `registry.go`: channel→action→[]backend declared IN CODE (declarative,
     not user-editable YAML — agent-reach's chains live in code; user config
     only toggles channel availability).
   - `chain.go`: dispatch through the ordered backends; errors classified
     into **fallback** (transient/upstream: try next backend) vs **terminal**
     (bad request: stop).
   - `health.go`: ledger keyed `channel::action::tool_rank` with
     `{code, at, fingerprint}` per entry + channel cooldowns; drives
     **automatic reorder** (demote broken, promote next) — deliberately NOT
     the model scorecard (different identity: tool_rank, not model; different
     failure classes: scrape/auth/breakage, not verify/stagnation).
2. **CLI surface (the only thing agents see)**: `g8s reach read <url>` and
   `g8s reach <channel> <action>` returning a versioned JSON envelope.
3. **Context Broker**: new 4th budgeted section `WorldSnippets` (fresh
   external evidence; own budget so page bodies cannot break the packet
   contract).
4. **Vault promotion**: scrape → distillation entries (mirrors the wiki flow)
   so research becomes durable knowledge.
5. **Research-gate upgrade**: reach-sourced citations must carry a
   machine-fetched artifact; a citation without one is REJECTED.

## Security invariants (non-negotiable)

1. Read-only is absolute — no posting/commenting/liking regardless of channel
   capability.
2. Credentials/sessions live OUTSIDE g8s state — never in receipts, ledger,
   logs, or ContextPacket (two-layer separation). Deletion follows an
   `owned_paths` ledger (delete only registered paths; no symlink follow).
3. No Python port — pattern only; adapters wrap external CLIs (bili-cli,
   yt-dlp, jina) exactly like the existing `platform_dispatch` class.
4. No browser/GUI automation in the steady-state loop.

## Consequences

**Positive**: Pillar 1 research gains machine-fetched evidence (anti-slogan
law 1 becomes executable); the coordinator's decomposition quality improves
through wider success-case reach; upstream churn stops breaking agents;
research evidence becomes durable vault knowledge.

**Negative / costs**: a new package + CLI surface to maintain; external CLI
dependencies (bili-cli, yt-dlp, jina) must exist on worker hosts; channel
health needs nightly verification; the reach layer is itself a W-layer tool
subject to the same receipts/gates.

**Neutral**: the model scorecard is untouched; `platform_dispatch` untouched;
channels expand by observed demand (ROI guard — no speculative scraping).

## Falsification

- If success-case research stays GitHub/web-heavy, the reach layer's ROI is
  limited to `read <url>` — S5 channels wait until demand is observed.
- If the health ledger's reorder produces worse recall than static order
  (measured over ≥20 fetches/channel), revert to static order and keep only
  the ledger as telemetry.
- If a channel requires credential handling that cannot stay outside g8s
  state, that channel is excluded until a runtime-only design exists.

## Alternatives considered

- Reuse the model scorecard for reach — rejected: different identity key
  (tool_rank vs model), different failure classes; mixing pollutes both.
- YAML-configurable chains — rejected: agent-reach's in-code registry is the
  validated design; YAML invites unreviewed runtime behavior changes.
- Port the Python implementation — rejected: pattern only; the value is the
  chain discipline, not the code.
