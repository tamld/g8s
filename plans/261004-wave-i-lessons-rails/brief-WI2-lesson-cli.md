# Task: Wave I2 — `g8s lesson` CLI + ledger wiring (issue #519, SCORECARD S-9, ratified design)

Repo: g8s @ main. The lessons package (internal/lessons, landed via the
Wave I1 PR) exposes schema/Verify/Ledger; you build the C layer: the CLI
surface that wires it to the real telemetry DB and the repo ledger file.
Read the ratified design first: plans/261004-wave-i-lessons-rails/brainstorm.md.

## Delivery protocol

Scratch worktree. Write ONLY to:

- cmd/g8s/lesson.go (new command group)
- cmd/g8s/lesson_test.go (new)
- cmd/g8s/main.go (ONE-LINE registration in the switch, per convention)
- docs/lessons/ledger.jsonl (new — empty ledger, created with a header
  comment is NOT valid JSONL; create it as a zero-byte file tracked by
  git)
- README.md — ONLY the tree entry structure sync demands (run
  `bash tools/ci_structure_sync.sh --render` if needed)

Do NOT run git commit. Do NOT touch internal/lessons (if its API lacks
something the CLI needs, report it — do not patch the W layer yourself).

## Required implementation

1. **`g8s lesson create`**: reads a lesson draft from `--file <json>` (a
   Lesson JSON with Observation.CitedEvents incl. Snapshots,
   AuthorClass, ReviewedClasses, Recommendation) — runs
   lessons.Verify(...) against the REAL telemetry DB (`--db <path>` to
   the state-dir telemetry database; wire the read-only engine), then on
   PASS appends to the ledger (`--ledger <path>`, default
   docs/lessons/ledger.jsonl) and prints the verdict JSON envelope; on
   FAIL prints the failing checks and exits non-zero WITHOUT appending.
   `--round <id>` and `--author-class <name>` flags set the fields.
2. **`g8s lesson verify`**: same checks, dry-run (no append) — the
   fail-closed proof surface for demos.
3. **`g8s lesson list`**: prints the ledger (JSONL passthrough or a
   compact table; `--status <f>` filter).
4. **Rules**: the CLI NEVER fabricates events, never writes telemetry,
   never amends a lesson after append (the ledger is append-only); all
   output via the internal/cli envelope pattern; the hand-rolled switch
   registration in main.go is one line.
5. **Tests** (cmd/g8s/lesson_test.go, in-process against t.TempDir()
   DBs mirroring the state-dir layout): create-pass appends;
   create-fail (fabricated event id) exits non-zero and appends NOTHING;
   verify dry-run never appends; list round-trips; the one-line main.go
   registration routes.

## Constraints

- stdlib + existing internal packages; `go test ./cmd/g8s/` green;
  `go build ./...` green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green before finishing.
- Report: the flag surface, the envelope shapes, and anything in the
  internal/lessons API you found missing (for the report only).
