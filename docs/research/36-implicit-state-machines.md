# Implicit State Machines in g8s — full audit (2026-11-08)

Prompt: *"find all of the implicit state machines in my app"* — executed against this repository by three read-only scouts (cursor/composer-2.5) over three areas, then F0-synthesized. This document is the committed artifact; the session that produced it also verified the two highest-value hazards in code and opened #844/#845 (both fixed same-day).

## Executive summary

- **~97 machine sections** enumerated across the three areas (worker+mesh 34, controlplane+harness+queue 31, provider+reach+CLI 52 — some sections cover coupled machines).
- Every machine is reported with: state field (file:line), states (including implicit zero-values), transitions, terminal states, implicitness, and one concrete hazard.
- **Top verified gaps (fixed same-day):**
  1. `internal/controlplane/lifecycle.go:430` — `determineNextState` computed then discarded (`_ =`), every attempt hardcoded `WORKER_COMPLETED`; `SUCCEEDED`/`OUTPUT_INVALID`/`REVIEW_REQUIRED` are writer-less dead states → #844.
  2. `internal/worker/worker_sweep_collect.go` — the mid-run `claim_refused_fence` status (worker_runner.go:247) has no collect branch → falls to default, mis-finish risk → #845.
- **Process findings (the audit observing itself):** 9router workers cannot read host absolute paths (briefs must be embedded in the prompt or referenced by cursor); two scouts returned ok:true with ask-for-help/echo responses → #840 (productivity gate); GitHub-edge 504s failed three CI steps in one push → #841 (retry wrapper).

## Area A — internal/worker + internal/mesh (scout A, 34 machines)

# SCOUT-SM-A — Implicit State Machines: `internal/worker` + `internal/mesh`

Read-only scout over production `.go` files (tests used only for confirmed surface-state literals). **34 distinct implicit machines** enumerated below.

---

## `internal/mesh`

### M1 — Mesh resource claim lifecycle
1. **NAME** — Coordinator ledger claim on a `task_id` / `resource` path set.
2. **STATE FIELD** — `claims.state` (`Claim.State`, `types.go:57`); time gate `expires_at` (`types.go:56`).
3. **STATES** — Row absent; `active`; `released`; `accepted`. Implicit: **expired-active** (`state=active` && `expires_at <= now`).
4. **TRANSITIONS** — Insert `active` (`claims.go:192-196`); idempotent renew `expires_at` only if same holder/scope (`claims.go:147-155`); `active`→`released` (`claims.go:225`); `active`→`accepted` + artifacts (`claims.go:284-285`); idempotent re-accept (`claims.go:263-267`). Blocked: reclaim when `accepted` (`claims.go:137-138`); transfer on live foreign claim (`claims.go:140-158`).
5. **TERMINAL** — `accepted` (no handler exits); `released` has no re-activate path in-package. **Cleanup**: none (rows persist); overlap prevented only while `active` && unexpired (`claims.go:163`).
6. **IMPLICITNESS** — SQL string literals only; expired-active not a distinct `state` value.
7. **HAZARD** — Crash after lease extension but before worker finish leaves `active` until `expires_at`; no TTL sweeper in `mesh`.

### M2 — Claim accept / meta revision coupling
1. **NAME** — Global context revision bump on successful accept.
2. **STATE FIELD** — `meta.revision` (`store.go:30`; read `claims.go:124-128`, `274-282`).
3. **STATES** — Integer ≥0; bumped on first accept.
4. **TRANSITIONS** — `revision` → `metaRev+1` on accept (`claims.go:280-282`); stale claim if `req.ContextRevision != metaRev` (`claims.go:127-128`).
5. **TERMINAL** — N/A (monotonic).
6. **IMPLICITNESS** — Undocumented enum; enforced only in handlers.
7. **HAZARD** — Fence checks tie claim `context_revision` to live `meta.revision` (`claim_fence.go:32-33`); accept race can invalidate in-flight workers.

### M3 — Mesh claim fence (coordinator projection)
1. **NAME** — “Is this claim live and scope-consistent for this actor?”
2. **STATE FIELD** — Derived from `Claim` fields + `meta.revision` (`claim_fence.go:29-34`).
3. **STATES** — Valid active claim; `ErrClaimFenceStale` (any mismatch/expiry).
4. **TRANSITIONS** — `ActiveClaim` read (`claim_fence.go:18-40`); `RenewClaim` → idempotent `claimHandler` (`claim_fence.go:44-58`).
5. **TERMINAL** — Error path returns nil claim.
6. **IMPLICITNESS** — Comment only (`claim_fence.go:16-17`).
7. **HAZARD** — Worker renews mesh + receipt binding separately (`worker/claim_fence.go:78-82`); partial failure desyncs fences.

### M4 — Transport retry budget + cool-off (per sender/recipient pair)
1. **NAME** — Send refusal accumulation until peer degraded.
2. **STATE FIELD** — `transport_retry.failures`, `transport_retry.degraded_until` (`store.go:28`; `transport.go:96-128`).
3. **STATES** — Row absent; accumulating (`degraded_until=0`); degraded (`degraded_until > now`); expired row (implicit after gate clears).
4. **TRANSITIONS** — Each refused send: `failures++` (`transport.go:121`); if `failures >= RetryBudget` set `degraded_until` (`transport.go:123-125`); `retryGateTx` blocks while degraded (`transport.go:105-106`); on expiry `ackProgressTx` DELETE (`transport.go:108-109`); successful ack clears (`transport.go:504`, `529`; `ackProgressTx:131-133`).
5. **TERMINAL** — DELETE is absorbing until next refuse.
6. **IMPLICITNESS** — Defaults in `TransportConfig` (`transport.go:12-17`); no named FSM.
7. **HAZARD** — Refusals committed even when `msgSend` returns error to caller (`transport.go:114-115`); crash mid-tx may leave ambiguous counts.

### M5 — Peer degraded admission gate (send path)
1. **NAME** — Pre-send check for cool-off window.
2. **STATE FIELD** — Same as M4; surfaced as `PeerDegradedError` (`transport.go:86-94`).
3. **STATES** — Open; degraded-until-`RetryAt`.
4. **TRANSITIONS** — `retryGateTx` before non-probe send (`messages.go:333-339`); degraded refusal recorded without enqueue (`messages.go:338-339`, `refusal returned 403-404`).
5. **TERMINAL** — Until timestamp passes (M4).
6. **IMPLICITNESS** — Error type only.
7. **HAZARD** — `PeerDegradedError` inside tx returns `nil` from `sendTx` with `refusal` set (`messages.go:334-339`) — easy to misread as success in superficial code reads.

### M6 — Per-peer flow window (in-flight cap)
1. **NAME** — Cap unacked non-probe messages per `(sender,sender_inc,recipient,recipient_inc)`.
2. **STATE FIELD** — Derived `inFlight` count vs `TransportConfig.WindowSize` (`messages.go:356-361`; policy `transport.go:20-27`).
3. **STATES** — Under window; at/over window (`FlowWindowError`).
4. **TRANSITIONS** — Refuse (counts as M4 failure) when `inFlight >= WindowSize` (`messages.go:360-361`).
5. **TERMINAL** — Clears as messages acked (reduces inFlight).
6. **IMPLICITNESS** — Count-based; not persisted.
7. **HAZARD** — Window counts only **unacked** rows; ack watermark can drop inFlight without recipient processing payload.

### M7 — Recipient pending pressure (global per recipient)
1. **NAME** — Hard cap on recipient pending non-probe queue.
2. **STATE FIELD** — `pending` count (`messages.go:353-364`).
3. **STATES** — `<1000`; `>=1000` refuse.
4. **TRANSITIONS** — refuse on max pending (`messages.go:363-364`).
5. **TERMINAL** — Until acks drain.
6. **IMPLICITNESS** — Magic number 1000.
7. **HAZARD** — Independent of per-peer window; asymmetric backpressure.

### M8 — Message delivery / ack lifecycle
1. **NAME** — In-ledger message from send to ack (or probe auto-ack).
2. **STATE FIELD** — `messages.acked` (`store.go:24`); runtime flags `Message.Gated`, `Message.Surfaced` (`types.go:45-46`).
3. **STATES** — `acked=0` pending; `acked=1` done. Probes: `ping` pending until `pong`; `pong` inserted as acked (`messages.go:322-323`).
4. **TRANSITIONS** — INSERT unacked (`messages.go:366-367`); `msgAck` by id or cumulative seq (`messages.go:487-529`); ping acked by `touchPeerTx`/`pong` (`keepalive.go:17-18`, `messages.go:310-320`).
5. **TERMINAL** — `acked=1` (row remains until prune).
6. **IMPLICITNESS** — `Gated`/`Surfaced` computed (`surface.go:33-34`, `messages.go:397-406`).
7. **HAZARD** — Cumulative ack by `seq<=watermark` (`messages.go:495`) can ack multiple senders if seq ordering assumptions break.

### M9 — Transport surface / gating phase
1. **NAME** — Recipient UI/worker surface blocks non-probe delivery.
2. **STATE FIELD** — `transport_surface.state` (`store.go:27`); default absent→`ready` (`surface.go:13-14`).
3. **STATES** — `ready`; `modal`; `busy` (`messages.go:119-120`); implicit default `ready`.
4. **TRANSITIONS** — Set on `register` (`messages.go:129-132`); gating when `state != ready` && unacked non-probe (`messages.go:397`, `surface.go:33`).
5. **TERMINAL** — None (re-register to change).
6. **IMPLICITNESS** — Validated on register only.
7. **HAZARD** — Messages enqueued while `modal` but not surfaced in inbox until `ready` (`messages.go:422-423`) — sender may think delivery succeeded (`gated: true`).

### M10 — Keepalive ping lifecycle (ledger-polled)
1. **NAME** — Stale peer detection via unacked `ping` messages.
2. **STATE FIELD** — `nodes.observed_at`; unacked `ping` rows (`keepalive.go:13-74`, `76-90`).
3. **STATES** — Fresh peer; due for ping; pending ping; stale peer (`Node.Stale=true` in snapshot).
4. **TRANSITIONS** — On inbox/wait: `keepaliveTx` may INSERT ping if idle (`keepalive.go:57-71`); skip if pending ping (`keepalive.go:64-65`); `peerStaleTx` if oldest unacked ping age ≥ `KeepaliveSeconds*KeepaliveThreshold` (`keepalive.go:90`); activity via `touchPeerTx` (`keepalive.go:13-18`).
5. **TERMINAL** — Stale is observational only (no node deletion).
6. **IMPLICITNESS** — Comment: “no process-local timer state” (`keepalive.go:21`).
7. **HAZARD** — Early return when `total messages >= 10000` skips keepalive (`keepalive.go:46-47`) — silent stop under load.

### M11 — Mesh node incarnation / actor identity
1. **NAME** — Only the registered incarnation may mutate ledger as a node.
2. **STATE FIELD** — `nodes.incarnation` vs request incarnation (`messages.go:28-42`).
3. **STATES** — Match; mismatch error.
4. **TRANSITIONS** — New node INSERT (`messages.go:153`); same incarnation touch (`messages.go:156-157`); replacement UPDATE + quota DELETE (`messages.go:162-166`).
5. **TERMINAL** — Old incarnation permanently invalid after replacement.
6. **IMPLICITNESS** — `actorTx` helper only.
7. **HAZARD** — In-flight messages keyed by old `recipient_inc` become undeliverable after incarnation bump without client refresh.

### M12 — Message idempotency / tombstone replay
1. **NAME** — Same `message_id` + digest replay vs conflict.
2. **STATE FIELD** — `messages` row or `message_tombstones` (`messages.go:269-290`).
3. **STATES** — New id; exact replay (return existing seq); digest conflict; tombstoned replay.
4. **TRANSITIONS** — Checks before insert (`messages.go:271-290`); prune moves acked old rows to tombstones (`retention.go:67-75`).
5. **TERMINAL** — Tombstone preserves id forever (`INSERT OR IGNORE`).
6. **IMPLICITNESS** — Error string `ID_CONFLICT`.
7. **HAZARD** — Prune uses `created_at` not ack time (`retention.go:42-43`) — long-lived unacked messages immune, old acked ones vanish.

### M13 — `msgWait` client poll session
1. **NAME** — Block until pending messages or timeout.
2. **STATE FIELD** — `msgWaitResp.TimedOut` (`messages.go:96-99`).
3. **STATES** — Immediate hit; polling; timed out; ctx cancelled.
4. **TRANSITIONS** — `msgGetPending` first (`messages.go:646-651`); zero timeout instant timeout (`messages.go:653-654`); 100ms ticker loop (`messages.go:658-673`).
5. **TERMINAL** — Response returned.
6. **IMPLICITNESS** — Only in wait handler.
7. **HAZARD** — Each tick runs full `keepaliveTx` — latency/DB load under many waiters.

### M14 — Quota window observation validity (publish-side)
1. **NAME** — Published quota snapshot window entries.
2. **STATE FIELD** — `QuotaWindow.Status` (`types.go:25`).
3. **STATES** — `observed`; `unknown` (`messages.go:185-207`).
4. **TRANSITIONS** — Validated on `quota-publish` (`messages.go:184-207`).
5. **TERMINAL** — Stored until node incarnation replaced (`messages.go:165`).
6. **IMPLICITNESS** — JSON contract only.
7. **HAZARD** — Not wired into send gating in this package — observational only.

---

## `internal/worker`

### M15 — Control-plane task lease supervision (worker view)
1. **NAME** — Worker holds lease while child runs; polls CP task row.
2. **STATE FIELD** — `controlplane.Task.State`, `LeaseOwner`, `LeaseToken`, `LeaseExpiresAt`, `CancelRequested` (read in `worker_runner.go:173-238`).
3. **STATES** — Implicit pre-`StartTask`; `StateRunning` + matching lease; cancel/loss/timeout outcomes via `reason` string.
4. **TRANSITIONS** — `StartTask` CAS (`worker.go:750-757`); loop in `awaitOutcome` (`worker_runner.go:169-303`); exit reasons: `""`, `cancelled`, `timeout`, `lease_lost:*`, `claim_refused_fence:*`, `invalid_timeout`.
5. **TERMINAL** — `collect` or early `FinishAttempt`.
6. **IMPLICITNESS** — CP states defined outside package; worker uses string prefix checks (`worker_sweep_collect.go:379`).
7. **HAZARD** — `lease_lost` uses `snapshot` **without** `FinishAttempt` (`worker_sweep_collect.go:379-380`) — depends on CP reaper elsewhere.

### M16 — `StartTask` claim-to-run promotion gate
1. **NAME** — Single winner transitions claimed task to running attempt.
2. **STATE FIELD** — `StartTask(...) bool` (`worker.go:37`; calls at `454`, `474`, `536`, `594`, `636`, `738`, `750`).
3. **STATES** — `false` (lost race / stale token); `true`.
4. **TRANSITIONS** — Must succeed before `awaitOutcome`; failure path terminates child (`worker.go:750-756`).
5. **TERMINAL** — `false` → snapshot only.
6. **IMPLICITNESS** — Boolean only in worker.
7. **HAZARD** — Pre-spawn failures call `StartTask` then `FinishAttempt` — ordering required; failure between them leaves ambiguous CP state if CP not atomic.

### M17 — Pre-run attempt abort pipeline (`RunOnce` before child)
1. **NAME** — Claimed task terminated without spawn (read-only, fence, isolation, resolution).
2. **STATE FIELD** — Local attempt artifacts + `FinishAttempt` status strings (`worker.go:453-650`).
3. **STATES** — `claim_refused_read_only`; `claim_refused_fence`; `isolation_unavailable`; `resolution_failed`; `spawn_failed` / `permission_denied`.
4. **TRANSITIONS** — Each guard: `StartTask` + `FinishAttempt` + `snapshot` (`worker.go:454-650`).
5. **TERMINAL** — All finish attempt.
6. **IMPLICITNESS** — JSON `status` strings only.
7. **HAZARD** — `claim_refused_fence` at pre-run uses structured finish; mid-run fence (`awaitOutcome`) returns `claim_refused_fence:` but **collect has no dedicated branch** (falls through to default — M18 hazard).

### M18 — `collect` terminal outcome router
1. **NAME** — Maps `reason` + child exit to `FinishAttempt` / pause.
2. **STATE FIELD** — `reason` parameter (`worker_sweep_collect.go:338`; switch `378-778`).
3. **STATES** — `lease_lost*` (no finish); `cancelled`; `timeout`; `invalid_timeout`; default (normal/pause/fence-failure/validation).
4. **TRANSITIONS** — Terminate child (`345-349`); branch-specific `FinishAttempt` (`395-772`); `maybePause` (`561-562`).
5. **TERMINAL** — Always `snapshot` (`779`).
6. **IMPLICITNESS** — Stringly-typed protocol.
7. **HAZARD** — `claim_refused_fence:` from M17/M19 not matched in switch — treated as success-path collect, may mis-finish while child was running.

### M19 — Heartbeat interval / renewal resilience counter
1. **NAME** — Grace for transient `GetTask` / `RenewHeartbeat` errors.
2. **STATE FIELD** — `consecutiveErrors`, `lastErr` (`worker_runner.go:166-167`).
3. **STATES** — 0..2 tolerating; ≥3 authoritative recheck or exit (`177-188`, `266-268`).
4. **TRANSITIONS** — Increment on CP errors; reset on success (`204-216`, `291-294`).
5. **TERMINAL** — `lease_lost: 3 consecutive...` strings.
6. **IMPLICITNESS** — Magic threshold 3.
7. **HAZARD** — During grace, child keeps running without lease extension if renewals fail but auth check passes once — window of expired lease depending on CP clock skew.

### M20 — Worker↔mesh claim fence renewal phases
1. **NAME** — Bind receipt to live mesh claim; renew on heartbeat.
2. **STATE FIELD** — `renew bool` arg (`claim_fence.go:44`; `worker.go:471`, `worker_runner.go:245-248`).
3. **STATES** — Validate-only (`renew=false` at claim); renew-on-beat (`renew=true`).
4. **TRANSITIONS** — `renewClaimFence` → `ActiveClaim` / `RenewClaim` / `mgr.RenewClaimBinding` (`claim_fence.go:66-82`).
5. **TERMINAL** — Error → pre-run refuse or `awaitOutcome` abort.
6. **IMPLICITNESS** — Comments only.
7. **HAZARD** — Missing `G8S_MESH_DIR` fails closed as stale (`claim_fence.go:61-63`) even if receipt valid.

### M21 — Worktree isolation acquire / release
1. **NAME** — Per-attempt private git worktree for `workspace_write`.
2. **STATE FIELD** — `releaseWorktree func(keep bool)`, `keepWorktree bool` (`worker.go:577-682`, `674-775`).
3. **STATES** — No isolator; acquired; released-discard (`keep=false`); released-retain (`keep=true`).
4. **TRANSITIONS** — `AcquireWorktree` / `AcquireRepoWorktree` (`616-620`); defer release (`676-682`); success sets `keepWorktree=true` (`773-775`).
5. **TERMINAL** — `release(...)` invoked in defer.
6. **IMPLICITNESS** — Interface contract (`worker.go:171-175`).
7. **HAZARD** — Acquire failure calls `releaseFailedWorktree` before finish (`622-624`) — may double-release if `release` nil mishandled (guarded).

### M22 — Failed worktree quarantine marker
1. **NAME** — Retain dirty failed worktree across attempts via marker file.
2. **STATE FIELD** — File `worktree-failed` in task dir (`failed_worktree.go:17`, `50`, `68-102`).
3. **STATES** — Absent; present (stores worktree path bytes).
4. **TRANSITIONS** — Write marker on dirty/status error (`31-53`); `cleanupFailedWorktree` on next write attempt (`579-581`, `67-102`); skip `release` if git inspect failed (`54-59`).
5. **TERMINAL** — Marker removed after git worktree remove (`99-101`).
6. **IMPLICITNESS** — Comments document invariant.
7. **HAZARD** — `release` deferred when status fails — isolator pool may leak directory until manual cleanup.

### M23 — Post-run diff firewall verdict phase
1. **NAME** — Attest git mutations vs receipt paths + claim scope.
2. **STATE FIELD** — `dfErr`, `violations`; `wr.ClaimFence` verdict (`worker_sweep_collect.go:601-642`; logic `diff_firewall.go:53-183`).
3. **STATES** — Skip (read perm); pass (`nil`); fail (stale/out-of-scope/unverifiable); verdict `allowed`/`rejected`.
4. **TRANSITIONS** — On success path only (`601-642`); `LoadClaimFence` + `VerifyWorktreeDiffScope`.
5. **TERMINAL** — Overrides `success` before `FinishAttempt`.
6. **IMPLICITNESS** — Error types from `receipt` package.
7. **HAZARD** — Claim validate uses `time.Now()` not supervisor clock (`diff_firewall.go:59`) — tests with fake clock may drift.

### M24 — Child process containment lifecycle
1. **NAME** — Spawned worker child until wait/terminate.
2. **STATE FIELD** — `processChild.done`, `processChild.code` (`worker_runner.go:20-37`).
3. **STATES** — Running (channel open); exited (`done` closed, `code` set).
4. **TRANSITIONS** — `cmd.Wait` goroutine (`32-36`); `Terminate` SIGTERM→SIGKILL (`62-80`); optional Windows `job` close (`77-80`, `354-356` in collect).
5. **TERMINAL** — Exited.
6. **IMPLICITNESS** — `Child` interface (`worker.go:95-100`).
7. **HAZARD** — `exec.CommandContext` timeout uses discarded `cancel` (`98-100`) — timer may leak until process exit.

### M25 — Attempt supervision poll loop (child vs lease vs deadline)
1. **NAME** — `awaitOutcome` multiplexed wait.
2. **STATE FIELD** — `nextBeat`, `deadline`, loop select (`worker_runner.go:162-302`).
3. **STATES** — Polling; child-done; heartbeat due; past deadline.
4. **TRANSITIONS** — `pollInterval` sleep (`298-302`); heartbeat renew (`244-295`); fence renew on beat (`245-248`).
5. **TERMINAL** — Returns `reason` to `collect`.
6. **IMPLICITNESS** — Empty string means normal completion.
7. **HAZARD** — Returns `""` immediately on `child.Done()` even if lease already invalid — `collect` may finish attempt as success if exit code 0.

### M26 — Pause / human-in-the-loop outcome
1. **NAME** — NEEDS_INFO / BLOCKED → `PauseTask` instead of finish.
2. **STATE FIELD** — `workerResult.Status` (`worker_loop_export.go:160-181`).
3. **STATES** — Running attempt; paused (CP state change via `PauseTask`).
4. **TRANSITIONS** — `maybePause` after result parse (`worker_sweep_collect.go:561-562`).
5. **TERMINAL** — `break` from switch without `FinishAttempt` in that branch.
6. **IMPLICITNESS** — Uses `controlplane.StateNeedsInfo` / `StateBlocked`.
7. **HAZARD** — Fenced JSON in stdout can trigger pause — spoof risk if stdout untrusted.

### M27 — Worker heartbeat emitter (process-level)
1. **NAME** — CLI worker session liveness file for fleet dashboards.
2. **STATE FIELD** — `currentStatus` (`heartbeat_emit.go:104-134`); `EmitterOptions.Status` (`heartbeat_emit.go:19`).
3. **STATES** — `running` | `idle` (CPU &lt;1%); shutdown `finished` | `failed` (ctx err) (`153-156`).
4. **TRANSITIONS** — Ticker poll (`118-143`); `stop()` once (`147-165`).
5. **TERMINAL** — Final record on stop.
6. **IMPLICITNESS** — Constants in `internal/heartbeat`.
7. **HAZARD** — CPU check error leaves status unchanged silently — stuck `running` while blocked on I/O.

### M28 — Telemetry engine refcount (per supervisor process)
1. **NAME** — Shared telemetry engine across concurrent runs.
2. **STATE FIELD** — `telRuns`, `telInited`, `telEngine` (`worker.go:309-312`; `worker_loop_export.go:70-106`).
3. **STATES** — Uninit; open with refcount N; closed (reset `telInited=false`).
4. **TRANSITIONS** — `telEnter`/`telExit` (`70-86`); lazy open on first use (`47-64`).
5. **TERMINAL** — Engine closed when `telRuns==0`.
6. **IMPLICITNESS** — Comments `#394`.
7. **HAZARD** — Close resets init — concurrent `telEnter` during close could race (mutex held only per call, not across ingest path).

### M29 — `RunLoop` drain / concurrency pool
1. **NAME** — Serial or N-worker claim drain.
2. **STATE FIELD** — `stop`, `active`, `parked` (`worker_loop_export.go:449-521`).
3. **STATES** — Worker claiming; parked (empty claim); global stop; all parked drain complete.
4. **TRANSITIONS** — `active++` before `RunOnce` (`500-501`); park increments (`517`); `parked==n && active==0` → stop (`518-520`).
5. **TERMINAL** — Goroutine exit on stop.
6. **IMPLICITNESS** — Condition variable pattern.
7. **HAZARD** — One worker parks on empty queue while others running — correct; but `isoErr` stops all without draining active attempts mid-flight beyond ctx cancel.

### M30 — Loop isolation per worker slot
1. **NAME** — Concurrent loop acquires private worktree dir once per goroutine.
2. **STATE FIELD** — `LoopIsolation.Acquire` / `release` (`worker_loop_export.go:356-363`, `480-492`).
3. **STATES** — Unacquired; holding dir; released on goroutine exit.
4. **TRANSITIONS** — Acquire before loop (`481-492`); `defer release()`.
5. **TERMINAL** — release at goroutine end.
6. **IMPLICITNESS** — Interface only.
7. **HAZARD** — Acquire failure sets global `isoErr` and `stop` — partial pool may have acquired dirs while others exit.

### M31 — Receipt export dual-destination status
1. **NAME** — Sealed evidence write to run dir + evidence lake.
2. **STATE FIELD** — `exportStatus.Local.Status`, `exportStatus.Central.Status` (`worker_loop_export.go:220-341`; constants in `controlplane`).
3. **STATES** — `succeeded` | `failed` | `disabled` per destination; aggregate `Success`, `FullyEvidenced`.
4. **TRANSITIONS** — Branch on env/paths (`261-335`); persist via `RecordReceiptExport` (`344`).
5. **TERMINAL** — Status snapshot on control plane (outside package).
6. **IMPLICITNESS** — Partially documented in CP model.
7. **HAZARD** — Export failure does not fail attempt (`ExportReceipt` swallows); operators may miss missing evidence.

### M32 — Real-time stream tee / pipe pump
1. **NAME** — Line-delimited callback streaming during attempt.
2. **STATE FIELD** — `UnbufferedPipeStreamer.wg`, `teeWriteCloser` (`stream.go:30-103`).
3. **STATES** — Open pipe; scanner running; closed after `Close`+`Wait`.
4. **TRANSITIONS** — `PipeTee` starts goroutine (`54-67`); close writer then `Wait` (`99-102`).
5. **TERMINAL** — `wg` drained.
6. **IMPLICITNESS** — None.
7. **HAZARD** — If caller forgets `Close` on writers, scanner blocks forever — `RunOnce` closes writers before collect (mitigated).

### M33 — Orphan process sweep (post-attempt)
1. **NAME** — Layer-1 process group + layer-2 `G8S_RUN_MARKER` sweep.
2. **STATE FIELD** — Ephemeral candidate PID list + time budget (`worker_sweep_collect.go:55-301`).
3. **STATES** — Child alive; group alive; marker-matched escapees.
4. **TRANSITIONS** — `defer sweepAttemptGroup` (`worker.go:762`); kill + trace (`58-62`, `291-298`).
5. **TERMINAL** — Sweep returns (best-effort).
6. **IMPLICITNESS** — Documented INVARIANT comment (`186-189`).
7. **HAZARD** — Budget/time cap (`287-288`) may leave marker-matched orphans alive.

### M34 — Startup orphan reap (`child.pid` stale files)
1. **NAME** — Pre-loop cleanup of crashed predecessor PIDs.
2. **STATE FIELD** — `child.pid` files under `runRoot` (`worker_loop_export.go:547-577`).
3. **STATES** — File absent; present with PID; removed after SIGKILL attempt.
4. **TRANSITIONS** — `reapOrphans` at `RunLoop` start (`396`).
5. **TERMINAL** — pid file removed.
6. **IMPLICITNESS** — POSIX-only (Windows skip `550-551`).
7. **HAZARD** — PID reuse could kill unrelated process if marker/file stale and PID recycled.

---

## Cross-cutting alignment (brief focus areas)

| Concern | Primary machines |
|--------|-------------------|
| Task/claim/lease lifecycle | M1, M3, M15–M17, M19–M20 (CP lease); mesh claim vs CP lease are **separate** stores |
| Retry / cool-off / budget | M4–M5 (`RetryBudget`, `CoolOffSeconds` in `transport.go:14-15`) |
| Flow window | M6 (`DefaultFlowWindow=1000`) |
| Keepalive | M10 (+ ping ack in M8) |
| Transport phases | M8–M9 (ack + surface gating + gated send response) |
| Worktree states | M21–M22 |
| Firewall phases | M3, M20, M23 (`diff_firewall.go`, `claim_fence.go`) |

**Documentation density:** No central FSM diagram in either package; behavior is inferable from SQL columns, string status codes, and bool gates. Highest-risk gaps: **M18** (`claim_refused_fence` mid-run), **M15** (`lease_lost` without `FinishAttempt`), **M10** (keepalive disabled at 10k messages), **M1** (expired `active` claims without sweeper).

---

*Scout complete — read-only, no files written.*


## Area B — internal/controlplane + harness + queue/receipt (scout B, 31 machines)

# SCOUT-SM-B — Implicit State Machines: `internal/controlplane` + `internal/harness`

**Scope:** Read-only scout over Go sources under `internal/controlplane` and `internal/harness`, plus `internal/receipt` and `internal/state` where controlplane wires them. **Out of scope in these packages:** ghost-process, orphan-worktree, and scratch-branch cleanup sweeps (only Phase 5 comments on `sessions`; implementation lives elsewhere, e.g. `internal/worker`).

---

## 1. SQLite task row lifecycle (primary queue FSM)

1. **NAME** — Durable worker task execution lifecycle  
2. **STATE FIELD** — `tasks.state` (`model.go:180`, `store_tasks.go:19-20`)  
3. **STATES** — `QUEUED`, `LEASED`, `RUNNING`, `NEEDS_INFO`, `BLOCKED`, `CHECKPOINTED`, `WORKER_COMPLETED`, `OUTPUT_INVALID`, `SUPERVISOR_ACCEPTED`, `SUPERVISOR_REJECTED`, `REVIEW_REQUIRED`, `SUCCEEDED`, `FAILED`, `CANCELLED` (`model.go:120-153`); row **absent** before `SubmitTask`; invalid/zero string rejected by `IsValidState`  
4. **TRANSITIONS** — `SubmitTask` → `QUEUED` (`store_tasks.go:586-592`); `ClaimTask*` CAS → `LEASED` (`store_claims.go:114-119`); `StartTask` → `RUNNING` (`store_claims.go:235-238`); `PauseTask` → `NEEDS_INFO`/`BLOCKED`/`CHECKPOINTED` (`lifecycle.go:680-684`); `FinishAttempt` → `WORKER_COMPLETED` (`lifecycle.go:446-451`); `AcceptResult` → `SUPERVISOR_ACCEPTED` (`lifecycle.go:1009-1011`); `RejectResult` → `SUPERVISOR_REJECTED` (`lifecycle.go:1052-1054`); `RequestEdit` → `NEEDS_INFO` (`lifecycle.go:1098-1100`); `RequeueResult` → `QUEUED` (`lifecycle.go:1148-1150`); `ResumeTask` → `QUEUED` from `NEEDS_INFO`/`BLOCKED` (`lifecycle.go:868-871`); `ResumeFromCheckpoint` → `RUNNING` from `CHECKPOINTED` (`lifecycle.go:806-809`); `CancelTask` → `CANCELLED` (non-`RUNNING`) or stay `RUNNING` + flag (`lifecycle.go:578-612`); `reconcileExpiredTx` → `QUEUED`/`FAILED`/`CANCELLED` (`store_claims.go:460-485`)  
5. **TERMINAL** — `FinalStates`: `SUCCEEDED`, `FAILED`, `CANCELLED`, `SUPERVISOR_ACCEPTED` (`model.go:155-162`); no store transition out; reconcile skips active reclaim  
6. **IMPLICITNESS** — Partially documented in `model.go` comments; **registry in `internal/state` omits** `WORKER_COMPLETED`, `CHECKPOINTED`, supervisor substates; store does **not** call `state.Apply` on mutations  
7. **HAZARD** — `FinishAttempt` always lands on `WORKER_COMPLETED` while `determineNextState` would choose `SUCCEEDED`/`QUEUED`/`FAILED` but is discarded (`lifecycle.go:390-431`); `SUCCEEDED`/`OUTPUT_INVALID`/`REVIEW_REQUIRED` are valid strings with **no writers** in this package → stuck or legacy rows if inserted externally

---

## 2. Lease possession sub-FSM (orthogonal to `state`)

1. **NAME** — CAS lease ownership for a task row  
2. **STATE FIELD** — `lease_owner`, `lease_token`, `lease_expires_at` (`model.go:189-191`)  
3. **STATES** — **Unleased** (all NULL), **Leased** (set at claim), **Held-through-RUNNING**; implicit **expired** when `lease_expires_at <= now` while still `LEASED`/`RUNNING`  
4. **TRANSITIONS** — Set on claim (`store_claims.go:116-117`); extended via `extendLease` (`store_claims.go:339-348`); cleared on finish/pause/cancel/reconcile (`lifecycle.go:447-448`, `store_claims.go:481-482`)  
5. **TERMINAL** — Unleased after terminal transitions; expired leases cleaned by reconcile  
6. **IMPLICITNESS** — Inferable only from SQL CAS predicates  
7. **HAZARD** — Reconcile matches `lease_token` in UPDATE (`store_claims.go:485`); if token rotated without row update, reconcile may **no-op** and leave ghost `RUNNING` until next claim-side reconcile

---

## 3. Cooperative cancellation flag

1. **NAME** — Cancel-requested vs terminal cancel  
2. **STATE FIELD** — `cancel_requested` INTEGER (`store_tasks.go:34-48`, `model.go:192`)  
3. **STATES** — `0` (false), `1` (true); readable via `ExecutionSignal` (`store_claims.go:395-397`)  
4. **TRANSITIONS** — `CancelTask` sets flag; immediate `CANCELLED` if not `RUNNING` (`lifecycle.go:584-612`); `FinishAttempt` does not clear flag on cooperative finish (`lifecycle.go:402-407`)  
5. **TERMINAL** — Cleared only indirectly when row reaches `CANCELLED` with full update  
6. **IMPLICITNESS** — Comments in `CancelTask`/`FinishAttempt` only  
7. **HAZARD** — `RUNNING` + `cancel_requested` depends on worker observing signal; crash before cooperative finish leaves **RUNNING** until lease expiry + reconcile → `CANCELLED` path (`store_claims.go:463-465`)

---

## 4. Worker execution signal (ephemeral read model)

1. **NAME** — Runtime poll state for a worker loop  
2. **STATE FIELD** — Derived from `state`, `cancel_requested`, `lease_owner`, `lease_token` (`store_claims.go:387-398`)  
3. **STATES** — `"active"`, `"cancel_requested"`, `"lease_lost"` (strings, not persisted)  
4. **TRANSITIONS** — `ExecutionSignal(taskID, workerID, leaseToken)` on each poll  
5. **TERMINAL** — `lease_lost` until worker exits; no DB edge  
6. **IMPLICITNESS** — Undocumented enum; string literals in code  
7. **HAZARD** — Non-`RUNNING` or token mismatch → `lease_lost` even if task merely `WORKER_COMPLETED` in DB → worker may treat success path as lease loss if it polls late

---

## 5. Expired-lease reconciliation sweep

1. **NAME** — Background lease expiry handler (inline on claim + `ReconcileExpired`)  
2. **STATE FIELD** — Same `tasks.state` + lease columns  
3. **STATES** — Input: `LEASED`/`RUNNING` with `lease_expires_at <= now`  
4. **TRANSITIONS** — `reconcileExpiredTx` (`store_claims.go:433-521`): default → `QUEUED`; `cancel_requested` → `CANCELLED`; `attempts >= max_attempts` → `FAILED`; may redact + seal `receipt_hash`  
5. **TERMINAL** — `FAILED`/`CANCELLED` terminal; `QUEUED` re-enters claim FSM  
6. **IMPLICITNESS** — Documented in function comments  
7. **HAZARD** — Requeue does **not** decrement `attempts` (already bumped at claim) → fast exhaust of `max_attempts` under repeated worker death

---

## 6. Control-plane maintenance lock

1. **NAME** — Singleton maintenance gate blocking new claims  
2. **STATE FIELD** — `control_plane_maintenance` row (`singleton=1`, `owner`, `expires_at`) (`store.go:254-258`, `lifecycle.go:914-930`)  
3. **STATES** — **Absent** / **Held** (unexpired) / **Stale** (expired, deleted opportunistically)  
4. **TRANSITIONS** — `BeginMaintenance` INSERT; `EndMaintenance` DELETE by owner; claim path deletes stale lock (`store_claims.go:71-83`); active lock → claim returns nil task (`store_claims.go:74-75`)  
5. **TERMINAL** — Absent after expiry or `EndMaintenance`; claims resume  
6. **IMPLICITNESS** — Operational API comments only  
7. **HAZARD** — Crash between COMMIT and `EndMaintenance` blocks claims until `expires_at` (no heartbeat extension on maintenance row)

---

## 7. Task event audit stream (`task_events`)

1. **NAME** — Append-only per-task operational log (not authoritative state)  
2. **STATE FIELD** — `event_type` + monotonic `event_id` (`lifecycle.go:149-157`, `store.go:244-250`)  
3. **STATES** — Open-ended event types (`task_submitted`, `task_claimed`, `attempt_finished`, `lease_expired`, …)  
4. **TRANSITIONS** — `insertTaskEvent` on each mutation (`store.go:755-768`)  
5. **TERMINAL** — Append-only; hashed into task evidence via `buildReceiptTx` (`lifecycle.go:307-355`)  
6. **IMPLICITNESS** — No central enum  
7. **HAZARD** — Events and `tasks.state` can be reasoned separately; partial tx rollback drops events but state may still change if another code path mutates

---

## 8. FSM `event_log` + replay

1. **NAME** — Cross-subject transition audit (DEBT-31)  
2. **STATE FIELD** — `event_log(from_state, to_state, event, subject, …)` (`store.go:703-713`)  
3. **STATES** — Rows are transitions, not aggregate state; replay builds history (`state/event_log.go:55-96`)  
4. **TRANSITIONS** — `LogStateEvent` → `state.Log` (`store.go:729-731`); `ReplayStateEvents` / `ShowStateEvents` read back  
5. **TERMINAL** — Append-only  
6. **IMPLICITNESS** — `internal/state` documents canonical FSM; **store task mutations do not auto-log here**  
7. **HAZARD** — DB commit can succeed with **no** `event_log` line unless caller logs → supervisor “fallback deadline” must use `tasks.state` + `task_events` (`store_claims.go:552-556`)

---

## 9. Pure FSM registry (`internal/state`)

1. **NAME** — Validator-only transition rules (Task, Orchestrator, Brief, Heartbeat)  
2. **STATE FIELD** — N/A in DB; `Registry` map (`state/state.go:127-128`)  
3. **STATES** — Task subset: through `CANCELLED` only (`state/state.go:31-40`, `191-215`); Brief `active`/`consumed`/`expired`; etc.  
4. **TRANSITIONS** — `state.Apply(subject, from, event, …)` (`state/state.go:154-175`)  
5. **TERMINAL** — Depends on subject; task `SUCCEEDED`/`FAILED`/`CANCELLED` have no exits in registry  
6. **IMPLICITNESS** — **Documented** in `state.go`  
7. **HAZARD** — **Drift** vs SQLite task FSM (extra states, `WORKER_COMPLETED` path) makes `Apply` unsafe as sole gate for queue rows

---

## 10. Delegated write receipt lifecycle (`receipts.db` + legacy mirror)

1. **NAME** — Single-use path-scoped write receipt  
2. **STATE FIELD** — `write_receipts.consumed` (0/1), `expires_at`, `consumer_task_id` (`lifecycle.go:40-41`, `receipt/receipt.go:231-232`)  
3. **STATES** — **Active** (unconsumed, unexpired), **Consumed**, **Expired** (time), **Absent**  
4. **TRANSITIONS** — Issue via `receipt.Manager.IssueReceipt` (`receipt.go:588-604`); consume via `ValidateAndConsume` CAS (`receipt.go:626-733`); controlplane `ConsumeWriteReceipt` tries ledger then legacy SQL (`lifecycle.go:73-124`); `AttachWriteReceiptSession` stamps `session_id` (`session.go:172-173`)  
5. **TERMINAL** — Consumed irreversible; expired may be purged (`receipt.go:1101-1135` `PurgeExpired`)  
6. **IMPLICITNESS** — Partially documented; split-brain dual stores  
7. **HAZARD** — **Split ledger**: issue in `receipts.db`, legacy row in `g8s.db` → consume path ambiguity; concurrent consume relies on CAS but two DBs can diverge

---

## 11. Task evidence receipt (`tasks.receipt_hash`)

1. **NAME** — Sealed completion evidence digest on task row  
2. **STATE FIELD** — `receipt_hash` (`model.go:186`, nullable)  
3. **STATES** — **Unset** / **Sealed** (hex digest)  
4. **TRANSITIONS** — Set on non-retryable `FinishAttempt`, `CancelTask` (terminal), `PauseTask`, reconcile terminal paths via `buildReceiptTx` (`lifecycle.go:498-508`, `628-635`, `711-716`, `store_claims.go:510-516`)  
5. **TERMINAL** — Sealed hash not cleared in these paths  
6. **IMPLICITNESS** — Comments on redaction + sealing  
7. **HAZARD** — Retryable finish keeps `receipt_hash` NULL but sets `WORKER_COMPLETED` → acceptance/export policies may see **incomplete evidence** while state looks “done”

---

## 12. Receipt export destination FSM

1. **NAME** — Per-task evidence lake export outcomes (orthogonal to execution `state`)  
2. **STATE FIELD** — `task_receipt_exports.status_json` → `ReceiptExportStatus` (`store_tasks.go:128-161`, `model.go:259-267`)  
3. **STATES** — Per destination: `disabled` | `succeeded` | `failed` (`model.go:243-247`); aggregate `Success`, `FullyEvidenced` booleans  
4. **TRANSITIONS** — `RecordReceiptExport` upsert only (`store_tasks.go:136-158`); does not touch `tasks.state`  
5. **TERMINAL** — Can be re-recorded via upsert  
6. **IMPLICITNESS** — Typed in `model.go`  
7. **HAZARD** — `CheckExportAcceptance` can block acceptance while task already `SUPERVISOR_ACCEPTED` if export recorded later—ordering is caller-dependent

---

## 13. Claim-fence verdict (export payload)

1. **NAME** — Harvest binding verdict bound to task result/export  
2. **STATE FIELD** — `ReceiptExportStatus.ClaimFence.Verdict`, `Code` (`model.go:271-278`)  
3. **STATES** — `unverified` (default), `rejected` (inferred from result strings) (`model.go:300-308`); codes e.g. `E_CLAIM_FENCE_STALE`  
4. **TRANSITIONS** — Inferred in `TaskClaimFenceVerdict`; not written by controlplane mutations  
5. **TERMINAL** — Snapshot at export time  
6. **IMPLICITNESS** — String sniffing on `task.Result`  
7. **HAZARD** — Verdict can remain `unverified` despite failed harvest if result JSON lacks marker strings

---

## 14. Session registry lifecycle

1. **NAME** — Live vs dead supervisor sessions (#393)  
2. **STATE FIELD** — `sessions.status`, `heartbeat_at`, `mode`, `worktree_path` (`session.go:11-23`, `store.go:267-273`)  
3. **STATES** — `active` | `dead` (`session.go:13-14`); mode `in_place` | `worktree` (`session.go:21-23`)  
4. **TRANSITIONS** — `RegisterSession` upsert → `active` (revives dead) (`session.go:75-84`); `HeartbeatSession` updates `heartbeat_at` (`session.go:94-96`); `MarkSessionDead` → `dead` (`session.go:111-113`)  
5. **TERMINAL** — `dead` until explicit re-register; **no automatic reap** in this package (`session.go:108-109` Phase 5 note)  
6. **IMPLICITNESS** — Comments describe heartbeat timeout contract not enforced here  
7. **HAZARD** — Stale `active` sessions without heartbeat → false “live” truth until external sweep

---

## 15. Session quota gate (admission control)

1. **NAME** — Per-session task submission limits  
2. **STATE FIELD** — `session_quotas` + live counts from `tasks` (`store_claims.go:654-740`)  
3. **STATES** — Implicit under/over quota; default quota synthetic when row absent (`store_claims.go:666-674`)  
4. **TRANSITIONS** — `CheckSessionQuota` before submit (callers); `SetSessionQuota` upsert  
5. **TERMINAL** — N/A  
6. **IMPLICITNESS** — Numeric thresholds only  
7. **HAZARD** — Hourly window uses `created_at` count, not completions → burst submit can bypass “concurrent” intent if tasks finish quickly

---

## 16. Brief row lifecycle (`briefs` table)

1. **NAME** — Dispatch brief consumption  
2. **STATE FIELD** — `briefs.status` (`brief_store.go:39-41`, default `active`)  
3. **STATES** — `active`, `consumed`, `expired` (`internal/brief/brief.go:18-21`); **absent** row → error  
4. **TRANSITIONS** — `CreateBrief` → `active` (`brief_store.go:43-48`); `brief.Consume` → `consumed` via `state.Apply` + `UpdateBriefStatus` (`brief/brief.go:121-127`); expiry → `expired` on consume/list (`brief/brief.go:113-117`, `147-149`); store-only `UpdateBriefStatus` allows arbitrary string (`brief_store.go:112-131`)  
5. **TERMINAL** — `consumed`, `expired`  
6. **IMPLICITNESS** — FSM in `state.go:232-236`; enforcement in `brief` package, not store  
7. **HAZARD** — Direct `UpdateBriefStatus` **bypasses** `state.Apply` → invalid brief states possible

---

## 17. Supervisor task row state (opaque)

1. **NAME** — Long-running supervisor run persistence  
2. **STATE FIELD** — `supervisor_tasks.state` (`model.go:78`, `supervisor_store.go:30-34`)  
3. **STATES** — **Free-form string** required non-empty on create; transitions owned by `internal/supervisor`  
4. **TRANSITIONS** — `CreateSupervisorTask`, `UpdateSupervisorTask` (`supervisor_store.go:76-99`)  
5. **TERMINAL** — Undefined in controlplane  
6. **IMPLICITNESS** — Only storage contract here  
7. **HAZARD** — Typos in state strings are persistently valid SQL; no controlplane validator

---

## 18. Supervisor decisions / metrics

1. **NAME** — Immutable decision log + replaceable metrics snapshot  
2. **STATE FIELD** — `supervisor_decisions.kind`; metrics row keyed by `supervisor_task_id`  
3. **STATES** — Append-only decisions; metrics **INSERT OR REPLACE** (`supervisor_store.go:159-184`, `186-200`)  
4. **TRANSITIONS** — Append on events; metrics overwrite  
5. **TERMINAL** — Decisions permanent  
6. **IMPLICITNESS** — Kind is free-form (`model.go:90-92`)  
7. **HAZARD** — Metrics replace can **erase** prior telemetry without history

---

## 19. DB schema migration gate

1. **NAME** — SQLite `user_version` schema generation  
2. **STATE FIELD** — `PRAGMA user_version` (`model.go:28-49`, `store.go:131-191`)  
3. **STATES** — Versions 0…`SchemaVersion` (11); **unsupported** if `> SchemaVersion` (`store.go:207-209`)  
4. **TRANSITIONS** — `initialize`: exclusive tx, layered `migrate*` + `apply*` (`store.go:141-188`); fast-path skip if already at version (`store.go:132-134`)  
5. **TERMINAL** — At `SchemaVersion` until binary upgrade  
6. **IMPLICITNESS** — History in `model.go` comment block  
7. **HAZARD** — Fast-path at v11 still runs `applyAccessAuditSchema` only—partial manual DB edits may **not** re-run column migrators

---

## 20. Auto-resubmit / retry-classification machine

1. **NAME** — ADR-0029 failed-task auto-retry (out-of-band new task)  
2. **STATE FIELD** — New row `QUEUED`; delay via `request_json.not_before` (`retry.go:6-11`, `537-539`); classification is ephemeral `RetryClass` (`retry.go:36-47`)  
3. **STATES** — `RETRYABLE` | `NOT_RETRYABLE`; descendant lineage tracked via `parent_task_id`  
4. **TRANSITIONS** — `ResubmitTask` from `FAILED` only (`retry.go:380-382`); `ClassifyRetryable` on result/error (`retry.go:192-293`); budget checks (`retry.go:517-524`); `SubmitTask` child (`retry.go:629`)  
5. **TERMINAL** — Original stays `FAILED`; child enters task FSM  
6. **IMPLICITNESS** — Documented in `retry.go` header  
7. **HAZARD** — Hourly budget is **in-memory** per `dbPath` (`retry.go:295-353`) → process restart resets cap, allowing retry storms

---

## 21. Submit idempotency deduplication

1. **NAME** — Same idempotency key → same task row  
2. **STATE FIELD** — `idempotency_key` UNIQUE; transient `Task.Deduplicated` (`model.go:225-227`)  
3. **STATES** — New insert vs dedup hit  
4. **TRANSITIONS** — `checkExistingTask` hash+parent match (`store_tasks.go:548-563`)  
5. **TERMINAL** — Dedup returns existing state unchanged  
6. **IMPLICITNESS** — API comment on `SubmitTask`  
7. **HAZARD** — Key collision with different hash errors hard (`store_tasks.go:556-558`); concurrent double-submit races UNIQUE constraint (must retry at app layer)

---

## 22. Submit rate limit (per actor)

1. **NAME** — Hourly submit throttle  
2. **STATE FIELD** — Count of `tasks` in rolling hour per extracted actor (`store_tasks.go:727-738`)  
3. **STATES** — Under limit vs `ErrSubmitRateLimited`  
4. **TRANSITIONS** — Checked inside submit tx when `SetSubmitRateLimitPerHour > 0`  
5. **TERMINAL** — Resets as window slides  
6. **IMPLICITNESS** — Global package var (`store_tasks.go:655-671`)  
7. **HAZARD** — Actor extracted from JSON/orchestrator fields—spoofable in `request_json.actor`

---

## 23. SQLite BUSY retry loop (lease heartbeats)

1. **NAME** — Transient DB lock recovery  
2. **STATE FIELD** — None persisted; retry counter in `withBusyRetry` (`store_claims.go:305-334`)  
3. **STATES** — Attempt 0..`busyRetryAttempts-1`  
4. **TRANSITIONS** — On `isBusyErr`, jittered sleep and retry (`store_claims.go:319-328`)  
5. **TERMINAL** — Success or non-busy error  
6. **IMPLICITNESS** — Comment #465  
7. **HAZARD** — After retries exhausted, heartbeat returns error → worker may think `ErrLeaseLost` vs `ErrBusy` depending on path (`store_claims.go:369-373`)

---

## 24. Task completion supervisor gate subgraph

1. **NAME** — Post-worker acceptance workflow  
2. **STATE FIELD** — `tasks.state` + `supervisor_feedback` JSON (`lifecycle.go:983-1162`)  
3. **STATES** — `WORKER_COMPLETED` hub → `SUPERVISOR_ACCEPTED` | `SUPERVISOR_REJECTED` | `NEEDS_INFO` (edit) | `QUEUED` (requeue)  
4. **TRANSITIONS** — As listed in machine #1 for accept/reject/edit/requeue  
5. **TERMINAL** — `SUPERVISOR_ACCEPTED` in `FinalStates`; `SUPERVISOR_REJECTED` **not** final—no automatic exit defined here  
6. **IMPLICITNESS** — Method names document intent  
7. **HAZARD** — `SUPERVISOR_REJECTED` has **no** built-in transition to `QUEUED` in store (unlike `RequestEdit`)

---

## 25. Checkpoint vs pause recovery

1. **NAME** — Mid-run checkpoint without leaving `RUNNING` vs pause-to-`CHECKPOINTED`  
2. **STATE FIELD** — `checkpoint_data` JSON; `state` either stays `RUNNING` (`CheckpointTask`) or `CHECKPOINTED` (`PauseTask`) (`lifecycle.go:730-767`, `647-684`)  
3. **STATES** — Empty vs populated checkpoint blob  
4. **TRANSITIONS** — `CheckpointTask` lease-guarded update only; `ResumeFromCheckpoint` requires `CHECKPOINTED`  
5. **TERMINAL** — N/A until finish  
6. **IMPLICITNESS** — Issue #290 comments  
7. **HAZARD** — Two pause mechanisms (`PauseTask`→`CHECKPOINTED` vs in-run `CheckpointTask`) confuse operators; wrong resume API for state → hard error

---

## 26. Terminal transition signal file

1. **NAME** — Post-commit wakeup signals for supervisor  
2. **STATE FIELD** — Lines in `signals/tasks.jsonl`; optional rotation to `.1` (`store_claims.go:524-636`)  
3. **STATES** — File size under/over `maxSignalFileBytes`  
4. **TRANSITIONS** — `appendTaskSignal` when `to` ∈ {`FAILED`,`WORKER_COMPLETED`,`SUCCEEDED`,`CANCELLED`,`NEEDS_INFO`} (`store_claims.go:532-559`)  
5. **TERMINAL** — File append best-effort  
6. **IMPLICITNESS** — Crash window documented (`store_claims.go:552-556`)  
7. **HAZARD** — Committed DB transition **without** signal line after crash → supervisor must poll DB, not file only

---

## 27. Harness: request admission gate (pre-queue)

1. **NAME** — Role/permission/prompt safety before task persist  
2. **STATE FIELD** — No persistence; pass/fail error from `ValidateSubmitRequest` → `harness.ValidateRequest` (`lifecycle.go:168-228`, `harness/harness.go:64-131`)  
3. **STATES** — **Admitted** vs validation error  
4. **TRANSITIONS** — Called from `prepareSubmitRequest` (`store_tasks.go:487-488`)  
5. **TERMINAL** — Reject = no row  
6. **IMPLICITNESS** — Permission rules in `permissions.go`; patterns in `harness.go`  
7. **HAZARD** — `workspace_write` gated by env `AGY_MCP_ALLOW_WORKSPACE_WRITE` in controlplane **and** receipt check in harness—bypass if one layer skipped on alternate submit API

---

## 28. Harness: permission profile selection

1. **NAME** — Static permission capability model  
2. **STATE FIELD** — Request string `permission` / `role` (in payload), not DB enum  
3. **STATES** — `read_only`, `automation_read`, `workspace_write`, `supplied_text` (`permissions.go:18-51`)  
4. **TRANSITIONS** — `GetPermission` lookup at validate time  
5. **TERMINAL** — N/A (declarative config)  
6. **IMPLICITNESS** — Documented map in code  
7. **HAZARD** — Unknown permission string fails closed at validate; **typo** blocks submit rather than defaulting

---

## 29. Harness: capability hydration (derived execution envelope)

1. **NAME** — Resolved role/stratum/MCP tool surface for workers  
2. **STATE FIELD** — `CapabilityHydration` struct (`hydration.go:10-16`); output of `ResolveCapabilityHydration`  
3. **STATES** — Fallback to `collector` or `implementer` on unknown role (`hydration.go:47-55`)  
4. **TRANSITIONS** — Pure function from role+permission  
5. **TERMINAL** — N/A  
6. **IMPLICITNESS** — Comments on S3-A vs S3-M  
7. **HAZARD** — Unknown role silently remapped—audit logs may say `scout` while hydration used `collector`

---

## 30. Access audit mirror (enterprise audit)

1. **NAME** — Normalized audit actions from task events  
2. **STATE FIELD** — `access_audit.action` (`access_audit.go:46-55`)  
3. **STATES** — Append-only rows  
4. **TRANSITIONS** — `insertAccessAuditTx` paired with `insertTaskEvent` (`store.go:769-771`)  
5. **TERMINAL** — Permanent  
6. **IMPLICITNESS** — `mapTaskEventToAuditAction` table (`access_audit.go:68-92`)  
7. **HAZARD** — Unknown `event_type` maps to generic `task.<event>`—downstream SIEM rules may miss semantic alignment

---

## Coverage gaps (brief checklist)

| Area | In `controlplane` / `harness`? |
|------|-------------------------------|
| Ghost processes | **No** implementation |
| Orphan worktrees | **No** (session `worktree_path` stored only) |
| Scratch branches | **No** |
| Receipt TTL purge | **`internal/receipt.PurgeExpired`**, not invoked from controlplane store |
| Event-log replay | **Yes** — `ReplayStateEvents`; population mostly external to task mutations |
| Lease resilience | **Yes** — BUSY retry, reconcile, CAS heartbeats, `ErrLeaseLost` |

---

**Machine count:** 30 distinct implicit lifecycles / state-bearing mechanisms enumerated above (primary task FSM + orthogonal sub-FSMs, persistence, gates, and harness admission).


## Area C — internal/provider + reach + CLI surfaces (scout C, 52 machines)

# SCOUT-SM-C — Implicit State Machines: `internal/provider` + `internal/reach` + `cmd/g8s` (provider / lifecycle / service)

Scope: read-only scout over provider handle & pool lifecycle, stderr refusal classification, reach fallback + health ledger, conformance gates, service daemon, `g8s update` / `uninstall`, and `g8s providers` CLI surfaces. Line refs are `path:line`.

---

## A. Provider handle & execution receipts

### SM-01 — `execHandle` subprocess lifecycle (`spawn` → `run` → `wait`/`cancel`)

1. **NAME** — OS subprocess worker handle from `Spawn` through exit or cancellation.
2. **STATE FIELD** — `done chan struct{}` (open = in flight, closed = reaped); `waitErr error` (`handle.go:34-35`, `47-49`); `cmd *exec.Cmd` / `Process` (`handle.go:30-31`).
3. **STATES** — **Running** (`done` open); **Exited** (`done` closed, `waitErr` set); **PID absent** (`cmd` nil or `Process` nil → `PID()==0`, `handle.go:55-59`).
4. **TRANSITIONS** — `newProcessHandle` starts goroutine `cmd.Wait()` → `close(done)` (`handle.go:47-49`); `Wait` selects `ctx.Done()` → `Cancel` + drain `done` → `TIMEOUT` receipt (`handle.go:64-75`); `Wait` on `<-done` → map exit/API error → `COMPLETED`/`FAILED` (`handle.go:76-109`); `Cancel` → `killProcessGroup` (`handle.go:112-120`).
5. **TERMINAL** — Post-`done` closed: no re-spawn; process group kill is best-effort cleanup.
6. **IMPLICITNESS** — Documented only via `Handle`/`Receipt` strings; no enum for running vs exited.
7. **HAZARD** — `Wait` on ctx cancel always returns `TIMEOUT` even if process already exited; race can mis-label a fast exit as timeout (`handle.go:64-75`).

### SM-02 — `immediateHandle` (HTTP / sync completion)

1. **NAME** — Synchronous “handle” for HTTP providers where work finishes in `Spawn`.
2. **STATE FIELD** — `status string`, pre-closed `done chan struct{}` (`openai.go:477-488`, `507-509`).
3. **STATES** — **Pre-resolved** (`close(h.done)` at construction, `openai.go:509`); receipt fields fixed at birth (`status` `COMPLETED` vs `FAILED`).
4. **TRANSITIONS** — `newImmediateHandle*` → `Wait` blocks `<-done` (no-op), returns frozen `Receipt` (`openai.go:515-532`); `Cancel` no-op (`openai.go:535`).
5. **TERMINAL** — Immediate terminal at construction; no running phase.
6. **IMPLICITNESS** — Comment at `openai.go:475-476`; shares `Receipt.Status` vocabulary with subprocess path.
7. **HAZARD** — `Cancel` cannot abort in-flight HTTP already completed in `Spawn`; callers assuming PID/process semantics get `PID()==0` (`openai.go:513`).

### SM-03 — Worker `Receipt.Status` outcome

1. **NAME** — Normalized execution outcome across handle kinds.
2. **STATE FIELD** — `Receipt.Status string` (`registry.go:50`).
3. **STATES** — `COMPLETED`, `FAILED`, `TIMEOUT` (documented); `CANCELLED` in comment only (`registry.go:50`); implicit from exit code / stderr heuristics (`handle.go:83-96`).
4. **TRANSITIONS** — Set in `execHandle.Wait` or `immediateHandle.Wait` (`handle.go:83-109`, `openai.go:518-523`).
5. **TERMINAL** — Receipt is immutable value; no transitions after `Wait`.
6. **IMPLICITNESS** — Partially documented in struct comment; `CANCELLED` unused in these paths.
7. **HAZARD** — CLI agents exiting 0 with `[API Error:` and empty stdout → forced `FAILED` (`handle.go:93-96`); easy to miss vs true success.

### SM-04 — CLI agent binary resolution cache (per-provider)

1. **NAME** — Lazy “discovered vs unknown” binary for platform_dispatch providers (pattern in `OpenCodeProvider`, peers in `cli_agents.go`).
2. **STATE FIELD** — `binary string`, `version string` under `sync.RWMutex` (e.g. `cli_agents.go:21-24`, `84-87`).
3. **STATES** — **Unresolved** (zero `binary`); **Resolved** (after successful `Available` probe).
4. **TRANSITIONS** — `Available` → `LookPath` / env override → version probe → lock write (`cli_agents.go:69-87`); `Spawn` calls `Available` first (`cli_agents.go:109-112`).
5. **TERMINAL** — Cache persists for process lifetime; no invalidation on PATH changes.
6. **IMPLICITNESS** — Inferable only; no explicit state type.
7. **HAZARD** — Stale `binary` if operator moves binary after first probe; subsequent spawns use old path.

### SM-05 — Registry `ProviderStatus` (list / detect)

1. **NAME** — CLI-facing OK/NO for each registered provider (`g8s providers`).
2. **STATE FIELD** — `ProviderStatus.Status` (`registry.go:71-73`).
3. **STATES** — `OK`, `NO` (+ optional `Reason`, `BinaryPath`, `Version`).
4. **TRANSITIONS** — `List` → `Available(ctx)` err≠nil → `NO` else `OK` (`registry.go:164-172`).
5. **TERMINAL** — Snapshot per call; no persistence.
6. **IMPLICITNESS** — Comment on struct (`registry.go:73`).
7. **HAZARD** — Transient probe failure (e.g. `boundedProbe` timeout) marks `NO` without retry distinction (`cli_agents.go:37-44`).

### SM-06 — Catalog recommendation overlay

1. **NAME** — Built-in catalog entries annotated by live registration/availability.
2. **STATE FIELD** — `CatalogEntry.InstallHint` string mutation (`registry.go:182-193`).
3. **STATES** — Default hint; **not registered**; **registered but unavailable** (prefix strings).
4. **TRANSITIONS** — `Recommend`: `Get` miss → prefix `not registered:` (`registry.go:186-188`); `probeOne` err → `registered but unavailable:` (`registry.go:190-191`).
5. **TERMINAL** — Per-call derived view.
6. **IMPLICITNESS** — String-encoded only.
7. **HAZARD** — Hints are prose, not machine codes; automation must parse prefixes.

---

## B. Pool registry, health, concurrency (DELTA-05/10)

### SM-07 — `HealthStatus` / `ProviderInfo` probe snapshot

1. **NAME** — Per-provider readiness in `PoolRegistry`.
2. **STATE FIELD** — `ProviderInfo.Status HealthStatus` (`provider.go:61`); cached in `state.last` (`provider.go:186-187`).
3. **STATES** — `READY`, `DEGRADED`, `UNAVAILABLE` (`provider.go:36-44`); **never probed** (`LastHealthCheckAt==0`, `provider.go:463-470`).
4. **TRANSITIONS** — `probe`: auth missing → `UNAVAILABLE` + `Reason` (`provider.go:265-271`); HTTP/binary paths (`provider.go:293-304`); `probeHTTP` ladder (`provider.go:415-452`); lazy `GetProvider` triggers first probe (`provider.go:469-470`).
5. **TERMINAL** — Snapshot until next `probe`/`DiscoverAll`; not a long-lived FSM on disk.
6. **IMPLICITNESS** — Documented consts (`provider.go:36-44`); zero `last` is implicit.
7. **HAZARD** — `GetProvider` returns stale `st.last` without re-probe when `LastHealthCheckAt≠0`; daemon can go down unnoticed.

### SM-08 — `probeHTTP` degradation ladder (9router / Ollama / api_call)

1. **NAME** — HTTP health probe outcome refinement.
2. **STATE FIELD** — Return value of `probeHTTP` → assigned to `info.Status` (`provider.go:273-299`).
3. **STATES** — `READY` (200 on primary or fallbacks), `DEGRADED` (reachable but bad status), `UNAVAILABLE` (dial/req error).
4. **TRANSITIONS** — Primary GET (`provider.go:416-427`); `/v1` → `/models`, `/api/health` (`provider.go:430-448`); else `DEGRADED` (`provider.go:452`).
5. **TERMINAL** — Per probe invocation.
6. **IMPLICITNESS** — Only in control flow.
7. **HAZARD** — Short `http.Client{Timeout: time.Second}` (`provider.go:210`) can flap `UNAVAILABLE` on slow 9router cold start.

### SM-09 — Concurrency slot / `inFlight`

1. **NAME** — Per-provider semaphore for dispatch slots.
2. **STATE FIELD** — `state.sem chan struct{}`, `inFlight int` (`provider.go:184-186`, `496-507`).
3. **STATES** — **Slots available** (can send on `sem`); **held** (after acquire, `inFlight++`); **blocked** (select waits on `sem` or `ctx.Done()`, `provider.go:489-493`).
4. **TRANSITIONS** — `AcquireSlot` send on `sem` (`provider.go:492-493`); `release` decrements and receives from `sem` (`provider.go:502-509`); `ctx` cancel → no slot, error (`provider.go:489-491`).
5. **TERMINAL** — Slot held until `release()` exactly once (documented contract, `provider.go:478-480`).
6. **IMPLICITNESS** — Channel occupancy is implicit.
7. **HAZARD** — Panic or forgotten `release()` exhausts `sem` permanently for that provider until process restart.

### SM-10 — `SelectForModel` candidate ordering

1. **NAME** — READY providers serving a model, preferring `api_call`.
2. **STATE FIELD** — Ephemeral `candidates []string` from `st.last.Status == StatusReady` (`provider.go:393-394`).
3. **STATES** — Empty candidates → error; else ordered try with `AcquireSlot`.
4. **TRANSITIONS** — Build candidates (`provider.go:373-395`); sort `api_call` first (`provider.go:397-403`); try acquire per name (`provider.go:406-410`).
5. **TERMINAL** — Success returns `(name, release, nil)`; total failure → error (`provider.go:411`).
6. **IMPLICITNESS** — Uses cached `last` only, no live re-probe in loop.
7. **HAZARD** — READY snapshot vs acquire-time slot exhaustion: race returns “no ready provider” despite recent probe.

### SM-11 — `DeviceLeasePool` exclusive lease

1. **NAME** — 1-device-1-worker lease (DELTA-38).
2. **STATE FIELD** — `DeviceSlot.ActiveLease *DeviceLease` (`device_lease.go:35`, `99-122`).
3. **STATES** — **Idle** (`ActiveLease==nil`); **Leased** (valid `ExpiresAt`); **Expired-but-not-reaped** (reaped on next `Acquire`, `device_lease.go:100-101`); **Same-holder renew** (`device_lease.go:102-105`).
4. **TRANSITIONS** — `AcquireLease` grant/reject/busy (`device_lease.go:98-109`, `112-124`); `ReleaseLease` clear (`device_lease.go:139-146`); `SnapshotStatus` omits expired (`device_lease.go:162-164`).
5. **TERMINAL** — Released → idle; expired lease cleared on acquire path only (not background).
6. **IMPLICITNESS** — Expiry checked opportunistically.
7. **HAZARD** — Expired lease blocks nothing in `SnapshotStatus` but still occupies slot until next `AcquireLease` on that device—window for stale “busy” reporting.

### SM-12 — `OllamaProvider` daemon reachability

1. **NAME** — Local Ollama HTTP daemon presence (not full spawn).
2. **STATE FIELD** — `host`, `version` after probe (`ollama.go:16-18`, `71-73`).
3. **STATES** — Unprobed; daemon **up** (`Available` nil); **Spawn refused** (not implemented, `ollama.go:115-120`).
4. **TRANSITIONS** — `Available` GET `/api/tags` (`ollama.go:52-75`); `Spawn` → always error after availability (`ollama.go:116-120`).
5. **TERMINAL** — Spawn path is terminal refusal.
6. **IMPLICITNESS** — Stub documented in comment.
7. **HAZARD** — Registry may list Ollama `OK` while worker dispatch cannot spawn—dual semantics across `Registry` vs `OllamaProvider.Spawn`.

---

## C. Provider refusal classification

### SM-13 — `ClassifyProviderStderrRefusal` decision tree

1. **NAME** — Typed refusal from CLI stderr (quota/auth/model).
2. **STATE FIELD** — Return `(ProviderRefusal, bool)`; `ProviderRefusal.Code` (`codex.go:22-27`, `45-79`).
3. **STATES** — **No refusal** (`ok==false`); **Quota** `E_PROVIDER_QUOTA_EXHAUSTED`; **Auth** `E_PROVIDER_AUTH`; **Model** `E_PROVIDER_MODEL_UNAVAILABLE`.
4. **TRANSITIONS** — Empty stderr → false (`codex.go:47-48`); keyword branches (`codex.go:51-76`); default false (`codex.go:77-78`).
5. **TERMINAL** — Classification is pure function per call.
6. **IMPLICITNESS** — Documented consts (`codex.go:16-20`); matching is substring heuristics.
7. **HAZARD** — Exit-0 + empty stdout refusals outside keyword set evade classification and may count as `COMPLETED` unless other heuristics catch them.

### SM-14 — `codexFinalizeReceipt` overlay on subprocess receipt

1. **NAME** — Post-wait reclassification for codex stderr refusals.
2. **STATE FIELD** — Mutates `exitCode`, `status`, `waitErr` (`codex.go:142-152`).
3. **STATES** — Unchanged if no refusal; else forces `exitCode=1`, `status=FAILED`, `waitErr` from `refusal.Reason()` when refusal detected (`codex.go:148-152`).
4. **TRANSITIONS** — Invoked from `execHandle.Wait` when `provider=="codex"` (`handle.go:98-100`).
5. **TERMINAL** — Receipt returned once from `Wait`.
6. **IMPLICITNESS** — Codex-only hook; other CLIs rely on generic API-error line scan (`handle.go:93-96`).
7. **HAZARD** — Inconsistent refusal handling: codex gets typed codes; qwen-like agents only get `E_PROVIDER_CLI_API_ERROR` path.

### SM-15 — Coded spawn/availability errors (`codedError`)

1. **NAME** — Structured provider error prefix taxonomy.
2. **STATE FIELD** — Error string prefix codes (`coded_error.go:5-10`).
3. **STATES** — `E_PROVIDER_BINARY_NOT_FOUND`, `E_PROVIDER_PROBE_FAILED`, `E_PROVIDER_SPAWN_FAILED`, `E_PROVIDER_CLI_API_ERROR`.
4. **TRANSITIONS** — Returned from `Available`/`Spawn` paths (e.g. `cli_agents.go:74`, `138`).
5. **TERMINAL** — Error values; no recovery state machine.
6. **IMPLICITNESS** — Const block only.
7. **HAZARD** — Callers using `errors.Is` on sentinel types may miss coded string errors.

---

## D. Reach: fallback chain + health ledger

### SM-16 — Reach `Chain.Run` backend attempt loop

1. **NAME** — Ordered backend fallback until success or exhaustion.
2. **STATE FIELD** — Loop index `rank`, `Attempt.Class`, accumulated `attempts` (`chain.go:64-87`).
3. **STATES** — Per-backend: pending try; outcome class **`ok`**, **`fallback`**, **`terminal`** (`chain.go:70-85`); chain ends on first `ok`, immediate return on `terminal`, or aggregate error (`chain.go:77-89`).
4. **TRANSITIONS** — `Orderer.Order` reorders names (`chain.go:56-58`); `Fetch` + `Classify` (`chain.go:69-70`); `Recorder.Record` (`chain.go:72-74`); branch on class (`chain.go:77-85`).
5. **TERMINAL** — Success (`ok`); hard stop (`terminal`); all backends fallback → error (`chain.go:89`).
6. **IMPLICITNESS** — `Attempt.Class` documented only in JSON tags (`chain.go:28-33`).
7. **HAZARD** — `terminal` with nil `err` still fails closed (`chain.go:80-84`)—callers must not assume HTTP body on terminal.

### SM-17 — `Classify` (refusal-style routing for reach, not provider stderr)

1. **NAME** — Maps errors/HTTP status to `ok` / `fallback` / `terminal`.
2. **STATE FIELD** — Return string (`chain.go:92-124`).
3. **STATES** — `ok`; `fallback` (429, 5xx, timeouts, net errors, default); `terminal` (4xx except 429, nil err non-2xx, invalid/not-in-path messages).
4. **TRANSITIONS** — Predicate order in `Classify` (`chain.go:94-124`).
5. **TERMINAL** — Pure function.
6. **IMPLICITNESS** — Comment `chain.go:92`; overlap with provider refusal is conceptual only.
7. **HAZARD** — Generic `invalid` substring in error text → `terminal` (`chain.go:121-122`), skipping fallback for potentially transient misconfiguration.

### SM-18 — Declared backend registry order (channel/action)

1. **NAME** — Static fallback order before health reorder.
2. **STATE FIELD** — `channelRegistry` map (`registry.go:57-64`).
3. **STATES** — `web/read` → `web-https-get`; `github/read` → `github-gh-api` (single-backend chains today).
4. **TRANSITIONS** — `Lookup` copies slice (`registry.go:76-78`).
5. **TERMINAL** — Compile-time registry.
6. **IMPLICITNESS** — `doc.go` mentions fallback; order is map iteration order avoided by explicit slices.
7. **HAZARD** — Adding a second backend without tests may rely solely on `Orderer` for order—declared order alone is not health-aware.

### SM-19 — Health ledger per-backend record (`Code` + cooldown + fingerprint)

1. **NAME** — Persisted evidence for backend attempts.
2. **STATE FIELD** — `healthRecord`: `Code`, `Fingerprint`, `CooldownUntil` (`health_ledger.go:19-24`, `140-155`).
3. **STATES** — **Absent** (key missing → healthy for ordering); **`ok`** (clears cooldown); **`fallback`** (sets `CooldownUntil = now+300s`); **`terminal`** (preserves prior `CooldownUntil`, `health_ledger.go:150-153`).
4. **TRANSITIONS** — `Record` switch on `code` (`health_ledger.go:145-155`); fingerprint from FNV-32a of `err.Error()` at chain (`chain.go:127-133`).
5. **TERMINAL** — Records persist until overwritten; cooldown expires logically in `Order`/`inCooldownLocked`.
6. **IMPLICITNESS** — `Code` values mirror chain class strings; not a Go enum.
7. **HAZARD** — `Record` does not auto-`Save()`—callers must persist or reorder is RAM-only until `Load`/`Save` pairing; crash loses demotion.

### SM-20 — Health ledger backend ordering (healthy vs cooling)

1. **NAME** — Demote backends in cooldown to tail of try order.
2. **STATE FIELD** — `inCooldownLocked` + partition in `Order` (`health_ledger.go:100-109`, `118-129`).
3. **STATES** — **Healthy** (not in cooldown); **Cooling** (`now.Before(CooldownUntil)`); **Cooldown parse failure** → treated as not cooling (`health_ledger.go:105-107`).
4. **TRANSITIONS** — Each `Order` call recomputes from `records` + clock (`health_ledger.go:113-129`).
5. **TERMINAL** — Order is ephemeral per call.
6. **IMPLICITNESS** — Comment `health_ledger.go:112`.
7. **HAZARD** — Bad `CooldownUntil` string silently restores backend to head—repeat hammering on flaky backend.

### SM-21 — Health ledger snapshot load (empty vs populated)

1. **NAME** — On-disk `reach_backend_health.json` hydration.
2. **STATE FIELD** — `HealthLedger.records map[string]healthRecord` (`health_ledger.go:31-34`).
3. **STATES** — **Missing file** → empty map (`health_ledger.go:63-65`); **loaded**; **corrupt JSON** → error, no partial state guaranteed.
4. **TRANSITIONS** — `Load`/`Save` with atomic write (`health_ledger.go:57-77`, `87-93`, `158-169`).
5. **TERMINAL** — File is source of truth between runs.
6. **IMPLICITNESS** — Missing file doc comment (`health_ledger.go:56`).
7. **HAZARD** — Concurrent processes writing same path without external locking → last writer wins on `Save`.

### SM-22 — `OwnedPaths` registration ledger

1. **NAME** — Allowlist for reach-managed filesystem cleanup.
2. **STATE FIELD** — `paths map[string]struct{}` (`owned_paths.go:29`, `73`).
3. **STATES** — **Unregistered**; **Registered**; removal requires membership (`owned_paths.go:130-131`).
4. **TRANSITIONS** — `RegisterOwnedPath` → save (`owned_paths.go:110-119`); `RemoveRegistered` → lstat, refuse symlink, delete file, drop key (`owned_paths.go:122-144`).
5. **TERMINAL** — Deleted path removed from map.
6. **IMPLICITNESS** — Errors `ErrPathNotRegistered`, `ErrSymlinkRefused` (`owned_paths.go:15-19`).
7. **HAZARD** — Register without later `RemoveRegistered` leaves stale entries—no TTL or GC.

---

## E. Provider conformance & live policy (`g8s providers conformance`)

### SM-23 — `LivePolicy` + `LiveDecision` gate

1. **NAME** — Whether live quota-consuming probes may run.
2. **STATE FIELD** — `LivePolicy` (`policy.go:8-18`); `LiveDecision.Allowed`, `.Reason` (`policy.go:46-50`).
3. **STATES** — Policies `free`, `paid`, `forbidden`; decisions allowed vs refused with reason codes (`E_LIVE_FORBIDDEN`, `E_PAID_NOT_ALLOWED`, `E_PAID_MODEL_NOT_ALLOWED`, `policy.go:56-87`).
4. **TRANSITIONS** — `DecideLive(name, policy, model, allowPaid)` (`policy.go:56-89`).
5. **TERMINAL** — Per probe run invocation.
6. **IMPLICITNESS** — Partially documented in comments; agy forbidden is unconditional in callers.
7. **HAZARD** — `PolicyFree` with wrong 9router model blocks live without elevating static probes to FAIL—matrix stays `UNVERIFIED`.

### SM-24 — Conformance probe verdict (`Status`)

1. **NAME** — Per-probe PASS/FAIL/UNSUPPORTED/UNVERIFIED.
2. **STATE FIELD** — `ProbeResult.Status` (`types.go:25-38`, `128`).
3. **STATES** — `PASS`, `FAIL`, `UNSUPPORTED`, `UNVERIFIED` (documented consts).
4. **TRANSITIONS** — `newResult`, `staticVerdict`, live runners (`types.go:254-269`; `cli.go` probe functions).
5. **TERMINAL** — Frozen in `Matrix` snapshot.
6. **IMPLICITNESS** — Documented in `types.go`.
7. **HAZARD** — Required probe FAIL vs UNVERIFIED treated differently in dispatch gates—ops may misread “unverified” as safe.

### SM-25 — `ProbeCLI` run phase (static → optional live)

1. **NAME** — CLI conformance pipeline for one provider.
2. **STATE FIELD** — `ProviderReport.Mode` `static`|`live` (`cli.go:217-221`, `282-299`); `rep.Available` bool (`cli.go:258`).
3. **STATES** — Binary missing (all UNVERIFIED); P0 fail (downstream skipped); static complete; live blocked by `DecideLive`; live running (`rep.Mode="live"`, `cli.go:299`).
4. **TRANSITIONS** — `resolveBinary` fail (`cli.go:231-238`); P0 (`cli.go:242-256`); `!opts.Live` return (`cli.go:282-284`); `!dec.Allowed` annotate (`cli.go:287-297`).
5. **TERMINAL** — Returned `ProviderReport`.
6. **IMPLICITNESS** — Mode string only.
7. **HAZARD** — Live refusal only upgrades reasons on still-`UNVERIFIED` probes (`cli.go:291-294`)—existing static PASS not reverted.

### SM-26 — Conformance subprocess `execResult` (incl. timeout kill)

1. **NAME** — Probe subprocess lifecycle inside conformance.
2. **STATE FIELD** — `TimedOut bool`, `ExitCode`, `StartErr` (`exec.go:17-25`, `58-80`).
3. **STATES** — Start failed; running; completed; **timed out** (group kill, `exit -1`, `exec.go:78-79`).
4. **TRANSITIONS** — `runCmd` select on `done` vs ctx (`exec.go:56-66`); kill group on timeout (`exec.go:59-64`).
5. **TERMINAL** — Result struct returned to probe logic.
6. **IMPLICITNESS** — Implicit via `TimedOut` flag.
7. **HAZARD** — Kill failure falls back to single `Process.Kill` (`exec.go:60-63`)—orphan children possible on partial PGID support.

---

## F. Service / daemon lifecycle (`g8s service` + `internal/service`)

### SM-27 — macOS `Manager` launchd load state

1. **NAME** — Whether LaunchAgent is loaded in user domain.
2. **STATE FIELD** — Derived `isLoaded()` via `launchctl print` (`service.go:267-270`); exposed as `ServiceStatus.Loaded` (`service.go:407-408`).
3. **STATES** — **Not loaded** (print fails); **Loaded** (print succeeds).
4. **TRANSITIONS** — `Install`: bootout if wasLoaded, write plist, bootstrap, kickstart (`service.go:346-361`); `Start` kickstart (`service.go:366-373`); `Stop` bootout (`service.go:377-384`); `Uninstall` bootout + remove plist (`service.go:430-435`).
5. **TERMINAL** — Uninstalled: plist absent; DB intentionally retained (`service.go:428-429`).
6. **IMPLICITNESS** — Inferred from launchctl errors.
7. **HAZARD** — `Install` bootstrap failure restores plist but may leave launchd in ambiguous state if bootout succeeded (`service.go:355-357`).

### SM-28 — Service install maintenance window

1. **NAME** — Control-plane maintenance guard during install.
2. **STATE FIELD** — `LifecycleGuard.BeginMaintenance` / `EndMaintenance` (`service.go:336-344`).
3. **STATES** — **Open window** (defer end); implicit refusal if active leased/running tasks (`service.go:273-288`).
4. **TRANSITIONS** — `refuseActiveWork` before install (`service.go:308-310`); maintenance on install only when `guard!=nil`.
5. **TERMINAL** — Window ended in defer (errors swallowed, `service.go:340-343`).
6. **IMPLICITNESS** — Coupled to controlplane states outside this package.
7. **HAZARD** — `cmd/g8s` passes `nil` guard (`main_ops_worker.go:230-233`)—production install skips active-work refusal and maintenance.

### SM-29 — `ServiceStatus` database presence / validity

1. **NAME** — Read-only DB sanity for status JSON.
2. **STATE FIELD** — `DatabaseExists bool` (`service.go:81-82`, `409-423`).
3. **STATES** — Missing; present+valid SQLite header; **corrupt** → Status error.
4. **TRANSITIONS** — `Status()` stat + header read (`service.go:409-423`).
5. **TERMINAL** — Observational only.
6. **IMPLICITNESS** — `sqliteHeader` constant (`service.go:22-23`).
7. **HAZARD** — Status never creates DB—operator may think service broken when DB simply never initialized.

### SM-30 — Linux `SystemdManager` active state

1. **NAME** — User systemd unit running or not.
2. **STATE FIELD** — `is-active` output → `ServiceStatus.Loaded` (`systemd.go:135-147`).
3. **STATES** — **inactive** (`Loaded=false`); **active** (`Loaded=true`).
4. **TRANSITIONS** — `Install` write unit + daemon-reload (`systemd.go:86-105`); `Start` enable--now (`systemd.go:108-114`); `Stop` (`systemd.go:117-123`); `Uninstall` disable--now + remove (`systemd.go:126-131`).
5. **TERMINAL** — Unit file removed on uninstall (best-effort stop).
6. **IMPLICITNESS** — `Loaded` conflates “installed” with “active”.
7. **HAZARD** — Installed but failed unit reports `Loaded=false`—indistinguishable from not installed without separate `is-enabled` check.

### SM-31 — Platform factory backend selection

1. **NAME** — OS-specific service manager implementation.
2. **STATE FIELD** — `runtime.GOOS` switch (`factory.go:15-31`).
3. **STATES** — darwin → `Manager`; linux → `SystemdManager`; windows → `WindowsServiceManager`; **unsupported** error.
4. **TRANSITIONS** — `NewPlatformServiceManager` (`factory.go:8-31`).
5. **TERMINAL** — Fixed at construction.
6. **IMPLICITNESS** — Comment on darwin MVP in `NewManager` (`service.go:125-126`) vs factory supporting linux/windows.
7. **HAZARD** — `NewManager` still errors on non-darwin even though factory routes linux—dual entrypoints confuse which API is canonical.

### SM-32 — CLI `runService` command dispatch

1. **NAME** — User-facing service subcommand state outcomes.
2. **STATE FIELD** — JSON `status` strings in envelopes (`main_ops_worker.go:244-277`).
3. **STATES** — `installed`, `started`, `stopped`, `uninstalled`; `status` subcmd returns `ServiceStatus` struct (`main_ops_worker.go:283-294`).
4. **TRANSITIONS** — Switch on `subcmd` (`main_ops_worker.go:238-297`).
5. **TERMINAL** — Each invocation completes with success envelope or `exitRuntime`.
6. **IMPLICITNESS** — CLI strings only.
7. **HAZARD** — `status` on non-macOS may use systemd `Loaded` semantics while help text is generic—cross-platform operator confusion.

---

## G. Binary update / install / uninstall lifecycle

### SM-33 — `Updater` check vs apply outcome (`UpdateResult`)

1. **NAME** — Self-update resolution and apply feedback.
2. **STATE FIELD** — `UpdateResult`: `CheckOnly`, `UpdateAvailable`, `Applied`, `RolledBack` (`update.go:71-87`, `403-430`, `436-516`).
3. **STATES** — Check-only; up-to-date; update available; applied; smoke-failed rolled back (`update.go:505-513`).
4. **TRANSITIONS** — `Check` sets flags (`update.go:409-429`); `Apply` early exit if !available && !force (`update.go:442-444`); verify chain; `ReplaceBinary`; optional `Smoke` + `Rollback` (`update.go:470-515`).
5. **TERMINAL** — Successful apply leaves new binary; rollback restores backup.
6. **IMPLICITNESS** — Struct JSON tags document fields; rollback is separate bool.
7. **HAZARD** — Checksum/verify failure before replace aborts, but partial download still consumed time/bandwidth—no resume state.

### SM-34 — Binary replace / rollback (POSIX vs Windows)

1. **NAME** — Atomic swap of running executable.
2. **STATE FIELD** — Temp staged file, `backup` path (`update.go:519-600`, `603-620`).
3. **STATES** — Staged tmp; backup created; target replaced; **rollback** restores backup; Windows `.failed` aside (`update.go:608-616`).
4. **TRANSITIONS** — `ReplaceBinary` platform branches (`update.go:575-600`); `Rollback` (`update.go:603-620`).
5. **TERMINAL** — Stable binary path or restored backup.
6. **IMPLICITNESS** — Comments document OS differences (`update.go:526-529`).
7. **HAZARD** — POSIX rename failure after backup link leaves tmp orphaned and target unchanged—operator may have `.bak` without knowing active binary version.

### SM-35 — CLI `g8s update` mode (`--check` vs apply)

1. **NAME** — Command-level update invocation.
2. **STATE FIELD** — `checkOnly` flag → `u.Check` vs `u.Apply` (`lifecycle.go:46-77`).
3. **STATES** — Check-only envelope; apply with `Applied` / `UpdateAvailable` messaging (`lifecycle.go:88-100`).
4. **TRANSITIONS** — Flag parse → updater call → JSON or human output (`lifecycle.go:69-100`).
5. **TERMINAL** — Process exit via `exitRuntime` on hard errors (`lifecycle.go:78-79`).
6. **IMPLICITNESS** — Help text on flags only.
7. **HAZARD** — `force` only in apply path—check cannot detect “reinstall same version” intent.

### SM-36 — Uninstall plan vs apply (`UninstallReport`)

1. **NAME** — Dry-run plan then optional destructive apply.
2. **STATE FIELD** — `DryRun bool`; per-item `Action` (`uninstall.go:23-28`, `77-83`).
3. **STATES** — Plan: `would_remove`; apply: `removed` | `failed` | kept (`skipped` in `Kept`, `uninstall.go:163-165`); default CLI = plan unless `--yes` (`lifecycle.go:119-147`).
4. **TRANSITIONS** — `PlanUninstall` (`uninstall.go:159-220`); `ApplyUninstall` per kind (`uninstall.go:312-367`); CLI `runUninstall` (`lifecycle.go:143-163`).
5. **TERMINAL** — Partial failures joined in return error (`uninstall.go:367`).
6. **IMPLICITNESS** — Action constants documented (`uninstall.go:22-28`).
7. **HAZARD** — Windows binary rename to `.uninstall-pending` leaves runnable exe until user exits—plan may say removed while process still live (`uninstall.go:345-351`).

### SM-37 — Uninstall item kind pipeline order

1. **NAME** — Ordered removal: service → MCP → state → backup → binary.
2. **STATE FIELD** — `KindService`, `KindMCPEntry`, `KindStateDir`, `KindBackup`, `KindBinary` (`uninstall.go:14-20`); plan build order (`uninstall.go:170-218`).
3. **STATES** — Each item **planned** or **kept** with reason (package-managed, git worktree, etc., `uninstall.go:203-208`).
4. **TRANSITIONS** — `ApplyUninstall` switch (`uninstall.go:325-357`).
5. **TERMINAL** — Items recorded in report even on failure.
6. **IMPLICITNESS** — Comment on apply order (`uninstall.go:13`).
7. **HAZARD** — Service removed before binary while daemon still running could respawn from old unit on some platforms if stop fails silently in hook.

### SM-38 — `serviceHook` in uninstall integration

1. **NAME** — Bridge from lifecycle uninstall to platform service removal.
2. **STATE FIELD** — `Detect()` bool + path (`lifecycle.go:197-216`); `Uninstall()` delegates (`lifecycle.go:219-233`).
3. **STATES** — Unit **present** / **absent** per OS paths or `sc query` on Windows.
4. **TRANSITIONS** — Used in `PlanUninstall` when `opts.Service!=nil` (`uninstall.go:170-173`).
5. **TERMINAL** — After `Uninstall`, unit should be gone; DB retained by service package policy.
6. **IMPLICITNESS** — Interface doc (`uninstall.go:39-47`).
7. **HAZARD** — `Detect` on Windows uses `sc query` but `Uninstall` builds full `PlatformServiceManager`—mismatch if service name differs from `g8s` label.

---

## H. `cmd/g8s` provider CLI surfaces

### SM-39 — `runProviders` top-level router

1. **NAME** — Default list vs subcommands discover/scorecard/evaluate/conformance.
2. **STATE FIELD** — `args[0]` switch (`main.go:348-361`).
3. **STATES** — List path; subcommand delegates; unknown args fall through to list flag parse.
4. **TRANSITIONS** — Switch (`main.go:348-361`); list builds `NewRegistry` + optional HTTP providers from `providers.json` (`main.go:372-391`).
5. **TERMINAL** — Single-shot CLI exit.
6. **IMPLICITNESS** — Comment only (`main.go:345-346`).
7. **HAZARD** — Malformed `providers.json` silently ignored on list (`loadErr` not surfaced, `main.go:383-388`)—HTTP providers missing from view without error.

### SM-40 — `providers.json` hydration for api_call providers

1. **NAME** — Runtime registration of HTTP providers (9router, etc.).
2. **STATE FIELD** — File existence + `entry.Class == "api_call"` (`main.go:381-388`; parallel in `runProvidersDiscover` `main.go:437-441`).
3. **STATES** — Absent file; present with entries; entries ignored if not `api_call`.
4. **TRANSITIONS** — Stat + `config.Load` + `reg.Register(NewHTTPProviderFromConfig)` (`main.go:381-388`).
5. **TERMINAL** — In-memory registry for process.
6. **IMPLICITNESS** — Env `G8S_PROVIDERS` path resolution (`main.go:374-379`).
7. **HAZARD** — Pool registry in MCP server uses `LoadProvidersJSON` with class mismatch errors (`provider.go:332-334`) while CLI list uses separate `Registry`—two discovery models can disagree.

### SM-41 — `runProvidersDiscover` target selection

1. **NAME** — Model discovery for cursor + configured api_call providers.
2. **STATE FIELD** — `targetEntries []config.ProviderEntry` built from flags/config (`main.go:444-476` area).
3. **STATES** — Single provider pin; all api_call from config; hard fail if not found (`main.go:464` per grep context).
4. **TRANSITIONS** — Flag `--provider` filters; default includes cursor + entries (`main.go:417+`).
5. **TERMINAL** — JSON or table output per provider.
6. **IMPLICITNESS** — Usage string in `exitUsage`.
7. **HAZARD** — Missing providers.json falls back to minimal 9router assumptions in conformance defaults, not necessarily in discover—operator may see empty discovery.

### SM-42 — MCP server `PoolRegistry` boot

1. **NAME** — Long-lived daemon registry with merged providers.json.
2. **STATE FIELD** — `registry := NewPoolRegistry(...); LoadProvidersJSON` (`main.go:588-598`).
3. **STATES** — DefaultConfigs only; merged config states per `LoadProvidersJSON` rules (`provider.go:312-367`).
4. **TRANSITIONS** — Stat providers path → load (`main.go:595-598`).
5. **TERMINAL** — Registry lives for `ServeStdio` lifetime.
6. **IMPLICITNESS** — `_ = registry.LoadProvidersJSON` ignores error (`main.go:597`).
7. **HAZARD** — Silent load failure leaves stale built-in fleet—9router override from JSON may never apply.

### SM-43 — Version / build provenance (`version` + update hint)

1. **NAME** — Running binary identity chain (ldflags → VCS → go-install → fallback).
2. **STATE FIELD** — `BuildSource` string (`version.go:126-186` region).
3. **STATES** — `go-install`, `local-untracked`, release-stamped, etc.; optional `--check-update` → `UpdateInfo` (`version.go:255-327`).
4. **TRANSITIONS** — `runVersion` flags (`version.go:255+`).
5. **TERMINAL** — Read-only except network check.
6. **IMPLICITNESS** — Comments in `version.go`.
7. **HAZARD** — Update check failure is warning-only in human mode—automation using non-JSON may miss `UpdateAvailable`.

---

## Summary count

**43 distinct implicit state machines / lifecycles** enumerated above across:

| Area | IDs |
|------|-----|
| Handle & receipts | SM-01 – SM-06 |
| Pool / leases | SM-07 – SM-12 |
| Refusal | SM-13 – SM-15 |
| Reach | SM-16 – SM-22 |
| Conformance | SM-23 – SM-26 |
| Service daemon | SM-27 – SM-32 |
| Update / uninstall | SM-33 – SM-38 |
| cmd/g8s provider CLI | SM-39 – SM-43 |

**Cross-cutting hazards (highest severity):**

1. **Dual registry models** (`provider.Registry` for CLI list vs `PoolRegistry` for MCP/worker) with different merge/error behavior (SM-40, SM-42).
2. **Health ledger persistence** not wired inside `Record`—fallback demotion may not survive process boundaries unless an external caller `Save()`s (SM-19).
3. **Service install on macOS without `LifecycleGuard`** skips active-task refusal (SM-28).
4. **Stale `PoolRegistry.last` health** without periodic re-probe (SM-07).
5. **Refusal classification asymmetry** (codex vs generic CLI vs reach `Classify`) (SM-13, SM-14, SM-17).

---

*Scout complete. Read-only; no files written.*


---

*Provenance: scouts via g8s dispatch (cursor/composer-2.5, receipts), F0 synthesis and gap verification. Generated 2026-11-08.*
