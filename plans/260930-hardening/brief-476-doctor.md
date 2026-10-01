# Task: #476 wave B2 — doctor critical checks, evidence retention, Makefile targets

Repo: g8s @ main. Tracking: tamld/g8s#476 (P1 doctor items + P2 retention + P3 Makefile).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/doctor/doctor.go (+ internal/doctor/doctor_newchecks_test.go new)
- Makefile
- internal/worker/worker.go (ONLY the evidence-directory default handling —
  no other edits in this file)
- internal/cleanup/cleanup.go (ONLY: add an evidence retention target —
  no edits to the existing sweeps' logic)
- internal/settings/settings.go (+ internal/settings/settings_retention_test.go new)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
cmd/g8s/ and tools/ in parallel.

## Required implementation

1. **doctor: three new checks** (internal/doctor/doctor.go, following the
   existing check function shape and registration):
   - `checkProvidersFile`: open/validate the operator's providers.json
     (path resolution + `config.Load` from internal/config — import
     direction is safe, config is a leaf); PASS = file absent (legacy
     default is legal) or parses clean; FAIL names the JSON/path error and
     the fix ("fix providers.json or remove it to fall back to built-ins").
   - `checkDiskSpace`: statfs the state dir's volume; FAIL/WARN when free
     space < 2GiB (disk exhaustion has masqueraded as random test/attempt
     failures twice — cite that in the check's message).
   - `checkStaleBinary`: IF a `bin/g8s` file exists in the working
     directory, compare its mtime against the newest commit time of the
     repo (exec git log -1 --format=%ct); WARN when the binary predates
     the last commit (stale-binary trap bit twice this campaign). Skip
     cleanly when bin/g8s or the git repo is absent.
2. **Evidence retention**:
   - internal/settings/settings.go: new allowed key `evidence_retention_days`
     (integer-as-string, validated >= 1 when set; empty = unlimited, the
     current behavior).
   - internal/cleanup/cleanup.go: new target `evidence` — age-gated removal
     of per-attempt evidence directories under the state dir's evidence/
     root (respect the retention setting via cfg; dry-run reports
     would_delete per directory; NEVER touches non-evidence paths). Wire
     the target into the target registry the same way existing targets
     register (follow the scratch/orphan-dir pattern).
   - internal/worker/worker.go: the evidence default (currently
     G8S_EVIDENCE_DIR or state-dir evidence/) stays as-is — do not change
     write behavior; the retention lever is cleanup-side only.
3. **Makefile**:
   - Add `local:` target → `go build -o bin/g8s ./cmd/g8s` (the documented
     way to produce the dogfood binary — today nobody can reproduce how
     bin/g8s appears).
   - Fix the lint toolchain pin: GOTOOLCHAIN go1.25.0 → match go.mod's
     1.26.0 (or drop the pin).
   - Leave `resource.syso`/goverinfo untouched (flagged for a separate
     decision, out of scope here).

## Tests — red first

- doctor_newchecks_test.go: providers.json valid/invalid/absent matrix;
  disk-space check's threshold logic unit-tested via an injectable stat
  func; stale-binary check with a fabricated old binary + fresh repo →
  warns, and with no bin/ → skips.
- settings_retention_test.go: set/get/unset round-trip; "0" and "-3"
  rejected; empty value legal (unlimited).
- cleanup evidence target: fixture evidence tree with old + fresh dirs →
  old removed, fresh kept, dry-run touches nothing, non-evidence paths
  untouched.

## Constraints

- stdlib + existing deps; match style. Do not change evidence WRITE
  behavior (retention is cleanup-side only).
- `go test ./internal/doctor/ ./internal/cleanup/ ./internal/settings/
  ./internal/worker/` and `go build ./...` locally green.
