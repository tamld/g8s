# OpenSpec DELTA-41: Feature Map v2 — Collision Domains, Exclusive Ownership & Pre-Dispatch Collision Verdicts

* **Specification ID**: `DELTA-41`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/featuremap`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **No ownership concept**: `.g8s/feature-map.yml` schema `1.0` maps 15 feature nodes to `code_paths`, but `code_paths` globs may overlap freely and there is no notion of exclusive ownership or collision. A search for `collision` across the repository returned zero hits.
2. **Undeclared hotspots**: files such as `cmd/g8s/main.go`, `manifest.json`, `docs/AGENTS_FULL.md`, `spec/openspec/README.md`, and `README.md` are edited by almost every feature but are not declared anywhere. When F0 fans out parallel F1 workers, their receipts silently intersect on those files and the merges conflict.
3. **No pre-dispatch check**: F0 has no mechanical way to decide, *before* issuing two write receipts, whether the scopes are `parallel-safe`, must be `split` (carve the hotspot edit out of one scope), or must be `serialize`d.
4. **Orphan files**: the v1 validator only checks that every `internal/<pkg>` directory is covered. Files outside `internal/` (CLI commands, CI gates, docs, registries) can be unowned.

---

## 2. Normative Requirements

### R1 — Schema 2.0, backward compatible (`internal/featuremap/featuremap.go`)
- `schema_version` MAY be `"1.0"` or `"2.0"`. Every `1.0` document MUST load and validate exactly as before.
- Per feature, schema `2.0` adds:
  - `owned_paths` (required in 2.0): exclusive glob set. Entries prefixed with `!` exclude a sub-region.
  - `shared_paths`: hotspot paths the feature legitimately edits. Every entry MUST be covered by a declared hotspot.
  - `collision_domain`: string id. Features with the same domain MUST NOT run in parallel (for example `FEAT-VERIFY-01` and `FEAT-BLIND-VERIFY-01` share the `internal/verifier` Go package). Defaults to the feature id.
  - `interfaces`: exported contracts — `cli` subcommands, `mcp_tools`, `envelope_kinds`, `go_packages`.
  - `openspec_deltas`: list of governing deltas. The legacy single-valued `openspec_delta` remains an alias: `Normalize` folds it into the list, and back-fills it from the first list entry so v1 consumers such as `g8s verify --feature` keep working.
- Top level, schema `2.0` adds `hotspots:`, a list of `{id, paths, policy, rationale}`. `policy` is `serialize`, `append-only`, or `owner:<FEATURE-ID>`.

### R2 — Glob algebra (`internal/featuremap/glob.go`)
- `MatchPath`, `GlobsOverlap`, and `GlobCovers` MUST decide match, intersection, and containment on patterns alone (no filesystem access), so scopes for files that do not exist yet can be checked.
- `**` as a whole segment matches zero or more segments. `*`, `?`, `[class]`, and `\x` follow `path.Match` within a segment.
- `GlobsOverlap` may return a false positive (two character classes are assumed to intersect). `GlobCovers` may return a false negative. Both errors are on the safe side: they make collisions more likely to be reported.

### R3 — Ownership validation (`g8s feature verify`, `internal/featuremap/ownership.go`)
For schema `2.0` maps, `ValidateWithOptions` MUST report an error when:
1. a feature has no `owned_paths`;
2. the `owned_paths` sets of two features overlap after exclusions;
3. a feature's `owned_paths` overlaps a hotspot, or two hotspots overlap;
4. a hotspot has an invalid policy, an unknown `owner:` feature, a duplicate id, or no paths;
5. a `shared_paths` entry is not covered by any hotspot;
6. an `interfaces` entry is declared by two features, or a `go_packages` entry is not a directory;
7. an `owned_paths`, `shared_paths`, or hotspot entry is absolute or escapes the repo root.

Every tracked file (`git ls-files`, or a filesystem walk outside git) that is not matched by any owned set or hotspot is an **orphan file**. It is a warning by default and an error under `--strict`. The existing `blast_radius` checks stay unchanged: unknown references, asymmetric `depends_on`/`direct_consumers` edges, and cycles in either graph are errors.

### R4 — Pre-dispatch collision verdicts (`g8s feature collisions`, `internal/featuremap/collisions.go`)
- `g8s feature collisions --paths <glob,...> [--json]` maps one write scope onto features, collision domains, hotspots, and unmapped paths.
- `g8s feature collisions --task-a <scope> --task-b <scope> [--strict] [--json]` compares two scopes. A scope is a list of comma-separated receipt globs, or `task:<id>` to read `allowed_paths` from the control-plane task (falling back to its receipt in `receipts.db`).
- The recommendation MUST be monotonic, `serialize` > `split` > `parallel-safe`:
  - `serialize`: the scopes share a collision domain, or can write the same non-hotspot file (owned or unmapped).
  - `split`: the scopes only meet on a `serialize` hotspot, or on an `owner:` hotspot. Carve the hotspot edit out of one receipt and apply it serially. For `owner:` hotspots the owner's scope keeps the edit.
  - `parallel-safe`: no shared domain or path, or only `append-only` hotspots are shared (merge by rebase and union).
- Exit codes: `0` analysis complete, `1` runtime error, `2` usage error, `3` with `--strict` when the recommendation is not `parallel-safe`.

### R5 — Self-application
- g8s's own `.g8s/feature-map.yml` MUST be schema `2.0`, declare owned paths for all 15 nodes, declare the hotspots `cmd/g8s/main.go`, `manifest.json`, `docs/AGENTS_FULL.md`, `spec/openspec/README.md`, and `README.md`, and pass `g8s feature verify --strict --json`.
- `tools/ci_feature_map_sync.sh` MUST run the verifier in `--strict` mode.

---

## 3. Verification Scenarios

#### Scenario: Glob algebra decides match, overlap, and containment without the filesystem
<!-- tests: TestGlobPrimitives -->
- **GIVEN** patterns with `**`, `*`, `?`, character classes, and `!` exclusions
- **WHEN** `MatchPath`, `GlobsOverlap`, `GlobCovers`, and `PathSet` are evaluated
- **THEN** `cmd/g8s/update*.go` overlaps `cmd/g8s/**` but not `internal/**`, `internal/**` covers `internal/a/b.go`, and an excluded sub-region is not owned.

#### Scenario: Schema 1.0 maps remain valid and openspec_delta aliases openspec_deltas
<!-- tests: TestValidateV2ContractAndErrors -->
- **GIVEN** an unmodified schema `1.0` map and a v2 node that uses only `openspec_deltas`
- **WHEN** the map is parsed and validated
- **THEN** the v1 map validates with no ownership errors, and `openspec_delta` is back-filled from the first entry of `openspec_deltas`.

#### Scenario: Ownership validation rejects overlaps, bad hotspots, and unbacked shared paths
<!-- tests: TestValidateV2ContractAndErrors -->
- **GIVEN** a v2 map where two features own `internal/x/**`, a feature owns a hotspot path, a shared path matches no hotspot, and two features declare the same CLI subcommand
- **WHEN** `ValidateWithOptions` runs
- **THEN** each defect is reported as an error and the report is invalid.

#### Scenario: Orphan files are warnings by default and errors under strict
<!-- tests: TestValidateV2ContractAndErrors -->
- **GIVEN** a tracked file matched by no owned set and no hotspot
- **WHEN** validation runs without and then with `Strict`
- **THEN** the orphan is a warning (still valid) and then an error (invalid).

#### Scenario: Collision verdicts are monotonic over domains, paths, and hotspot policy
<!-- tests: TestCompareScopesAndPolicies -->
- **GIVEN** pairs of scopes that are disjoint, meet only on a `serialize` hotspot, share a collision domain, or meet on `append-only` / `owner:` hotspots
- **WHEN** `CompareScopes` runs
- **THEN** the verdicts are `parallel-safe`, `split`, `serialize`, `parallel-safe`, and `split` respectively.

#### Scenario: g8s's own feature map passes strict verification and drives the CLI
<!-- tests: TestValidateV2ContractAndErrors -->
- **GIVEN** the repository's `.g8s/feature-map.yml`
- **WHEN** `g8s feature verify --strict --json` and `g8s feature collisions --task-a ... --task-b ... --json` run
- **THEN** verification is valid with zero orphan files, and two scopes that only share `cmd/g8s/main.go` get the verdict `split`.
