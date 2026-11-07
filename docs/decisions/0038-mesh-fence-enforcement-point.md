# ADR-0038: Mesh Fence Enforcement Point — Claim-Scoped Mutation Gating at the Worker Edit Path

- **Status**: Accepted (2026-11-07) — operator-ratified; implementation issues 1-2 this PR, red-tests follow.
- **Depends on**: ADR-0032 (containment invariant `F2 ⊆ F1 ⊆ F0`), ADR-0035 (epoch-fenced
  charter engine, Kleppmann fencing tokens), DELTA-46 ([`spec/openspec/46-peer-coordinator-ledger-spec.md`](../../spec/openspec/46-peer-coordinator-ledger-spec.md)),
  #776/#777 (fail-loud `read_only` precedent).
- **Evidence**: [`plans/261104-g8s-product-site/collision-matrix.md`](../../plans/261104-g8s-product-site/collision-matrix.md) rows C13–C15.

## Context

The peer coordinator ledger (DELTA-46, `internal/mesh`) gives equal coordinators
**scoped task claims**: `claim` carries normalized literal relative `paths`
(`claimNormalizePaths`, [`internal/mesh/claims.go:53`](../../internal/mesh/claims.go)), rejects overlapping unexpired
claims on the same resource in one immediate SQLite transaction
([`internal/mesh/claims.go:156-181`](../../internal/mesh/claims.go)), and binds accepted artifacts to a
monotonic `context_revision` fence ([`internal/mesh/claims.go:270-274`](../../internal/mesh/claims.go),
tested by `TestClaimRevisionFence` in [`internal/mesh/drills_test.go:94`](../../internal/mesh/drills_test.go)).

But the ledger **arbitrates only at claim-acquisition time**. Nothing anywhere
in the codebase asks, at mutation time, "is this write covered by a live mesh
claim?" The claim's `Paths` are advisory bookkeeping between F0 peers, while
actual write authority flows through three unrelated mechanisms:

1. **Worker edit path (pre-mutation, in-process)** — `fsops.ResolveDelegationContext`
   ([`internal/fsops/fsops.go:197`](../../internal/fsops/fsops.go): refuses mutation under `G8S_PERMISSION=read_only`
   at :205-207, requires a receipt for delegated tiers at :222-224, enforces the
   `G8S_SCOPE_ROOT` ceiling from #598) and `fsops.VerifyWriteScope`
   ([`internal/fsops/fsops.go:349`](../../internal/fsops/fsops.go): canonical-path + symlink-escape check in
   `VerifyScope` at :274, plus `.git/**`/`.g8s/**` protection from #664). This
   is where every `g8s edit`/apply mutation is checked against receipt
   `AllowedPaths` *before* the write syscall.
2. **Receipt verify (post-hoc)** — `worker.VerifyWorktreeDiffScope`
   ([`internal/worker/diff_firewall.go:51`](../../internal/worker/diff_firewall.go)) runs `git status --porcelain`
   after an attempt and rejects `read_only` mutations
   (`E_DIFF_FIREWALL_READ_ONLY_MUTATION`, :99) and out-of-scope files
   (`E_DIFF_FIREWALL_OUT_OF_SCOPE`, :114); wired into harvest at
   [`internal/worker/worker_sweep_collect.go:599`](../../internal/worker/worker_sweep_collect.go). Receipts
   themselves are single-use and TTL-bounded (`ValidateAndConsume`,
   [`internal/receipt/receipt.go:604`](../../internal/receipt/receipt.go); `VerifyReceipt`, :875).
3. **Harness isolation (containment envelope)** — `WorktreeIsolator`
   ([`internal/worker/worker.go:169`](../../internal/worker/worker.go)) fail-closed since #656
   (`isolation_unavailable`, worker.go:539-631); `read_only` + write-capable
   external CLI refused at claim time since #777
   (`checkReadOnlyEnforcement`, [`internal/worker/read_only.go:39`](../../internal/worker/read_only.go),
   `E_READ_ONLY_UNENFORCEABLE`, invoked at worker.go:450 before any spawn).

### Why the gap is real (campaign evidence C13–C15)

- **C13** — a `read_only` receipted codex task free-wrote 5 tracked files
  straight into the operator's main checkout: no worktree, no receipt firewall
  (#776). The in-process edit gate was never consulted because the external
  CLI never routes through `g8s edit`. Closed fail-loud by #777, but only for
  the `read_only` × `external-cli` cell of the matrix.
- **C14** — a brief handed video deliverables to a subagent whose working
  clone belonged to a different repo; the subagent committed flowaxon paths
  into rig3 at the exact moment F0 was rebasing, and the commit landed in the
  wrong PR. Deliverable paths were never bound to the assigned repo's claim
  scope — a cross-claim write that no layer stopped.
- **C15** — a worker editing one header silently removed a sibling security
  header; the post-hoc gate only checked the header being edited. Late,
  narrow verification missed drift across three surfaces.

The pattern: **late or narrow enforcement is probabilistic; early, mechanical,
path-scoped enforcement is not.** #777 already established the governing
stance — refuse loudly at the earliest mechanically-checkable boundary rather
than detect a breach after the fact ("silent probabilistic containment breach
is worse than a loud dispatch error").

## Decision

**Primary enforcement point: (a) the worker edit path** — the pre-mutation,
in-process fsops gate. The mesh claim fence is enforced by making the **write
receipt the carrier of the claim scope**, bound at issuance by F0:

1. **Issuance-time binding (F0).** A dispatch targeting a claimed task mints
   its receipt only after (or together with) the mesh claim, with
   `receipt.AllowedPaths := claim.Paths` (the claim's normalized literal
   paths, already overlap-free within the resource per
   [`internal/mesh/claims.go:156-181`](../../internal/mesh/claims.go)) and
   `receipt.TTL ≤ remaining claim lease`. Renewal for long tasks uses the
   already-normative idempotent same-owner re-claim
   ([`internal/mesh/claims.go:147-150`](../../internal/mesh/claims.go), "Same owner/task/content may retry
   idempotently") on the task heartbeat, re-aligning the receipt TTL.
2. **Mutation-time enforcement (F1/F2, no ledger access).** The existing fsops
   gate becomes claim-aware with zero new F1→ledger edges: the delegated
   environment already carries `G8S_TIER=F1`, `G8S_TASK_ID`,
   `G8S_PERMISSION`, `G8S_RECEIPT_ID`, `G8S_SCOPE_ROOT`
   ([`internal/worker/worker.go:659-668`](../../internal/worker/worker.go)). Effective mutation scope is
   `receipt.AllowedPaths ∩ active claim Paths`; because F0 clips the receipt
   to the claim at issuance, the intersection degrades to "the receipt" and
   the gate reuses `VerifyWriteScope` unchanged in the common case. Typed
   failures `E_CLAIM_FENCE_OUT_OF_SCOPE` and `E_CLAIM_FENCE_STALE` (mirroring
   `charter.ErrCharterFenceStale`, [`internal/charter/charter.go:65`](../../internal/charter/charter.go))
   fail closed before the write syscall.

This keeps the containment invariant intact: authority flows strictly
downward (`F2 ⊆ F1 ⊆ F0`, ADR-0032). F1 workers never read the peer ledger
(coordinator mesh operations are F0-only,
[`cmd/g8s/coordinator_mesh.go:20-29`](../../cmd/g8s/coordinator_mesh.go)); the claim fence data is *projected*
into the F1 environment by F0 at dispatch. The ledger still "does not grant
OS write authority" (DELTA-46 Contract) — the fence only ever **narrows** an
independently-issued receipt, never widens one.

**Complementary backstop: (b) the post-run receipt/diff verify.**
`VerifyWorktreeDiffScope` is extended to cross-check touched files against
claim paths (not just receipt globs) and made **fail-closed on tooling
errors** — today it silently returns "no violation" when `git status` fails
([`internal/worker/diff_firewall.go:56-64`](../../internal/worker/diff_firewall.go): both `os.Stat` and
`cmd.Output()` errors yield `return nil, nil`). This is the only layer that
sees writes made *around* the in-process gate (external CLIs inside the
worktree), so it guards exactly the primary's blind spot.

**Why not (c) harness isolation as the fence point.** Isolation is
repo-granular containment, not path-granular authorization. DELTA-46's core
scenario is peers making "disjoint contributions to one problem" *in one
repository* (spec 46 Verification scenarios); a worktree is writable
end-to-end, so isolation cannot arbitrate between two claims on disjoint
paths of the same repo. It remains a shipped, fail-closed precondition
(#656, #777) — substrate, not fence. Why not (b) as primary: it is post-hoc
(mutation already durable — C15's drift shipped before anyone looked) and
currently fail-open on tooling errors, which is precisely the property the
#777 precedent forbids for a primary gate.

## Threat model

| # | Threat | Stopped by | Residual risk |
|---|---|---|---|
| T1 | External-CLI provider ignores `read_only` and writes the host checkout (C13, #776) | Pre-gate: #777 refuses the dispatch (`E_READ_ONLY_UNENFORCEABLE`, read_only.go:39); isolation (#656) contains `workspace_write` to the worktree | Backstop (b) flags out-of-scope/untracked files before harvest. Residual: providers declaring `enforcement: in-process` (agy) are trusted to enforce internally — declaration errors are a supply-chain risk |
| T2 | Cross-claim write inside one repo — worker mutates a path claimed by a peer (C14 class) | Primary (a): path outside `receipt ∩ claim` → `E_CLAIM_FENCE_OUT_OF_SCOPE` pre-syscall | Backstop (b) at harvest for non-g8s tool writes. Residual: damage confined to the worker's own worktree until harvest rejects |
| T3 | Stale-claim mutation — write after lease expiry, after peer accept (revision bumped), or after incarnation replacement | Primary (a): receipt TTL clipped to lease → `ExpiredError` (receipt.go:664); stale binding → `E_CLAIM_FENCE_STALE`; `actorTx` incarnation check (messages.go:28) invalidates old incarnations ledger-side | Residual: TOCTOU between in-process check and syscall (single host, in-process — negligible without a kernel LSM gate, explicitly out of scope) |
| T4 | Path escape via symlink, traversal, or absolute path (#598 class) | Primary (a): `VerifyScope` canonical-path + symlink resolution (fsops.go:292-318); ledger-side, `claimNormalizePaths` rejects `..`, absolute, and wildcard paths (claims.go:59-70) | Backstop (b) normalizes porcelain entries (diff_firewall.go:73-84). Residual: kernel-level escapes — OS sandbox is `Setpgid` process-group only, no seccomp/LSM (future ADR territory) |
| T5 | Receipt replay, or a receipt wider than its claim | `ValidateAndConsume` single-use (receipt.go:604, `AlreadyConsumedError`); intersection rule means widening requires BOTH ledger row and receipt row to agree — each alone fails closed | Residual: F0 issuance bugs (receipt minted wider than the claim). Mitigated by the issuance-side validator in implementation issue IMP-1 and the red tests |
| T6 | N2N mesh message smuggling — a crafted inbox payload convinces a worker to mutate | Messages are data, never executed (spec 46: "No input command is executed"); the fence requires the *worker's own* actor match (claims.go:253-255) — an inbox message cannot mint authority; accept binds `artifact_sha256` of what was actually written | Residual: prompt injection that misuses the worker's *own* claim scope — bounded by that scope's path set and detected by backstop (b) digest binding |

## Failure modes

| Layer | Current posture | Post-ADR posture |
|---|---|---|
| (a) worker edit path | Fail-closed by construction: `ResolveDelegationContext` and `VerifyWriteScope` return errors that abort the operation (fsops.go:205-207, 222-224, 306, 317, 322, 349-370). Blind to writes that bypass `g8s` tools | **Fail-closed, fail-loud — the primary.** New `E_CLAIM_FENCE_*` errors keep the same contract. Audit duty: every new fsops mutation entry point must route through `VerifyWriteScope` (enforced by review + red tests) |
| (b) receipt verify / diff firewall | **Fail-open in two ways**: tooling errors return `nil, nil` (diff_firewall.go:56-64 — a broken `git` binary silently disables the check), and post-hoc timing means the mutation is already durable. C15 shows late/narrow gates miss sibling invariants | **Fail-closed on tooling errors** (typed error → attempt FAILED), scope extended to full claim-path set. Stays a backstop because post-hoc timing is inherent |
| (c) harness isolation | Fail-closed since #656 (`isolation_unavailable`) and #777 (refuse at claim, worker.go:450) — the model citizen | Unchanged. Fail-loud-at-earliest-boundary is the default stance; #777 is the precedent this ADR generalizes |

Default stance (per #777): **when a layer cannot mechanically enforce a
constraint, it must refuse loudly rather than degrade silently.** A silent
`return nil, nil` is a policy decision, and it is the wrong one.

## Red-test plan

All tests are mechanically runnable with existing patterns: fake-clock ledger
tests (`store.now` injection, `TestMeshLeaseExpiry`,
[`internal/mesh/claims_test.go:137`](../../internal/mesh/claims_test.go)), table-driven provider×permission
matrices (`TestReadOnlyEnforcementDecision`,
[`internal/worker/read_only_test.go:24`](../../internal/worker/read_only_test.go)), and worktree fixtures
(`TestWorktreeDiffFirewallScopeEnforcement`,
[`internal/worker/diff_firewall_test.go:40`](../../internal/worker/diff_firewall_test.go)).

| # | Test name | Attack | Expected block point | Layer |
|---|---|---|---|---|
| R1 | `TestFenceEditOutsideClaimPathsRejected` | fsops edit to a path covered by the receipt glob but outside the claim's literal paths | `E_CLAIM_FENCE_OUT_OF_SCOPE` before the write syscall | (a) |
| R2 | `TestFenceExpiredLeaseFailsClosed` | fake-clock advance past claim lease / receipt TTL, then edit | mutation refused (`E_CLAIM_FENCE_STALE` / `ExpiredError`), receipt not consumed | (a) |
| R3 | `TestFenceCrossClaimWriteRejected` | node B edits a path claimed by node A (disjoint claims, same resource; `TestClaimRaceSingleWinner` fixture, drills_test.go:126) | rejected at the gate; ledger overlap rejection remains green (regression guard) | (a) + ledger |
| R4 | `TestFenceReceiptClaimIntersection` | receipt `AllowedPaths=["src/**"]`, claim `["src/a.go"]`: write `src/b.go` then `src/a.go` | `src/b.go` blocked, `src/a.go` allowed — intersection narrows, never widens | (a) |
| R5 | `TestDiffFirewallDetectsExternalCLIViolation` | direct `os.WriteFile` (simulating an external CLI) inside the worktree, outside claim paths, then sweep-collect | `E_DIFF_FIREWALL_OUT_OF_SCOPE` at harvest; artifact not accepted | (b) |
| R6 | `TestDiffFirewallFailsClosedOnGitError` | unreadable `.git` / corrupt worktree → diff firewall must error, never silently pass (kills the diff_firewall.go:56-64 fail-open) | typed error → attempt FAILED | (b) |
| R7 | `TestFenceStaleAfterAcceptRevisionBump` | claim accepted (revision+1, claims.go:273), late write attempt against the old revision binding | `E_CLAIM_FENCE_STALE`; acceptance stays idempotent (pattern: `TestClaimRevisionFence`, drills_test.go:94; `TestMeshAcceptRevisionAndIdempotence`, claims_test.go:95) | (a) |
| R8 | `ConformanceProbeP7FenceExternalCLI` | end-to-end: external-CLI template writes outside claim scope under `workspace_write` (extends probes P5/P6, [`internal/provider/conformance/cli.go:567,599`](../../internal/provider/conformance/cli.go)) | claim refused or diff firewall blocks; no out-of-tree write ever reaches the main checkout | (a)+(b)+(c) |

## Implementation issues (proposed, NOT filed)

1. **`feat(fence): bind claim scope into the worker mutation gate via receipt issuance`**
   Scope: F0 dispatch path orders claim-before-receipt and clips
   `AllowedPaths` to claim paths with `TTL ≤ lease remaining`; idempotent
   re-claim renewal wired to the task heartbeat for tasks exceeding the 300s
   lease cap (claims.go:110); issuance-side validator that a receipt can
   never be minted wider than its claim (closes T5's residual); typed
   `E_CLAIM_FENCE_OUT_OF_SCOPE` / `E_CLAIM_FENCE_STALE` errors surfaced
   through the CLI envelope and `claim_refused_*`-style task events
   (pattern from #777). No new mesh operations and no F1 ledger access —
   the delegated env (worker.go:659-668) is already sufficient.
2. **`feat(fence): fail-closed claim-aware worktree diff firewall at harvest`**
   Scope: remove the `return nil, nil` fail-open paths in
   `VerifyWorktreeDiffScope` (diff_firewall.go:56-64) in favor of typed
   errors; extend the signature with claim paths; wire the claim cross-check
   into `worker_sweep_collect.go:599` and the `g8s verify` receipt path;
   extend `receipt_export` with claim/task identity so
   `./bin/g8s receipt-export --task <id>` recovery shows the fence verdict
   (#658 pattern).
3. **`test(fence): red-test matrix R1-R8 + provider conformance probe P7`**
   Scope: implement the red-test table above as Go tests in
   `internal/mesh`, `internal/fsops`, `internal/worker`, and
   `internal/provider/conformance`; add the P7 end-to-end external-CLI fence
   probe; wire the suite into the existing CI lanes (ADR-0031) so the fence
   regressions are release-blocking.

## Consequences

**Easier**

- Same-repo disjoint multi-peer claims (DELTA-46's headline scenario) become
  mechanically trustworthy instead of honor-system: the fence is enforced
  where the write happens, not where the claim was recorded.
- Harvest arbitration turns mechanical: accept only diffs inside the claim;
  C14-class wrong-repo deliverables are caught at gate *and* at accept.
- C13 is already closed fail-loud (#777); C15-class sibling drift is caught
  by the backstop's full-claim-scope diff rather than a single-file check.
- The fence composes with ADR-0035's charter fencing: `E_CLAIM_FENCE_STALE`
  mirrors `ErrCharterFenceStale`, giving S1/S2 one uniform stale-authority
  semantic across charters and claims.

**Harder**

- Every implement dispatch now has an ordering constraint (claim → receipt)
  and a lease-renewal discipline for tasks longer than 300s; violating it
  fails loudly mid-task by design.
- Brief authors must keep deliverable paths inside one claim's literal path
  set — the C14 "1 subagent = 1 repo" law becomes mechanically enforced, so
  sloppy briefs now bounce at dispatch instead of drifting into wrong PRs.
- Receipt workflows that used bare `**` globs must enumerate the claim's
  literal paths (ledger claims are wildcard-free, spec 46).

**Migration notes for existing mesh code**

- No mesh schema change: the `claims` table already stores paths; no
  retention change (claims are never pruned, spec 46).
- No flag day: tasks dispatched without a claim binding keep today's
  receipt-only scope; claim binding activates the fence additively.
- No DELTA-46 normative change required for v1: the ledger remains
  non-authoritative (spec 46: "It does not grant OS write authority";
  accept "is ledger acceptance only, not permission to mutate a Git ref"),
  the fence only narrows. Two documented resolutions, not conflicts:
  (i) claim paths are literal while receipt paths admit globs — the
  intersection always resolves toward the *claim* (narrower wins), which
  must be stated in the fsops docs; (ii) if a future direct claim-lookup
  operation is ever wanted (e.g. worker-side `claim-get`), that IS a
  DELTA-46 amendment — v1 deliberately avoids it by using the receipt as
  the carrier.

## Falsification clocks

- If a red test shows a mutation that satisfies `receipt ∩ claim` yet harms
  a peer (semantic conflicts inside disjoint paths), path fencing is
  insufficient → resource-level serialization (single active claim per
  resource) becomes mandatory, not just overlap checks.
- If lease renewal on heartbeat proves flaky in practice (fence failing
  closed mid-task at rates that break workflows), the 300s lease cap must be
  revisited in DELTA-46 — never by widening the gate.
- If external-CLI providers gain OS-level sandbox declarations that make
  in-CLI enforcement mechanical, the #777 refusal for that provider class
  can be relaxed by ADR, not by configuration.

## Related

ADR-0032 (containment rings), ADR-0035 (epoch-fenced charters — the fencing
token precedent), ADR-0033 (F0-verified delegation), DELTA-46 (peer
coordinator ledger), #699/#755 (the mesh fence epic), #776/#777 (fail-loud
read_only precedent), plans/261104-g8s-product-site/collision-matrix.md
(C13–C15).
