# Profile: knowledge (wiki / vault / content projects)

**Nature**: content-heavy vaults (Obsidian, markdown corpora) where the
deliverable is knowledge itself. Markdown edits default to read_only role; workspace_write requires receipts — they rarely bulk-write content.

## Trust boundaries (seed — tune to the project)

```yaml
# Content integrity IS the security surface: no agent may bulk-rewrite or
# delete vault content. The write path is human-gated.
trust_boundaries:
  - "00-Inbox/**"        # capture zone — agents may append, never reorder
  - "1-Knowledge/**"     # curated knowledge — human-authored, agent-audited
```

## Lane bundle recommendation

| Lane | Usage |
|---|---|
| D (docs) | Dominant — content audit, link integrity, structure regen |
| F | Rare — tooling only (vault maintenance scripts) |
| S | Rare — only if the vault carries security-classified notes |
| Cadence | Docs-freshness audit weekly; link integrity per PR |

## Audit dimensions (weight order)

1. **docs-freshness** (heavy) — link integrity across the vault (hundreds of
   notes), orphan-note detection, stale-backlink report.
2. **epistemic-audit** — run `g8s-epistemic-audit` to classify claims into
   `MECHANICAL_VERIFIED`, `OBSERVATIONAL_TELEMETRY`, or `UNVERIFIED_PROSE`,
   preventing unverified LLM prose from being promoted as proven knowledge.
3. **spec-sync** — if the vault has structural conventions (folder schema,
   naming), pin them as a convention spec and enforce via the same gate
   pattern.
4. **evidence** — session-type markers on any campaign ledgers the vault
   hosts.

## Role & Skill Routing Matrix

| Task Phase | Recommended Role | Permission | Mandatory Skills |
|---|---|---|---|
| Primary Source & Citation Lookup | `researcher` | `read_only` | `g8s-epistemic-audit`, `wiki-researcher` |
| PRD / SRS / DoD Decomposition | `analyst` | `read_only` | `wiki-business-analyst`, `g8s-scenario-matrix` |
| Vault Inventory & Link Discovery | `collector` / `scout` | `read_only` | `g8s-epistemic-audit`, `wiki-knowledge-linker` |
| Synthesis & Compression | `summarizer` | `read_only` | `g8s-epistemic-audit`, `stop-slop` |
| Epistemic & Link Verification | `verifier` | `read_only` | `g8s-epistemic-audit`, `g8s-blind-verifier` |

## Worker recommendation

- `read_only` (`researcher`, `analyst`, `collector`, `summarizer`, `verifier`) for audits and synthesis (agent never bulk-writes curated vault content without explicit receipt).
- Model: `docs-retriever` is ideal for link/freshness scans; escalate to `deep-coding` or `reasoning-high` for epistemic classification and SRS/FSM modeling.

## Contribute back

Link-rot patterns, vault-convention spec sync findings, freshness-audit
recall numbers.
