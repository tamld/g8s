# Living Domain Spec 08 — Declarative Feature Map, Offer Bundle & Spec-Kit Lineage

- **Domain ID**: `08-offer-and-spec-kit`
- **Collision Domain**: `ssot-governance`
- **Feature IDs**: `FEAT-FEATUREMAP-01`
- **Constitutional Laws**: Law 3 (Blast-Radius Containment), Law 5 (Self-Describing Surface), Law 6 (Evidence over Verdicts)

---

### R1 — Exclusive Feature-Map Ownership & Bidirectional Spec Lineage {#R1-feature-map-and-spec-lineage}

<!-- inherited-from: DELTA-23, DELTA-41 -->
<!-- feature_id: FEAT-FEATUREMAP-01 -->

Every tracked file in the repository MUST be covered by exactly one feature `owned_paths` set or a declared hotspot in `.g8s/feature-map.yml` (`g8s feature verify --strict`), and every applied OpenSpec delta declaring `domain_href:` MUST resolve to a valid Living Domain Spec anchor with reciprocal `inherited-from` and `<!-- tests: ... -->` pins (`E_BROKEN_SPEC_LINEAGE`).

#### Scenario: Core repository Markdown files contain zero unbound CLI claims
<!-- tests: TestScanRepoCoreMarkdownFilesBound -->
- **GIVEN** `AGENTS.md`, `README.md`, `spec/constitution.md`, and `skills/g8s-supervisor/SKILL.md`
- **WHEN** `ScanMarkdownCLIInvocations` validates each file
- **THEN** zero `E_UNBOUND_CLI_CLAIM` errors are emitted
