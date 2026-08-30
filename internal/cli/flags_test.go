package cli

import (
	"flag"
	"testing"
)

func TestAddCommonFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := AddCommonFlags(fs)

	err := fs.Parse([]string{"--actor", "custom-agent", "--trace-id", "019154a1-test", "--jsonl"})
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if *actor != "custom-agent" {
		t.Errorf("actor = %q, want 'custom-agent'", *actor)
	}
	if *traceID != "019154a1-test" {
		t.Errorf("traceID = %q, want '019154a1-test'", *traceID)
	}
	if !*jsonl {
		t.Errorf("jsonl = false, want true")
	}
	if !*jsonMode {
		t.Errorf("jsonMode = false, want true (default for AddCommonFlags)")
	}
}

func TestAddCommonFlagsWithDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test-default-false", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := AddCommonFlagsWithDefaults(fs, false)

	err := fs.Parse([]string{})
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if *actor != "operator" {
		t.Errorf("actor default = %q, want 'operator'", *actor)
	}
	if *traceID == "" {
		t.Errorf("traceID default should be non-empty UUID v7")
	}
	if *jsonl {
		t.Errorf("jsonl default should be false")
	}
	if *jsonMode {
		t.Errorf("jsonMode default should be false when configured false")
	}
}
