# Reusable brief: anti-slop document review (stop-slop skill, standardized)

Purpose: hand this brief to any worker agent to de-slop and shape a
document. Parameterize the two CAPS fields; everything else is fixed.

## Task

Review and rewrite `<TARGET_FILES>` (repo-relative paths) using the
installed **stop-slop** skill at `~/.agents/skills/stop-slop/` (read
`SKILL.md` first, then the four `references/*.md` files).

## Mode

`--both` (clean + shape) from the skill's mode table:

1. **Clean** — run the Quick Checks; remove every banned phrase,
   structural cliché, passive construction, and em dash in prose
   (em dashes inside ASCII diagrams are exempt).
2. **Shape** — action-first ordering, numbered bounded steps for any
   multi-step instruction, one concrete next action at the end.
   Reference documents (README/ADR/ledger) keep their reference shape:
   shaping applies to prose paragraphs and instructions, not to tables
   of facts or the structure tree.
3. **Score** before finishing — Directness, Rhythm, Trust,
   Authenticity, Density (1–10 each). Below 35/50, revise again. Record
   the before/after scores in your report.

## Hard constraints (do not break)

- Do NOT touch: the `<!-- structure:start -->`…`<!-- structure:end -->`
  region (auto-rendered by `tools/ci_structure_sync.sh`), code blocks'
  commands and flags, badges, license lines, or any URL.
- Do NOT change technical meaning: versions, counts, file paths, env
  var names, and CLI flags are facts, not prose. If a fact looks WRONG,
  report it — do not fix silently.
- Keep the document's contract-bearing sections intact if the doc has
  any (claims tables, receipts registries, directive rows).
- Vietnamese prose: same rules, but punctuation follows Vietnamese
  convention (dấu hai chấm thay em dash; no English filler words).

## Definition of done

- All gates green on the changed files: link integrity
  (`tools/ci_link_integrity.sh`), doc-contract
  (`tools/ci_doc_contract_check.sh`), AI-lint.
- Report includes: slop categories found (with one example each),
  before/after stop-slop score, and any fact flagged as suspicious.
- Do NOT git commit (the supervisor owns commits).
