package mcp

import (
	"encoding/json"
	"testing"
)

func TestSanitizeRequestView(t *testing.T) {
	tests := []struct {
		name     string
		input    json.RawMessage
		expected string
	}{
		{
			name:     "valid JSON with prompt",
			input:    json.RawMessage(`{"prompt": "hello world", "other_key": "value"}`),
			expected: `{"other_key":"value"}`,
		},
		{
			name:     "valid JSON without prompt",
			input:    json.RawMessage(`{"other_key": "value", "another_key": 123}`),
			expected: `{"another_key":123,"other_key":"value"}`,
		},
		{
			name:     "empty input",
			input:    json.RawMessage(""),
			expected: "null",
		},
		{
			name:     "invalid JSON",
			input:    json.RawMessage(`{"bad": json`),
			expected: "null",
		},
		{
			name:     "JSON array instead of object",
			input:    json.RawMessage(`[1, 2, 3]`),
			expected: "null",
		},
		{
			name:     "primitive input",
			input:    json.RawMessage(`"hello"`),
			expected: "null",
		},
		{
			name:     "JSON object only with prompt",
			input:    json.RawMessage(`{"prompt": "remove me"}`),
			expected: `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeRequestView(tt.input)

			// For "null", simple string comparison is fine
			if tt.expected == "null" {
				if string(got) != "null" {
					t.Errorf("sanitizeRequestView() = %v, want null", string(got))
				}
				return
			}

			// For JSON objects, unmarshal and compare to avoid key ordering issues
			var gotObj, expObj map[string]any
			if err := json.Unmarshal(got, &gotObj); err != nil {
				t.Fatalf("failed to unmarshal got JSON: %v, got: %s", err, string(got))
			}
			if err := json.Unmarshal([]byte(tt.expected), &expObj); err != nil {
				t.Fatalf("failed to unmarshal expected JSON: %v", err)
			}

			// Simple comparison via re-marshaling or reflect.DeepEqual would work,
			// let's use json.Marshal to get a normalized string for comparison
			gotNormalized, _ := json.Marshal(gotObj)
			expNormalized, _ := json.Marshal(expObj)

			if string(gotNormalized) != string(expNormalized) {
				t.Errorf("sanitizeRequestView() = %s, want %s", string(gotNormalized), string(expNormalized))
			}
		})
	}
}
