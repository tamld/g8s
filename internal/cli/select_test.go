package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelectPath(t *testing.T) {
	jsonDoc := []byte(`{
		"cmd": "test",
		"data": {
			"receipt_id": "rc-42",
			"count": 100,
			"active": true,
			"empty_val": null,
			"items": [
				{"id": "item-1", "name": "alpha"},
				{"id": "item-2", "name": "beta"}
			]
		},
		"matches": [
			{"file": "foo.go", "line": 10},
			{"file": "bar.go", "line": 25}
		],
		"tags": ["go", "security"]
	}`)

	t.Run("nested fields with and without leading dot", func(t *testing.T) {
		got, err := SelectPath(jsonDoc, ".data.receipt_id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "rc-42" {
			t.Fatalf("expected rc-42, got %q", got)
		}

		gotNoDot, err := SelectPath(jsonDoc, "data.receipt_id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotNoDot != "rc-42" {
			t.Fatalf("expected rc-42, got %q", gotNoDot)
		}
	})

	t.Run("root selector with dot and empty", func(t *testing.T) {
		gotDot, err := SelectPath(jsonDoc, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(gotDot, `"receipt_id":"rc-42"`) {
			t.Fatalf("expected root json, got %s", gotDot)
		}

		gotEmpty, err := SelectPath(jsonDoc, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(gotEmpty, `"receipt_id":"rc-42"`) {
			t.Fatalf("expected root json, got %s", gotEmpty)
		}
	})

	t.Run("array indexing", func(t *testing.T) {
		got, err := SelectPath(jsonDoc, "matches[0].file")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "foo.go" {
			t.Fatalf("expected foo.go, got %q", got)
		}

		gotSecond, err := SelectPath(jsonDoc, ".matches[1].line")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotSecond != "25" {
			t.Fatalf("expected 25, got %q", gotSecond)
		}

		gotTag, err := SelectPath(jsonDoc, "tags[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotTag != "go" {
			t.Fatalf("expected go, got %q", gotTag)
		}
	})

	t.Run("array projection matches[].file", func(t *testing.T) {
		got, err := SelectPath(jsonDoc, "matches[].file")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "foo.go\nbar.go"
		if got != expected {
			t.Fatalf("expected %q, got %q", expected, got)
		}

		gotItems, err := SelectPath(jsonDoc, "data.items[].id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotItems != "item-1\nitem-2" {
			t.Fatalf("expected item-1\nitem-2, got %q", gotItems)
		}

		gotTags, err := SelectPath(jsonDoc, "tags[]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotTags != "go\nsecurity" {
			t.Fatalf("expected go\nsecurity, got %q", gotTags)
		}
	})

	t.Run("formatting rules: numbers, booleans, null, map", func(t *testing.T) {
		gotNum, err := SelectPath(jsonDoc, "data.count")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotNum != "100" {
			t.Fatalf("expected 100, got %q", gotNum)
		}

		gotBool, err := SelectPath(jsonDoc, "data.active")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotBool != "true" {
			t.Fatalf("expected true, got %q", gotBool)
		}

		gotNull, err := SelectPath(jsonDoc, "data.empty_val")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotNull != "" {
			t.Fatalf("expected empty string for null, got %q", gotNull)
		}

		gotMap, err := SelectPath(jsonDoc, "matches[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotMap != `{"file":"foo.go","line":10}` {
			t.Fatalf("expected compact json, got %q", gotMap)
		}
	})

	t.Run("error cases", func(t *testing.T) {
		// Missing field includes available keys so AI agents never have to guess
		_, err := SelectPath(jsonDoc, "data.missing")
		if err == nil || !strings.Contains(err.Error(), "field \"missing\" not found") || !strings.Contains(err.Error(), "available keys: [active, count, empty_val, items, receipt_id]") {
			t.Fatalf("expected missing field error with available keys, got %v", err)
		}

		// Field on non-object
		_, err = SelectPath(jsonDoc, "cmd.subfield")
		if err == nil || !strings.Contains(err.Error(), "non-object") {
			t.Fatalf("expected non-object error, got %v", err)
		}

		// Index out of bounds
		_, err = SelectPath(jsonDoc, "matches[99].file")
		if err == nil || !strings.Contains(err.Error(), "out of bounds") {
			t.Fatalf("expected out of bounds error, got %v", err)
		}

		// Index non-array
		_, err = SelectPath(jsonDoc, "cmd[0]")
		if err == nil || !strings.Contains(err.Error(), "cannot index non-array") {
			t.Fatalf("expected index non-array error, got %v", err)
		}

		// Project non-array
		_, err = SelectPath(jsonDoc, "cmd[]")
		if err == nil || !strings.Contains(err.Error(), "cannot project across non-array") {
			t.Fatalf("expected project non-array error, got %v", err)
		}

		// Invalid index
		_, err = SelectPath(jsonDoc, "matches[abc]")
		if err == nil || !strings.Contains(err.Error(), "invalid array index") {
			t.Fatalf("expected invalid array index error, got %v", err)
		}

		// Unmatched bracket
		_, err = SelectPath(jsonDoc, "matches[0")
		if err == nil || !strings.Contains(err.Error(), "unmatched '['") {
			t.Fatalf("expected unmatched bracket error, got %v", err)
		}

		// Invalid json
		_, err = SelectPath([]byte(`{not-json`), "cmd")
		if err == nil {
			t.Fatal("expected invalid json error")
		}
	})
}

func TestDefaultSelectorIntegration(t *testing.T) {
	defer SetDefaultSelector("")

	env := NewEnvelope("result", "test", "sub", map[string]any{
		"receipt_id": "rc-999",
		"items":      []string{"one", "two"},
	})

	t.Run("selector extracts field through WriteResponse", func(t *testing.T) {
		SetDefaultSelector(".data.receipt_id")

		var buf bytes.Buffer
		if err := WriteResponse(&buf, env, false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := strings.TrimSpace(buf.String())
		if got != "rc-999" {
			t.Fatalf("expected rc-999, got %q", got)
		}
	})

	t.Run("selector extracts projection through WriteJSONL", func(t *testing.T) {
		SetDefaultSelector("data.items[]")

		var buf bytes.Buffer
		if err := WriteJSONL(&buf, env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := strings.TrimSpace(buf.String())
		if got != "one\ntwo" {
			t.Fatalf("expected one\\ntwo, got %q", got)
		}
	})

	t.Run("error envelope bypasses selector", func(t *testing.T) {
		SetDefaultSelector(".data.receipt_id")

		errEnv := NewErrorEnvelope("test", "sub", "tr-1", CodeInvalid, "bad input", "", "")
		var buf bytes.Buffer
		if err := WriteResponse(&buf, errEnv, false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, CodeInvalid) || !strings.Contains(got, "bad input") {
			t.Fatalf("error envelope was suppressed by selector: %s", got)
		}
	})

	t.Run("reset selector restores standard envelope output", func(t *testing.T) {
		SetDefaultSelector("")

		var buf bytes.Buffer
		if err := WriteResponse(&buf, env, false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, `"kind": "result"`) || !strings.Contains(got, `"rc-999"`) {
			t.Fatalf("expected standard envelope, got %s", got)
		}
	})
}
