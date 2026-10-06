# Task: C4 — signal-based effort resolution (issue #550, design pass-3)

Repo: g8s @ main. The operator challenged path-class as the primary
effort signal; pass-3 (plans/261004-effort-optimization/
design-pass3-signal-model.md) replaces it with a declared-signal
precedence chain. Path-class is DEMOTED to fallback prior. Read first:
the pass-3 design (binding semantics) + cmd/g8s/submit.go (current
resolution: explicit flag → class default) + internal/lane/
effortclass.go (registry).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/lane/effortsignal.go (new — signal resolution) +
  effortsignal_test.go
- cmd/g8s/submit.go (new flags + resolution wiring) +
  submit_effort_test.go (extend)

Do NOT run git commit. Do NOT touch internal/routing, internal/worker,
internal/ladder, internal/controlplane.

## Required implementation

1. **New submit flags**: `--blast-radius` (low|medium|high, optional,
   validated at parse), `--loc-estimate` (positive int, optional,
   surface scale declared by the brief author).
2. **Resolution** (lane/effortsignal.go): `ResolveEffortSignals(explicit
   string, blast string, loc int, registryClass string,
   registryEffort string) SignalResult` — precedence per the pass-3
   design: explicit → declared → registry → floor. The hypothesis
   matrix v1 (BINDING for this slice): blast=high → at least medium;
   blast=high ∧ loc>200 → high; blast=low ∧ loc≤50 → low; otherwise
   the registry result (or medium) stands. Result carries: applied
   level, `Source` (explicit|declared|registry|floor), and every
   input's suggestion (what each source said) for recording.
3. **Submit wiring**: when --effort is passed → explicit wins
   (unchanged). Otherwise signals run BEFORE the registry: the
   declared result (when any signal present) replaces the registry
   default as `requestedEffort`; registry keeps handling when NO
   signals declared (today's behavior, byte-identical payloads for
   existing callers). Payload keys: `effort_source`
   (explicit|declared|registry|floor), `effort_signals` (object with
   blast_radius, loc_estimate, declared_suggestion,
   registry_suggestion, override_down recomputed against the WINNING
   source). Priority is NOT an effort input (scheduling-only — do not
   read it here).
4. **Model realignment interplay**: the C3 alignment runs on the
   WINNING effort (the realigned baked-name model must match
   effort_applied whatever source decided it).
5. **Tests (red-first)**: precedence matrix (explicit over declared
   over registry over floor; each source's suggestion recorded), each
   matrix row (blast high/low, loc threshold boundaries 200/50,
   blast+loc combined), no-signals = byte-identical legacy payloads,
   invalid blast value → typed usage error, override_down computed
   against the winning source, realignment consumes the winning level.

## Constraints

- stdlib; `go test ./internal/lane/ ./cmd/g8s/` green; `go build ./...`
  green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Keep the diff surgical: one new file + one wiring site.

## Report

The exported signature, the matrix, the payload keys, and the test
matrix with pass counts.
