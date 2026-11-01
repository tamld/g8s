# OpenSpec DELTA-47: Reach Layer — Read-Only Platform Data Acquisition

* **Specification ID**: `DELTA-47`
* **Status**: `APPLIED` — merged with the reach S1 core (ADR-0037).
* **Implementation Status**: `APPLIED` (S1 core; S2-S4 slices tracked in issue #704)
* **Target Packages**: `internal/reach`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **domain_href**: `spec/domains/06-knowledge-and-memory/spec.md`
* **feature_id**: `FEAT-REACH-01`
* **decision_href**: `docs/decisions/0037-reach-layer.md` (ADR-0037, peer co-signed)

## Contract

`internal/reach` provides read-only platform data acquisition: an in-code
channel→action→backend registry, ordered fallback dispatch with
fallback-vs-terminal error classification, and provenance-labeled responses.
The only agent-facing surface is the `g8s reach` CLI returning the standard
versioned JSON envelope. Read-only is absolute: no operation may mutate any
upstream platform. Credentials never enter g8s state, receipts, logs, or
ContextPacket (two-layer separation). Backends wrap external CLIs (gh) or
stdlib HTTP — no Python port, no GUI automation (ADR-0037 invariants).

## Surface

- Registry (in-code, no YAML): `Lookup(channel, action)` returns backends in
  stable declared order; unknown channel/action is a terminal error.
  S1 channels: `web/read` (bounded HTTPS GET with timeout, size cap, and a
  descriptive User-Agent), `github/read` (`gh api` wrapper with a terminal
  error when the tool is absent).
- Chain: execution runs backends in declared (or `Orderer`-reordered) order;
  transient failures (network errors, timeouts, 429, 5xx) classify
  **fallback** and try the next backend; bad requests (other 4xx, invalid
  URL, missing tool) classify **terminal** and stop immediately. Optional
  nil-safe `Orderer`/`Recorder` seams (ADR-0037 Amendment A1) let the S2
  health ledger attach without coupling; `Recorder` observes one record per
  attempt with rank, backend, class, and error fingerprint.
- CLI: `g8s reach read <url>` and `g8s reach <channel> read --url <url>`
  emit the versioned envelope (`"kind": "reach"`) with channel, action,
  backend, provenance, body, url, fetched_at, body_sha256, and the per-attempt
  trail, honoring the common `--json`/`--jsonl` flags.
- Brief citations: prose MAY embed `reach-cite:` followed by a JSON object
  (inline or brace-balanced across lines) carrying the same machine-artifact
  fields as the CLI envelope (`url`, `channel`, `action`, `backend`,
  `body_sha256`, `fetched_at` RFC3339 UTC). Markers without a valid artifact
  are rejected by the deterministic DoR floor on lanes F/R/S.

## Invariants (ADR-0037, non-negotiable)

1. Read-only absolute — no posting/commenting/liking on any channel.
2. Credentials/sessions stay OUTSIDE g8s state — never in receipts, ledger,
   logs, or ContextPacket.
3. No Python port — pattern only; adapters wrap external CLIs exactly like
   the existing `platform_dispatch` class.
4. No GUI automation in the steady-state loop.

## Verification scenarios

#### Scenario: Reach registry, chain fallback classification, and CLI envelope parity
<!-- tests: TestLookupDeclaredOrder, TestLookupUnknownChannelAction, TestClassify, TestChainFallbackOnTransient, TestChainStopsOnTerminal, TestChainAllBackendsFailAggregates, TestChainNilOrdererKeepsDeclaredOrder, TestChainOrdererReorders, TestChainRecorderPerAttempt, TestGithubAPIPath, TestParseWebURLRejectsNonHTTP -->
- **GIVEN** the in-code channel registry with `web/read` and `github/read` backends and a chain with injected fake backends
- **WHEN** lookups run against known and unknown channels, backends fail transiently or terminally, an Orderer reorders the chain, and a Recorder observes attempts
- **THEN** declared order is stable, unknown channel/action is terminal, transient failures advance to the next backend, terminal failures stop the chain, all-fail aggregates per-attempt classes, and every attempt is recorded with rank and class

#### Scenario: Reach CLI emits the versioned envelope without network
<!-- tests: TestReachReadJSONEnvelope, TestReachUsageMissingURL -->
- **GIVEN** the `g8s reach` command registered on the canonical CLI surface
- **WHEN** `reach read` runs against a stubbed backend (and with a missing URL)
- **THEN** the versioned envelope carries channel, action, backend, provenance, body, url, fetched_at, body_sha256 (digest from `reach.DigestBody`), and the attempt trail, and missing-URL usage exits with a terminal error

#### Scenario: Reach citation machine-artifact validation and brief floor binding (S4)
<!-- tests: TestValidateReachCitation_RejectsWithoutArtifact, TestValidateReachCitation_AcceptsFullArtifact, TestValidateReachCitation_RejectsStaleArtifact, TestValidateReachCitation_RejectsMalformedDigest, TestParseReachCiteMarker_FindsAndRejectsMalformedJSON, TestDoRFloor_ReachCiteInvalidArtifactFails, TestDoRFloor_NonReachCitationPassthrough, TestDoRFloor_ReachCiteValidArtifactPasses -->
- **GIVEN** brief prose on lanes F/R/S that may embed zero or more `reach-cite:` markers whose JSON grammar is `reach-cite:` optionally followed by whitespace then a `{` … `}` object with keys `url`, `channel`, `action`, `backend`, `body_sha256`, `fetched_at`
- **WHEN** `reach.ValidateReachCitation` runs against parsed artifacts (absolute http(s) url, non-blank channel/backend, `action` exactly `read`, 64-char lowercase hex `body_sha256`, RFC3339 `fetched_at` not older than `MaxCitationAge`) and `CheckDoRFloorForLane` evaluates `reach_citation_valid` over the combined brief text
- **THEN** malformed marker JSON yields parse errors without panic, any validation error fails `reach_citation_valid` with the error in `Detail`, zero markers passes through without tightening upstream prose regexes, and a fully valid marker passes alongside the existing DELTA-35 checks

#### Scenario: Reach S2 health ledger persistence and backend reorder (ADR-0037 A1)
<!-- tests: TestHealthLedgerLoadSaveRoundTrip, TestHealthLedgerRecorderUpdatesLedger, TestHealthLedgerDemoteOnFallbackCooldown, TestHealthLedgerCooldownExpiresRestoresOrder, TestHealthLedgerOrdererStableAmongHealthy, TestReachReadUsesHealthLedger -->
- **GIVEN** a persistent health ledger keyed `channel::action::backend` with atomic JSON snapshot under operator state (override via `G8S_REACH_HEALTH_PATH`)
- **WHEN** fallback outcomes are recorded, the CLI runs with a wired ledger, cooldown windows elapse, and ok/terminal outcomes are recorded
- **THEN** reload survives restart, fallback sets a 300s demotion tail in `Order`, expired cooldown restores declared order among healthy backends, ok clears cooldown, terminal never sets a new cooldown, and the CLI envelope shape is unchanged

#### Scenario: Reach S2 owned-paths deletion ledger (ADR-0037 A1)
<!-- tests: TestOwnedPathsRegisterDeleteSuccess, TestOwnedPathsDeleteUnregisteredFails, TestOwnedPathsRejectSymlink -->
- **GIVEN** the reach owned-paths ledger (override via `G8S_REACH_OWNED_PATHS_PATH`) tracking absolute cleaned paths
- **WHEN** operators register paths and invoke registered deletion
- **THEN** only registered paths are removed from disk, unregistered deletion fails closed, and symlink targets are refused via `os.Lstat` without touching WriteReceipt scope
