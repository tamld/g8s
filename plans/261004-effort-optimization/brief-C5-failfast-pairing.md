# Task: C5 — submit fail-fast pairing + root-dispatch classification (issues #566 #567)

Repo: g8s @ main (v0.16.0 era). Two evidence-backed defects from the
stress test, both at the submit seam. Read first: issues #566 and
#567 on g8s (full variant tables + repro) + cmd/g8s/submit.go (model/
effort realignment, classPaths) + internal/lane/effortsignal.go +
internal/lane/effortclass.go (ResolveClassForRoots) +
internal/routing/DefaultManifest().

## Delivery protocol

Scratch worktree. Write ONLY to:

- cmd/g8s/submit.go (triple validation, stale-manifest warning,
  provider inference) + submit_effort_test.go (extend)
- internal/routing/router.go (DefaultManifest refresh to current-tier
  3.8 trio — data + the model→provider inference helper if it lives
  better here)
- internal/lane/effortclass.go (optional root-dispatch class) +
  effortclass_test.go (extend)
- .g8s/effort-classes.yml (default_class field on the g8s registry)
  and cmd/g8s/offer.go (knowledge seed gains the field)

Do NOT run git commit. Do NOT touch internal/worker, cmd/g8s/deliver
(#569 is a separate slice), internal/dispatch.

## Required implementation

1. **#566 — fail-fast triple validation** (ALL four variants): after
   realignment, validate the (model, effort_applied, provider) triple
   against the effective manifest BEFORE store.SubmitTask; any invalid
   pairing exits E_USAGE with a hint naming the flag to fix — a
   worker claim must never be burned on a pairing submit could reject:
   - baked-name model whose suffix ≠ effort_applied when the manifest
     does NOT know the model (C3 realign no-ops) → usage error
     suggesting the matching variant or --provider refresh;
   - model that rejects effort flags (named-style, no effort support
     declared) + effort_applied ≠ "" → usage error suggesting the
     model be dropped or the effort omitted;
   - provider EMPTY on manual route → infer model→provider from the
     manifest when unambiguous; ambiguous or absent → usage error
     demanding -provider.
   Red-first table tests use the EXACT four variants from #566.
2. **#566 — stale-manifest warning**: when the effective model is
   absent from the effective manifest, emit one stderr warning line
   (non-blocking, flag-naming) — the realignment safety net is only
   as fresh as the manifest, and silence is the trap.
3. **#566 — DefaultManifest refresh**: routing.DefaultManifest()
   ships the current-tier trio (gemini-3.8-flash-low/medium/high
   baked-name) so the no-manifest path stops pairing a 3.8 effort
   with a high-only default.
4. **#567 — root-dispatch classification**: registries may declare a
   top-level `default_class: <name>` (the registry author decides
   what a WHOLE-REPO dispatch means). ResolveClassForRoots: when all
   classPaths reduce to "." (repo-root dispatch) and the chosen
   registry declares default_class → that class wins (source stays
   declared/registry per the normal precedence); subtree dispatches
   keep glob matching (byte-identical to today). Registry YAML header
   documents the rule; the knowledge seed and the g8s registry gain
   default_class: docs (pilot-proven). Red-first tests: root dispatch
   with default_class, without default_class (unregistered floor),
   subtree dispatch unchanged.
5. Payload/telemetry keys unchanged; the #566 exits happen BEFORE
   store.SubmitTask (that is the point — no receipt burned, no
   worker claim).

## Constraints

- stdlib; `go test ./cmd/g8s/ ./internal/lane/ ./internal/routing/`
  green; `go build ./...` green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Layer note (DEBT-34): this slice is cmd/g8s + neutral internals
  only — #569 (deliver/worker seam) is deliberately excluded.

## Report

The validation rule table (which pairing fails fast, which warns),
the inference logic, the default_class semantics, and the test matrix
with pass counts.
