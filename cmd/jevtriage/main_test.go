package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	if got := split(""); got != nil {
		t.Fatalf("split(\"\") = %v, want nil", got)
	}
	got := split("cmd/a.go, internal/b.go ,c.go")
	want := []string{"cmd/a.go", "internal/b.go", "c.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split mismatch: got %v, want %v", got, want)
	}
}

func TestRunJevTriage_JSONAndBadFlag(t *testing.T) {
	t.Setenv("JEV_URL", "http://127.0.0.1:1")
	var buf bytes.Buffer
	err := runJevTriage([]string{
		"--task", "task-test-1",
		"--files", "cmd/jevtriage/main.go",
		"--summary", "unit test triage",
		"--allowed", "cmd/**",
	}, &buf)
	if err != nil {
		t.Fatalf("runJevTriage unexpected error: %v", err)
	}
	var parsed map[string]any
	if uErr := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &parsed); uErr != nil {
		t.Fatalf("invalid JSON output %q: %v", buf.String(), uErr)
	}
	if _, ok := parsed["action"]; !ok {
		t.Fatalf("expected 'action' key in %v", parsed)
	}

	var errBuf bytes.Buffer
	if fErr := runJevTriage([]string{"--definitely-unknown-flag"}, &errBuf); fErr == nil {
		t.Fatal("expected error for unknown flag")
	}
}
