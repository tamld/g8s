# Access-Audit Log — Design Note

Issue #441. Status: **design only — no code in this slice**. This note
defines WHAT to log, WHERE, and RETENTION, so the implementation slice can
be scoped without re-litigating the shape.

## What problem this solves

Operation telemetry answers "how did it perform" (durations, streams,
promotions). It deliberately does not answer **"who did what to the
control plane, when"** — the procurement/enterprise-transparency question.
This design adds an append-only access-audit surface distinct from
operation telemetry.

## What to log

One record per control-plane **mutation** surface:

| Surface | Actor | Action | Subject |
| :--- | :--- | :--- | :--- |
| `g8s submit` | `--actor` | `task.submit` | task_id (+ idempotency_key) |
| claim / StartTask | worker id | `task.claim` / `task.start` | task_id (+ lease token id) |
| FinishAttempt / acceptance | worker / supervisor | `task.finish` / `task.accept` | task_id, success |
| `g8s cancel` | `--actor` | `task.cancel` | task_id, reason |
| `g8s receipt issue/consume/revoke` | issuer/consumer | `receipt.*` | receipt_id, allowed_paths |
| `g8s config set/unset` | `--actor` | `config.*` | key (value NOT logged — may contain paths) |
| Pause/Resume (`NEEDS_INFO`, `BLOCKED`) | actor | `task.pause` / `task.resume` | task_id, state |

Each record: `ts (RFC3339)`, `actor`, `action`, `subject_id`, `trace_id`
(the existing common flag), `instance` (post-#465 instance identity), and
a redaction rule identical to the worker's (`dispatch.SanitizeOutput`).

## Where

**Recommendation: a dedicated `access_audit` table in the existing g8s.db**
(WAL), written in the same transaction as the mutation where possible.

- Same-transaction writes give audit correctness for free (a mutation
  without its audit row cannot commit).
- A separate audit.db was rejected deliberately: the receipts split-brain
  lesson (receipts.db vs g8s.db PRAGMA user_version conflicts) showed
  sibling databases drift and fight over connection settings. One writer
  boundary per file is the safer invariant.
- Read path: `g8s audit list --actor --action --since --limit` (CLI slice).

## Retention

- Default TTL 90 days, swept by the existing cleanup machinery (same
  clock style as receipt/session sweeps, and — post-#468 — scoped to the
  state dir).
- Export-before-delete: rows leaving retention are appended to the
  Evidence Lake (`G8S_EVIDENCE_DIR`) as monthly JSONL shards, so the
  audit trail outlives the hot table.

## Explicitly distinct from operation telemetry

| | Access audit (this design) | Operation telemetry (existing) |
| :--- | :--- | :--- |
| Question | who did what | how did it perform |
| Writes | on mutation, transactional | on events/heartbeats, best-effort |
| Consumers | operators, auditors | reflex/reflex loops, dashboards |
| Sensitivity | actor names, paths | performance data |

## Non-goals (v1)

- No auth/identity provider integration: `--actor` remains a trust-me
  string; the trust model is single-operator host. Hardening (server-side
  identity) is a future ADR once multi-operator access exists.
- No log shipping/SIEM integration; the JSONL export is the interface.
- No retrofit of historical events (the table starts at merge).

## Implementation sketch (for the follow-up slice)

1. Schema: `CREATE TABLE access_audit (id INTEGER PRIMARY KEY, ts TEXT,
   actor TEXT, action TEXT, subject_id TEXT, trace_id TEXT, instance TEXT,
   detail TEXT)` + index on (ts), (actor), (action).
2. Insert points: control-plane store methods listed above (same tx).
3. CLI: `g8s audit list` + cleanup sweep registration.
4. Tests: audit row exists iff mutation committed; redaction of detail;
   retention sweep moves rows to Evidence Lake shard.
