package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGenerateTraceID(t *testing.T) {
	id1 := GenerateTraceID()
	id2 := GenerateTraceID()

	if id1 == "" || id2 == "" {
		t.Fatalf("expected non-empty trace IDs, got id1=%q, id2=%q", id1, id2)
	}
	if id1 == id2 {
		t.Fatalf("expected unique trace IDs, got identical %q", id1)
	}
	// Verify it's a valid UUID string
	u, err := uuid.Parse(id1)
	if err != nil {
		t.Fatalf("expected valid UUID, got parse error: %v", err)
	}
	if u.Version() != 7 && u.Version() != 4 {
		t.Errorf("expected UUID version 7 or 4, got %d", u.Version())
	}
}

func TestNewEnvelope(t *testing.T) {
	data := map[string]string{"foo": "bar"}
	env := NewEnvelope("task", "submit", "test", data)

	if env.V != CurrentEnvelopeVersion {
		t.Errorf("expected V=%d, got %d", CurrentEnvelopeVersion, env.V)
	}
	if env.Kind != "task" {
		t.Errorf("expected Kind='task', got %q", env.Kind)
	}
	if env.Command != "submit" {
		t.Errorf("expected Command='submit', got %q", env.Command)
	}
	if env.Subcommand != "test" {
		t.Errorf("expected Subcommand='test', got %q", env.Subcommand)
	}
	if env.At.IsZero() {
		t.Errorf("expected non-zero timestamp At")
	}
	if env.Error != nil {
		t.Errorf("expected Error=nil, got %+v", env.Error)
	}
}

func TestNewErrorEnvelope(t *testing.T) {
	env := NewErrorEnvelope("submit", "issue", "trace-123", CodeHarness, "denied", "check roles", "policy")

	if env.V != CurrentEnvelopeVersion {
		t.Errorf("expected V=%d, got %d", CurrentEnvelopeVersion, env.V)
	}
	if env.Kind != "error" {
		t.Errorf("expected Kind='error', got %q", env.Kind)
	}
	if env.Command != "submit" {
		t.Errorf("expected Command='submit', got %q", env.Command)
	}
	if env.TraceID != "trace-123" {
		t.Errorf("expected TraceID='trace-123', got %q", env.TraceID)
	}
	if env.Error == nil {
		t.Fatalf("expected Error payload, got nil")
	}
	if env.Error.Code != CodeHarness {
		t.Errorf("expected Code=%q, got %q", CodeHarness, env.Error.Code)
	}
	if env.Error.Message != "denied" {
		t.Errorf("expected Message='denied', got %q", env.Error.Message)
	}
	if env.Error.Hint != "check roles" {
		t.Errorf("expected Hint='check roles', got %q", env.Error.Hint)
	}
	if env.Error.Cause != "policy" {
		t.Errorf("expected Cause='policy', got %q", env.Error.Cause)
	}
}

func TestWriteJSONAndWriteJSONL(t *testing.T) {
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	env := Envelope{
		V:       1,
		Kind:    "task",
		Command: "submit",
		Data:    map[string]string{"task_id": "t-123"},
		TraceID: "019154a1-0000-7000-8000-000000000000",
		At:      now,
	}

	t.Run("WriteJSON multiline", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteJSON(&buf, env)
		if err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}
		str := buf.String()
		if !strings.Contains(str, "\n  \"v\": 1,") {
			t.Errorf("expected indented JSON, got: %s", str)
		}
		if !strings.Contains(str, `"task_id": "t-123"`) {
			t.Errorf("expected data field, got: %s", str)
		}

		var decoded Envelope
		if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if decoded.V != 1 || decoded.Command != "submit" || decoded.Kind != "task" {
			t.Errorf("decoded mismatch: %+v", decoded)
		}
	})

	t.Run("WriteJSONL single line", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteJSONL(&buf, env)
		if err != nil {
			t.Fatalf("WriteJSONL: %v", err)
		}
		str := buf.String()
		trimmed := strings.TrimSpace(str)
		if strings.Contains(trimmed, "\n") {
			t.Errorf("expected single line for JSONL, got multiple lines: %s", str)
		}

		var decoded Envelope
		if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if decoded.TraceID != env.TraceID {
			t.Errorf("decoded TraceID = %q, want %q", decoded.TraceID, env.TraceID)
		}
	})

	t.Run("WriteResponse routes correctly", func(t *testing.T) {
		var bufNormal bytes.Buffer
		_ = WriteResponse(&bufNormal, env, false)
		if !strings.Contains(bufNormal.String(), "\n  \"v\": 1,") {
			t.Errorf("expected formatted JSON when jsonl=false")
		}

		var bufL bytes.Buffer
		_ = WriteResponse(&bufL, env, true)
		if strings.Contains(strings.TrimSpace(bufL.String()), "\n") {
			t.Errorf("expected single line when jsonl=true")
		}
	})

	t.Run("WriteError writes error envelope", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteError(&buf, CodeUsage, "bad input", "check flags")
		if err != nil {
			t.Fatalf("WriteError: %v", err)
		}

		var decoded Envelope
		if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.Kind != "error" {
			t.Errorf("expected Kind='error', got %q", decoded.Kind)
		}
		if decoded.Error == nil || decoded.Error.Code != CodeUsage {
			t.Errorf("expected Code=%q, got %+v", CodeUsage, decoded.Error)
		}
	})
}
