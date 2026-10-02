# Task: #465 P1 — instance identity + lease resilience (worker/controlplane hardening)

Repo: g8s @ main. Tracking: tamld/g8s#465 (P1 slices 3-4 of the ratified
plan `plans/260929-465-multi-tenant/plan.md`).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/worker/worker.go (ONLY: lease-resilience gating in
  awaitOutcome + the instance-id plumbing — no other edits)
- internal/worker/lease_resilience_test.go (new file)
- internal/controlplane/store.go (ONLY: RenewHeartbeat busy-retry +
  extendLease error distinction — no other edits)
- internal/controlplane/lease_resilience_test.go (new file)
- internal/pathutil/pathutil.go (ONLY: add InstanceID() helper)
- internal/pathutil/pathutil_instance_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
cmd/g8s/ in parallel.

## Context (verified at main; red-team adjudicated)

- worker.go:771-773: ANY `GetTask` error in awaitOutcome is treated as
  `lease_lost` — before renewal is even attempted.
- worker.go:783-784: a renewal error is immediately fatal.
- store.go:1539-1542: `extendLease` returns a generic wrapped error,
  indistinguishable from a genuine `ErrLeaseLost`.
- WAL + busy_timeout(30000) + MaxOpenConns(10) make real expiry rare —
  the fatality comes from transient DB errors being conflated with death.
- There is no per-instance identity: two g8s sessions on one host cannot
  be distinguished by heartbeats, markers, or logs (#465 incident root).

## Required implementation

1. **Instance identity** (internal/pathutil):
   - `InstanceID() string`: reads `<state_dir>/instance_id`; if absent,
     generates a UUID-style random hex (crypto/rand, 16 bytes → 32 hex
     chars), writes it (0o600), and returns it. Deterministic per state
     dir afterwards (G8S_STATE_DIR override respected — two state dirs =
     two instances, by design).
2. **Stamp the instance**:
   - worker.go: the run marker stays as-is (attempt-unique); ADD the
     instance id to the sweep's skip/attribution log lines and expose it
     via `GetInstanceID()` in the worker package (pathutil call, cached
     once).
   - store.go RenewHeartbeat: extend the lease row update to also record
     the instance id in the existing session/instance column IF one
     exists in the schema; if no column exists, do NOT migrate the schema
     (out of scope) — instead include the instance id in the heartbeat's
     error/log paths. State clearly in your report which you did.
3. **Lease resilience** (the core):
   - store.go: `extendLease` distinguishes outcomes — returns a wrapped
     `ErrLeaseLost` ONLY when the DB says the lease no longer matches /
     is expired; transient DB errors (busy/locked) get retried via the
     `withBusyRetry` idiom from internal/memory (import direction:
     controlplane must not import memory — replicate the small helper
     locally with a comment, or move the helper to a leaf package if
     cleaner; state your choice in the report).
   - worker.go awaitOutcome: a GetTask error or renewal error triggers a
     **second authoritative check after one heartbeat interval of grace**:
     re-read lease_expires_at; only declare lease_lost when the DB
     actually shows expiry/mismatch. Transient errors extend the loop
     (bounded: 3 consecutive failures then give up with attribution).
   - Kill reason on true lease loss carries attribution: reason string
     includes the DB-side evidence ("lease expired at <ts>").
4. **Heartbeat error handling** in RenewHeartbeat (store.go): apply the
   busy-retry around the UPDATE (same idiom; reuse a local copy of the
   jittered-backoff helper — controlplane cannot import internal/memory).

## Tests — red first

- lease_resilience_test.go (controlplane): busy error injected into the
  lease UPDATE (via a failing driver wrapper if the package has one, or
  a settings-free injection seam — check how existing tests inject
  failures; if none, test the helper in isolation) → renewal retried and
  succeeds; genuine expiry → ErrLeaseLost distinguishable from
  ErrBusy via errors.Is.
- lease_resilience_test.go (worker): awaitOutcome with a stub control
  plane whose GetTask fails transiently (1-2 times) then recovers → the
  attempt SURVIVES (no kill); GetTask says lease genuinely expired →
  kill fires with an attribution reason containing "expired".
- pathutil_instance_test.go: InstanceID stable across calls in one state
  dir; different G8S_STATE_DIR → different id; file mode 0o600.

## Constraints

- stdlib + existing deps; match style; comments only for non-obvious
  constraints. Schema changes are OUT OF SCOPE (no new columns/tables).
- `go test ./internal/worker/ ./internal/controlplane/ ./internal/
  pathutil/` and `go build ./...` locally green; the existing
  TestHeartbeatRejectsStaleLeaseToken must stay green.
