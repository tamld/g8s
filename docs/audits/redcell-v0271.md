# Red Cell D-01 Adversarial Audit — v0.27.1 Release Gate

- **Scope**: new surface merged in the 2026-11-20/21 compliance wave (v0.27.0) at `audit/redcell-v0271` (HEAD = origin/main `4d9dd54`, release 0.27.0), plus the `fix/988-989-envelope-honesty` branch (`357ca01`) reviewed as a diff against its merge base (`4d9dd54`).
- **Method**: read-only code audit of the merged code; every finding classified exploitable / theoretical / by-design with file:line evidence and a red-test PROPOSAL (spec only, none implemented). No GitHub issues/PRs/CHANGELOG consulted. The `#926` dogfood-ledger diagnostic was verified empirically by running the real classifier over the real `origin/main` history.
- **Verdict**: **FIX-FIRST** for v0.27.1 (no critical exploit found on the new surface; the release gate is blocked by the #926 ledger regression, plus a medium cluster of cheap hardenings — see Executive verdict).

Finding tally: **0 critical / 0 high / 6 medium / 7 low** (+ #926 root cause, fixed-class = medium).

---

## Surface 1 — `RefuseClaim` (internal/controlplane/store_claims.go)

Mechanism recap: `RefuseClaim` (store_claims.go:42-82) CAS-releases a LEASED claim back to QUEUED **restoring the attempt budget** (`attempts = CASE WHEN attempts > 0 THEN attempts - 1 ...`, line 61) and defers re-claim visibility by setting `request_json $.not_before` in the same statement (line 63). The only production caller passes the hard-coded `claimRefusalDeferSeconds = 60` (worker.go:159, worker.go:772); `RefuseClaim` is **not** exposed over the HTTP API.

### F1.1 — Unbounded refusal loop: a task can be kept alive forever (medium, exploitable under a hostile/hostile-configured fleet)

- **Evidence**: store_claims.go:61 restores the attempt the claim just consumed; the claim eligibility filter only requires `attempts < max_attempts` (store_claims.go:146, 262). Nothing counts refusals: no refusal counter on the row, no cap on consecutive `task_claim_refused` events, no terminal transition after K refusals.
- **Scenario**: a worker (or fleet of workers) whose execution context permanently cannot honor a task requirement claims the task every 60 s and refuses it. Attempts oscillate `max-1 ↔ max` forever; the task never reaches FAILED. Two concrete harms: (a) **immortal QUEUED tasks** — a task whose requirement no fleet member can ever satisfy is never terminally resolved and permanently occupies its queue slot ordering; (b) **unbounded `task_claim_refused` + `task_claimed` event growth** in the DB (2 events per 60 s per poisoned task, forever). Rate is bounded by `not_before` (1 refusal/task/60 s across all workers) but the total is unbounded.
- **Classification**: exploitable (requires an incapable-but-persistent worker, which is exactly the population the fail-before-consume mechanism exists for; any permanent misconfiguration such as a worker launched from `cwd=/` via launchd against a queue full of `ephemeral read_only` tasks reproduces it without any malice).
- **Red-test proposal**: submit a task, refuse it N (e.g. 50) consecutive times via the worker refusal path; assert either (a) the task transitions to a terminal state after K consecutive refusals, or (b) the task row carries a mechanical `refusal_count` that the test can assert is monotonically increasing and that gates a terminal fail. Also assert the `task_claim_refused` event count for one task ID is capped or provably bounded.

### F1.2 — Concurrent RefuseClaim / ClaimTask / reconcile races: no bypass found (by-design, verified)

- **Evidence**: the store DSN pins `_txlock=immediate` (store.go:72), so every transaction — refuse, claim, reconcile, finish — serializes at BEGIN; `RefuseClaim` CASes on `lease_token = ? AND state = 'LEASED'` (store_claims.go:64) and reconcileExpiredTx requeues by the same token CAS (store_claims.go:593-598) without touching `attempts`.
- **Scenarios checked**: (1) refuse vs. refuse on the same lease — second gets `affected != 1` → `ErrClaimNotHeld`, no double attempt-restore; (2) refuse vs. lease-expiry reconcile — whoever holds the token wins, loser is a benign no-op; (3) refuse vs. claim — the LEASED→QUEUED transition and `not_before` write are one statement, so there is no observable QUEUED-without-deferral state; (4) refuse vs. CancelTask — a LEASED task is cancelled terminally (lifecycle.go:648-653), the subsequent refuse CAS fails, no cancelled-task resurrection; RUNNING tasks get flag-only cancellation but cannot be refused (`state='LEASED'` gate).
- **Classification**: by-design / sound. One residual asymmetry worth a verifier: reconcileExpiredTx does NOT decrement attempts on requeue (correct — attempt consumed), while RefuseClaim does (fail-before-consume). A red-test pinning this asymmetry prevents a future "helpful" decrement in reconcile from accidentally coupling the two paths.
- **Red-test proposal**: table-driven race test interleaving RefuseClaim/ReconcileExpired/FinishAttempt/CancelTask on one lease with randomized order; invariant: at most one attempt-restore ever applies per claimed attempt, and a cancelled task can never return to QUEUED via refusal.

### F1.3 — `deferSeconds` and code/reason reach the store unvalidated (low, latent API hazard)

- **Evidence**: store_claims.go:42-50 honors any `deferSeconds` (≤ 0 ⇒ `not_before = 0` ⇒ immediately claimable, defeating the deferral) and stores `code`/`reason` verbatim (lines 62-63, 72-76) with no length cap. Today only the internal caller passes `60`, so this is latent — but `FinishAttempt`-style APIs have historically grown HTTP surfaces; a future `POST /api/v1/tasks/:id/refuse` would let a hostile worker zero the deferral and spam unbounded `last_error`/event payloads.
- **Classification**: theoretical (latent).
- **Red-test proposal**: store-level unit test asserting `RefuseClaim` clamps `deferSeconds` to a floor (e.g. ≥ 1 s, or ≥ some minimum when caller passes ≤ 0) and rejects/wraps `code`/`reason` above a length cap; the test must fail while the current signature trusts the caller.

---

## Surface 2 — `trust_root.go` nil NonceStore dry-run + cross-process nonce lock

### F2.1 — Dry-run authorization is indistinguishable from a real Tier-B authorization in the decision struct (low, latent API hazard — no current confusion path)

- **Evidence**: `EvaluateTrustRootMutation` with `NonceStore == nil && DryRun` returns `Allowed: true, EffectiveTier: TierBS0HumanTCB, TouchesTCB: true` (trust_root.go:823-833) — the identical field shape as a real authorization (lines 842-849). The only discriminator is the `Reason` prose ("DRY-RUN planning decision…"); `TrustRootDecision` has **no `DryRun` field**.
- **Scenario**: a future caller serializes the decision (the struct is JSON-tagged) or reuses `dec.Allowed` across a dry-run→apply boundary; a dry-run planning verdict then authorizes a real TCB mutation. Today this is unreachable: the sole real consumer `enforceDeliverTrustRootGate` always constructs a `FileS0NonceStore` (deliver.go:1051-1067), so the dry-run runs `CheckAndConsume(consume=false)` (check-without-burn) on a real store, and `deliver` re-evaluates the gate fresh on the real run (deliver.go:460 vs the apply path gated by `!*dryRun` at deliver.go:592) — no decision reuse. The doctor contract probes (trust_assurance.go:393-428) deny before reaching the nonce step.
- **Classification**: theoretical (defense-in-depth gap at the type level).
- **Red-test proposal**: add `DryRun bool` to `TrustRootDecision`, set it on every return, and specify a verifier that (a) asserts `Allowed==true ⇒ (DryRun==false || reason-prefix=="DRY-RUN")`, and (b) rejects any consumer that persists a decision with `DryRun==true` (serialization snapshot test).

### F2.2 — The `.lock` sibling can be symlinked: fail-closed DoS only (low)

- **Evidence**: `acquireNonceLock` (trust_root.go:219-231) locks `<noncePath>.lock` via `lockfile.TryLock` = `os.OpenFile(O_CREATE|O_RDWR, 0600)` + `flock(LOCK_EX|LOCK_NB)` (lockfile_unix.go:22-45). No `O_NOFOLLOW` and no `O_EXCL`: a pre-planted symlink makes the flock land on the **target** inode.
- **Scenario**: an attacker with write access to the operator state dir (0700, same trust domain — so effectively a same-user attacker) symlinks `s0-used-nonces.json.lock` → any file. Outcomes: (a) target is a writable regular file — the lock is taken on it, **content is never written and no truncation occurs** (the lockfile contract is "content is irrelevant", lockfile.go header), so no integrity damage; (b) target is a directory/read-only/missing-parent — `OpenFile` fails, `acquireNonceLock` burns the 100×50 ms retry window then errors → `CheckAndConsume` fails → Tier-B application **fail-closed** (availability-only DoS, ~5 s per attempt, plus permanent DoS if the attacker holds a flock on the file forever). Direction of failure is always deny.
- **Classification**: theoretical (requires same-user state-dir access, which already implies game over for the nonce registry itself — an attacker who can plant the symlink can also truncate `s0-used-nonces.json` directly, which merely fail-closes or, if cleared, enables replay within an already-compromised host).
- **Red-test proposal**: verification test (not attack): create the `.lock` path as a symlink to (1) a regular file and (2) a directory; assert (1) the nonce file content is byte-identical before/after a consume and (2) the consume errors closed with a lock error and no partial state was written. Optionally add `O_NOFOLLOW` (unix) to `lockfile.TryLock` and pin the test to that.

### F2.3 — Nonce store read-modify-write is correctly locked cross-process (by-design, verified)

- **Evidence**: the lock spans read-check-commit inside `FileS0NonceStore.CheckAndConsume` (trust_root.go:144-152), the temp-file + `os.Rename` commit is atomic (lines 191-211), and `consume=false` dry-run checks take the same lock. Two coordinators sharing one state dir serialize; the fail-closed timeout was the intended fix for #972. Note the contrast with the telemetry outbox (F4.3), which got no such lock.

---

## Surface 3 — Receipt dual-handle design (internal/receipt/receipt.go)

### F3.1 — Consume (FULL handle) vs issue/purge (NORMAL pool) consistency: no exploitable window (by-design, verified)

- **Evidence**: both handles target the same WAL database with `_txlock=immediate` + `busy_timeout(30000)` (receipt.go:314, 337); consume runs in a CAS transaction (`WHERE consumed = 0`, receipt.go:1080-1088) under `m.mu` with `MaxOpenConns(1)` on the consume handle (receipt.go:346); `PurgeExpired` deletes only `consumed = 1 OR expires_at < now` rows (receipt.go:1512-1522); `RevokeReceipt` deletes only `consumed = 0` rows (receipt.go:1115). Cross-handle ordering is therefore a strict one-of-two: whichever write commits first wins the CAS, the loser gets `AlreadyConsumedError` / `NotFoundError` / `affected==0`.
- **Key durability property that makes the NORMAL/FULL split sound**: WAL frames are appended sequentially; a consume commit fsynced by the FULL handle necessarily flushes all *earlier* issue-commit frames in the same WAL. So "consume acknowledged, then power loss rolls back the issue" — the dangerous direction — is impossible; the acknowledged-consume-lost window (#984 C-2) is closed exactly as claimed. The surviving NORMAL-path loss ("issue lost ⇒ consume returns NotFound") fails closed.
- **Classification**: by-design / sound.
- **Red-test proposal** (verification): crash-injection test asserting (a) after an acknowledged `ValidateAndConsume`, a simulated crash before WAL checkpoint always replays the receipt as consumed; (b) concurrent consume/revoke/purge fuzz over both handles maintains exactly-one-winner; (c) `verifyConsumeSyncPragma` rejects a handle whose pragma silently downgrades.

### F3.2 — In-memory alias detection edge cases (low, theoretical)

- **Evidence**: the alias branch keys on `signing.IsInMemorySQLitePath` = exact `:memory:` or substring `mode=memory` (ed25519.go:45-50, receipt.go:331-336). A DSN like `file::memory:?cache=shared` matches **neither** → a second handle is opened. With `cache=shared` both handles still see the same database, so consume works and `verifyConsumeSyncPragma` passes; the unhandled worst case is a *private* memory DSN variant, where the second handle is an empty database → every consume returns `NotFoundError` — fail-closed denial, never a false success.
- **Classification**: theoretical; no bypass direction.
- **Red-test proposal**: parameterize `NewReceiptManager` tests over the DSN zoo (`:memory:`, `file::memory:?cache=shared`, `file:x?mode=memory&cache=shared`, disk) asserting issue→consume round-trips and that the alias decision is logged/explicit for each.

---

## Surface 4 — Telemetry flush retention + JSONL outbox (internal/telemetry/engine.go)

### F4.1 — Torn/corrupt outbox lines and replay duplication: handled (by-design, verified)

- **Evidence**: `readOutbox` skips empty and JSON-unparsable lines (torn final line tolerated, engine.go:511-535); replay is idempotent via `ON CONFLICT(id) DO UPDATE` upsert (engine.go:553-568); the file is removed only after a successful persist and a surviving file re-replays harmlessly (engine.go:385-391, 538-546); spill is bounded at `maxOutboxEvents = 10000` with oldest-first drop (engine.go:315-318, 489-494). The spill-then-replay recursion concern is a double read, not recursion (spill reads the outbox once; the file is not appended to during replay).
- **Red-test proposal** (verification): fuzz `readOutbox` with truncated/mid-object/oversized-line files asserting skip-not-fail and bounded memory; assert spill→crash→replay→clear round-trip loses nothing that survived the disk and duplicates nothing observable (same IDs).

### F4.2 — Outbox as injection surface (low, by-design trust domain)

- **Evidence**: `TraceEvent.Payload` is `map[string]any` marshaled by `encoding/json` (types.go:19, engine.go:607), so string values cannot smuggle raw newlines into the JSONL framing; only a caller planting a `json.RawMessage` (or similar raw-JSON value) containing literal newline bytes could split a line. Even then, the injected fake event can only upsert into `telemetry_events` with an attacker-chosen ID — and the outbox file lives next to the telemetry DB in the same state dir, so anyone able to write it can write the DB directly; the outbox adds no new privilege.
- **Classification**: by-design (same-trust-domain surface). Worth a hardening note: reject non-flat-JSON values (RawMessage) in `TraceEvent.Payload` at ingest.
- **Red-test proposal**: ingest an event whose payload contains a crafted raw-JSON value with embedded newlines; assert either the value is rejected at ingest or the serialized outbox line remains a single parseable event.

### F4.3 — Spill failure ⇒ unbounded in-memory retention; cross-process outbox races (medium, exploitable-adjacent / operational)

- **Evidence**: on persist failure with a full batch, `spillToOutbox` failure leaves the batch wholly in memory by explicit policy ("losing the memory bound is preferable to losing events", engine.go:372-380). If the outbox path is persistently unwritable (read-only FS, state-dir quota, or a hostile process replacing the outbox file with a directory — `os.WriteFile` on a dir fails every flush), the retained batch grows without any cap for the life of the process → worker memory exhaustion during a sustained outage. Additionally the outbox read-modify-write (`readOutbox` → rewrite whole file via `os.WriteFile`, engine.go:489-508) has **no cross-process lock** — two g8s sessions sharing a state dir can lose spilled events (last-writer-wins) or clear a file another process just wrote; contrast the nonce registry which got a lock in #972 (F2.3). A mid-rewrite crash can also truncate the entire file (all spilled history lost, not just the last line) — bounded by design but larger than the "torn final line" comment implies.
- **Classification**: medium for the memory-growth path (operational DoS reachable without an attacker: read-only state dir + sustained DB outage); low for the cross-process race (observational data loss only).
- **Red-test proposal**: (a) run the engine with a persistently failing spill target under sustained ingest; assert RSS / retained event count stays under a hard cap (e.g. `4×maxOutboxEvents`) and metrics surface `persist_failures`; (b) two-engine test sharing one DBPath with concurrent spill/flush asserting the file protocol either serializes (add a lockfile, as #972 did for nonces) or loses only whole-file contents, never partial lines.

---

## Surface 5 — CAPABILITY_DETECTED + ComputeNonCompensatoryVerdict (internal/doctor/trust_assurance.go)

### F5.1 — Veto coverage for the known enum is complete (by-design, verified)

- **Evidence**: `gateStatusRank` ranks CAPABILITY_DETECTED = UNVERIFIED = 1 (trust_assurance.go:106-114); both the canonical-gate loop and the non-canonical-gate loop veto CAPABILITY_DETECTED and UNVERIFIED (trust_assurance.go:188-196, 220-231); `overallVerdictFor` degrades CAPABILITY_DETECTED to UNVERIFIED at the overall level (lines 128-137); missing canonical gates veto fail-closed (lines 173-181). Doctor rendering is warning-tier yellow and prints the degraded overall verdict (cmd/g8s/doctor.go:201-209). The benchmark script ranks CAPABILITY_DETECTED as 1, includes it in `veto_reasons`, and its CAPABILITY_DETECTED→ENFORCED upgrade for TRUST-G1 is gated on the **live** as-001 escape probe having actually passed (tools/assurance_benchmark.sh:150, 190-200, 328) — evidence-supersedes-capability, not a bypass. The single producer of the status is the G1 fast path when only the sandbox policy contract was probed (trust_assurance.go:366-374); it is not attacker-controllable.
- **Classification**: by-design / sound for the current enum.
- **Red-test proposal** (verification): golden test enumerating all 5 statuses × canonical/non-canonical gate positions asserting verdict/veto/mediated triple; plus a benchmark-script fixture test asserting a doctor output with G1=CAPABILITY_DETECTED and a *failing* as-001 probe yields NOT_ENFORCED and veto.

### F5.2 — Unknown gate-status strings pass through `overallVerdictFor` verbatim **without a veto** (medium, latent — unreachable from current producers)

- **Evidence**: for a **non-canonical** gate carrying a status outside the enum (e.g. `"enforced"`, `"ENFORCED_PENDING"`, any garbage), `gateStatusRank` falls to the default `return 0` (trust_assurance.go:115-117) so `rank < minRank` ⇒ `verdict = overallVerdictFor(g.Status)` — which passes unknown strings through **unchanged** (lines 128-137: only CAPABILITY_DETECTED is rewritten) — and the non-canonical switch (lines 220-231) has no default arm, so **no veto and no vetoReason entry is recorded**. Result: a crafted gate array can produce `overall_verdict = "<garbage>", non_compensatory_veto = false` while a gate is not actually verified. Any consumer keying on `veto == false` alone (release assertions, dashboards) is bypassed; consumers comparing `overall_verdict == "ENFORCED"` are safe.
- **Reachability**: `ComputeNonCompensatoryVerdict`'s only internal caller is `EvaluateTrustAssurance`, whose statuses are compile-time constants — so this is not exploitable *today* through `g8s doctor`. It becomes exploitable the moment any consumer feeds externally-parsed `TrustGateAssessment` JSON into the function (the schema is a published JSON contract, `g8s.trust-assurance/v1`), or a future status typo (`"ENFORCED "` / lowercase) is introduced.
- **Classification**: latent / theoretical, medium because the failure direction is silent-pass.
- **Red-test proposal**: unit test feeding `ComputeNonCompensatoryVerdict` a non-canonical gate with status `"enforced"` (lowercase), `"ENFORCED "` (trailing space), and `"GARBAGE"`; assert the verdict never equals the input string, the verdict is UNVERIFIED-or-worse, and `veto == true` with a `E_TRUST_GATE_STATUS_UNKNOWN`-style reason. Fix direction: normalize (`EqualFold`+`TrimSpace`) into the enum and treat unknowns as NOT_ENFORCED-tier with veto.

---

## Surface 6 — Ops worker drain hot-loop (cmd/g8s/main_ops_worker.go + worker RunLoop)

### F6.1 — Refusal `continue` has no backoff; a continuous task stream turns the drain into claim/refuse churn (medium, exploitable by task-volume; per-task deferral works as designed)

- **Evidence**: the drain loop (main_ops_worker.go:706-742) treats `worker.ErrWorkerCwdNotGitRepo` as `continue` with no sleep (line 739). The 60 s deferral is per-task via `$.not_before`, so: (a) a **bounded** queue is safe — each pass refuses each task at most once, then claim returns nil ⇒ `queue drained` ⇒ process exits (main_ops_worker.go:743-752); the OS-level relaunch loop is launchd's business, re-throttled by `not_before`; (b) an **unbounded/continuous** stream of NEW task IDs (hostile submitter, or simply a busy coordinator) keeps every iteration claimable: each cycle costs 2 transactions + 2 events (`task_claimed` + `task_claim_refused`) with zero backoff, at full submit-rate speed, indefinitely.
- **Scenario**: non-git-cwd worker (launchd, cwd=/) + a submitter pumping tasks: infinite claim/refuse churn at submit rate; event table and `last_error` churn grow unboundedly; no CPU spin (claim blocks on DB) but sustained write amplification on the control-plane DB (synchronous=FULL).
- **Classification**: medium — reachable with two misconfigurations and no malice; the mechanism (per-task deferral) is correctly scoped, the missing piece is loop-level damping.
- **Red-test proposal**: drain-loop test with a scripted queue that yields N new claimable tasks after each refusal; assert the loop accumulates a refusal-streak counter and sleeps/exits after K consecutive refusals (loop-level damping independent of task identity), and that `task_claim_refused` events per wall-clock minute are bounded.

---

## Surface 7 — `stream.go` PipeTee reader-side `CloseWithError` + fsops 64 MiB stat-guards

### F7.1 — `CloseWithError` racing writers: sound ordering, bounded evidence truncation (by-design, verified)

- **Evidence**: the scanner goroutine closes the *read* end with the typed cause on scanner error (stream.go:96-103), unblocking pending/future `pw.Write`s — the tee writer fails fast instead of deadlocking the child on a full OS pipe. `teeWriteCloser.Close` closes the pipe writer **then** waits on the scanner (stream.go:118-122) — correct order; `io.MultiWriter(dst, pw)` writes dst first, so a pipe error after a successful dst write reports the error and stops further tee writes: captured output after an over-budget (> `dispatch.MaxCaptureBytes+1`, stream.go:16-20) line is truncated deliberately (#981 budget). The only residual nit: a `bufio.Scanner` `ErrTooLong` abort means the remainder of the oversized line is discarded mid-line — acceptable under the documented capture budget, and the stderr breadcrumb names the stream.
- **Red-test proposal** (verification): race test with a writer goroutine hammering the tee while the reader closes with error; assert every Write returns an error (never blocks forever), the dst file contains a byte-prefix of the stream, and `Wait()` returns.

### F7.2 — stat-guard → `os.ReadFile` TOCTOU: file can be swapped after the guard into an unbounded buffer (medium, exploitable inside the worker sandbox)

- **Evidence**: all three fsops read paths stat then separately read: fsops.go:439-447, 601-609, 866-873 (`os.Stat` → `guardReadFileBytes` (64 MiB cap, fsops.go:37, 46-52) → `os.ReadFile(canonTarget)`). The same pattern exists in the deliver path at the 512 MiB tier (`readDeliverableFile`, trust_root.go:492-502). Nothing pins the fd between stat and read: a file swapped (grown, ideally sparse) after the guard is read to EOF into RAM regardless of size.
- **Scenario**: a misbehaving model inside the worker sandbox creates a multi-TB sparse file via its own shell access, then invokes `g8s read`/edit on it — the guard passes (small at stat time or swapped post-stat), `os.ReadFile` buffers the whole expansion → OOM kill of the worker process. Blast radius is the worker itself (its own sandbox), but it defeats the whole point of the #981 guard and can take down the run dir/capture state with it. The deliver-path variant lets a hostile worker memory-spike the F0 deliver process between digest computation steps.
- **Classification**: exploitable (worker-sandbox-local DoS), medium.
- **Red-test proposal**: change the guard to a single-fd protocol — `os.Open` → `f.Stat` → bounded `io.LimitReader(f, max+1)` — and specify the red-test: a sparse file grown to >64 MiB between an exported stat hook and the read must yield `ErrFileTooLarge` (or a bounded read), never an unbounded allocation; mirror it for `readDeliverableFile` at 512 MiB.

---

## Surface 8 — `fix/988-989-envelope-honesty` (envelope_invariant.go + funnel + suspect heuristic)

Reviewed as the `357ca01` diff against merge base `4d9dd54`.

### F8.1 — Mechanical seal can DERIVE an attacker-chosen E_* code from untrusted text (medium, exploitable for taxonomy corruption)

- **Evidence**: `envelopeCodePattern = E_[A-Z0-9_]+` is unanchored and first-match-wins over `reason → error → summary` (envelope_invariant.go:22-24, 47-56); `sealFailureEnvelope` sets `env["code"]` from that extraction whenever the envelope lacks a code (lines 69-76), falling back to `E_WORKER_FAILURE_CODE_MISSING` only when *no* token exists. The `reason`/`error` fields routinely carry provider-CLI/model-controlled text (`errMsg = dispatch.SanitizeOutput(spawnErr.Error())`, worker.go diff at the spawn-failure branch; model output flows into collect-path envelopes). Sites that set `code` explicitly are immune (seal preserves existing fields verbatim, lines 66-68) — the exposure is any code-less ok=false envelope.
- **Scenarios**: (a) a provider CLI or model embeds a plausible-but-false token (e.g. `E_TASK_TIMEOUT`) in prose; the seal promotes it to the typed code, **mislabeling the failure taxonomy** and defeating the `E_WORKER_FAILURE_CODE_MISSING` fallback — the very signal that exists to detect untyped failure sites; (b) a model that fails mid-audit emits `ok=false` with reason "retried after E_PROVIDER_EXEC_FAILED was ruled out" — downstream supervisors route on the wrong code (e.g. treating a non-timeout failure as retryable-timeout; the separate `Retryable` flag limits but does not eliminate the misrouting).
- **Classification**: exploitable (taxonomy integrity, not auth) — medium.
- **Red-test proposal**: seal a code-less ok=false envelope whose reason contains `E_FAKE_CODE_9000` and `E_TASK_TIMEOUT` mid-prose; assert the derived code is either rejected (unknown vocabulary → fallback `E_WORKER_FAILURE_CODE_MISSING`) or the extractor is anchored (code must be the reason's leading token: `^E_[A-Z0-9_]+(\s|:|$)`), and add an allow-list check against the known E_* vocabulary.

### F8.2 — Minimal `{}` (and any backticked JSON fragment) satisfies `resultCarriesJSONBlock` — suspect heuristic is defeatable (medium, false-negative by construction)

- **Evidence**: `fencedJSONPattern = (?s)(?:```|`)(?:json)?\s*(\{.*?\})\s*(?:```|`)` (worker.go:468) matches a bare `` ```json {} ``` `` — or even `` `{"a":1}` `` inline in a doc line — and `suspectFastCompletion` clears the flag on any match (envelope_invariant.go:148-151, 153-170). The captured group is never parsed, never checked for non-emptiness or schema. The heuristic input is `wr.Response+"\n"+stdoutText` (worker_sweep_collect.go diff, collect default path), so *any* stdout line containing a fenced `{}` — including prompt echoes, banner JSON, or the model printing markdown examples — defeats the phantom-success flag while delivering zero contract work.
- **Scenario**: a phantom completion emits `ok=true` plus the literal line `` ```json {} ``` `` in stdout within 2 s; no suspect flag lands in telemetry, the result envelope, or `ModelOutcomeRecord.SuspectFastCompletion` — exactly the measurement blind spot #989 was written to close, reopened with three characters.
- **Classification**: exploitable (measurement/monitoring evasion); the flag is flag-never-fail so there is no direct security bypass, but the routing down-weighting promised in the scorecard change (internal/routing/scorecard.go diff) silently never fires.
- **Red-test proposal**: property test over `suspectFastCompletion` asserting a fenced `{}`, a fenced `` {"x":{}} ``, and an inline backticked `{}` in an otherwise-empty result do NOT clear the flag; fix direction: parse the captured group, require ≥1 non-empty key (or a minimum content floor), and scan only the *result payload section* of stdout rather than the concatenation.

### F8.3 — Funnel completeness and idempotency: single seal is idempotent; the store layer does not enforce the invariant (low-medium, by-design gap)

- **Evidence**: after `357ca01`, the worker package contains exactly one direct `s.cp.FinishAttempt` call — inside `finishAttempt` itself (envelope_invariant.go:143); the other 19 sites route through the funnel (14 in worker.go, 5 in worker_sweep_collect.go). `sealFailureEnvelope` is idempotent (existing `code`/`error` preserved verbatim; `mustJSON` re-marshals a `map[string]any`, and `encoding/json` sorts map keys deterministically). `ValidateResultEnvelope` post-seal failure only logs a stderr breadcrumb (envelope_invariant.go:111-117) — a violating envelope is **persisted anyway**; the snapshot backstop (worker_result.go diff) likewise warns, never fails. `controlplane.Store.FinishAttempt` (lifecycle.go:454+) enforces lease CAS and JSON canonicalization but **no envelope invariant** — any non-worker caller (store-level retry helper at lifecycle.go:582/598, future HTTP surface, F2 code paths) can persist untyped ok=false envelopes untouched, and the #988 invariant reduces to convention + breadcrumbs at the persistence boundary.
- **Classification**: by-design gap (low today — all in-repo callers funnel; medium trajectory as call sites grow).
- **Red-test proposal**: (a) CI reflection/lint test: no `cp.FinishAttempt` call in `internal/worker` outside `finishAttempt` (the property can be asserted mechanically today); (b) golden test that `sealFailureEnvelope(sealFailureEnvelope(x)) == sealFailureEnvelope(x)` for malformed/partial envelopes; (c) decide-and-pin the policy question: either `Store.FinishAttempt` gains an optional invariant validator (fail-closed at the store boundary) or the stderr breadcrumb is escalated to a `task_finish_envelope_violation` telemetry event so violations are observable, not just console noise.

### F8.4 — Typed-code nits (low)

- **Evidence**: `E_PROVIDER_EXEC_FAILED` is assigned to two distinct conditions — `sandbox.PolicyRejectionError` and `sandbox.ErrKernelSandboxRequired` (worker.go spawn-failure branch diff) — collapsing two routing-relevant causes into one code; `FinishAttemptParams.Err` (persisted to `last_error`) is **not** sealed, so `last_error` can remain code-less while the result envelope is compliant; `E_ENVELOPE_INVARIANT_VIOLATED` is defined but unreachable-by-design (breadcrumb only).
- **Red-test proposal**: vocabulary test asserting every emitted `(status, code)` pair maps 1:1 to a documented cause, and that `last_error` for ok=false finishes always begins with an E_* token.

---

## Dogfood ledger diagnostic (#926)

**Symptom**: the weekly dogfood audit fails with "unclassified honesty gap exceeds budget" (`tools/dogfood_provenance`, threshold `maxUnclassifiedPercent = 30`, tools/dogfood_provenance/main.go:18, 93-97, 135).

**Empirical measurement** (real classifier copied verbatim, run over `origin/main`):
- last 200 commits (`windowSize = 200`, main.go:15): **receipt=18, f0_direct=47, unclassified=135 → 67.5% unclassified** vs the 30% budget.
- last 30 commits: 20/30 unclassified (66.7%).

**Root cause — trailer-window arithmetic vs. the squash-merge footer convention (primary)**:
`ClassifyCommit` parses **only the final blank-line-separated paragraph** as the trailer block (internal/dogfood/ledger.go:151-159: `start := len(lines)-1; for start > 0 && lines[start-1] != "" { start-- }`, then `trailer := lines[start:]`). But the repo's squash-merge template places each `Provenance: F0-direct — …` trailer **mid-body** (per bullet section), followed by a blank line and a closing footer paragraph — `Closes #NNN`, `Refs #…`, `Part of #984`. The final paragraph is therefore the footer, not the trailer: `"Closes #982"` has no colon ⇒ `strings.Cut` fails ⇒ `return unknown` (ledger.go:166-169). Every multi-section squash commit is unclassified **despite carrying one or more perfectly valid `Provenance:` trailers**. Verified against real messages: e.g. the `#1012` release commit ends `…\n\nPart of the v0.27.0 release flow`; the `#1011` commit ends `…\n\nPart of #984`; six of the last ten commits show the `Provenance: …` / `Closes #…` split. The classifier is functioning as specified; the specification (`ClassifyCommit` doc comment, ledger.go:138-147, "reads the final blank-line-separated trailer paragraph") does not match the landed commit convention.

**Secondary contributors** (both observed in the failing window, correctly-unclassified but gap-inflating):
1. **Wrapped F0-direct reasons containing colons**: `unfoldTrailerLines` folds a continuation line only if it contains **no colon** (ledger.go:80-88); a wrapped reason whose continuation contains `:` (e.g. `release (461-family gap: manifest/tag consistency gates must fetch tags…`) becomes its own trailer line, fails `isValidTrailerKey` (ledger.go:167) and poisons the whole paragraph to unknown even when the Provenance line is the final paragraph.
2. **Non-grammar `Provenance:` variants**: e.g. `Provenance: worker-authored qua g8s worktree-parallel agent (…)` (in `#1011`) — `parseProvenanceValue` accepts only `F0-direct —`, `Receipt <id>`, `worker task <id>` (ledger.go:92-114) → unknown. Similarly `Receipt: operator directive 2026-11-20 (audit …)` (multi-token value fails `isValidReceiptID`, ledger.go:174-177). These are convention violations that *should* stay unclassified, but they currently count under the generic `subject-without-trailer` bucket, hiding the real cause mix (`UnclassifiedBreakdown`, ledger.go:219-234).

**Minimal fix proposal** (one change + one optional):
- **Primary** — internal/dogfood/ledger.go:151-163: replace the last-paragraph window with a **backward paragraph scan**: split the (already CRLF-normalized, trimmed) message into blank-line-separated paragraphs; walk from the end and select the **first paragraph (from the end) that contains at least one line whose colon-key is `receipt` or `provenance` (case-insensitive)**; classify that paragraph with the existing per-line rules unchanged; if no paragraph qualifies → unknown. All existing fail-closed properties are preserved: the chosen paragraph must still parse in full (every other line must be a valid trailer or a legal unfold continuation), prose paragraphs with colons still render unknown, and commits with no trailer lines anywhere remain unclassified. This re-classifies the dominant failure mode (footer-after-trailer) without weakening any guard.
- **Optional accuracy fix** — ledger.go:80-88: when the previous trailer line satisfies `provenanceAllowsF0DirectUnfold`, allow colon-bearing continuation lines to fold into the F0-direct reason (the no-colon rule exists to stop prose paragraphs masquerading as trailers; inside an already-identified F0-direct reason, free text is legitimate). And route `Provenance:` values that fail parsing to `classification_error` with a distinct code (e.g. `E_PROVENANCE_TAG_MALFORMED`) instead of generic `unclassified`, so the breakdown table distinguishes "footer after trailer" (fixed by the primary change) from genuine convention violations.
- **Convention-side companion** (no code): the footer (`Closes #NNN` / `Part of #NNN`) should be emitted **before** the Provenance trailer, or the trailer last — either way the ledger ADR should pin the order; the classifier fix above makes the ledger tolerant regardless.

---

## Executive verdict: FIX-FIRST for v0.27.1

**No critical or high-severity security exploit was found on the audited surface.** The race-sensitive designs (RefuseClaim CAS + immediate-txlock, dual-handle receipt consume, nonce cross-process lock) held up under attack; the durability direction of the NORMAL/FULL receipt split is provably safe (consume-ack ⇒ issue-durable via WAL ordering); the CAPABILITY_DETECTED veto is complete for the defined enum.

FIX-FIRST is driven by four cheap, well-scoped items, any of which is a one-day fix:

1. **#926 ledger fix (medium, release-blocking)**: the weekly dogfood audit gate is failing today at 67.5% unclassified vs a 30% budget, purely from the trailer-window/footer mismatch — the backward-paragraph-scan fix in `internal/dogfood/ledger.go` unblocks the gate. (The gate failing *is* a release-gate failure by definition.)
2. **F5.2** verdict normalization: unknown gate-status strings currently yield `verdict=<garbage>, veto=false` — silent-pass direction; normalize + veto unknowns before any external consumer feeds parsed JSON into `ComputeNonCompensatoryVerdict`.
3. **F7.2** single-fd bounded read in fsops + `readDeliverableFile`: close the stat→read TOCTOU that defeats the #981 memory guard.
4. **F1.1 + F6.1** refusal bounds: a refusal counter / terminal transition after K refusals in the store, and loop-level damping in the drain loop — both close the immortal-task / churn DoS paths opened by fail-before-consume.

Recommended for the following cut (not blocking): F8.1 (anchored/allow-listed E_* extraction), F8.2 (parse the extracted JSON in the suspect heuristic), F1.3 (store-level clamp of `deferSeconds`/reason length), F2.1 (`DryRun` field on `TrustRootDecision`), F4.3 (hard in-memory cap + outbox lockfile), F8.3 (mechanical funnel-completeness test + observable envelope-violation signal).
