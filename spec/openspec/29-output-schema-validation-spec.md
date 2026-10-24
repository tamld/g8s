# OpenSpec DELTA-29: Task Output Schema Validation (JSON Schema Draft 7) (#655)

* **Specification ID**: `DELTA-29`
* **Title**: Task Output Schema Validation (JSON Schema Draft 7)
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Milestone**: `M6` (Agent Ergonomics & Safety Hardening)
* **Target Packages**: `internal/worker`
* **ADR Reference**: [`docs/decisions/0032-containment-levels.md`](../../docs/decisions/0032-containment-levels.md)
* **Foundational Axioms**: Axiom 1 (Brain Decides, Worker Proves), Axiom 2 (Capability-Scoped Contracts), Issue #289
* **Issue**: `#655`

---

## 1. Context & Motivation

In issue #289, the task execution contract introduced `output_schema` (`SubmitTaskRequest.OutputSchema`) as a declared JSON Schema contract for task outputs, alongside `allowed_paths`, `allowed_tools`, and `max_output_size`.

However, the previous supervisor implementation only marshaled the raw schema placeholder and recorded `schema_validated: "true"` unconditionally without validating the actual result JSON against the schema. Consequently, a worker could declare a schema, produce a malformed or non-conforming payload, and still receive `ContractValidation.Valid = true` and `schema_validated = "true"`.

Task output schema validation is strictly distinct from provider result envelope validation (`validateResult` / issue #383 / #407). Provider result envelope validation checks wrapper fields (`ok`, `status`, AGY errors, exit codes). In contrast, `validateContract` enforces the task-specific contract against the actual `resultJSON` payload produced by the worker.

This specification establishes normative Draft 7 JSON Schema validation in `internal/worker`, enforcing dialect constraints, fail-closed resolution, strict loader isolation, and tamper-proof outcome persistence.

---

## 2. Normative Requirements

### Requirement 1: Draft 7 Specification & Dialect Governance (DELTA-29 R1)

1. **Declared Standard**: The supervisor MUST validate task output against the declared JSON Schema Draft 7 specification.
2. **Compiler Standard**: The implementation MUST use an established, spec-compliant Go JSON Schema compiler (`github.com/santhosh-tekuri/jsonschema/v6`). Homegrown, heuristic, or partial validators are forbidden.
3. **Explicit Draft 7 Default**: When a schema omits the `$schema` keyword, the compiler MUST default explicitly to Draft 7.
4. **Dialect Rejection**: If a schema explicitly specifies a `$schema` keyword:
   - It MUST identify JSON Schema Draft 7 (`http://json-schema.org/draft-07/schema#`, `http://json-schema.org/draft-07/schema`, `https://json-schema.org/draft-07/schema#`, or `https://json-schema.org/draft-07/schema`).
   - Any other `$schema` dialect (e.g., Draft 4, Draft 6, Draft 2019-09, Draft 2020-12, or custom URI) MUST be rejected as unsupported, yielding `Valid: false`, actionable `SchemaErrors`, and no `schema_validated` marker.

### Requirement 2: Zero-Trust Loader Isolation & Fail-Closed Refs (DELTA-29 R2)

1. **Disabled External Loaders**: All external network (HTTP/HTTPS) and filesystem (`file://`) `$ref` loaders MUST be completely disabled.
2. **No External I/O**: Compiling a schema MUST NOT make network calls or read files from the host filesystem.
3. **Local Fragment Resolution**: Local fragment references (e.g., `"$ref": "#/definitions/Item"`) within the same schema document are permitted and resolved in memory.
4. **Fail-Closed Resolution**: Any reference that cannot be resolved locally or attempts external resolution MUST fail closed immediately with an actionable error.

### Requirement 3: Exact JSON Representation & Null Preservation (DELTA-29 R3)

1. **Direct Result Validation**: The schema validates the exact `resultJSON` passed to `validateContract`. No artificial `TaskSpec` or inner `Result` wrappers shall be invented.
2. **Preserve JSON Null**: A `resultJSON` value of `null` (`[]byte("null")`) MUST be parsed and validated as JSON `null` (`nil`). It MUST NOT be coerced to an empty object `{}`. If the schema requires an object, `null` MUST fail validation.
3. **Boolean Schemas**: A schema of `true` is valid and accepts any well-formed JSON output. A schema of `false` is valid and rejects all outputs with actionable errors.
4. **Omitted / Null Schema Preservation**: If `output_schema` is omitted or explicitly `null`, existing behavior is preserved: contract validation does not run schema checks, does not populate `SchemaErrors`, and does not record `schema_validated: "true"`.

### Requirement 4: Validation Failure Outcomes & Error Persistence (DELTA-29 R4)

1. **Invalid Outputs & Malformed Schemas**: If the schema is malformed, specifies an unsupported dialect, fails compilation, or the result JSON is invalid or non-conforming:
   - `ContractValidation.Valid` MUST be `false`.
   - `ContractValidation.SchemaErrors` MUST contain nonempty, actionable error messages identifying the location and reason.
   - `ContractValidation.Details["schema_validated"]` MUST NOT be set to `"true"`.
   - A `controlplane.ErrorCallRecord` with `ErrorType: "contract_violation"` MUST be generated for each schema violation and persisted to `error_call_history`.
2. **Valid Outputs**: If validation succeeds:
   - `ContractValidation.Valid` remains `true` (unless other contract fields like `max_output_size` failed).
   - `ContractValidation.SchemaErrors` remains empty.
   - `ContractValidation.Details["schema_validated"]` MUST be `"true"`.
3. **Outcome Persistence**: The canonical worker completion flow MUST persist the `ContractValidation` record in the database. Attempt completion (`FinishAttempt`) must never overwrite or erase contract invalidity.
4. **No Cache / Performance Subsystem**: Schema compilation and validation MUST be stateless and side-effect free per contract check.

---

## 3. Scenarios & Mechanical Test Pins

#### Scenario: Valid JSON object conforming to Draft-7 schema passes
<!-- tests: TestOutputSchema_ValidObject -->
- Given a task declaring a JSON Schema Draft 7 requiring specific fields and types.
- When worker output contains a conforming JSON object.
- Then contract validation succeeds with `Valid = true`, empty `SchemaErrors`, and `schema_validated: "true"`.
- And no contract violation error call records are created.

#### Scenario: Non-conforming output fails contract validation with actionable errors
<!-- tests: TestOutputSchemaRejectsInvalidResult, TestOutputSchema_ContractMismatches -->
- Given a task declaring a JSON Schema with required fields, type constraints, enums, or `additionalProperties: false`.
- When worker produces output violating any of these constraints (missing required property, wrong type, disallowed enum value, unexpected property).
- Then contract validation fails with `Valid = false` and descriptive `SchemaErrors`.
- And `schema_validated` marker is NOT set to `"true"`.
- And a `contract_violation` error call record is logged for each failure.

#### Scenario: Nested structures and local fragment references resolve and validate
<!-- tests: TestOutputSchema_NestedArraysAndLocalRef -->
- Given a schema utilizing nested object hierarchies, arrays, and local `"$ref": "#/definitions/..."` fragment references.
- When worker output conforming to the referenced definition is evaluated.
- Then references resolve in memory and pass validation.
- When output violates constraints in nested definitions, validation fails with actionable errors.

#### Scenario: Boolean schemas evaluate correctly
<!-- tests: TestOutputSchema_BooleanSchemas -->
- Given a task specifying schema `true`.
- When any well-formed JSON output is validated.
- Then validation succeeds.
- Given a task specifying schema `false`.
- When any JSON output is validated.
- Then validation fails with `Valid = false`.

#### Scenario: Malformed schema fails closed immediately
<!-- tests: TestOutputSchema_MalformedSchema -->
- Given a task with invalid schema JSON, invalid keyword types, or non-object/boolean schema.
- When contract validation runs.
- Then schema compilation fails closed with `Valid = false` and actionable schema errors.

#### Scenario: Unsupported schema dialects are rejected
<!-- tests: TestOutputSchema_UnsupportedDialects -->
- Given a task with `$schema` explicitly set to Draft 4, Draft 6, Draft 2020-12, or an unknown dialect URI.
- When contract validation compiles the schema.
- Then it is rejected as unsupported, returning `Valid = false` and descriptive error.

#### Scenario: External network and filesystem refs are blocked
<!-- tests: TestOutputSchema_ExternalRefBlocked_NoHTTPRead -->
- Given a schema attempting external `$ref` resolution over HTTP/HTTPS or filesystem.
- When schema compilation executes.
- Then external loaders are completely disabled, performing 0 HTTP requests and 0 filesystem reads, and failing closed.

#### Scenario: Null output is preserved and not coerced to empty object
<!-- tests: TestOutputSchema_InvalidJSONAndNullPreservation -->
- Given a schema requiring an object and a worker output of `null` or invalid JSON bytes.
- When contract validation runs.
- Then `null` is evaluated strictly as `null` (not coerced to `{}`), failing validation with `Valid = false`.

#### Scenario: Omitted or null schema preserves legacy behavior
<!-- tests: TestOutputSchema_OmittedAndNullSchema -->
- Given a task with omitted or `null` `output_schema`.
- When contract validation runs on any JSON payload.
- Then schema validation is bypassed, `SchemaErrors` remains empty, and `schema_validated` is not set.

#### Scenario: Invalid contract status is persisted across database retrieval
<!-- tests: TestOutputSchema_PersistedInvalidStatus -->
- Given a task producing schema-violating output executed through the worker supervisor.
- When the task completes and is re-fetched from the database.
- Then `ContractValidation.Valid` remains `false` in the persisted task record with non-retryable failure status.
