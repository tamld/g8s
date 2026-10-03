# Task: Wave H2b — the unattended round's docs slice (issue #516, SCORECARD S-8, red test RT-H)

Repo: g8s @ main. Context: this is the CONTENT of the first unattended
closed round — a real docs-class goal. Everything about the round
(dispatch, drain, verify, merge) is orchestrated by the supervisor; your
job is the docs deliverable itself, docs-class clean (ADR-0031 prose set
ONLY — not one code byte, or the merger's lane gate will refuse the PR).

## Delivery protocol

Scratch worktree. Write ONLY to:

- docs/user-guide/verifier-and-autonomy.md (new page)
- README.md — ONLY the docs tree entry the structure sync requires (run
  `bash tools/ci_structure_sync.sh --render` and commit whatever it
  changes, nothing else)

Do NOT run git commit. Do NOT touch any .go file, tools/, .g8s/, docs/
elsewhere.

## Required content (the new page)

Audience: a g8s operator who wants machine acceptance of delegated
rounds. Sections, in this order, each grounded in the ACTUAL shipped
behavior (read the source listed before writing — no invented flags,
no aspirational features):

1. **Why**: trust is earned per class, never assumed (the advisory→hard
   model; #515). The deny-by-default floor: an unregistered class = human
   acceptance — the tool says so, exit 0.
2. **The registry** (`.g8s/verifier-classes.yml`): entry anatomy (name,
   status, founding_catch citation, priority, paths, checks); the two
   seeded classes (docs, test) and their founding catches (#208, #510);
   how a class EARNS hard status (cite real catches; hard without a
   citation is refused to unregistered by the loader — fail-closed).
3. **Running it**: `g8s verify --task <id> [--as-task <id>]` — paths come
   from the task's receipt; verdict fields (class, registered, status,
   outcome, checks); the self-grade rule (a task never grades itself);
   honest results (runner errors are `not-run`, never a fake pass);
   telemetry event `verifier_verdict`.
4. **The autonomy ladder**: `autonomy_level` setting — 0 (default, the
   merger refuses to act) and 1 (the merger may squash-merge a docs-lane
   PR whose full gate set passes); the flip is an OPERATOR decision
   recorded on the round, never a default; levels above 1 are reserved
   and unratified.
5. **The merger** (`tools/merger.sh --pr <num> --task <task-id>
   [--dry-run]`): the four gates IN ORDER (autonomy → lane=docs →
   verifier pass → CI pending=0 ∧ failure=0), all gate lines printed so
   a round log can quote them; the D3 boundary in one sentence (it never
   mints receipts, never submits tasks — it reads, checks, and merges
   what CI already greenlit).

## Constraints

- Cross-check EVERY claim against the code on main (internal/verifier/,
  internal/settings/settings.go autonomy_level, tools/merger.sh); if the
  code and this brief disagree, the CODE wins and you note the divergence
  in your report.
- `bash tools/ci_doc_contract_check.sh`, `bash tools/ci_link_integrity.sh`,
  `bash tools/claims_check.sh`, and `bash tools/ai_lint.sh docs/ README.md`
  must all pass on your worktree before you finish (this page rides the
  DOCS lane battery — the merger's CI gate will hold the PR to exactly
  that).
- Report: the sections written, every command/flag you documented (for a
  cross-check against the code), and any divergence found.
