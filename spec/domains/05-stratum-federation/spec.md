# Living Domain Spec 05 — Orthogonal Stratum Federation & Fleet Hygiene

- **Domain ID**: `05-stratum-federation`
- **Collision Domains**: `federation`, `platform-hygiene`
- **Feature IDs**: `FEAT-CHARTER-FEDERATION-01`, `FEAT-DOCTOR-DIAG-01`
- **Constitutional Laws**: Law 1 (Zero-Trust Delegation), Law 3 (Blast-Radius Containment)

---

### R1 — Stratum Adjacency Law & Cross-Repository Fleet Hygiene {#R1-stratum-and-fleet-hygiene}

<!-- inherited-from: DELTA-07, DELTA-14, DELTA-16, DELTA-32, DELTA-33 -->
<!-- feature_id: FEAT-CHARTER-FEDERATION-01 -->

Multi-repository delegation MUST obey Orthogonal Control Strata (`S0..S3 + O-Plane`) via `g8s charter` and cross-repository workspace cleanliness via `g8s fleet status`.

#### Scenario: Federation and fleet hygiene commands are registered in CLI surface
<!-- tests: TestCanonicalCommandsAndAliases -->
- **GIVEN** the canonical CLI surface contract
- **WHEN** `IsRegisteredCommand` checks `charter` and `fleet`
- **THEN** both commands are registered and exposed in `g8s --help`
