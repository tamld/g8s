# T013 Port Map — Unported Python Suite Inventory & Slice Boundaries

Scout deliverable consolidating the 5 unported reference suites into Worker card boundaries.
Conventions follow notes/T004-delta03-map.md. All line refs are `reference/python/scripts/<file>`.

## 1. Summary

| Suite | Lines | Tests | Go landing zone | Overlap verdict |
|---|---|---|---|---|
| test_agy_dispatch.py | 229 | 12 | NEW `internal/dispatch` (+ extend provider resolver) | Partial |
| test_agy_mcp_server.py | 292 | 12 | EXTEND `internal/mcp` | Partial — true gaps listed §3 |
| test_agy_service.py | 291 | 16 | NEW `internal/service` (kardianos) | None — net-new Phase 4 |
| test_agy_worker.py | 263 | 9 | NEW `internal/worker` supervisor | None — net-new Phase 4 |
| test_safety_coordination.py | 742 | 32 | HARDEN `internal/receipt` + `internal/harness` + integration | High overlap, real gaps in D/E/F |
| **Total** | **1817** | **81** | | |

Parity math: repo currently has 95 top-level Go test funcs (~102 cases). Faithful ports of these 81
(Go-idiomatic splits typically expand counts) project ~160-170 funcs ≥ oracle 140 with margin.

## 2. test_agy_dispatch.py — 12 tests → internal/dispatch

1-5. `resolve_agy_binary` precedence chain: explicit path > `AGY_BIN` env > PATH `which()` > win32 `.cmd`
   suffix appended to explicit base > home fallback `%APPDATA%/Roaming/npm/agy.cmd`. Resolver takes
   injectable env/which/platform/home params (no globals).
6. `build_agy_command`: skip_permissions keeps `--sandbox` alongside `--dangerously-skip-permissions`.
7. `no_sandbox=True` omits `--sandbox`.
8. Detector ignores negative instruction ("Do not run wiki.py reflect") → [].
9. Detector flags "Session logged to log.md" → violation type `wiki_reflect_side_effect`.
10. main() turns rc=0 side-effect into exit `READ_ONLY_CONTRACT_EXIT` + result envelope
    `{ok:false, returncode:0, harness_returncode, policy:'read_only', contract_violation}`.
11. `--print-stdout` sanitizes credentials: `postgresql://user:password@` → `<REDACTED>`.
12. Capture replaces invalid utf8 with `\ufffd`, bounds output with `<OUTPUT_TRUNCATED>` under
    MAX_CAPTURE_BYTES (3MB probe).

Overlap vs Go: internal/provider already resolves AGY_BIN > LookPath (T010). MISSING in Go: win32
suffix, home fallbacks, capture bounds/truncation marker, utf8 replacement, credential redaction,
wiki-policy detector. Harness ValidateRequest blocks patterns pre-dispatch but does not classify
post-run side effects — detector is net-new.

## 3. test_agy_mcp_server.py — 12 tests → internal/mcp expansion

Python surface = 8 tools: agy_list_roles, agy_list_permissions, agy_self_awareness,
agy_dispatch_task, agy_submit_task, agy_get_task, agy_list_tasks, agy_cancel_task.
Go internal/mcp currently exposes SIX g8s_* tools (submit/get/receipt-issue/self-awareness +
run/blast-radius stubs). TRUE GAPS:
- list-tasks and cancel-task tools (controlplane ListTasks/CancelTask already exist underneath).
- roles/permissions listing incl. workspace_write metadata `{mcp_enabled:false}` + disabled_reason.
- protocolVersion negotiation echo (client 2024-11-05 echoed back; server default 2025-06-18).
- sanitized request view: returned request omits prompt field.
- sync dispatch guard statuses: blocked_by_policy (workspace_write), blocked_by_harness
  (read_only+skip_permissions), setup_required (+setup_hint) on missing binary, explicit-add_dirs
  and no-sandbox guards; automation_read+skip_permissions keeps BOTH sandbox flags.
- contract-violation surfacing: rc=0 + "Session logged to log.md" → isError w/ harness_returncode ==
  READ_ONLY_CONTRACT_EXIT + violations[0].type == wiki_reflect_side_effect.
- durable round-trip: idempotent submit dedup returns SAME task_id; QUEUED state filters; cancel → CANCELLED.

NAMING: keep g8s_* convention per T005-D1 (our spec is SSoT, not python names). DELTA-04 spec listed
six tools — additions require a spec amendment BEFORE code (spec-first axiom).

## 4. test_agy_worker.py — 9 tests → internal/worker (net-new)

1. parse_duration: '250ms'→0.25, '1m2s'→62.0, '0s'→error.
2. Successful run_once → SUCCEEDED + exports runs/<task_id>/attempt-N/receipt.json + deletes prompt.txt.
3. Cancel while RUNNING → CANCELLED + child process group killed.
4. Timeout → FAILED + result.status 'timeout'.
5. Dispatch returns NEEDS_INFO → task paused NEEDS_INFO + lease released (lease_owner nil).
6. 200KB stdout / non-utf8 → SUCCEEDED, no deadlock, worker.stdout/stderr NOT persisted.
7. Exception during start → propagates AND prompt.txt removed (cleanup-on-failure).
8. SIGTERM to worker subprocess (--once) → exit 143 + active child killed (child.pid coordination file).
9. Leader exits normally while orphan child spawned → child reaped.

This is the Phase 4 supervisor core: process groups, signal handling, attempt dirs, receipt export,
pause semantics. NO Go equivalent today. REQUIRES new OpenSpec delta before code (charter axiom).

## 5. test_agy_service.py — 16 tests → internal/service (net-new)

Python is launchd-only (plist build/install/bootout/kickstart/status/uninstall + FakeRunner injection).
Go equivalent must decide abstraction level: kardianos/service abstracts launchd/systemd/windows —
matches charter Phase 4 wording (cobra+kardianos). Security properties to port verbatim:
- Unit file: Label, KeepAlive true, no RunAtLoad, Background process type, Umask 0077, --quiet arg,
  pinned absolute binary via env, PATH excludes user bin dirs, unit contains no API/TOKEN strings.
- File modes: unit 0644, logs 0600; world-writable binary rejected; symlinked unit/log rejected with
  victim untouched ("symlinked LaunchAgent plist" / "cannot prepare private service log").
- Lifecycle ordering: reinstall boots-out BEFORE bootstrap; failed bootstrap restores previous unit
  bytes; restart requires loaded install; timeout fails closed.
- Control-plane integration: lifecycle ops refuse when task LEASED/RUNNING (zero runner calls);
  maintenance gate holds BeginMaintenance across install so claims return none mid-install, succeed after.
- Status hygiene: sanitized JSON (no prompt), does NOT create DB when missing; corrupt DB fails closed;
  uninstall removes unit but PRESERVES database (state_preserved true); unsupported platform rejected.
REQUIRES new OpenSpec delta (cross-platform design decisions: which platforms, unit naming, paths).

## 6. test_safety_coordination.py — 32 tests → hardening pass

Categories (A:8 prompt-injection-of-scope, B:6 receipt revocation, C:4 active listing, D:6 multi-agent
concurrency, E:5 contract-prompt security, F:3 e2e orchestration).

Already covered conceptually in Go: basic issue/validate/consume/revoke/TTL (receipt pkg 28 tests);
harness role/permission gates; controlplane ValidateRequest workspace_write/no_sandbox/traversal.
TRUE GAPS to implement:
- B: revoke(consumed)=False; revoked row persists auditable (consumed=1, consumer_task_id);
  revoke→reissue same paths yields fresh id, old id unvalidatable.
- C: ListActiveReceipts enrichment — remaining_seconds = round(ttl-elapsed,1), exact expires_at epoch,
  consumed+expired excluded, cross-session expiry visibility.
- D: multi-agent concurrency on ONE sqlite file via separate Store handles — independent validates OK;
  barrier race on SAME receipt → exactly one winner, loser gets 'already consumed'; 10 concurrent
  validates all-or-nothing; previous-session expired receipts invisible to fresh handle. MUST run under
  CGO_ENABLED=1 -race (dual-pass).
- E: contract prompt content — exact wiki ALLOWED/FORBIDDEN strings injected for EVERY
  mutation_allowed=false profile (≥2), absent for workspace_write; DELEGATED WRITE block carries
  receipt_id/issuer/each allowed path line; receipt_info=None renders generic mutation line;
  empty allowed_paths still injects block; hostile newline payload in allowed_paths rendered as
  literal text (no interpretation).
- F: full-flow integration issue→gate(validate_dispatch)→prompt→list_active==0; revoked receipt makes
  gate fail closed; consume-via-dispatch visible to Brain listing.

## 7. Recommended Worker slices

| Card | Scope | Est. new tests | Prereq |
|---|---|---|---|
| T014 | internal/dispatch parity (resolver fallbacks, capture bounds, redaction, wiki detector) | 12-16 | spec check |
| T015 | internal/mcp expansion (list/cancel tools, roles/permissions metadata, negotiation echo, guards) | 10-14 | DELTA-04 spec amendment |
| T016 | internal/worker supervisor (run_once, attempt dirs, process groups, signals, pause) | 12-14 | NEW OpenSpec delta REQUIRED |
| T017 | internal/service manager (kardianos abstraction, unit security, maintenance gate) | 14-18 | NEW OpenSpec delta REQUIRED |
| T018 | safety/coordination hardening (B/C/D/E/F gaps above, -race proven) | 20-25 | none |
| T019 | goreleaser snapshot runtime smoke + merge feat/mvp-consolidation→main + final dual-pass re-verify | 0 | T014-T018 |

Sequencing rationale: T014/T015/T018 touch existing packages (lowest risk, immediate count gains);
T016/T017 are the net-new Phase 4 verticals gated behind spec deltas; T019 is the release gate.
Order T014→T015→T018→T016→T017→T019 keeps every slice independently shippable while climbing toward
the ≥140 oracle early (95+12+10+20 ≈ 137 by end of T018; T016/T017 push well past 140).

Cross-cutting invariants for ALL cards: modernc.org/sqlite only; injectable clock for any TTL/timeout;
process containment 0600/0700; 100% English; dual-pass Docker golang:1.25 gate per card.
