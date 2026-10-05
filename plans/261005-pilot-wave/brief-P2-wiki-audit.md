# Task: Pilot Phase 2 — docs-freshness audit of the wiki's 1-Knowledge directory (read_only)

Repo: g8s @ main; audit TARGET: the tamld-llm-wiki sibling (added via
-add-dir — read the wiki files, write NOTHING anywhere). This is the
pilot's first real worker dispatch (#442 M2 continuation + the #550
effort telemetry's first real-workload point; you run at the LOW effort
tier deliberately).

## Audit scope

The wiki directory `1-Knowledge/` only (bounded — not the whole repo).

## What to check (docs-freshness dimensions)

1. **Stale cross-references**: entries citing tools/versions/procedures
   that the wiki's own recent commits changed (check `git -C <wiki> log
   --oneline -15 -- 1-Knowledge/` for what moved recently).
2. **Internal link rot**: `[text](path)` links in the 1-Knowledge/*.md
   files whose target file does not exist (verify with the filesystem —
   relative to the LINKING file's directory).
3. **Date rot**: entries whose "last verified"/date claims are >6 months
   old while the subject area changed since (use the wiki git log per
   file).
4. **Duplicate/orphaned entries**: two files claiming the same topic
   title; files nothing links to from the map layers (only if cheap to
   determine — do not crawl everything).

## Report format (your final message, exactly this shape)

```
WIKI-AUDIT 1-Knowledge
files_scanned: <n>
FINDINGS: <n>
  - <file> | <dimension> | <one-line evidence>
CLEAN: <n dimensions with no findings>
```

≤ 20 findings, each with a concrete file reference. Verification
vocabulary only. You write NOTHING — the report is your final message.

## Constraints

- Read-only: no file writes, no dispatches, no commits anywhere.
- Evidence-based: every finding cites a file and the observable fact
  (a missing target path, a date, a log line) — no speculation.
- Report in English.
