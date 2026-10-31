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
  backend, provenance, body, and the per-attempt trail, honoring the common
  `--json`/`--jsonl` flags.

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
- **THEN** the versioned envelope carries channel, action, backend, provenance, body, and the attempt trail, and missing-URL usage exits with a terminal error
