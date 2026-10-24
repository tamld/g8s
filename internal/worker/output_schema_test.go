package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOutputSchemaRejectsInvalidResult satisfies the direct reproduction probe
// from issue #655: a task declaring an output schema rejects an invalid result
// during validateContract.
func TestOutputSchemaRejectsInvalidResult(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	schema := []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	task := submitTask(t, env, "test-output-schema-reject", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(schema),
	})

	validation, errorCalls := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(`{"ok":true}`))
	assert.False(t, validation.Valid, "expected contract validation to fail for missing required property")
	assert.NotEmpty(t, validation.SchemaErrors, "expected actionable schema errors")
	assert.NotEqual(t, "true", validation.Details["schema_validated"], "schema_validated must not be true on failure")
	require.NotEmpty(t, errorCalls, "expected error call record on failure")
	assert.Equal(t, "contract_violation", errorCalls[0].ErrorType)
}

// TestOutputSchema_ValidObject verifies that a conforming object passes contract validation.
func TestOutputSchema_ValidObject(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	schema := []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	task := submitTask(t, env, "test-valid-object", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(schema),
	})

	validation, errorCalls := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(`{"answer":"hello"}`))
	assert.True(t, validation.Valid, "valid output must pass contract validation")
	assert.Empty(t, validation.SchemaErrors, "schema errors must be empty on success")
	assert.Equal(t, "true", validation.Details["schema_validated"], "schema_validated must be true on success")
	assert.Empty(t, errorCalls, "no error calls should be logged on success")
}

// TestOutputSchema_ContractMismatches verifies required, type, enum, and additionalProperties.
func TestOutputSchema_ContractMismatches(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	schema := []byte(`{
		"type": "object",
		"required": ["answer", "status"],
		"properties": {
			"answer": {"type": "string"},
			"status": {"type": "string", "enum": ["ok", "pending"]}
		},
		"additionalProperties": false
	}`)

	tests := []struct {
		name          string
		resultJSON    string
		expectedError string
	}{
		{
			name:          "missing required field",
			resultJSON:    `{"status":"ok"}`,
			expectedError: "missing property 'answer'",
		},
		{
			name:          "type mismatch",
			resultJSON:    `{"answer":12345,"status":"ok"}`,
			expectedError: "got number, want string",
		},
		{
			name:          "enum violation",
			resultJSON:    `{"answer":"valid","status":"unknown_status"}`,
			expectedError: "value must be one of 'ok', 'pending'",
		},
		{
			name:          "additional property disallowed",
			resultJSON:    `{"answer":"valid","status":"ok","extra":"forbidden"}`,
			expectedError: "additional properties 'extra' not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := submitTask(t, env, "test-mismatch-"+tc.name, 1, map[string]any{
				"permission":    "read_only",
				"output_schema": json.RawMessage(schema),
			})

			validation, errorCalls := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(tc.resultJSON))
			assert.False(t, validation.Valid)
			assert.NotEmpty(t, validation.SchemaErrors)
			assert.NotEqual(t, "true", validation.Details["schema_validated"])
			require.NotEmpty(t, errorCalls)

			foundExpected := false
			for _, errStr := range validation.SchemaErrors {
				if assert.Contains(t, errStr, tc.expectedError) {
					foundExpected = true
					break
				}
			}
			assert.True(t, foundExpected, "expected error %q in schema errors: %v", tc.expectedError, validation.SchemaErrors)
		})
	}
}

// TestOutputSchema_NestedArraysAndLocalRef verifies nested arrays, objects, and in-memory local fragment references.
func TestOutputSchema_NestedArraysAndLocalRef(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	schema := []byte(`{
		"type": "object",
		"required": ["items"],
		"properties": {
			"items": {
				"type": "array",
				"items": { "$ref": "#/definitions/Item" }
			}
		},
		"definitions": {
			"Item": {
				"type": "object",
				"required": ["id", "val"],
				"properties": {
					"id": { "type": "integer" },
					"val": { "type": "string" }
				}
			}
		}
	}`)

	task := submitTask(t, env, "test-nested-localref", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(schema),
	})

	// Valid nested items
	validJSON := `{"items":[{"id":1,"val":"one"},{"id":2,"val":"two"}]}`
	vValid, errsValid := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(validJSON))
	assert.True(t, vValid.Valid)
	assert.Equal(t, "true", vValid.Details["schema_validated"])
	assert.Empty(t, errsValid)

	// Invalid nested item (item id is string instead of integer)
	invalidJSON := `{"items":[{"id":1,"val":"one"},{"id":"not-an-int","val":"two"}]}`
	vInvalid, errsInvalid := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(invalidJSON))
	assert.False(t, vInvalid.Valid)
	assert.NotEmpty(t, vInvalid.SchemaErrors)
	assert.NotEqual(t, "true", vInvalid.Details["schema_validated"])
	assert.NotEmpty(t, errsInvalid)

	// Unresolvable local fragment ref must fail closed
	badRefSchema := []byte(`{
		"type": "object",
		"properties": { "item": { "$ref": "#/definitions/NonExistent" } }
	}`)
	badRefTask := submitTask(t, env, "test-unresolvable-ref", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(badRefSchema),
	})
	vBadRef, errsBadRef := env.sup.validateContract(ctx, badRefTask.TaskID, "worker-1", 1, json.RawMessage(`{"item":"val"}`))
	assert.False(t, vBadRef.Valid)
	assert.NotEmpty(t, vBadRef.SchemaErrors)
	assert.NotEmpty(t, errsBadRef)
}

// TestOutputSchema_BooleanSchemas verifies boolean schemas true and false.
func TestOutputSchema_BooleanSchemas(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	// Schema true: any valid JSON passes
	taskTrue := submitTask(t, env, "test-schema-true", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": true,
	})
	vTrue, errsTrue := env.sup.validateContract(ctx, taskTrue.TaskID, "worker-1", 1, json.RawMessage(`{"anything": 123}`))
	assert.True(t, vTrue.Valid)
	assert.Equal(t, "true", vTrue.Details["schema_validated"])
	assert.Empty(t, errsTrue)

	// Schema false: any valid JSON fails
	taskFalse := submitTask(t, env, "test-schema-false", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": false,
	})
	vFalse, errsFalse := env.sup.validateContract(ctx, taskFalse.TaskID, "worker-1", 1, json.RawMessage(`{"anything": 123}`))
	assert.False(t, vFalse.Valid)
	assert.NotEmpty(t, vFalse.SchemaErrors)
	assert.NotEqual(t, "true", vFalse.Details["schema_validated"])
	require.NotEmpty(t, errsFalse)
	assert.Contains(t, vFalse.SchemaErrors[0], "false schema")
}

// TestOutputSchema_MalformedSchema verifies that invalid schema syntax or invalid keyword types fail closed.
func TestOutputSchema_MalformedSchema(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	malformedCases := []struct {
		name   string
		schema any
	}{
		{
			name:   "non-object non-boolean number",
			schema: 12345,
		},
		{
			name:   "non-object non-boolean string",
			schema: "type: object",
		},
		{
			name:   "non-object non-boolean array",
			schema: []any{"string"},
		},
		{
			name:   "invalid keyword type value",
			schema: json.RawMessage(`{"type": 123}`),
		},
		{
			name:   "invalid required type",
			schema: json.RawMessage(`{"required": "not-an-array"}`),
		},
	}

	for _, tc := range malformedCases {
		t.Run(tc.name, func(t *testing.T) {
			task := submitTask(t, env, "test-malformed-"+tc.name, 1, map[string]any{
				"permission":    "read_only",
				"output_schema": tc.schema,
			})

			validation, errorCalls := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(`{"ok": true}`))
			assert.False(t, validation.Valid)
			assert.NotEmpty(t, validation.SchemaErrors)
			assert.NotEqual(t, "true", validation.Details["schema_validated"])
			assert.NotEmpty(t, errorCalls)
		})
	}
}

// TestOutputSchema_UnsupportedDialects verifies that explicit $schema dialects other than Draft 7 are rejected.
func TestOutputSchema_UnsupportedDialects(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	dialects := []struct {
		name       string
		schemaJSON string
		shouldPass bool
	}{
		{
			name:       "Draft 4 rejected",
			schemaJSON: `{"$schema":"http://json-schema.org/draft-04/schema","type":"object"}`,
			shouldPass: false,
		},
		{
			name:       "Draft 6 rejected",
			schemaJSON: `{"$schema":"http://json-schema.org/draft-06/schema","type":"object"}`,
			shouldPass: false,
		},
		{
			name:       "Draft 2019-09 rejected",
			schemaJSON: `{"$schema":"https://json-schema.org/draft/2019-09/schema","type":"object"}`,
			shouldPass: false,
		},
		{
			name:       "Draft 2020-12 rejected",
			schemaJSON: `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`,
			shouldPass: false,
		},
		{
			name:       "Custom URI rejected",
			schemaJSON: `{"$schema":"https://example.com/custom","type":"object"}`,
			shouldPass: false,
		},
		{
			name:       "Non-string dialect rejected",
			schemaJSON: `{"$schema":7,"type":"object"}`,
			shouldPass: false,
		},
		{
			name:       "Draft 7 with hash accepted",
			schemaJSON: `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`,
			shouldPass: true,
		},
		{
			name:       "Draft 7 https accepted",
			schemaJSON: `{"$schema":"https://json-schema.org/draft-07/schema","type":"object"}`,
			shouldPass: true,
		},
	}

	for _, tc := range dialects {
		t.Run(tc.name, func(t *testing.T) {
			task := submitTask(t, env, "test-dialect-"+tc.name, 1, map[string]any{
				"permission":    "read_only",
				"output_schema": json.RawMessage(tc.schemaJSON),
			})

			validation, _ := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(`{"ok": true}`))
			if tc.shouldPass {
				assert.True(t, validation.Valid, "Draft 7 dialect should pass")
				assert.Equal(t, "true", validation.Details["schema_validated"])
			} else {
				assert.False(t, validation.Valid, "non-Draft-7 dialect must be rejected")
				assert.NotEmpty(t, validation.SchemaErrors)
				assert.NotEqual(t, "true", validation.Details["schema_validated"])
			}
		})
	}
}

// TestOutputSchema_ExternalRefBlocked_NoHTTPRead verifies that external refs fail closed
// and make zero network requests or filesystem reads.
func TestOutputSchema_ExternalRefBlocked_NoHTTPRead(t *testing.T) {
	var httpRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"object"}`))
	}))
	defer server.Close()

	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	// External HTTP reference
	httpSchema := map[string]any{
		"$ref": server.URL + "/schema.json",
	}
	taskHTTP := submitTask(t, env, "test-external-http-ref", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": httpSchema,
	})

	validationHTTP, errorCallsHTTP := env.sup.validateContract(ctx, taskHTTP.TaskID, "worker-1", 1, json.RawMessage(`{"foo":"bar"}`))
	assert.False(t, validationHTTP.Valid)
	assert.NotEmpty(t, validationHTTP.SchemaErrors)
	assert.NotEqual(t, "true", validationHTTP.Details["schema_validated"])
	assert.NotEmpty(t, errorCallsHTTP)

	// Invariant check: exactly ZERO HTTP requests were sent to the server
	assert.Equal(t, int32(0), httpRequests.Load(), "external ref loader must be disabled; zero HTTP requests permitted")

	// External file reference
	fileSchema := map[string]any{
		"$ref": "file:///etc/passwd",
	}
	taskFile := submitTask(t, env, "test-external-file-ref", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": fileSchema,
	})

	validationFile, errorCallsFile := env.sup.validateContract(ctx, taskFile.TaskID, "worker-1", 1, json.RawMessage(`{"foo":"bar"}`))
	assert.False(t, validationFile.Valid)
	assert.NotEmpty(t, validationFile.SchemaErrors)
	assert.NotEqual(t, "true", validationFile.Details["schema_validated"])
	assert.NotEmpty(t, errorCallsFile)
}

// TestOutputSchema_InvalidJSONAndNullPreservation verifies invalid JSON detection
// and strict preservation of JSON null without coercion to empty object {}.
func TestOutputSchema_InvalidJSONAndNullPreservation(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	objectSchema := []byte(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	task := submitTask(t, env, "test-invalid-null", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(objectSchema),
	})

	// 1. Invalid JSON
	vInvalid, errsInvalid := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(`{not-json`))
	assert.False(t, vInvalid.Valid)
	assert.NotEmpty(t, vInvalid.SchemaErrors)
	assert.Contains(t, vInvalid.SchemaErrors[0], "result is not valid JSON")
	assert.NotEqual(t, "true", vInvalid.Details["schema_validated"])
	assert.NotEmpty(t, errsInvalid)

	// 2. Empty JSON
	vEmpty, errsEmpty := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(``))
	assert.False(t, vEmpty.Valid)
	assert.NotEmpty(t, vEmpty.SchemaErrors)
	assert.Contains(t, vEmpty.SchemaErrors[0], "result is empty")
	assert.NotEqual(t, "true", vEmpty.Details["schema_validated"])
	assert.NotEmpty(t, errsEmpty)

	// 3. JSON null against object schema: MUST NOT coerce to empty object {}
	// An empty object {} would pass {"type": "object"}, but null must fail!
	vNullObj, errsNullObj := env.sup.validateContract(ctx, task.TaskID, "worker-1", 1, json.RawMessage(`null`))
	assert.False(t, vNullObj.Valid, "null output must not pass object schema; must not coerce to {}")
	assert.NotEmpty(t, vNullObj.SchemaErrors)
	assert.Contains(t, vNullObj.SchemaErrors[0], "got null, want object")
	assert.NotEqual(t, "true", vNullObj.Details["schema_validated"])
	assert.NotEmpty(t, errsNullObj)

	// 4. JSON null against null-accepting schema: passes
	nullSchema := []byte(`{"type":"null"}`)
	taskNull := submitTask(t, env, "test-null-schema", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(nullSchema),
	})
	vNullValid, errsNullValid := env.sup.validateContract(ctx, taskNull.TaskID, "worker-1", 1, json.RawMessage(`null`))
	assert.True(t, vNullValid.Valid)
	assert.Equal(t, "true", vNullValid.Details["schema_validated"])
	assert.Empty(t, errsNullValid)
}

// TestOutputSchema_OmittedAndNullSchema verifies that omitted or explicit null schemas
// preserve existing behavior without recording schema_validated.
func TestOutputSchema_OmittedAndNullSchema(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	// 1. Omitted schema
	taskOmitted := submitTask(t, env, "test-omitted-schema", 1, map[string]any{
		"permission": "read_only",
	})
	vOmitted, errsOmitted := env.sup.validateContract(ctx, taskOmitted.TaskID, "worker-1", 1, json.RawMessage(`{"any":"json"}`))
	assert.True(t, vOmitted.Valid)
	assert.Empty(t, vOmitted.SchemaErrors)
	assert.NotContains(t, vOmitted.Details, "schema_validated", "schema_validated must not be set when schema is omitted")
	assert.Empty(t, errsOmitted)

	// 2. Explicit null schema
	taskNull := submitTask(t, env, "test-explicit-null-schema", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": nil,
	})
	vNull, errsNull := env.sup.validateContract(ctx, taskNull.TaskID, "worker-1", 1, json.RawMessage(`{"any":"json"}`))
	assert.True(t, vNull.Valid)
	assert.Empty(t, vNull.SchemaErrors)
	assert.NotContains(t, vNull.Details, "schema_validated", "schema_validated must not be set when schema is null")
	assert.Empty(t, errsNull)

	// 3. Raw null schema in json.RawMessage
	taskNullRaw := submitTask(t, env, "test-raw-null-schema", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(`null`),
	})
	vNullRaw, errsNullRaw := env.sup.validateContract(ctx, taskNullRaw.TaskID, "worker-1", 1, json.RawMessage(`{"any":"json"}`))
	assert.True(t, vNullRaw.Valid)
	assert.Empty(t, vNullRaw.SchemaErrors)
	assert.NotContains(t, vNullRaw.Details, "schema_validated", "schema_validated must not be set when schema is raw null")
	assert.Empty(t, errsNullRaw)
}

// TestOutputSchema_PersistedInvalidStatus verifies end-to-end execution persistence:
// when worker output violates the declared schema, execution completion persists
// the invalid status, schema errors, and error call history to the database.
func TestOutputSchema_PersistedInvalidStatus(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()

	// Task 1: Invalid result. The task requires an "answer" property in output.
	schemaReject := []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	taskReject := submitTask(t, env, "test-persisted-invalid", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(schemaReject),
	})

	// Configure fakeRunner to emit a valid worker envelope that lacks the required "answer" property
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded","summary":"done without answer"}`, 5*time.Millisecond)
		return child
	}

	// Run the task attempt through supervisor lifecycle
	claimedTask, err := env.sup.RunOnce(ctx, RunOptions{WorkerID: "worker-test", LeaseSeconds: 60})
	require.NoError(t, err)
	require.NotNil(t, claimedTask)
	assert.Equal(t, taskReject.TaskID, claimedTask.TaskID)

	// Verify from direct store query to ensure database persistence
	persistedReject, err := env.store.GetTask(ctx, taskReject.TaskID)
	require.NoError(t, err)
	require.NotNil(t, persistedReject)

	// Contract validation must be persisted and marked invalid
	require.NotNil(t, persistedReject.ContractValidation, "contract_validation must be persisted in database")
	assert.False(t, persistedReject.ContractValidation.Valid, "persisted contract_validation.valid must be false")
	assert.NotEmpty(t, persistedReject.ContractValidation.SchemaErrors, "persisted schema_errors must not be empty")
	assert.Contains(t, persistedReject.ContractValidation.SchemaErrors[0], "missing property 'answer'")
	assert.NotEqual(t, "true", persistedReject.ContractValidation.Details["schema_validated"])

	// Attempt finished as failure: last_error records contract validation failure
	assert.NotNil(t, persistedReject.LastError)
	assert.Contains(t, *persistedReject.LastError, "contract validation failed")

	// Error call history must have contract_violation record persisted
	require.NotEmpty(t, persistedReject.ErrorCallHistory)
	foundContractViolation := false
	for _, ec := range persistedReject.ErrorCallHistory {
		if ec.ErrorType == "contract_violation" {
			foundContractViolation = true
			break
		}
	}
	assert.True(t, foundContractViolation, "error call history must persist contract_violation record")

	// Task 2: Conforming result. The task requires status == succeeded and ok == true.
	schemaConforming := []byte(`{
		"type": "object",
		"required": ["ok", "status"],
		"properties": {
			"ok": {"type": "boolean", "enum": [true]},
			"status": {"type": "string", "enum": ["succeeded"]}
		}
	}`)
	taskConforming := submitTask(t, env, "test-persisted-valid", 1, map[string]any{
		"permission":    "read_only",
		"output_schema": json.RawMessage(schemaConforming),
	})

	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded","summary":"valid delivery"}`, 5*time.Millisecond)
		return child
	}

	claimedConf, err := env.sup.RunOnce(ctx, RunOptions{WorkerID: "worker-test-2", LeaseSeconds: 60})
	require.NoError(t, err)
	require.NotNil(t, claimedConf)
	assert.Equal(t, taskConforming.TaskID, claimedConf.TaskID)

	persistedConf, err := env.store.GetTask(ctx, taskConforming.TaskID)
	require.NoError(t, err)
	require.NotNil(t, persistedConf)

	require.NotNil(t, persistedConf.ContractValidation)
	assert.True(t, persistedConf.ContractValidation.Valid)
	assert.Empty(t, persistedConf.ContractValidation.SchemaErrors)
	assert.Equal(t, "true", persistedConf.ContractValidation.Details["schema_validated"])
}
