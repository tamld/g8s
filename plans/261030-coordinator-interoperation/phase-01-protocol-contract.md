# Protocol contract and negative controls

Status: ready under D2. Initial task owner: Codex, with one bounded g8s documentation/contract worker per packet. This is a temporary task role available to any peer. Depends on phase 0. See [Frozen design](./design.md) and [D2 peer/budget contract](./peer-budget-contract.md).

## Deliverable and scope

Freeze versioned NodeIdentity, CapabilityObservation, QuotaObservation, MessageEnvelope, DeliveryObservation and ResultReference contracts, including registry/inbox API signatures consumed by the two implementation owners. The new public declarations follow the next available OpenSpec delta and its Living Domain lineage; reserve the actual ID at dispatch rather than assuming it is free. Split manifest/index/domain documentation glue into a separate packet if the eight-file limit would be exceeded.

Do not edit worker/store/receipt security or global state-path defaults. Code blocks are specification examples until implemented. Define actor authorization separately from schema validation. Same-host user/session identity binding is an experiment boundary, not cryptographic peer authentication.

## Required decisions

- [ ] Define fields, enums, nullable quota semantics, provenance and expiry rules.
- [ ] Define stable node identity and a fresh session incarnation.
- [ ] Define sender/recipient authorization and rejection semantics.
- [ ] Define message and observation deduplication keys and retry horizon.
- [ ] Define symmetric task/integration claims and accepted context revision binding; actual integration must enforce them.
- [ ] Bind one CLI and one MCP client to identical native semantics, including bounded wait and unavailable-runtime reporting.
- [ ] Set explicit pending capacity, payload limit, artifact path bounds and retention.
- [ ] Define how out-of-order ACK/result observations are reconciled without regression.
- [ ] Specify exact store/API signatures so AGY and Zcode do not invent incompatible types.
- [ ] Register feature ownership, spec lineage and planned interfaces before implementation.
- [ ] Freeze meaningful tests with actual fail-before/pass-after evidence.

## Verification

Every negative control in design.md gets a named test and a concrete falsifier. Missing/replayed messages, stale incarnation, mismatched baseline/scope, unavailable adapter and unknown quota must be covered. Verify declarations with the repository's feature, CLI and doc/spec gates. Zero tests/checks is not success. This phase is complete when both owners can consume the same typed contract without changing it independently.
