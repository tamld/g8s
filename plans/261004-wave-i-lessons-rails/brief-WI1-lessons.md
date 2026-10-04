# Task: Wave I1 — lessons package: schema + fail-closed machine checks (issue #519, SCORECARD S-9, RATIFIED design)

Repo: g8s @ main. The ratified design is binding: read
plans/261004-wave-i-lessons-rails/brainstorm.md FIRST (the threat model
T1-T6 and rail set are the contract). You build the W layer: the lesson
schema, the ledger, the fail-closed machine checks, and the telemetry
read API they need. The CLI layer is a LATER slice (another worker) —
expose everything through clean package APIs.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/lessons/lessons.go (new package; MUST open with a package doc
  comment — CI structure sync requires it)
- internal/lessons/lessons_test.go (new — includes the RT-I hostile red-tests)
- internal/telemetry/engine.go — ONLY to add a minimal read API (see 3),
  smallest possible diff, justified in your report

Do NOT run git commit. Do NOT touch cmd/, docs/, internal/routing,
internal/controlplane, internal/verifier.

## Required implementation

1. **Lesson schema** (two layers, per the ratified design):
   `Lesson{ID, RoundID, CreatedAt, AuthorClass, ReviewedClasses []string,
   Observation{CitedEvents []CitedEvent}, Recommendation string,
   RecommendationIsLLMOpinion bool (always true — const), Status
   (proposed|ratified|rejected), Checks []CheckResult}`.
   `CitedEvent{EventID, TaskID, EventType, Claim string, Snapshot
   map[string]any}` — the snapshot is carried INLINE because telemetry
   DBs are per-state-dir and ephemeral; future readers see the evidence
   without the DB.
2. **Ledger**: append-only JSONL (load/append/list; dedup by the
   SHA-256 of the sorted cited-event-ID set). Ledger file path is
   injected (the CLI will wire docs/lessons/ledger.jsonl later).
3. **Telemetry read API** (internal/telemetry, minimal): add
   `EventsByTask(ctx, taskID string) ([]TraceEvent, error)` (query the
   existing trace_events table by task_id; mirror the schema/persist
   conventions in the file). Nothing else.
4. **Machine checks — `Verify(l Lesson, deps) Verdict`, ALL fail-closed**
   (any failure ⇒ Status=rejected with the failing check recorded;
   rejected lessons are NEVER appended):
   a. **Citation resolution (T1)**: every CitedEvent must resolve via
      telemetry EventsByTask(CitedEvent.TaskID) to a real row whose ID,
      EventType match, and whose Payload contains every key/value in the
      Snapshot (subset match). Missing/mismatched ⇒ rejected.
   b. **No-self-review (T3)**: AuthorClass must not appear in
      ReviewedClasses.
   c. **Dedup (T6)**: the cited-ID-set hash must not already exist in
      the ledger.
   d. **Budget (T6)**: per-RoundID lesson count under a configurable cap
      (default 3).
   e. **Schema integrity**: Recommendation non-empty AND
      RecommendationIsLLMOpinion true (the one-way gate is structural —
      an "observation" may never smuggle a recommendation: if the
      observation text contains the marker "recommend", "should", or
      "propose", the checker splits it out as a recommendation or
      rejects — your call, document it).
5. **RT-I red-tests (red-first, table-driven)** — each must be a live
   assertion:
   - hostile fabrication: a CitedEvent whose EventID does not exist in
     the telemetry fixture ⇒ REFUSED (the operator's challenge, proven
     by test);
   - snapshot mismatch (event exists, payload differs) ⇒ REFUSED;
   - self-review (AuthorClass in ReviewedClasses) ⇒ REFUSED;
   - duplicate citation-set ⇒ REFUSED;
   - over-budget round ⇒ REFUSED;
   - a valid lesson (real-fixture events) ⇒ accepted, appended, and
     re-loadable from the ledger with snapshots intact.

## Constraints

- stdlib; match internal/ package style; no schema migrations outside
  the telemetry read addition.
- `go test ./internal/lessons/ ./internal/telemetry/` green; `go build
  ./...` green; gofumpt + golangci-lint clean; `bash
  tools/ci_doc_contract_check.sh` green (new package = structure tree
  change; run `bash tools/ci_structure_sync.sh --render` if it demands).
- Report: the schema you implemented, each check's refusal semantics,
  the telemetry read API diff, and the marker rule you chose for 4e.
