package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const outputSchemaSyntheticURI = "g8s://task/output-schema.json"

// blockedURLLoader blocks all network and filesystem schema reference loading.
// Local fragment references (#/...) are resolved in-memory by the compiler.
type blockedURLLoader struct{}

func (b blockedURLLoader) Load(urlStr string) (any, error) {
	return nil, fmt.Errorf("external schema loading is disabled: %s", urlStr)
}

// schemaValidationOutcome holds the outcome of evaluating task output against output_schema.
type schemaValidationOutcome struct {
	Evaluated    bool
	Valid        bool
	SchemaErrors []string
}

// isDraft7Dialect checks whether the given dialect URL matches JSON Schema Draft 7.
func isDraft7Dialect(uri string) bool {
	switch uri {
	case "http://json-schema.org/draft-07/schema",
		"http://json-schema.org/draft-07/schema#",
		"https://json-schema.org/draft-07/schema",
		"https://json-schema.org/draft-07/schema#":
		return true
	default:
		return false
	}
}

// validateOutputSchema evaluates resultJSON against declared output_schema.
// If output_schema is omitted or null, it returns Evaluated=false and Valid=true
// to preserve existing no-schema behavior.
func validateOutputSchema(schemaRaw any, resultJSON json.RawMessage) schemaValidationOutcome {
	if schemaRaw == nil {
		return schemaValidationOutcome{Evaluated: false, Valid: true}
	}

	// Handle RawMessage or []byte if passed
	switch v := schemaRaw.(type) {
	case json.RawMessage:
		if len(v) == 0 || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return schemaValidationOutcome{Evaluated: false, Valid: true}
		}
		var unmarshaled any
		if err := json.Unmarshal(v, &unmarshaled); err != nil {
			return schemaValidationOutcome{
				Evaluated:    true,
				Valid:        false,
				SchemaErrors: []string{fmt.Sprintf("output schema is not valid JSON: %v", err)},
			}
		}
		return validateOutputSchema(unmarshaled, resultJSON)
	case []byte:
		if len(v) == 0 || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return schemaValidationOutcome{Evaluated: false, Valid: true}
		}
		var unmarshaled any
		if err := json.Unmarshal(v, &unmarshaled); err != nil {
			return schemaValidationOutcome{
				Evaluated:    true,
				Valid:        false,
				SchemaErrors: []string{fmt.Sprintf("output schema is not valid JSON: %v", err)},
			}
		}
		return validateOutputSchema(unmarshaled, resultJSON)
	}

	// A valid JSON Schema in Draft 7 must be a JSON object or boolean.
	var schemaDoc any
	switch v := schemaRaw.(type) {
	case bool:
		schemaDoc = v
	case map[string]any:
		// Check explicit $schema dialect if specified
		if dialectVal, hasDialect := v["$schema"]; hasDialect {
			dialectStr, ok := dialectVal.(string)
			if !ok {
				return schemaValidationOutcome{
					Evaluated:    true,
					Valid:        false,
					SchemaErrors: []string{"invalid $schema: dialect identifier must be a string"},
				}
			}
			if !isDraft7Dialect(dialectStr) {
				return schemaValidationOutcome{
					Evaluated:    true,
					Valid:        false,
					SchemaErrors: []string{fmt.Sprintf("unsupported $schema dialect %q: only Draft 7 is supported", dialectStr)},
				}
			}
		}
		schemaDoc = v
	default:
		return schemaValidationOutcome{
			Evaluated:    true,
			Valid:        false,
			SchemaErrors: []string{fmt.Sprintf("invalid output schema: must be object or boolean, got %T", schemaRaw)},
		}
	}

	// Setup JSON schema compiler pinned to Draft 7 with external loaders blocked
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	c.UseLoader(blockedURLLoader{})

	if err := c.AddResource(outputSchemaSyntheticURI, schemaDoc); err != nil {
		return schemaValidationOutcome{
			Evaluated:    true,
			Valid:        false,
			SchemaErrors: extractSchemaErrors(err),
		}
	}

	sch, err := c.Compile(outputSchemaSyntheticURI)
	if err != nil {
		return schemaValidationOutcome{
			Evaluated:    true,
			Valid:        false,
			SchemaErrors: extractSchemaErrors(err),
		}
	}

	// Result JSON must be valid JSON and cannot be empty
	if len(bytes.TrimSpace(resultJSON)) == 0 {
		return schemaValidationOutcome{
			Evaluated:    true,
			Valid:        false,
			SchemaErrors: []string{"result is empty"},
		}
	}

	// Preserve null exactly: jsonschema.UnmarshalJSON on "null" returns nil, nil.
	// Never coerce null to an empty object.
	valDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(resultJSON))
	if err != nil {
		return schemaValidationOutcome{
			Evaluated:    true,
			Valid:        false,
			SchemaErrors: []string{fmt.Sprintf("result is not valid JSON: %v", err)},
		}
	}

	// Validate unmarshaled result value against the compiled schema
	if err := sch.Validate(valDoc); err != nil {
		return schemaValidationOutcome{
			Evaluated:    true,
			Valid:        false,
			SchemaErrors: extractSchemaErrors(err),
		}
	}

	return schemaValidationOutcome{
		Evaluated: true,
		Valid:     true,
	}
}

// extractSchemaErrors formats validation and compilation errors into actionable strings.
func extractSchemaErrors(err error) []string {
	if err == nil {
		return nil
	}
	var valErr *jsonschema.ValidationError
	if errors.As(err, &valErr) {
		lines := strings.Split(valErr.Error(), "\n")
		var errs []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "- ") {
				trimmed := strings.TrimSpace(strings.TrimPrefix(l, "- "))
				if trimmed != "" {
					errs = append(errs, trimmed)
				}
			}
		}
		if len(errs) > 0 {
			return errs
		}
		return []string{valErr.Error()}
	}
	return []string{err.Error()}
}
