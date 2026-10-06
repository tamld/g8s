# Task: G4 — user-facing docs for the effort wave (v0.16 gate G4)

Repo: g8s @ main. The effort wave (#556–#565) + the #568 fixes
(baked-name coercion + agy catalog entry) ship in v0.16.0 — the
self-measuring release. Users can currently discover none of it.
Write the user guide, wire the reference doc, flip the roadmap.

Read first (in order):

- docs/user-guide/routing.md + docs/user-guide/cli-reference.md —
  house style, format conventions (and where the new content
  links from)
- cmd/g8s/submit.go flag surface (--effort, validation, payload
  keys) and cmd/g8s/ladder.go subcommands (status/advance/gauges)
- .g8s/effort-classes.yml + .g8s/agent-models.yml — the two
  registries you document
- internal/routing/effort.go — final adapter semantics INCLUDING
  the baked-suffix coercion rule (issue #568, F1)
- internal/lane/effortsignal.go — the pass-3 signal model
  (blast-radius + LOC estimate demotion)
- README.md lines 190-200 (roadmap table) and README.vi.md line
  ~160 (its Vietnamese mirror)

## Delivery protocol

Scratch worktree. Write ONLY to:

- docs/user-guide/effort.md (NEW)
- docs/user-guide/cli-reference.md (add submit --effort + ladder
  subcommands, matching the existing entry format)
- README.md (roadmap row + docs tree)
- README.vi.md (mirror of the README.md changes)

Do NOT run git commit. Do NOT touch CHANGELOG.md, manifest.json,
version.go, packaging, or any Go code.

## docs/user-guide/effort.md required sections

1. The knob: `g8s submit --effort <level>` — the 7-level ladder
   (none|minimal|low|medium|high|xhigh|max), default medium,
   parse-time validation (usage error names the allowed ladder),
   and the hard-refuse case (mandatory providers reject none).
2. How effort is decided: --effort override > task class from the
   write-scope registries (.g8s/effort-classes.yml, first match by
   priority wins, unregistered fails open to medium) > declared
   signals (blast radius + LOC estimate demote the path class —
   cite the matrix v1.1 rule: blast=low declares low at any LOC).
3. Baked-name platform models: ids ending in -low/-medium/-high
   carry their effort; a class/flag effort that disagrees with the
   suffix gets the MODEL realigned to the matching variant
   (model_realigned=true in the payload, requested model kept as
   model_requested); ids unknown to the catalog are coerced to the
   baked level with effort_mismatch=true instead (defense in
   depth, issue #568). Show the payload fields a user can inspect:
   effort_requested / effort_applied / effort_class /
   effort_budget_tokens / effort_mismatch / model_realigned.
4. The telemetry: where per-class cost comes from (token capture
   in worker usage records, supervisor-metrics aggregation) — one
   sentence + pointer, no fabricated numbers.
5. The quality ladder: `g8s ladder status|advance|gauges` — what
   each does, the HITL evidence packet (--out), escalation policy
   in one paragraph, and that gauges feed policy thresholds.
6. The two registries: table of effort-classes.yml fields and
   agent-models.yml fields (style, supported_efforts,
   default_effort, mandatory) with a pointer that the catalog is
   research-verified and staleness-marked.

Keep it honest: document only what the code does (cite file paths
in inline code, not line numbers). No aspirational features. Every
CLI example must be copy-pasteable and match real flag spellings.

## README/README.vi changes

- Roadmap row v0.16.0: flip the status column to the released/
  delivered marker used by earlier rows, keep the description.
- Add docs/user-guide/effort.md to the README docs tree at the
  correct alphabetical position (structure-sync gate verifies the
  tree matches the filesystem — both READMEs).
- README.vi.md: mirror both changes in Vietnamese prose.

## Constraints

- `bash tools/ci_doc_contract_check.sh` green;
  `bash tools/ci_structure_sync.sh` green;
  `bash tools/ci_link_integrity.sh` green (verify all three and
  include the output tails in the report).
- Link targets must exist; new-file link from README trees.

## Report

- File-by-file summary; the three gate-script output tails; any
  doc-code mismatch you found while verifying flags (report, do
  not fix code).
