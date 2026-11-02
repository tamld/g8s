package cli

import (
	"flag"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestParseTimeoutDuration(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantDur    time.Duration
		wantEff    string
		wantErr    bool
		errContain string
	}{
		{
			name:    "bare positive integer treated as seconds",
			input:   "900",
			wantDur: 900 * time.Second,
			wantEff: "900s",
			wantErr: false,
		},
		{
			name:    "standard seconds duration",
			input:   "900s",
			wantDur: 900 * time.Second,
			wantEff: "900s",
			wantErr: false,
		},
		{
			name:    "standard minutes duration",
			input:   "15m",
			wantDur: 15 * time.Minute,
			wantEff: "15m",
			wantErr: false,
		},
		{
			name:    "compound duration",
			input:   "1h30m",
			wantDur: 90 * time.Minute,
			wantEff: "1h30m",
			wantErr: false,
		},
		{
			name:       "invalid string banana fails with hint",
			input:      "banana",
			wantErr:    true,
			errContain: "must be a valid duration (e.g. '900s', '15m'",
		},
		{
			name:       "empty string fails",
			input:      "",
			wantErr:    true,
			errContain: "cannot be empty",
		},
		{
			name:       "zero bare integer fails",
			input:      "0",
			wantErr:    true,
			errContain: "must be positive",
		},
		{
			name:       "zero duration fails",
			input:      "0s",
			wantErr:    true,
			errContain: "must be positive",
		},
		{
			name:       "negative bare integer fails",
			input:      "-10",
			wantErr:    true,
			errContain: "must be positive",
		},
		{
			name:       "negative duration fails",
			input:      "-5s",
			wantErr:    true,
			errContain: "must be positive",
		},
		{
			name:       "huge bare integer exceeding max duration fails (9223372037)",
			input:      "9223372037",
			wantErr:    true,
			errContain: "exceeds maximum representable",
		},
		{
			name:       "huge bare integer math.MaxInt64 fails",
			input:      strconv.FormatInt(math.MaxInt64, 10),
			wantErr:    true,
			errContain: "exceeds maximum representable",
		},
		{
			name:       "huge bare integer 10000000000 fails without wrapping negative",
			input:      "10000000000",
			wantErr:    true,
			errContain: "exceeds maximum representable",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dur, eff, err := ParseTimeoutDuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTimeoutDuration(%q) expected error, got nil", tc.input)
				}
				if tc.errContain != "" && !strings.Contains(err.Error(), tc.errContain) {
					t.Errorf("error %q should contain %q", err.Error(), tc.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTimeoutDuration(%q) unexpected error: %v", tc.input, err)
			}
			if dur != tc.wantDur {
				t.Errorf("dur = %v, want %v", dur, tc.wantDur)
			}
			if eff != tc.wantEff {
				t.Errorf("eff = %q, want %q", eff, tc.wantEff)
			}
		})
	}
}
