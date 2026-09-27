# Profile: knowledge (wiki / vault / content projects)

**Nature**: content-heavy vaults (Obsidian, markdown corpora) where the
deliverable is knowledge itself. Writes are often human-led; agents audit,
link-check, and distill — they rarely bulk-write content.

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
2. **spec-sync** — if the vault has structural conventions (folder schema,
   naming), pin them as a convention spec and enforce via the same gate
   pattern.
3. **evidence** — session-type markers on any campaign ledgers the vault
   hosts.

## Worker recommendation

- `read_only` verifiers only for audits (agent never bulk-writes content).
- Model: flash-tier is sufficient for link/freshness scans; escalate to a
  stronger model only for content-quality judgments (subjective — keep
  human).

## Contribute back

Link-rot patterns, vault-convention spec sync findings, freshness-audit
recall numbers.
